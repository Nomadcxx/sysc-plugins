package hostcall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestCallTimesOutWhenReplyNeverArrives(t *testing.T) {
	restore := SetTimeout(100 * time.Millisecond)
	defer restore()

	// The host side of the pipe never delivers a reply; the writer is a plain
	// buffer so the request encoding itself cannot block.
	requests, hostSide := io.Pipe()
	defer requests.Close()
	defer hostSide.Close()
	_ = hostSide
	c := v1.NewClient(requests, &bytes.Buffer{})

	start := time.Now()
	_, err := Call(context.Background(), c, v1.CallStateGet, nil)
	if err == nil {
		t.Fatal("Call returned nil error with no reply coming")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Call error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Call took %s, want about %s", elapsed, Timeout())
	}
}

func TestAsyncReturnsBeforeHostReply(t *testing.T) {
	toPlugin, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	defer toPlugin.Close()
	defer hostOut.Close()
	defer hostIn.Close()
	defer pluginOut.Close()

	c := v1.NewClient(toPlugin, pluginOut)
	go func() {
		for {
			if _, err := c.Recv(); err != nil {
				return
			}
		}
	}()
	calls := make(chan *v1.HostCall, 1)
	go func() {
		dec := v1.NewDecoder(hostIn, v1.ToHost)
		msg, err := dec.Decode()
		if err == nil {
			if call, ok := msg.(*v1.HostCall); ok {
				calls <- call
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan struct{})
	go func() {
		Async(ctx, c, v1.CallNotify, v1.NotifyParams{Summary: "test"})
		close(returned)
	}()
	var call *v1.HostCall
	select {
	case call = <-calls:
	case <-time.After(time.Second):
		t.Fatal("async call was not written")
	}
	select {
	case <-returned:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Async waited for the host reply")
	}
	if err := v1.NewEncoder(hostOut).Encode(&v1.HostReply{ID: call.ID, OK: true}); err != nil {
		t.Fatal(err)
	}
}

func TestStateSaverWritesNewestCoalescedValue(t *testing.T) {
	toPlugin, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	defer toPlugin.Close()
	defer hostOut.Close()
	defer hostIn.Close()
	defer pluginOut.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := v1.NewClient(toPlugin, pluginOut)
	saver := NewStateSaver(ctx, c)
	calls := make(chan *v1.HostCall, 4)
	go func() {
		dec := v1.NewDecoder(hostIn, v1.ToHost)
		for {
			msg, err := dec.Decode()
			if err != nil {
				return
			}
			if call, ok := msg.(*v1.HostCall); ok {
				calls <- call
			}
		}
	}()
	go func() {
		for {
			if _, err := c.Recv(); err != nil {
				return
			}
		}
	}()
	host := v1.NewEncoder(hostOut)
	nextCall := func() *v1.HostCall {
		t.Helper()
		select {
		case call := <-calls:
			return call
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for state.set")
			return nil
		}
	}
	answer := func(call *v1.HostCall) {
		t.Helper()
		if err := host.Encode(&v1.HostReply{ID: call.ID, OK: true}); err != nil {
			t.Fatalf("reply to state.set: %v", err)
		}
	}

	saver.Save("key", json.RawMessage(`"first"`))
	first := nextCall()
	start := time.Now()
	saver.Save("key", json.RawMessage(`"middle"`))
	saver.Save("key", json.RawMessage(`"latest"`))
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("Save blocked for %s while a host call was held", elapsed)
	}
	answer(first)
	second := nextCall()
	var params v1.StateSetParams
	if err := json.Unmarshal(second.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.Key != "key" || string(params.Value) != `"latest"` {
		t.Fatalf("coalesced write = {%q %s}, want {key \"latest\"}", params.Key, params.Value)
	}
	answer(second)
}

func TestStateSaverReportsTrackedReply(t *testing.T) {
	toPlugin, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	defer toPlugin.Close()
	defer hostOut.Close()
	defer hostIn.Close()
	defer pluginOut.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := v1.NewClient(toPlugin, pluginOut)
	saver := NewStateSaver(ctx, c)
	results := make(chan StateWriteResult, 1)
	calls := make(chan *v1.HostCall, 1)
	go func() {
		dec := v1.NewDecoder(hostIn, v1.ToHost)
		if msg, err := dec.Decode(); err == nil {
			if call, ok := msg.(*v1.HostCall); ok {
				calls <- call
			}
		}
	}()
	go func() {
		for {
			if _, err := c.Recv(); err != nil {
				return
			}
		}
	}()

	revision := saver.SaveTracked("key", json.RawMessage(`"value"`), results)
	var call *v1.HostCall
	select {
	case call = <-calls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for state.set")
	}
	if err := v1.NewEncoder(hostOut).Encode(&v1.HostReply{ID: call.ID, OK: false, Error: "disk full"}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-results:
		if result.Key != "key" || result.Revision != revision || result.Reply.OK || result.Reply.Error != "disk full" || result.Err != nil {
			t.Fatalf("state result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the state.set result")
	}
}

func TestFlushRetriesStateWriteInterruptedByShutdown(t *testing.T) {
	toPlugin, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	defer toPlugin.Close()
	defer hostOut.Close()
	defer hostIn.Close()
	defer pluginOut.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := v1.NewClient(toPlugin, pluginOut)
	saver := NewStateSaver(ctx, c)
	calls := make(chan *v1.HostCall, 4)
	go func() {
		dec := v1.NewDecoder(hostIn, v1.ToHost)
		for {
			msg, err := dec.Decode()
			if err != nil {
				return
			}
			if call, ok := msg.(*v1.HostCall); ok {
				calls <- call
			}
		}
	}()
	go func() {
		for {
			if _, err := c.Recv(); err != nil {
				return
			}
		}
	}()

	saver.Save("key", json.RawMessage(`"latest"`))
	select {
	case <-calls: // Keep the first write in flight.
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first state.set")
	}
	cancel()
	flushed := make(chan struct{})
	go func() {
		saver.Flush(time.Second)
		close(flushed)
	}()

	var retry *v1.HostCall
	select {
	case retry = <-calls:
	case <-time.After(time.Second):
		t.Fatal("shutdown flush did not retry the interrupted state.set")
	}
	var params v1.StateSetParams
	if err := json.Unmarshal(retry.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.Key != "key" || string(params.Value) != `"latest"` {
		t.Fatalf("flushed state = {%q %s}, want latest value", params.Key, params.Value)
	}
	if err := v1.NewEncoder(hostOut).Encode(&v1.HostReply{ID: retry.ID, OK: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("shutdown flush did not finish after its reply")
	}
}

// The event loop of these plugins must never make an unbounded host call; the
// reader that decodes replies can be stuck behind a full incoming queue while
// the loop waits.
var loopCallPlugins = []string{
	"sysc-plugin-timer",
	"sysc-plugin-kdeconnect",
	"sysc-plugin-screen-recorder",
	"sysc-plugin-notes",
	"sysc-plugin-aiusage",
	"sysc-plugin-mini-docker",
	"sysc-plugin-github-notifications",
}

func TestEventLoopCallsGoThroughHelper(t *testing.T) {
	for _, plugin := range loopCallPlugins {
		path := filepath.Join("..", "..", "cmd", plugin, "main.go")
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(src), "c.Call(ctx,") {
			t.Errorf("%s still makes an unbounded host call from the event loop", plugin)
		}
	}
}
