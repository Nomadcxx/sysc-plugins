package faith

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func commentaryServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	fixture, err := os.ReadFile("testdata/adam-clarke-GEN-1.json")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case r.URL.Path == "/api/c/adam-clarke/GEN/1.json":
			_, _ = w.Write(fixture)
		case r.URL.Path == "/api/c/adam-clarke/GEN/2.json":
			_, _ = w.Write([]byte(strings.Repeat(" ", maxCommentaryBytes+10)))
		case strings.HasPrefix(r.URL.Path, "/api/c/adam-clarke/EXO/"):
			_, _ = w.Write([]byte(`{"chapter":{"content":[]}}`))
		case r.URL.Path == "/api/c/adam-clarke/LEV/1.json":
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestCommentaryEntry(t *testing.T) {
	srv, hits := commentaryServer(t)
	c := NewCommentary(srv.URL + "/api")
	text, found, err := c.Entry(context.Background(), mustRef(t, "GEN 1:1"))
	if err != nil || !found || !strings.HasPrefix(text, "God in the beginning") || !strings.Contains(text, "\nIn the beginning") {
		t.Fatalf("Genesis 1:1 = %q, %v, %v", text, found, err)
	}
	text, found, err = c.Entry(context.Background(), mustRef(t, "GEN 1:2"))
	if err != nil || !found || text != "Without form, and void - the original materials of all things.\n\nThe Spirit\nThe Spirit of God moved." {
		t.Fatalf("Genesis 1:2 = %q, %v, %v", text, found, err)
	}
	if _, found, err := c.Entry(context.Background(), mustRef(t, "GEN 1:3")); err != nil || found {
		t.Fatalf("Genesis 1:3 found=%v err=%v, want absent", found, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("three verses of one chapter made %d requests", hits.Load())
	}
}

func TestCommentaryErrors(t *testing.T) {
	srv, _ := commentaryServer(t)
	c := NewCommentary(srv.URL + "/api")
	var se *StatusError
	if _, _, err := c.Entry(context.Background(), mustRef(t, "NUM 1:1")); !errors.As(err, &se) || se.Code != 404 {
		t.Fatalf("404 gave %v", err)
	}
	if _, _, err := c.Entry(context.Background(), mustRef(t, "GEN 2:1")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized body gave %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := c.Entry(ctx, mustRef(t, "LEV 1:1")); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("cancelled request gave %v after %v", err, time.Since(start))
	}
}

func TestCommentaryCacheIsBounded(t *testing.T) {
	srv, _ := commentaryServer(t)
	c := NewCommentary(srv.URL + "/api")
	for ch := 1; ch <= 20; ch++ {
		if _, _, err := c.Entry(context.Background(), Ref{Book: 1, Chapter: ch, Verse: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if c.Cached() != commentaryChapters {
		t.Fatalf("cache holds %d chapters, want %d", c.Cached(), commentaryChapters)
	}
}
