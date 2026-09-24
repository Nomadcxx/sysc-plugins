package aiusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

type openCodeGoCollector struct {
	env  Env
	base string
}

type openCodeGoResponse struct {
	Usage struct {
		Rolling *openCodeGoWindow `json:"rolling"`
		Weekly  *openCodeGoWindow `json:"weekly"`
		Monthly *openCodeGoWindow `json:"monthly"`
	} `json:"usage"`
}

type openCodeGoWindow struct {
	Status   string   `json:"status"`
	Percent  *float64 `json:"percent"`
	ResetsAt string   `json:"resetsAt"`
}

func NewOpenCodeGo(env Env) Collector {
	return &openCodeGoCollector{env: env, base: "https://opencode.ai"}
}

func (c *openCodeGoCollector) ID() string { return "opencode-go" }

func (c *openCodeGoCollector) apiKey() (string, *ErrSetup) {
	tried := []string{"plugin setting opencode_go_api_key"}
	if key := strings.TrimSpace(c.env.key("opencode-go")); key != "" {
		return key, nil
	}
	tried = append(tried, "env OPENCODE_GO_API_KEY")
	if key := strings.TrimSpace(c.env.getenv("OPENCODE_GO_API_KEY")); key != "" {
		return key, nil
	}

	path := openCodeAuthPath(c.env)
	tried = append(tried, path+" (opencode API key)")
	if raw, err := os.ReadFile(path); err == nil {
		var auth struct {
			OpenCode struct {
				Type string `json:"type"`
				Key  string `json:"key"`
			} `json:"opencode"`
		}
		if json.Unmarshal(raw, &auth) == nil && auth.OpenCode.Type == "api" && strings.TrimSpace(auth.OpenCode.Key) != "" {
			// ponytail: reuse OpenCode's stored API key read-only; do not refresh or rewrite its credential store.
			return strings.TrimSpace(auth.OpenCode.Key), nil
		}
	}
	return "", &ErrSetup{Tried: tried}
}

func (c *openCodeGoCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	rep := ProviderReport{ID: c.ID(), Name: "OpenCode Go", UpdatedAt: c.env.now()}
	key, setup := c.apiKey()
	if setup != nil {
		rep.State, rep.Err = StateNeedsSetup, setup.Error()
		return rep, setup
	}
	var wire openCodeGoResponse
	err := bearerGet(ctx, c.env, c.base+"/zen/go/v1/usage", key, &wire)
	if err != nil {
		var status *httpStatus
		if errors.As(err, &status) && status.code == http.StatusForbidden {
			rep.State = StateNoData
			rep.Err = "OpenCode Go subscription required (HTTP 403)"
			return rep, nil
		}
		rep.State = StateFault
		if errors.As(err, &status) {
			rep.Err = faultMessage(err)
		} else if isJSONDecodeError(err) {
			rep.Err = "OpenCode Go response was not valid usage JSON"
		} else {
			rep.Err = Scrub(faultMessage(err))
		}
		return rep, err
	}

	for _, item := range []struct {
		key, label, short string
		data              *openCodeGoWindow
	}{
		{"primary", "Rolling", "Roll", wire.Usage.Rolling},
		{"secondary", "Weekly", "Wk", wire.Usage.Weekly},
		{"tertiary", "Monthly", "Mo", wire.Usage.Monthly},
	} {
		window, ok := item.data.window(item.key, item.label, item.short)
		if !ok {
			rep.State = StateFault
			rep.Err = "OpenCode Go response was not a complete quota body"
			return rep, fmt.Errorf("OpenCode Go response was not a complete quota body")
		}
		rep.Windows = append(rep.Windows, window)
	}
	rep.State = StateFresh
	return rep, nil
}

func isJSONDecodeError(err error) bool {
	var syntax *json.SyntaxError
	var mismatch *json.UnmarshalTypeError
	return errors.As(err, &syntax) || errors.As(err, &mismatch)
}

func (w *openCodeGoWindow) window(key, label, short string) (Window, bool) {
	if w == nil || w.Percent == nil || math.IsNaN(*w.Percent) || math.IsInf(*w.Percent, 0) || *w.Percent < 0 || *w.Percent > 100 {
		return Window{}, false
	}
	if w.Status != "ok" && w.Status != "rate-limited" {
		return Window{}, false
	}
	reset, err := time.Parse(time.RFC3339, w.ResetsAt)
	if err != nil {
		return Window{}, false
	}
	display := ""
	if w.Status == "rate-limited" {
		display = "Rate limited"
	}
	// ponytail: the endpoint gives reset instants but not window lengths; keep
	// durations unknown so the UI omits pace until the API exposes them.
	return Window{
		Key: key, Label: label, ShortLabel: short, HasPercent: true,
		UsedPercent: *w.Percent, ResetsAt: reset, DisplayValue: display,
	}, true
}
