package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/matoy/mypresence/internal/db"
	"github.com/matoy/mypresence/internal/metrics"
	"github.com/matoy/mypresence/internal/middleware"
	"github.com/matoy/mypresence/internal/models"
)

// NotificationsHandler handles in-app notification endpoints.
type NotificationsHandler struct {
	DB     *db.DB
	Render func(w http.ResponseWriter, r *http.Request, page string, data interface{})
}

// AcknowledgeNotification marks a notification as acknowledged for the current user.
func (h *NotificationsHandler) AcknowledgeNotification(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		jsonError(w, "Invalid notification ID", http.StatusBadRequest)
		return
	}

	if err := h.DB.AcknowledgeNotification(id, user.ID); err != nil {
		jsonError(w, "Failed to acknowledge notification", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]string{"status": "ok"})
}

// GetUnreadNotificationsAPI returns unacknowledged notifications for the current user.
func (h *NotificationsHandler) GetUnreadNotificationsAPI(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	notifs, err := h.DB.GetUnreadNotifications(user.ID)
	if err != nil {
		jsonError(w, "Failed to fetch notifications", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(notifs)
}

// AdminNotificationsPage renders the admin notifications management page.
func (h *NotificationsHandler) AdminNotificationsPage(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil || !user.HasRole(models.RoleGlobal) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	users, err := h.DB.ListUsers()
	if err != nil {
		http.Error(w, "Failed to list users", http.StatusInternalServerError)
		return
	}

	teams, err := h.DB.ListTeams()
	if err != nil {
		teams = nil
	}

	sites, err := h.DB.ListSites()
	if err != nil {
		sites = nil
	}

	recentNotifs, _ := h.DB.GetAllNotifications(200)

	h.Render(w, r, "admin_notifications", map[string]interface{}{
		"Users":         users,
		"Teams":         teams,
		"Sites":         sites,
		"Notifications": recentNotifs,
		"Success":       r.URL.Query().Get("success"),
		"Error":         r.URL.Query().Get("error"),
	})
}

// AdminSendNotification handles sending a notification from a global admin.
// Accepts both JSON and form data.
func (h *NotificationsHandler) AdminSendNotification(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser == nil || !currentUser.HasRole(models.RoleGlobal) {
		if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("Content-Type") == "application/json" {
			jsonError(w, "Forbidden", http.StatusForbidden)
		} else {
			http.Error(w, "Forbidden", http.StatusForbidden)
		}
		return
	}

	var notifType, title, message, link string
	var rawRecipients []string
	isJSON := strings.Contains(r.Header.Get("Content-Type"), "application/json")

	if isJSON {
		var payload struct {
			Recipient  string   `json:"recipient"` // "all", "team:<id>", "site:<id>", "user:<id>", comma-separated or user ID
			Recipients []string `json:"recipients"`
			UserIDs    []int64  `json:"user_ids"`
			TeamIDs    []int64  `json:"team_ids"`
			SiteIDs    []int64  `json:"site_ids"`
			UserID     int64    `json:"user_id"`
			Type       string   `json:"type"`
			Title      string   `json:"title"`
			Message    string   `json:"message"`
			Link       string   `json:"link"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			jsonError(w, "Invalid JSON payload", http.StatusBadRequest)
			return
		}
		if payload.Recipient != "" {
			for _, part := range strings.Split(payload.Recipient, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					rawRecipients = append(rawRecipients, trimmed)
				}
			}
		}
		for _, rStr := range payload.Recipients {
			for _, part := range strings.Split(rStr, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					rawRecipients = append(rawRecipients, trimmed)
				}
			}
		}
		for _, uid := range payload.UserIDs {
			if uid > 0 {
				rawRecipients = append(rawRecipients, fmt.Sprintf("user:%d", uid))
			}
		}
		for _, tid := range payload.TeamIDs {
			if tid > 0 {
				rawRecipients = append(rawRecipients, fmt.Sprintf("team:%d", tid))
			}
		}
		for _, sid := range payload.SiteIDs {
			if sid > 0 {
				rawRecipients = append(rawRecipients, fmt.Sprintf("site:%d", sid))
			}
		}
		if payload.UserID > 0 {
			rawRecipients = append(rawRecipients, strconv.FormatInt(payload.UserID, 10))
		}
		notifType = payload.Type
		title = payload.Title
		message = payload.Message
		link = payload.Link
	} else {
		_ = r.ParseForm()
		for _, rVal := range r.Form["recipient"] {
			for _, part := range strings.Split(rVal, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					rawRecipients = append(rawRecipients, trimmed)
				}
			}
		}
		if len(rawRecipients) == 0 {
			if recVal := r.FormValue("recipient"); recVal != "" {
				for _, part := range strings.Split(recVal, ",") {
					if trimmed := strings.TrimSpace(part); trimmed != "" {
						rawRecipients = append(rawRecipients, trimmed)
					}
				}
			}
		}
		for _, rVal := range r.Form["recipients"] {
			for _, part := range strings.Split(rVal, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					rawRecipients = append(rawRecipients, trimmed)
				}
			}
		}
		if uVal := r.FormValue("user_id"); uVal != "" {
			rawRecipients = append(rawRecipients, uVal)
		}
		for _, uVal := range r.Form["user_ids"] {
			rawRecipients = append(rawRecipients, "user:"+uVal)
		}
		for _, tVal := range r.Form["team_ids"] {
			rawRecipients = append(rawRecipients, "team:"+tVal)
		}
		for _, sVal := range r.Form["site_ids"] {
			rawRecipients = append(rawRecipients, "site:"+sVal)
		}
		notifType = r.FormValue("type")
		title = r.FormValue("title")
		message = r.FormValue("message")
		link = r.FormValue("link")
	}

	title = strings.TrimSpace(title)
	message = strings.TrimSpace(message)
	link = strings.TrimSpace(link)
	notifType = strings.TrimSpace(notifType)
	if notifType == "" {
		notifType = "info"
	}

	if len(rawRecipients) == 0 {
		if isJSON {
			jsonError(w, "Recipient is required", http.StatusBadRequest)
		} else {
			http.Redirect(w, r, "/admin/notifications?error=recipient_required", http.StatusSeeOther)
		}
		return
	}
	if title == "" {
		if isJSON {
			jsonError(w, "Title is required", http.StatusBadRequest)
		} else {
			http.Redirect(w, r, "/admin/notifications?error=title_required", http.StatusSeeOther)
		}
		return
	}
	if message == "" {
		if isJSON {
			jsonError(w, "Message is required", http.StatusBadRequest)
		} else {
			http.Redirect(w, r, "/admin/notifications?error=message_required", http.StatusSeeOther)
		}
		return
	}

	isAll := false
	targetUserIDs := make(map[int64]bool)

	for _, token := range rawRecipients {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if token == "all" {
			isAll = true
			continue
		}
		if strings.HasPrefix(token, "team:") {
			teamIDStr := strings.TrimPrefix(token, "team:")
			teamID, err := strconv.ParseInt(teamIDStr, 10, 64)
			if err != nil || teamID <= 0 {
				if isJSON {
					jsonError(w, "Invalid recipient ID", http.StatusBadRequest)
				} else {
					http.Redirect(w, r, "/admin/notifications?error=invalid_recipient", http.StatusSeeOther)
				}
				return
			}
			members, err := h.DB.GetTeamMembers(teamID)
			if err != nil {
				if isJSON {
					jsonError(w, "Failed to load team members", http.StatusInternalServerError)
				} else {
					http.Redirect(w, r, "/admin/notifications?error=server_error", http.StatusSeeOther)
				}
				return
			}
			for _, m := range members {
				if !m.Disabled {
					targetUserIDs[m.ID] = true
				}
			}
		} else if strings.HasPrefix(token, "site:") {
			siteIDStr := strings.TrimPrefix(token, "site:")
			siteID, err := strconv.ParseInt(siteIDStr, 10, 64)
			if err != nil || siteID <= 0 {
				if isJSON {
					jsonError(w, "Invalid recipient ID", http.StatusBadRequest)
				} else {
					http.Redirect(w, r, "/admin/notifications?error=invalid_recipient", http.StatusSeeOther)
				}
				return
			}
			users, err := h.DB.GetUsersBySite(siteID)
			if err != nil {
				if isJSON {
					jsonError(w, "Failed to load site users", http.StatusInternalServerError)
				} else {
					http.Redirect(w, r, "/admin/notifications?error=server_error", http.StatusSeeOther)
				}
				return
			}
			for _, u := range users {
				if !u.Disabled {
					targetUserIDs[u.ID] = true
				}
			}
		} else {
			cleanIDStr := strings.TrimPrefix(token, "user:")
			targetUserID, err := strconv.ParseInt(cleanIDStr, 10, 64)
			if err != nil || targetUserID <= 0 {
				if isJSON {
					jsonError(w, "Invalid recipient ID", http.StatusBadRequest)
				} else {
					http.Redirect(w, r, "/admin/notifications?error=invalid_recipient", http.StatusSeeOther)
				}
				return
			}
			targetUserIDs[targetUserID] = true
		}
	}

	if isAll {
		users, err := h.DB.ListUsers()
		if err != nil {
			if isJSON {
				jsonError(w, "Failed to load users", http.StatusInternalServerError)
			} else {
				http.Redirect(w, r, "/admin/notifications?error=server_error", http.StatusSeeOther)
			}
			return
		}
		for _, u := range users {
			if !u.Disabled {
				targetUserIDs[u.ID] = true
			}
		}
	}

	count := 0
	var lastErr error
	for uid := range targetUserIDs {
		_, err := h.DB.CreateNotification(uid, currentUser.ID, notifType, title, message, link)
		if err != nil {
			lastErr = err
		} else {
			count++
		}
	}

	if len(targetUserIDs) > 0 && count == 0 && lastErr != nil {
		metrics.AdminOpsTotal.WithLabelValues("notification", "send", "failure").Inc()
		if isJSON {
			jsonError(w, "Failed to create notification", http.StatusInternalServerError)
		} else {
			http.Redirect(w, r, "/admin/notifications?error=send_failed", http.StatusSeeOther)
		}
		return
	}

	metrics.AdminOpsTotal.WithLabelValues("notification", "send", "success").Inc()
	if isJSON {
		jsonOK(w, map[string]interface{}{
			"status": "ok",
			"count":  count,
		})
	} else {
		http.Redirect(w, r, "/admin/notifications?success=1", http.StatusSeeOther)
	}
}

// AdminDeleteNotification handles deletion of a notification from the admin panel.
func (h *NotificationsHandler) AdminDeleteNotification(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser == nil || !currentUser.HasRole(models.RoleGlobal) {
		metrics.AdminOpsTotal.WithLabelValues("notification", "delete", "failure").Inc()
		if strings.Contains(r.Header.Get("Accept"), "application/json") || strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			jsonError(w, "Forbidden", http.StatusForbidden)
		} else {
			http.Error(w, "Forbidden", http.StatusForbidden)
		}
		return
	}

	idStr := r.PathValue("id")
	if idStr == "" {
		idStr = r.FormValue("id")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		metrics.AdminOpsTotal.WithLabelValues("notification", "delete", "failure").Inc()
		if strings.Contains(r.Header.Get("Accept"), "application/json") || strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			jsonError(w, "Invalid notification ID", http.StatusBadRequest)
		} else {
			http.Redirect(w, r, "/admin/notifications?error=invalid_id", http.StatusSeeOther)
		}
		return
	}

	if err := h.DB.DeleteNotification(id); err != nil {
		metrics.AdminOpsTotal.WithLabelValues("notification", "delete", "failure").Inc()
		if strings.Contains(r.Header.Get("Accept"), "application/json") || strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			jsonError(w, "Failed to delete notification", http.StatusInternalServerError)
		} else {
			http.Redirect(w, r, "/admin/notifications?error=delete_failed", http.StatusSeeOther)
		}
		return
	}

	metrics.AdminOpsTotal.WithLabelValues("notification", "delete", "success").Inc()
	if strings.Contains(r.Header.Get("Accept"), "application/json") || strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		jsonOK(w, map[string]string{"status": "ok"})
	} else {
		http.Redirect(w, r, "/admin/notifications?deleted=1", http.StatusSeeOther)
	}
}
