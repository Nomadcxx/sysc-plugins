package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/service"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

const maxBody = 16 << 10

// NewHandler mounts /mcp and /api/v1, everything behind auth.
func NewHandler(svc *service.Service, auth *Auth, mcp http.Handler) http.Handler {
	mux := http.NewServeMux()
	if mcp != nil {
		mux.Handle("/mcp", mcp)
	}
	mux.HandleFunc("GET /api/v1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		var since int64
		if v := r.URL.Query().Get("since"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				writeError(w, &service.ValidationError{Field: "since", Reason: "want a non-negative integer"})
				return
			}
			since = n
		}
		snap, err := svc.Snapshot(r.Context(), since)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, snap)
	})
	mux.HandleFunc("POST /api/v1/subscriptions/{id}/intent", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Intent subs.Intent `json:"intent"`
		}
		if !decode(w, r, &body, false) {
			return
		}
		_, err := svc.SetIntent(r.Context(), ActorFrom(r.Context()), r.PathValue("id"), body.Intent)
		respond(w, err)
	})
	mux.HandleFunc("POST /api/v1/subscriptions/{id}/confirm-cancelled", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AccessUntil string `json:"access_until"`
		}
		if !decode(w, r, &body, true) {
			return
		}
		_, err := svc.ConfirmCancelled(r.Context(), ActorFrom(r.Context()), r.PathValue("id"), body.AccessUntil)
		respond(w, err)
	})
	return auth.Wrap(mux)
}

// decode reads one JSON object (at most maxBody, no unknown fields). With
// allowEmpty an empty body leaves v untouched.
func decode(w http.ResponseWriter, r *http.Request, v any, allowEmpty bool) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if errors.Is(err, io.EOF) && allowEmpty {
		return true
	}
	if err == nil {
		if _, extra := dec.Token(); extra != io.EOF {
			err = errors.New("trailing data after the JSON object")
		}
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return false
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body: " + err.Error()})
		return false
	}
	return true
}

func respond(w http.ResponseWriter, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeError(w http.ResponseWriter, err error) {
	var ve *service.ValidationError
	var dup *service.DuplicateError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ve.Reason, "field": ve.Field})
	case errors.As(err, &dup):
		writeJSON(w, http.StatusConflict, map[string]string{"error": dup.Error(), "existing_id": dup.Existing.ID})
	case errors.Is(err, service.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
	default:
		log.Printf("bitsubd: api: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
