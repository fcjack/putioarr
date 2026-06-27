package rest

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// errInvalidConfirmToken is returned when a destructive request lacks a valid confirm token.
var errInvalidConfirmToken = errors.New("missing or invalid confirmation token")

// BasicAuth returns middleware enforcing HTTP basic auth against the given credentials
// using constant-time comparison.
func BasicAuth(username, password string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="putioarr"`)
				http.Error(w, "authentication required", http.StatusUnauthorized)

				return
			}

			userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1
			passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(password)) == 1

			if !userMatch || !passMatch {
				http.Error(w, "invalid username or password", http.StatusUnauthorized)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// confirmTokenManager issues and validates short-lived, stateless confirmation tokens
// (HMAC over an expiry) used to guard destructive endpoints against CSRF-style requests.
type confirmTokenManager struct {
	secret []byte
	ttl    time.Duration
}

func newConfirmTokenManager(secret []byte, ttl time.Duration) *confirmTokenManager {
	return &confirmTokenManager{secret: secret, ttl: ttl}
}

func (m *confirmTokenManager) issue() string {
	payload := strconv.FormatInt(time.Now().Add(m.ttl).Unix(), 10)
	token := payload + "." + m.sign(payload)

	return base64.RawURLEncoding.EncodeToString([]byte(token))
}

func (m *confirmTokenManager) valid(token string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return false
	}

	payload, mac, found := strings.Cut(string(raw), ".")
	if !found {
		return false
	}

	exp, err := strconv.ParseInt(payload, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}

	return hmac.Equal([]byte(mac), []byte(m.sign(payload)))
}

func (m *confirmTokenManager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))

	return hex.EncodeToString(mac.Sum(nil))
}

// middleware rejects requests without a valid X-Confirm-Token header.
func (m *confirmTokenManager) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.valid(r.Header.Get("X-Confirm-Token")) {
			writeError(w, r, http.StatusForbidden, errInvalidConfirmToken)

			return
		}

		next.ServeHTTP(w, r)
	})
}
