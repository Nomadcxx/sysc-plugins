package main

import (
	"bytes"
	"image"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/thumbnail"
)

// stubThumbnailBytes is a valid blank thumbnail every test release "ships".
var stubThumbnailBytes = func() []byte {
	data, err := thumbnail.Encode(image.NewNRGBA(image.Rect(0, 0, thumbnail.Width, thumbnail.Height)))
	if err != nil {
		panic(err)
	}
	return data
}()

// stubThumbnail answers a request for a tagged thumbnail.webp with the valid
// stub; any other request is not its concern.
func stubThumbnail(r *http.Request) (*http.Response, bool) {
	if !strings.HasSuffix(r.URL.Path, "/thumbnail.webp") {
		return nil, false
	}
	return &http.Response{
		StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK),
		Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(stubThumbnailBytes)), Request: r,
	}, true
}

type thumbnailStub struct{ next http.RoundTripper }

func (s thumbnailStub) RoundTrip(r *http.Request) (*http.Response, error) {
	if resp, ok := stubThumbnail(r); ok {
		return resp, nil
	}
	return s.next.RoundTrip(r)
}

// TestMain makes every test release carry a thumbnail unless a test says
// otherwise. Other requests behave exactly as before.
func TestMain(m *testing.M) {
	catalogHTTPClient.Transport = thumbnailStub{next: http.DefaultTransport}
	os.Exit(m.Run())
}

// withThumbnailResponse makes the tagged thumbnail.webp answer with status
// and body for the rest of the test.
func withThumbnailResponse(t *testing.T, status int, body []byte) {
	t.Helper()
	prev := catalogHTTPClient.Transport
	catalogHTTPClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/thumbnail.webp") {
			return &http.Response{
				StatusCode: status, Status: http.StatusText(status),
				Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: r,
			}, nil
		}
		return prev.RoundTrip(r)
	})
	t.Cleanup(func() { catalogHTTPClient.Transport = prev })
}
