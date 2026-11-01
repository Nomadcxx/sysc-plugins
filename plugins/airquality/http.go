package airquality

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxBody caps how much a source may send. The largest response the plugin
// reads is the AirHealth SA3 dashboard at about 150 KB, so this is generous
// without letting a misconfigured URL stream indefinitely.
const maxBody = 4 << 20 // 4 MiB

// readLimited reads a response body under maxBody and reports a body that
// overran it rather than silently truncating JSON.
func readLimited(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("response larger than %d bytes", maxBody)
	}
	return body, nil
}

// ErrUnavailable marks a source that cannot serve this request right now. The
// caller keeps the previous reading rather than showing a failure, because a
// stale pollen count is more useful than no pollen count.
var ErrUnavailable = errors.New("source unavailable")

// ErrNoCoverage marks a valid location for which a regional source has no data.
var ErrNoCoverage = errors.New("source has no coverage at location")

// statusError turns a non-200 response into ErrUnavailable when the status
// suggests a transient or access problem, so callers can distinguish "try
// again later" from "the payload was malformed".
func statusError(source string, resp *http.Response) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	err := fmt.Errorf("%s: %s", source, resp.Status)
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}
