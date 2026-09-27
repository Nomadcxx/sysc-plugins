package protonvpn

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	natpmpLifetime = 60 // seconds; the loop re-requests every 45s
	natpmpWgPort   = 5180
)

// NATPMP asks the gateway for an external port mapping so P2P traffic can
// reach the tunnel. A plain struct so tests can point it at a fake gateway.
type NATPMP struct {
	Gateway string // host:port, defaults to the Proton tunnel gateway
	// Now is the injectable clock; conn deadlines already pace the protocol,
	// so it stays reserved unless timing tests need it.
	Now func() time.Time
}

func (n NATPMP) gateway() string {
	if n.Gateway != "" {
		return n.Gateway
	}
	return "10.2.0.1:5351"
}

// BuildMapRequest encodes a NAT-PMP mapping request (RFC 6886):
// version, opcode, 2 reserved bytes, internal port, external port, lifetime.
func BuildMapRequest(op uint8, internalPort, externalPort, lifetime uint32) []byte {
	b := make([]byte, 12)
	b[1] = op
	binary.BigEndian.PutUint16(b[4:6], uint16(internalPort))
	binary.BigEndian.PutUint16(b[6:8], uint16(externalPort))
	binary.BigEndian.PutUint32(b[8:12], lifetime)
	return b
}

// ParseMapResponse decodes a 16-byte mapping response, returning the
// gateway-assigned external port and the epoch.
func ParseMapResponse(b []byte) (port int, epoch uint32, err error) {
	if len(b) < 16 {
		return 0, 0, fmt.Errorf("nat-pmp: short response (%d bytes)", len(b))
	}
	if result := binary.BigEndian.Uint16(b[2:4]); result != 0 {
		return 0, 0, fmt.Errorf("nat-pmp: gateway refused with result %d", result)
	}
	return int(binary.BigEndian.Uint16(b[10:12])), binary.BigEndian.Uint32(b[4:8]), nil
}

// RequestPort maps UDP then TCP and returns the external port. Refusals and
// socket errors fail fast; timeouts retry with 250ms..2s backoff until ctx
// ends, so port forwarding is best-effort and never blocks the plugin.
func (n NATPMP) RequestPort(ctx context.Context) (int, error) {
	a, err := net.ResolveUDPAddr("udp", n.gateway())
	if err != nil {
		return 0, err
	}
	conn, err := net.DialUDP("udp", nil, a)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	backoff := 250 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		port, err := n.mapBoth(ctx, conn)
		if err == nil {
			return port, nil
		}
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			return 0, err // refused, unreachable, or malformed: not retriable
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > 2*time.Second {
			backoff = 2 * time.Second
		}
	}
}

func (n NATPMP) mapBoth(ctx context.Context, conn *net.UDPConn) (int, error) {
	var port int
	for _, op := range []uint8{1, 2} { // UDP (op 1) first, then TCP (op 2)
		p, err := n.request(ctx, conn, op)
		if err != nil {
			return 0, err
		}
		if op == 1 {
			port = p
		}
	}
	return port, nil
}

func (n NATPMP) request(ctx context.Context, conn *net.UDPConn, op uint8) (int, error) {
	deadline := time.Now().Add(time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return 0, err
	}
	if _, err := conn.Write(BuildMapRequest(op, natpmpWgPort, 0, natpmpLifetime)); err != nil {
		return 0, err
	}
	resp := make([]byte, 16)
	nr, err := conn.Read(resp)
	if err != nil {
		return 0, err
	}
	port, _, err := ParseMapResponse(resp[:nr])
	return port, err
}
