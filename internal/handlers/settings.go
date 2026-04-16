package handlers

import (
	"net/http"
	"strconv"
	"time"

	"presence-app/internal/db"
	"presence-app/internal/middleware"
	"presence-app/internal/models"
)

// SettingsHandler handles personal user settings pages.
type SettingsHandler struct {
	DB     *db.DB
	Render func(w http.ResponseWriter, r *http.Request, page string, data interface{})
}

// MyLogsPage renders the current user's own presence and activity logs.
func (h *SettingsHandler) MyLogsPage(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

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

	logs, _ := h.DB.GetUserLogs(user.ID, since)
	adminLogs, _ := h.DB.GetAdminLogsByActor(user.ID, since)
	statuses, _ := h.DB.ListStatuses()

	// Only show admin actions section if user has a role beyond basic.
	// Uses model constants to avoid typos.
	hideAdminSection := !user.HasAnyRole(
		models.RoleGlobal,
		models.RoleTeamManager,
		models.RoleTeamLeader,
		models.RoleStatusManager,
		models.RoleActivityViewer,
		models.RoleFloorplanManager,
	)

	h.Render(w, r, "admin_user_logs", map[string]interface{}{
		"TargetUser":       user,
		"Logs":             logs,
		"AdminLogs":        adminLogs,
		"Statuses":         statuses,
		"Days":             days,
		"BackURL":          "/",
		"FilterBaseURL":    "/settings/my-logs",
		"HideAdminSection": hideAdminSection,
	})
}

// ChangePasswordPage renders the password change form (local accounts only).
func (h *SettingsHandler) ChangePasswordPage(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user != nil && !user.IsLocal {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.Render(w, r, "settings_change_password", map[string]interface{}{
		"Error":   r.URL.Query().Get("error"),
		"Success": r.URL.Query().Get("success"),
	})
}

// ChangePasswordPost processes the password change form.
func (h *SettingsHandler) ChangePasswordPost(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil || !user.IsLocal {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	current := r.FormValue("current_password")
	newPwd := r.FormValue("new_password")
	confirm := r.FormValue("confirm_password")

	if current == "" || newPwd == "" || confirm == "" {
		http.Redirect(w, r, "/settings/change-password?error=Tous+les+champs+sont+obligatoires", http.StatusSeeOther)
		return
	}
	if newPwd != confirm {
		http.Redirect(w, r, "/settings/change-password?error=Les+mots+de+passe+ne+correspondent+pas", http.StatusSeeOther)
		return
	}
	if len(newPwd) < 8 {
		http.Redirect(w, r, "/settings/change-password?error=Le+mot+de+passe+doit+faire+au+moins+8+caract%C3%A8res", http.StatusSeeOther)
		return
	}

	// Verify current password using bcrypt-aware comparison
	dbUser, err := h.DB.GetUserByID(user.ID)
	if err != nil || !h.DB.CheckPassword(dbUser.ID, dbUser.PasswordHash, current) {
		http.Redirect(w, r, "/settings/change-password?error=Mot+de+passe+actuel+incorrect", http.StatusSeeOther)
		return
	}

	if err := h.DB.SetUserPassword(user.ID, newPwd); err != nil {
		http.Redirect(w, r, "/settings/change-password?error=Erreur+lors+du+changement", http.StatusSeeOther)
		return
	}

	// Invalidate all other active sessions — other devices must re-authenticate.
	if cookie, err := r.Cookie("session"); err == nil {
		h.DB.DeleteUserSessions(user.ID, cookie.Value)
	}

	http.Redirect(w, r, "/settings/change-password?success=Mot+de+passe+modifi%C3%A9+avec+succ%C3%A8s", http.StatusSeeOther)
}
