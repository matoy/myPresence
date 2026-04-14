package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"presence-app/internal/db"
	"presence-app/internal/middleware"
	"presence-app/internal/models"
)

// PATHandler handles Personal Access Token management.
type PATHandler struct {
	DB     *db.DB
	Render func(w http.ResponseWriter, r *http.Request, page string, data interface{})
}

// PATPage renders the token management page.
// Route: GET /settings/tokens
func (h *PATHandler) PATPage(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	pats, _ := h.DB.ListUserPATs(user.ID)
	if pats == nil {
		pats = []models.PersonalAccessToken{}
	}
	h.Render(w, r, "pat", map[string]interface{}{
		"Tokens": pats,
	})
}

// CreatePAT creates a new Personal Access Token for the authenticated user.
// Route: POST /api/tokens
func (h *PATHandler) CreatePAT(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	var req struct {
		Description string `json:"description"`
		ExpiresIn   int    `json:"expires_in"` // days; 0 = no expiry
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Requête invalide", http.StatusBadRequest)
		return
	}

	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" {
		jsonError(w, "La description est requise", http.StatusBadRequest)
		return
	}
	if len(req.Description) > 200 {
		jsonError(w, "Description trop longue (max 200 caractères)", http.StatusBadRequest)
		return
	}
	if req.ExpiresIn < 0 || req.ExpiresIn > 3650 {
		jsonError(w, "Durée invalide (0–3650 jours)", http.StatusBadRequest)
		return
	}

	var expiresAt *time.Time
	if req.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresIn) * 24 * time.Hour)
		expiresAt = &t
	}

	raw, pat, err := h.DB.CreatePAT(user.ID, req.Description, expiresAt)
	if err != nil {
		jsonError(w, "Erreur lors de la création du token", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]interface{}{
		"id":           pat.ID,
		"token":        raw, // raw token displayed exactly once
		"description":  pat.Description,
		"token_prefix": pat.TokenPrefix,
		"expires_at":   pat.ExpiresAt,
		"created_at":   pat.CreatedAt,
	})
}

// RevokePAT revokes a token owned by the authenticated user.
// Route: DELETE /api/tokens/{id}
func (h *PATHandler) RevokePAT(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		jsonError(w, "ID invalide", http.StatusBadRequest)
		return
	}

	if err := h.DB.RevokePAT(id, user.ID); err != nil {
		jsonError(w, "Token introuvable", http.StatusNotFound)
		return
	}

	jsonOK(w, map[string]string{"status": "ok"})
}

// ListPATs returns all PATs for the authenticated user as JSON.
// Route: GET /api/tokens
func (h *PATHandler) ListPATs(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	pats, err := h.DB.ListUserPATs(user.ID)
	if err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	if pats == nil {
		pats = []models.PersonalAccessToken{}
	}
	jsonOK(w, pats)
}
