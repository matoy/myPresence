package middleware

import (
	"context"
	"net/http"
	"presence-app/internal/db"
	"presence-app/internal/models"
)

type contextKey string

const userContextKey contextKey = "user"

// Auth is an authentication middleware that checks for a valid session.
func Auth(database *db.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
