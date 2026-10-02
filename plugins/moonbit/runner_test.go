package moonbit

import (
	"bufio"
	"encoding/json"
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

func TestLostConnectionResetsScanAndClean(t *testing.T) {
	scan := &State{}
	scan.StartScan()
	scan.ScanCat, scan.ScanIdx, scan.ScanTotal = "apt", 1, 4
	if !scan.LostConnection() || scan.Phase != PhaseError || scan.Err != "moonbit connection lost" {
		t.Fatalf("scan end: phase=%v err=%q", scan.Phase, scan.Err)
	}
	if scan.ScanCat != "" || scan.ScanIdx != 0 || scan.ScanTotal != 0 {
		t.Fatalf("scan progress left set: %+v", scan)
	}

	clean := &State{}
	clean.StartClean()
	clean.CleanTotal, clean.CleanDone, clean.CleanFile = 9, 3, "/tmp/x"
	if !clean.LostConnection() || clean.Phase != PhaseError {
		t.Fatalf("clean end: phase=%v err=%q", clean.Phase, clean.Err)
	}
	if clean.CleanTotal != 0 || clean.CleanDone != 0 || clean.CleanFile != "" {
		t.Fatalf("clean progress left set: %+v", clean)
	}

	review := &State{Phase: PhaseReview}
	if review.LostConnection() || review.Phase != PhaseReview {
		t.Fatal("a finished review must survive a late stream close")
	}
}
