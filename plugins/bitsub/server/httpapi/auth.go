// Package httpapi serves bitsubd's HTTP front doors: the MCP endpoint and
// the plugin's JSON API, both behind per-client bearer tokens.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
)

// Token is a named client credential; only the token's sha256 is kept.
type Token struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type Auth struct {
	tokens []Token
}

func NewAuth(tokens []Token) *Auth { return &Auth{tokens: tokens} }

type actorKey struct{}

// ActorFrom is the authenticated token's name, or "" outside Wrap.
func ActorFrom(ctx context.Context) string {
	name, _ := ctx.Value(actorKey{}).(string)
	return name
}

// Wrap admits requests carrying a known bearer token, comparing every token
// in constant time.
func (a *Auth) Wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plain, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		name := ""
		if ok && plain != "" {
			sum := sha256.Sum256([]byte(plain))
			got := []byte(hex.EncodeToString(sum[:]))
			for _, t := range a.tokens {
				if subtle.ConstantTimeCompare(got, []byte(strings.ToLower(t.SHA256))) == 1 {
					name = t.Name
				}
			}
		}
		if name == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, name)))
	})
}

// GenerateToken returns a new "bsk_" token and its sha256 hex.
func GenerateToken() (plain, sha string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plain = "bsk_" + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(plain))
	return plain, hex.EncodeToString(sum[:]), nil
}
