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

// NewUserPage renders the dedicated local account creation page.
func (h *UsersAdminHandler) NewUserPage(w http.ResponseWriter, r *http.Request) {
	h.Render(w, r, "admin_user_new", map[string]interface{}{
		"Error": r.URL.Query().Get("error"),
	})
}

// CreateUser creates a new local user account.
func (h *UsersAdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Name = strings.TrimSpace(req.Name)
	if req.Email == "" || req.Name == "" || req.Password == "" {
		jsonError(w, "All fields are required", http.StatusBadRequest)
		return
	}
	uid, err := h.DB.CreateLocalUser(req.Email, req.Name, req.Password)
	if err != nil {
		jsonError(w, "Email already in use", http.StatusConflict)
		return
	}
	currentUser := middleware.GetUser(r)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "user", uid, "create", req.Email)
	}
	jsonOK(w, map[string]interface{}{"id": uid, "status": "ok"})
}

// UpdateUser updates a user's email and display name.
func (h *UsersAdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
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
	currentUser := middleware.GetUser(r)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "user", id, "update", req.Email+" "+req.Name)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// SetPassword changes the password of a local account.
func (h *UsersAdminHandler) SetPassword(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	if req.Password == "" {
		jsonError(w, "Password is required", http.StatusBadRequest)
		return
	}
	if len(req.Password) < 8 {
		jsonError(w, "Password must be at least 8 characters", http.StatusBadRequest)
		return
	}
	if err := h.DB.SetUserPassword(id, req.Password); err != nil {
		jsonError(w, "Error updating password", http.StatusInternalServerError)
		return
	}
	currentUser := middleware.GetUser(r)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "user", id, "set_password", "")
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
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	if err := h.DB.SetUserDisabled(id, req.Disabled); err != nil {
		jsonError(w, "Error updating user", http.StatusInternalServerError)
		return
	}
	if currentUser != nil {
		action := "set_enabled"
		if req.Disabled {
			action = "set_disabled"
		}
		h.DB.LogAdminAction(currentUser.ID, "user", id, action, "")
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
	targetUser, _ := h.DB.GetUserByID(id)
	if err := h.DB.DeleteLocalUser(id); err != nil {
		jsonError(w, "Error deleting user", http.StatusInternalServerError)
		return
	}
	if currentUser != nil {
		details := ""
		if targetUser != nil {
			details = targetUser.Name + " <" + targetUser.Email + ">"
		}
		h.DB.LogAdminAction(currentUser.ID, "user", id, "delete", details)
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

	// Parse period filter: 7 (default), 30, 90, 0 = all
	daysParam := r.URL.Query().Get("days")
	days := 7
	if daysParam != "" {
		if v, err := strconv.Atoi(daysParam); err == nil && v >= 0 {
			days = v
		}
	}
	var since time.Time
	if days > 0 {
		since = time.Now().AddDate(0, 0, -days)
	}

	logs, _ := h.DB.GetUserLogs(id, since)
	adminLogs, _ := h.DB.GetAdminLogsByActor(id, since)
	statuses, _ := h.DB.ListStatuses()
	h.Render(w, r, "admin_user_logs", map[string]interface{}{
		"TargetUser":       targetUser,
		"Logs":             logs,
		"AdminLogs":        adminLogs,
		"Statuses":         statuses,
		"Days":             days,
		"FilterBaseURL":    "/admin/users/" + strconv.FormatInt(id, 10) + "/logs",
		"HideAdminSection": false,
	})
}
