package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"presence-app/internal/db"
	"presence-app/internal/middleware"
	"presence-app/internal/models"
)

// UsersAdminHandler handles local user account management.
type UsersAdminHandler struct {
	DB     *db.DB
	Render func(w http.ResponseWriter, r *http.Request, page string, data interface{})
}

// UsersPage renders the user management page.
func (h *UsersAdminHandler) UsersPage(w http.ResponseWriter, r *http.Request) {
	users, _ := h.DB.ListUsers()
	currentUser := middleware.GetUser(r)
	var currentUserID int64
	if currentUser != nil {
		currentUserID = currentUser.ID
	}
	h.Render(w, r, "admin_users", map[string]interface{}{
		"Users":         users,
		"AllRoles":      models.AllRoles,
		"CurrentUserID": currentUserID,
		"Error":         r.URL.Query().Get("error"),
	})
}

// CreateUser creates a new local user account.
func (h *UsersAdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	name := strings.TrimSpace(r.FormValue("name"))
	password := r.FormValue("password")

	if email == "" || name == "" || password == "" {
		http.Redirect(w, r, "/admin/users?error=All+fields+are+required", http.StatusSeeOther)
		return
	}
	if err := h.DB.CreateLocalUser(email, name, password); err != nil {
		http.Redirect(w, r, "/admin/users?error=Email+already+in+use", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// UpdateUser updates a user's email and display name.
func (h *UsersAdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	req.Email = strings.TrimSpace(req.Email)
	req.Name = strings.TrimSpace(req.Name)
	if req.Email == "" || req.Name == "" {
		jsonError(w, "Email and name are required", http.StatusBadRequest)
		return
	}
	if err := h.DB.UpdateLocalUser(id, req.Email, req.Name); err != nil {
		jsonError(w, "Email already in use", http.StatusConflict)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// SetPassword changes the password of a local account.
func (h *UsersAdminHandler) SetPassword(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Password == "" {
		jsonError(w, "Password is required", http.StatusBadRequest)
		return
	}
	if err := h.DB.SetUserPassword(id, req.Password); err != nil {
		jsonError(w, "Error updating password", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// SetDisabled enables or disables a user account.
func (h *UsersAdminHandler) SetDisabled(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	currentUser := middleware.GetUser(r)
	if currentUser != nil && currentUser.ID == id {
		jsonError(w, "You cannot disable your own account", http.StatusBadRequest)
		return
	}
	var req struct {
		Disabled bool `json:"disabled"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if err := h.DB.SetUserDisabled(id, req.Disabled); err != nil {
		jsonError(w, "Error updating user", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// DeleteUser permanently deletes a user account.
func (h *UsersAdminHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	currentUser := middleware.GetUser(r)
	if currentUser != nil && currentUser.ID == id {
		jsonError(w, "You cannot delete your own account", http.StatusBadRequest)
		return
	}
	if err := h.DB.DeleteLocalUser(id); err != nil {
		jsonError(w, "Error deleting user", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// UserLogsPage renders the presence log history for a specific user.
func (h *UsersAdminHandler) UserLogsPage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	targetUser, err := h.DB.GetUserByID(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	logs, _ := h.DB.GetUserLogs(id)
	h.Render(w, r, "admin_user_logs", map[string]interface{}{
		"TargetUser": targetUser,
		"Logs":       logs,
	})
}
