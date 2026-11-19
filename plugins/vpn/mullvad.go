package vpn

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
)

func ParseMullvadStatus(raw []byte) (Observation, error) {
	s := Observation{Traffic: TrafficUnknown}
	raw = bytes.TrimSpace(raw)
	for {
		removed := false
		for _, warning := range []string{
			"Warning: You are not logged in to an account.",
			"Warning: This device has been revoked.",
		} {
			if bytes.HasPrefix(raw, []byte(warning+"\n")) {
				raw = bytes.TrimSpace(raw[len(warning)+1:])
				s.Detail = "Check your account in the Mullvad app."
				removed = true
				break
			}
		}
		if !removed {
			break
		}
	}
	var envelope struct {
		State   Phase           `json:"state"`
		Details json.RawMessage `json:"details"`
	}
	bad := errors.New("Mullvad status format changed")
	if json.Unmarshal(raw, &envelope) != nil {
		return Observation{}, bad
	}
	s.Phase = envelope.State
	switch s.Phase {
	case Disconnected, Connecting, Connected:
		var d struct {
			LockedDown *bool `json:"locked_down"`
			Location   *struct {
				City, Country, Hostname, IPv4 string
			} `json:"location"`
			Endpoint struct {
				TunnelType string `json:"tunnel_type"`
			} `json:"endpoint"`
		}
		if !jsonObject(envelope.Details) || json.Unmarshal(envelope.Details, &d) != nil {
			return Observation{}, bad
		}
		if d.Location != nil {
			s.City, s.Country, s.Exit = display(d.Location.City), display(d.Location.Country), display(d.Location.Hostname)
			if ip := net.ParseIP(d.Location.IPv4); ip != nil {
				s.IP = ip.String()
			}
		}
		s.Transport = display(d.Endpoint.TunnelType)
		if s.Phase == Disconnected && d.LockedDown != nil {
			s.Traffic = TrafficNotBlocked
			if *d.LockedDown {
				s.Traffic = TrafficBlocked
			}
		}
	case Disconnecting:
		// The details are an ActionAfterDisconnect string, not a location.
	case TunnelError:
		var d map[string]json.RawMessage
		if !jsonObject(envelope.Details) || json.Unmarshal(envelope.Details, &d) != nil {
			return Observation{}, bad
		}
		if failure, exists := d["block_failure"]; exists {
			s.Traffic = TrafficNotBlocked
			if bytes.Equal(bytes.TrimSpace(failure), []byte("null")) {
				s.Traffic = TrafficBlocked
			}
		}
		s.Detail = "Check the connection in the Mullvad app."
	default:
		return Observation{}, bad
	}
	return s, nil
}

func jsonObject(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) >= 2 && raw[0] == '{'
}
