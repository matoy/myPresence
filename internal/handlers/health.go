package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"presence-app/internal/db"
)

// HealthHandler handles the /healthz endpoint.
type HealthHandler struct {
	DB        *db.DB
	StartedAt time.Time
}

type healthResponse struct {
	Status string            `json:"status"`
	Uptime string            `json:"uptime"`
	Checks map[string]string `json:"checks"`
	Time   string            `json:"time"`
}

// Health responds with application and dependency health status.
// Returns HTTP 200 if all checks pass, HTTP 503 otherwise.
// This endpoint is public and requires no authentication.
// Route: GET /health
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{}
	healthy := true

	// Database connectivity check
	if err := h.DB.Ping(); err != nil {
		checks["database"] = "error: " + err.Error()
		healthy = false
	} else {
		checks["database"] = "ok"
	}

	status := "ok"
	code := http.StatusOK
	if !healthy {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	resp := healthResponse{
		Status: status,
		Uptime: time.Since(h.StartedAt).Round(time.Second).String(),
		Checks: checks,
		Time:   time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
