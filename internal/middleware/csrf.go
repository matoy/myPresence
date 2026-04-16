package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// GenerateCSRFToken derives a CSRF token from the raw session token and the application
// secret key. The token is bound to the session, so it expires naturally with the session.
func GenerateCSRFToken(secretKey, rawSessionToken string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(rawSessionToken))
	return hex.EncodeToString(mac.Sum(nil))
}

// ValidateCSRF returns a middleware that checks the csrf_token form field on all
// state-changing requests with a form-encoded body (application/x-www-form-urlencoded
// or multipart/form-data). JSON API calls are exempt: they are protected by CORS and
// the SameSite=Lax session cookie policy.
func ValidateCSRF(secretKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				ct := r.Header.Get("Content-Type")
				if strings.HasPrefix(ct, "application/x-www-form-urlencoded") ||
					strings.HasPrefix(ct, "multipart/form-data") {
					cookie, err := r.Cookie("session")
					if err != nil {
						http.Error(w, "Forbidden", http.StatusForbidden)
						return
					}
					expected := GenerateCSRFToken(secretKey, cookie.Value)
					given := r.FormValue("csrf_token")
					if !hmac.Equal([]byte(expected), []byte(given)) {
						http.Error(w, "Invalid CSRF token", http.StatusForbidden)
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
