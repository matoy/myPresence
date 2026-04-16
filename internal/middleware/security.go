package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// SecurityHeaders adds defensive HTTP response headers to every response.
// CSP allows CDN scripts/styles used by the application (Tailwind, Alpine.js,
// Google Fonts) while blocking everything else by default.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com https://cdn.jsdelivr.net; "+
				"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
				"font-src 'self' https://fonts.gstatic.com; "+
				"img-src 'self' data:; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none';",
		)
		next.ServeHTTP(w, r)
	})
}

// loginAttempt tracks failed login attempts for a single IP.
type loginAttempt struct {
	count     int
	firstFail time.Time
	blockedAt time.Time
}

const (
	loginMaxFailures   = 5                // attempts before block
	loginWindow        = 15 * time.Minute // rolling window
	loginBlockDuration = 15 * time.Minute // block duration after exceeding limit
)

// LoginRateLimiter is a per-IP failed-login rate limiter.
// It blocks an IP for loginBlockDuration after loginMaxFailures failures
// within loginWindow.
type LoginRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
}

// NewLoginRateLimiter creates a ready-to-use limiter and starts background cleanup.
func NewLoginRateLimiter() *LoginRateLimiter {
	l := &LoginRateLimiter{attempts: make(map[string]*loginAttempt)}
	go l.cleanupLoop()
	return l
}

// Allow returns true if the request is allowed (not rate-limited).
// Call RecordFailure on login failure and Reset on success.
func (l *LoginRateLimiter) Allow(r *http.Request) bool {
	ip := clientIP(r)
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.attempts[ip]
	if !ok {
		return true
	}

	// Still in block period?
	if !a.blockedAt.IsZero() && time.Since(a.blockedAt) < loginBlockDuration {
		return false
	}

	// Window expired — reset
	if time.Since(a.firstFail) > loginWindow {
		delete(l.attempts, ip)
		return true
	}

	return a.count < loginMaxFailures
}

// RecordFailure increments the failure counter for the request's IP.
func (l *LoginRateLimiter) RecordFailure(r *http.Request) {
	ip := clientIP(r)
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.attempts[ip]
	if !ok {
		l.attempts[ip] = &loginAttempt{count: 1, firstFail: time.Now()}
		return
	}

	// Reset window if expired
	if time.Since(a.firstFail) > loginWindow {
		a.count = 1
		a.firstFail = time.Now()
		a.blockedAt = time.Time{}
		return
	}

	a.count++
	if a.count >= loginMaxFailures {
		a.blockedAt = time.Now()
	}
}

// Reset clears the failure record for the request's IP on successful login.
func (l *LoginRateLimiter) Reset(r *http.Request) {
	ip := clientIP(r)
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip)
}

// cleanupLoop removes stale entries every 10 minutes.
func (l *LoginRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		for ip, a := range l.attempts {
			expired := time.Since(a.firstFail) > loginWindow
			blockExpired := a.blockedAt.IsZero() || time.Since(a.blockedAt) > loginBlockDuration
			if expired && blockExpired {
				delete(l.attempts, ip)
			}
		}
		l.mu.Unlock()
	}
}

// clientIP extracts the real client IP, checking X-Forwarded-For first.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first (leftmost) address — the original client
		if idx := len(xff); idx > 0 {
			for i := 0; i < len(xff); i++ {
				if xff[i] == ',' {
					xff = xff[:i]
					break
				}
			}
		}
		if ip := net.ParseIP(trimSpace(xff)); ip != nil {
			return ip.String()
		}
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
