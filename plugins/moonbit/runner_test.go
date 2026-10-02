package moonbit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// stubDaemon answers one request per connection like the real daemon. With
// hang=false it closes after the canned events (what handlePanelConn does for
// status); with hang=true it first reads to EOF, so a test can watch the
// client-close cancel contract.
func stubDaemon(t *testing.T, lines func(req request) []Event, hang bool) (string, chan request, chan struct{}) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "panel.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	reqs := make(chan request, 4)
	closed := make(chan struct{}, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				sc := bufio.NewScanner(conn)
				if !sc.Scan() {
					return
				}
				var req request
				if json.Unmarshal(sc.Bytes(), &req) != nil {
					return
				}
				reqs <- req
				for _, ev := range lines(req) {
					b, _ := json.Marshal(ev)
					if _, err := conn.Write(append(b, '\n')); err != nil {
						return
					}
				}
				if !hang {
					return
				}
				for sc.Scan() {
				}
				closed <- struct{}{}
			}()
		}
	}()
	return sock, reqs, closed
}

func TestStatusRoundTrip(t *testing.T) {
	sock, _, _ := stubDaemon(t, func(request request) []Event {
		return []Event{{T: "status", Daemon: true, Cache: &CacheInfo{
			Files: 3, Bytes: 1024, Categories: []CategoryStat{{Name: "apt", Files: 3, Bytes: 1024}}}}}
	}, false)
	ev, err := Runner{Path: sock}.Status()
	if err != nil {
		t.Fatal(err)
	}
	if ev.Cache == nil || len(ev.Cache.Categories) != 1 || ev.Cache.Categories[0].Name != "apt" {
		t.Fatalf("bad status: %+v", ev)
	}
}

func TestStatusErrorPropagates(t *testing.T) {
	sock, _, _ := stubDaemon(t, func(request request) []Event {
		return []Event{{T: "error", Msg: "another operation in progress"}}
	}, false)
	if _, err := (Runner{Path: sock}).Status(); err == nil {
		t.Fatal("error event must surface as an error")
	}
}

func TestDialFailure(t *testing.T) {
	if _, err := (Runner{Path: filepath.Join(t.TempDir(), "absent.sock")}).Status(); err == nil {
		t.Fatal("dial to missing socket must error")
	}
}

func TestCancelClosesStream(t *testing.T) {
	sock, reqs, closed := stubDaemon(t, func(request request) []Event {
		return []Event{
			{T: "category", Name: "a", I: 1, Total: 9},
			{T: "scan", Files: 10},
		}
	}, true)
	r := Runner{Path: sock}
	op, err := r.Scan("deep", nil)
	if err != nil {
		t.Fatal(err)
	}
	if req := <-reqs; req.Cmd != "scan" || req.Mode != "deep" {
		t.Fatalf("bad request: %+v", req)
	}
	<-op.Events // category
	op.Cancel()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("stub never saw the connection close")
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-op.Events:
			if !ok {
				return // channel closed after cancel: contract holds
			}
		case <-deadline:
			t.Fatal("Events not closed after Cancel")
		}
	}
}

func TestFoldCancelFromDaemon(t *testing.T) {
	s := &State{}
	s.StartScan()
	if !s.Fold(Event{T: "cancelled"}) || s.Phase != PhaseIdle {
		t.Fatal("cancelled must fold to idle")
	}
}

func TestStreamEndedRecoversActiveOperation(t *testing.T) {
	for _, phase := range []Phase{PhaseScanning, PhaseCleaning} {
		s := &State{
			Phase:      phase,
			ScanCat:    "cache",
			ScanDir:    "/tmp/cache",
			ScanTotal:  3,
			ScanFiles:  8,
			ScanBytes:  512,
			CleanTotal: 8,
			CleanDone:  4,
			CleanFile:  "/tmp/cache/item",
		}
		if !s.StreamEnded() || s.Phase != PhaseError || s.Err == "" {
			t.Fatalf("phase %d did not recover to an error state: %+v", phase, s)
		}
		if s.ScanCat != "" || s.ScanDir != "" || s.ScanTotal != 0 || s.ScanFiles != 0 || s.ScanBytes != 0 ||
			s.CleanTotal != 0 || s.CleanDone != 0 || s.CleanFile != "" {
			t.Fatalf("phase %d retained stale progress: %+v", phase, s)
		}
	}
}

// The daemon cancels on EOF of its request reader and still writes its
// terminal event, so Cancel must leave the read side open to receive it.
func TestCancelStillReadsTheDaemonsCancelledEvent(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "panel.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		sc := bufio.NewScanner(conn)
		sc.Scan() // request
		_, _ = conn.Write([]byte(`{"t":"category","name":"a","i":1,"total":9}` + "\n"))
		for sc.Scan() {
		}
		_, _ = conn.Write([]byte(`{"t":"cancelled"}` + "\n"))
	}()
	op, err := Runner{Path: sock}.Scan("quick", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-op.Events // category
	op.Cancel()
	var last string
	for ev := range op.Events {
		last = ev.T
	}
	if last != "cancelled" {
		t.Fatalf("last event after Cancel = %q, want cancelled", last)
	}
}

// clean_done carries every failed path; a long run makes one line far past
// bufio.Scanner's 64 KiB default, which used to drop the terminal event.
func TestLongEventLineIsDelivered(t *testing.T) {
	errs := make([]string, 2000)
	for i := range errs {
		errs[i] = fmt.Sprintf("/var/cache/pacman/pkg/package-%04d.pkg.tar.zst: permission denied", i)
	}
	sock, _, _ := stubDaemon(t, func(request) []Event {
		return []Event{{T: "clean_done", Deleted: 3, Errors: errs}}
	}, false)
	op, err := Runner{Path: sock}.Clean(true, []string{"Pacman Cache"})
	if err != nil {
		t.Fatal(err)
	}
	var got []Event
	for ev := range op.Events {
		got = append(got, ev)
	}
	if len(got) != 1 || got[0].T != "clean_done" || len(got[0].Errors) != len(errs) {
		t.Fatalf("got %d events, want the one clean_done with %d errors", len(got), len(errs))
	}
}
