package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"presence-app/internal/db"
	"presence-app/internal/models"
)

type contextKey string

const userContextKey contextKey = "user"

// Auth is an authentication middleware that checks for a valid session or a Bearer PAT.
// Set bearerEnabled=false to disable Personal Access Token authentication (API disabled mode).
func Auth(database *db.DB, next http.Handler) http.Handler {
	return AuthWithOptions(database, true, next)
}

// AuthWithOptions is Auth with explicit control over PAT Bearer token support.
func AuthWithOptions(database *db.DB, bearerEnabled bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// --- Personal Access Token (Bearer) ---
		if bearerEnabled {
			if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
				token := strings.TrimPrefix(authHeader, "Bearer ")
				user, err := database.GetUserByPAT(token)
				if err != nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					w.Write([]byte(`{"error":"token invalide ou expiré"}`)) //nolint:errcheck
					return
				}
				ctx := context.WithValue(r.Context(), userContextKey, user)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// --- Session cookie ---
		cookie, err := r.Cookie("session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		user, err := database.GetSessionUser(cookie.Value)
		if err != nil {
			http.SetCookie(w, &http.Cookie{Name: "session", MaxAge: -1, Path: "/"})
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole restricts access to users with any of the specified roles.
// Users with the "global" role always pass.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := GetUser(r)
			if user == nil || !user.HasAnyRole(roles...) {
				http.Error(w, "Accès refusé", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetUser extracts the user from the request context.
func GetUser(r *http.Request) *models.User {
	u, _ := r.Context().Value(userContextKey).(*models.User)
	return u
}

// OptionalAuth tries to load the user but doesn't require it.
func OptionalAuth(database *db.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err == nil {
			user, err := database.GetSessionUser(cookie.Value)
			if err == nil {
				ctx := context.WithValue(r.Context(), userContextKey, user)
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// responseWriter captures the HTTP status code written by a handler.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// AccessLog logs every HTTP request as a structured JSON line to stdout.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		// Skip metrics and health-check endpoints to avoid log noise
		if r.URL.Path == "/metrics" || r.URL.Path == "/health" {
			return
		}

		// Resolve remote IP (honour X-Forwarded-For set by a trusted proxy)
		ip := r.RemoteAddr
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			ip = strings.SplitN(fwd, ",", 2)[0]
		}

		slog.Info("http.request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", ip,
		)
	})
}
