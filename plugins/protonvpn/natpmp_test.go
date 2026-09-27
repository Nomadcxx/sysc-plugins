package protonvpn

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestBuildMapRequest(t *testing.T) {
	want := []byte{0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 60}
	if got := BuildMapRequest(2, 0, 0, 60); len(got) != 12 || string(got) != string(want) {
		t.Fatalf("wildcard: got %v", got)
	}
	want = []byte{0, 1, 0, 0, 0x14, 0x3C, 0x02, 0x00, 0, 0, 0, 60}
	if got := BuildMapRequest(1, 5180, 512, 60); string(got) != string(want) {
		t.Fatalf("explicit: got %v", got)
	}
}

func TestParseMapResponse(t *testing.T) {
	resp := []byte{0, 2, 0, 0, 0, 0, 0, 100, 0x14, 0x3C, 0x14, 0x3C, 0, 0, 0, 60}
	port, epoch, err := ParseMapResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if port != 5180 || epoch != 100 {
		t.Fatalf("got port %d epoch %d", port, epoch)
	}
}

func TestParseMapResponseErrors(t *testing.T) {
	if _, _, err := ParseMapResponse([]byte{0, 2}); err == nil {
		t.Fatal("short response must error")
	}
	failed := make([]byte, 16)
	binary.BigEndian.PutUint16(failed[2:4], 6) // insufficient resources
	if _, _, err := ParseMapResponse(failed); err == nil {
		t.Fatal("non-zero result must error")
	}
}

func TestNATPMPFailureIsNonFatal(t *testing.T) {
	n := NATPMP{Gateway: "127.0.0.1:1"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := n.RequestPort(ctx); err == nil {
		t.Fatal("dead gateway must error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second+200*time.Millisecond {
		t.Fatalf("must not hang past the context: %v", elapsed)
	}
}

func TestNATPMPAgainstFakeGateway(t *testing.T) {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		req := make([]byte, 12)
		for {
			n, peer, err := ln.ReadFromUDP(req)
			if err != nil || n != 12 {
				return
			}
			resp := make([]byte, 16)
			resp[1] = req[1] // echo opcode
			resp[7] = 7      // epoch
			copy(resp[8:10], req[6:8])
			binary.BigEndian.PutUint16(resp[10:12], 4160) // gateway's mapped port
			binary.BigEndian.PutUint32(resp[12:16], 60)
			ln.WriteToUDP(resp, peer)
		}
	}()
	n := NATPMP{Gateway: ln.LocalAddr().String()}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	port, err := n.RequestPort(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if port != 4160 {
		t.Fatalf("got %d, want the gateway's mapped port", port)
	}
}
