package covers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchGridHappyPath(t *testing.T) {
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/search/autocomplete/hades", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"data":[{"id":42,"name":"Hades"}]}`)
	})
	mux.HandleFunc("/grids/game/42", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":[{"url":"http://%s/grid.jpg"}]}`, r.Host)
	})
	mux.HandleFunc("/grid.jpg", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "fakejpegbytes")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	dest := t.TempDir()
	path := FetchGrid(context.Background(), srv.Client(), "key123", srv.URL, "hades", dest)
	if path == "" {
		t.Fatalf("got empty path")
	}
	if gotAuth != "Bearer key123" {
		t.Errorf("auth header %q", gotAuth)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "fakejpegbytes" {
		t.Errorf("downloaded %q err %v", body, err)
	}
	if filepath.Dir(path) != dest {
		t.Errorf("path %q outside dest", path)
	}
}

func TestFetchGridEscapesSlug(t *testing.T) {
	var gotURI string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		fmt.Fprint(w, `{"data":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	FetchGrid(context.Background(), srv.Client(), "key", srv.URL, "hades ii/2", t.TempDir())
	if !strings.Contains(gotURI, "hades%20ii%2F2") {
		t.Fatalf("slug not escaped: %q", gotURI)
	}
}

func TestFetchGridNoKey(t *testing.T) {
	if p := FetchGrid(context.Background(), http.DefaultClient, "", "", "hades", t.TempDir()); p != "" {
		t.Errorf("want empty with no key, got %q", p)
	}
}

func TestFetchGridUnauthorizedAndEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/autocomplete/secret", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/search/autocomplete/none", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	for _, slug := range []string{"secret", "none"} {
		if p := FetchGrid(context.Background(), srv.Client(), "bad", srv.URL, slug, t.TempDir()); p != "" {
			t.Errorf("slug %s: want empty, got %q", slug, p)
		}
	}
}

func TestFetchGridUsesCache(t *testing.T) {
	hits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hits++
		fmt.Fprint(w, `{"data":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "hades-grid.jpg"), []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := FetchGrid(context.Background(), srv.Client(), "k", srv.URL, "hades", dest)
	if p == "" || hits != 0 {
		t.Errorf("want cache hit with no requests, got %q hits=%d", p, hits)
	}
}
