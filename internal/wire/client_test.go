package v1

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestClientHandshakeAndSnapshotStayOnStdout(t *testing.T) {
	t.Parallel()
	pluginIn, hostWrites := io.Pipe()
	hostReads, pluginOut := io.Pipe()
	defer hostWrites.Close()
	defer pluginOut.Close()

	c := NewClient(pluginIn, pluginOut)
	done := make(chan error, 1)
	go func() {
		_, err := c.Handshake(Identity{ID: "org.sysc.timer", Name: "Timer", Version: "1.0.0"})
		if err != nil {
			done <- err
			return
		}
		done <- c.Snapshot("v1", 1, &Node{Kind: KindRow, Children: []*Node{
			{Kind: KindText, Text: "05:00", Tabular: true},
		}})
	}()

	enc := NewEncoder(hostWrites)
	if err := enc.Encode(&HostHello{
		Supported:    []Version{{Major: 1, Minor: 0}},
		Plugin:       Identity{ID: "org.sysc.timer", Name: "Timer", Version: "1.0.0"},
		Capabilities: []string{"notifications", "state"},
		Limits:       DefaultLimits,
	}); err != nil {
		t.Fatal(err)
	}
	dec := NewDecoder(hostReads, ToHost)
	hello, err := dec.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hello.(*PluginHello); !ok {
		t.Fatalf("got %T", hello)
	}
	snap, err := dec.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.(*ViewSnapshot); !ok {
		t.Fatalf("got %T", snap)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientCallPairsReplyAndHonoursCancel(t *testing.T) {
	t.Parallel()
	pluginIn, hostWrites := io.Pipe()
	hostReads, pluginOut := io.Pipe()
	defer hostWrites.Close()
	defer pluginOut.Close()

	c := NewClient(pluginIn, pluginOut)
	hostEnc := NewEncoder(hostWrites)
	hostDec := NewDecoder(hostReads, ToHost)

	go func() {
		_, _ = c.Handshake(Identity{ID: "org.sysc.timer", Name: "Timer", Version: "1.0.0"})
		for {
			if _, err := c.Recv(); err != nil {
				return
			}
		}
	}()
	if err := hostEnc.Encode(&HostHello{
		Supported: []Version{{Major: 1}},
		Plugin:    Identity{ID: "org.sysc.timer", Name: "Timer", Version: "1.0.0"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := hostDec.Decode(); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			msg, err := hostDec.Decode()
			if err != nil {
				return
			}
			if hc, ok := msg.(*HostCall); ok {
				_ = hostEnc.Encode(&HostReply{ID: hc.ID, OK: true})
			}
		}
	}()

	reply, err := c.Call(context.Background(), CallStateGet, StateGetParams{Key: "deadline"})
	if err != nil || !reply.OK {
		t.Fatalf("call: %+v %v", reply, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Call(ctx, CallStateGet, StateGetParams{Key: "x"}); err == nil {
		t.Fatal("cancelled call succeeded")
	}
}

func TestClientStdoutContainsProtocolOnly(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	c := NewClient(strings.NewReader(""), &stdout)
	if err := c.Send(&PluginStatus{State: StatusOK, Message: "ok"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"type":"plugin.status"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "\n\n") {
		t.Fatal("blank line on stdout")
	}
	_ = time.Second
}
