package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/matoy/mypresence/internal/config"
	"github.com/matoy/mypresence/internal/db"
	"github.com/matoy/mypresence/internal/metrics"
	"github.com/matoy/mypresence/internal/middleware"
	"github.com/matoy/mypresence/internal/models"
)

// ReminderRunner runs automated reminder checks for teams.
type ReminderRunner interface {
	CheckAndSendReminders(now time.Time, teamIDFilter int64, force bool) (presenceSent, activitySent int, err error)
}

// AdminHandler handles all admin pages and API endpoints.
type AdminHandler struct {
	DB               *db.DB
	Config           *config.Config
	Render           func(w http.ResponseWriter, r *http.Request, page string, data interface{})
	RemindersService ReminderRunner
}

// --- Team management ---

// TeamsPage renders the team management page.
func (h *AdminHandler) TeamsPage(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	teams, _ := h.DB.ListTeams()
	users, _ := h.DB.ListUsers()
	sites, _ := h.DB.ListSites()
	domains, _ := h.DB.ListDomains()

	canManageTeams := currentUser != nil && currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal)
	var ledTeamIDs map[int64]bool
	if !canManageTeams && currentUser != nil {
		ids, _ := h.DB.GetLedTeamIDs(currentUser.ID)
		ledTeamIDs = make(map[int64]bool, len(ids))
		for _, id := range ids {
			ledTeamIDs[id] = true
		}
	}

	type TeamWithMembers struct {
		Team      models.Team
		Members   []models.TeamMember
		LeaderIDs []int64
		Leaders   []models.User
		CanEdit   bool
	}

	var teamsList []TeamWithMembers
	for _, t := range teams {
		if !canManageTeams && !ledTeamIDs[t.ID] {
			continue
		}
		members, _ := h.DB.GetAllTeamMembers(t.ID)
		leaderIDs, _ := h.DB.GetTeamLeaderIDs(t.ID)
		leaders, _ := h.DB.ListTeamLeaders(t.ID)
		if leaderIDs == nil {
			leaderIDs = []int64{}
		}
		if leaders == nil {
			leaders = []models.User{}
		}
		canEdit := canManageTeams || ledTeamIDs[t.ID]
		teamsList = append(teamsList, TeamWithMembers{
			Team:      t,
			Members:   members,
			LeaderIDs: leaderIDs,
			Leaders:   leaders,
			CanEdit:   canEdit,
		})
	}

	h.Render(w, r, "admin_teams", map[string]interface{}{
		"Teams":          teamsList,
		"Users":          users,
		"Sites":          sites,
		"Domains":        domains,
		"Countries":      models.AllCountries,
		"CanManageTeams": canManageTeams,
		"IsGlobalAdmin":  currentUser != nil && currentUser.HasRole(models.RoleGlobal),
		"JiraEnabled":    h.Config != nil && h.Config.JiraEnabled,
	})
}

// ListTeamsAPI returns all teams as JSON.
func (h *AdminHandler) ListTeamsAPI(w http.ResponseWriter, r *http.Request) {
	teams, err := h.DB.ListTeams()
	if err != nil {
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}
	jsonOK(w, teams)
}

// CreateTeam creates a new team.
func (h *AdminHandler) CreateTeam(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser != nil && !currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal) {
		metrics.AdminOpsTotal.WithLabelValues("team", "create", "failure").Inc()
		jsonError(w, "Access denied", http.StatusForbidden)
		return
	}
	var req struct {
		Name                      string `json:"name"`
		JiraSpaceKey              string `json:"jira_space_key"`
		TimesheetsManagedManually bool   `json:"timesheets_managed_manually"`
		RequireActivityComment    bool   `json:"require_activity_comment"`
		DomainID                  int64  `json:"domain_id"`
		CountryCodes              string `json:"country_codes"`
		RemindPresence            bool   `json:"remind_presence"`
		PresenceReminderDays      int    `json:"presence_reminder_days"`
		RemindActivity            bool   `json:"remind_activity"`
		ActivityReminderDays      int    `json:"activity_reminder_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		metrics.AdminOpsTotal.WithLabelValues("team", "create", "failure").Inc()
		jsonError(w, "name required", http.StatusBadRequest)
		return
	}
	id, err := h.DB.CreateTeamWithDetails(strings.TrimSpace(req.Name), strings.TrimSpace(req.JiraSpaceKey), req.TimesheetsManagedManually, req.RequireActivityComment, req.CountryCodes)
	if err != nil {
		metrics.AdminOpsTotal.WithLabelValues("team", "create", "failure").Inc()
		jsonError(w, "Erreur création équipe", http.StatusInternalServerError)
		return
	}
	if req.DomainID > 0 {
		h.DB.UpdateTeamDomain(id, req.DomainID) //nolint:errcheck
	}
	if req.RemindPresence || req.RemindActivity {
		h.DB.UpdateTeamReminders(id, req.RemindPresence, req.PresenceReminderDays, req.RemindActivity, req.ActivityReminderDays) //nolint:errcheck
	}
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "team", id, "create", req.Name)
		slog.Info("admin.team.create", "actor", currentUser.Email, "team", req.Name, "team_id", id)
	}
	metrics.AdminOpsTotal.WithLabelValues("team", "create", "success").Inc()
	jsonOK(w, map[string]interface{}{"id": id, "status": "ok"})
}

// DeleteTeam deletes a team.
func (h *AdminHandler) DeleteTeam(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser != nil && !currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal) {
		metrics.AdminOpsTotal.WithLabelValues("team", "delete", "failure").Inc()
		jsonError(w, "Access denied", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	teamName := h.DB.GetTeamName(id)
	h.DB.DeleteTeam(id) //nolint:errcheck
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "team", id, "delete", teamName)
		slog.Info("admin.team.delete", "actor", currentUser.Email, "team", teamName, "team_id", id)
	}
	metrics.AdminOpsTotal.WithLabelValues("team", "delete", "success").Inc()
	jsonOK(w, map[string]string{"status": "ok"})
}

// UpdateTeam renames a team and updates its settings.
func (h *AdminHandler) UpdateTeam(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser != nil && !currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal) {
		metrics.AdminOpsTotal.WithLabelValues("team", "update", "failure").Inc()
		jsonError(w, "Access denied", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Name                      string `json:"name"`
		JiraSpaceKey              string `json:"jira_space_key"`
		TimesheetsManagedManually bool   `json:"timesheets_managed_manually"`
		RequireActivityComment    bool   `json:"require_activity_comment"`
		DomainID                  int64  `json:"domain_id"`
		CountryCodes              string `json:"country_codes"`
		RemindPresence            bool   `json:"remind_presence"`
		PresenceReminderDays      int    `json:"presence_reminder_days"`
		RemindActivity            bool   `json:"remind_activity"`
		ActivityReminderDays      int    `json:"activity_reminder_days"`
	}
	json.NewDecoder(r.Body).Decode(&req)                                                                                                                   //nolint:errcheck
	h.DB.UpdateTeamDetails(id, req.Name, strings.TrimSpace(req.JiraSpaceKey), req.TimesheetsManagedManually, req.RequireActivityComment, req.CountryCodes) //nolint:errcheck
	h.DB.UpdateTeamDomain(id, req.DomainID)                                                                                                                //nolint:errcheck
	h.DB.UpdateTeamReminders(id, req.RemindPresence, req.PresenceReminderDays, req.RemindActivity, req.ActivityReminderDays)                                 //nolint:errcheck
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "team", id, "update", req.Name)
		slog.Info("admin.team.update", "actor", currentUser.Email, "team", req.Name, "team_id", id)
	}
	metrics.AdminOpsTotal.WithLabelValues("team", "update", "success").Inc()
	jsonOK(w, map[string]string{"status": "ok"})
}

// TriggerTeamReminders triggers an immediate reminder evaluation for a team (Global Admin only).
func (h *AdminHandler) TriggerTeamReminders(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser == nil || !currentUser.HasRole(models.RoleGlobal) {
		jsonError(w, "Access denied", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		jsonError(w, "Invalid team ID", http.StatusBadRequest)
		return
	}
	if h.RemindersService == nil {
		jsonError(w, "Reminders service unavailable", http.StatusServiceUnavailable)
		return
	}
	presenceSent, activitySent, err := h.RemindersService.CheckAndSendReminders(time.Now(), id, true)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"status":        "ok",
		"presence_sent": presenceSent,
		"activity_sent": activitySent,
	})
}

// AddTeamMember adds a user to a team.
func (h *AdminHandler) AddTeamMember(w http.ResponseWriter, r *http.Request) {
	teamID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	currentUser := middleware.GetUser(r)
	if currentUser != nil && !currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal) {
		isLeader, _ := h.DB.IsLeaderOfTeam(currentUser.ID, teamID)
		if !isLeader {
			metrics.AdminOpsTotal.WithLabelValues("team", "add_member", "failure").Inc()
			jsonError(w, "Access denied", http.StatusForbidden)
			return
		}
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	memberName := strconv.FormatInt(req.UserID, 10)
	if u, _ := h.DB.GetUserByID(req.UserID); u != nil {
		memberName = u.Name
	}
	h.DB.AddTeamMember(teamID, req.UserID) //nolint:errcheck
	var actorID int64
	actorName := "Admin"
	if currentUser != nil {
		actorID = currentUser.ID
		if currentUser.Name != "" {
			actorName = currentUser.Name
		}
		h.DB.LogAdminAction(currentUser.ID, "team", teamID, "add_member", memberName)
		slog.Info("admin.team.add_member", "actor", currentUser.Email, "team_id", teamID, "member", memberName)
	}
	teamName := h.DB.GetTeamName(teamID)
	if teamName == "" {
		teamName = strconv.FormatInt(teamID, 10)
	}
	notifTitle := "Ajout à une équipe"
	notifMsg := fmt.Sprintf("Vous avez été ajouté à l'équipe « %s » par %s.", teamName, actorName)
	h.DB.CreateNotification(req.UserID, actorID, "team_added", notifTitle, notifMsg, teamName) //nolint:errcheck

	metrics.AdminOpsTotal.WithLabelValues("team", "add_member", "success").Inc()
	jsonOK(w, map[string]string{"status": "ok"})
}

// RemoveTeamMember removes a user from a team.
func (h *AdminHandler) RemoveTeamMember(w http.ResponseWriter, r *http.Request) {
	teamID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	currentUser := middleware.GetUser(r)
	if currentUser != nil && !currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal) {
		isLeader, _ := h.DB.IsLeaderOfTeam(currentUser.ID, teamID)
		if !isLeader {
			metrics.AdminOpsTotal.WithLabelValues("team", "remove_member", "failure").Inc()
			jsonError(w, "Access denied", http.StatusForbidden)
			return
		}
	}
	userID, _ := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	memberName := strconv.FormatInt(userID, 10)
	if u, _ := h.DB.GetUserByID(userID); u != nil {
		memberName = u.Name
	}
	h.DB.RemoveTeamMember(teamID, userID) //nolint:errcheck
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "team", teamID, "remove_member", memberName)
		slog.Info("admin.team.remove_member", "actor", currentUser.Email, "team_id", teamID, "member", memberName)
	}
	metrics.AdminOpsTotal.WithLabelValues("team", "remove_member", "success").Inc()
	jsonOK(w, map[string]string{"status": "ok"})
}

// UpdateUserSite updates a user's assigned site.
func (h *AdminHandler) UpdateUserSite(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	targetUserID, _ := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if targetUserID == 0 {
		targetUserID, _ = strconv.ParseInt(r.PathValue("id"), 10, 64)
	}

	canManage := currentUser != nil && currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal)
	if !canManage && currentUser != nil {
		// Check if currentUser is a leader of any team that targetUserID belongs to
		ledTeams, _ := h.DB.GetLedTeamIDs(currentUser.ID)
		if len(ledTeams) > 0 {
			userTeams, _ := h.DB.GetUserTeams(targetUserID)
			for _, ut := range userTeams {
				for _, lt := range ledTeams {
					if ut.ID == lt {
						canManage = true
						break
					}
				}
				if canManage {
					break
				}
			}
		}
	}

	if !canManage {
		metrics.AdminOpsTotal.WithLabelValues("user", "update_site", "failure").Inc()
		jsonError(w, "Access denied", http.StatusForbidden)
		return
	}

	var req struct {
		SiteID int64 `json:"site_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if err := h.DB.UpdateUserSite(targetUserID, req.SiteID); err != nil {
		slog.Error("admin.user.update_site", "error", err)
		metrics.AdminOpsTotal.WithLabelValues("user", "update_site", "failure").Inc()
		jsonError(w, "Database error", http.StatusInternalServerError)
		return
	}

	siteName := ""
	siteCountry := ""
	if req.SiteID > 0 {
		if s, _ := h.DB.GetSite(req.SiteID); s != nil {
			siteName = s.Name
			siteCountry = s.CountryCode
		}
	}

	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "user", targetUserID, "update_site", fmt.Sprintf("site_id=%d", req.SiteID))
		slog.Info("admin.user.update_site", "actor", currentUser.Email, "target_user_id", targetUserID, "site_id", req.SiteID)
	}

	metrics.AdminOpsTotal.WithLabelValues("user", "update_site", "success").Inc()
	jsonOK(w, map[string]interface{}{
		"status":            "ok",
		"site_id":           req.SiteID,
		"site_name":         siteName,
		"site_country_code": siteCountry,
	})
}

// SetTeamMemberLeftAt sets or clears the departure date for a team member.
func (h *AdminHandler) SetTeamMemberLeftAt(w http.ResponseWriter, r *http.Request) {
	teamID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	userID, _ := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	currentUser := middleware.GetUser(r)

	canManageTeams := currentUser != nil && currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal)
	if !canManageTeams {
		isLeader := false
		if currentUser != nil {
			isLeader, _ = h.DB.IsLeaderOfTeam(currentUser.ID, teamID)
		}
		if !isLeader {
			metrics.AdminOpsTotal.WithLabelValues("team", "set_left_at", "failure").Inc()
			jsonError(w, "Access denied", http.StatusForbidden)
			return
		}
	}

	var req struct {
		LeftAt *string `json:"left_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		metrics.AdminOpsTotal.WithLabelValues("team", "set_left_at", "failure").Inc()
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.LeftAt != nil {
		if _, err := time.Parse("2006-01-02", *req.LeftAt); err != nil {
			metrics.AdminOpsTotal.WithLabelValues("team", "set_left_at", "failure").Inc()
			jsonError(w, "Invalid date format (YYYY-MM-DD expected)", http.StatusBadRequest)
			return
		}
	}

	if err := h.DB.SetTeamMemberLeftAt(teamID, userID, req.LeftAt); err != nil {
		metrics.AdminOpsTotal.WithLabelValues("team", "set_left_at", "failure").Inc()
		jsonError(w, "Erreur", http.StatusInternalServerError)
		return
	}

	memberName := strconv.FormatInt(userID, 10)
	if u, _ := h.DB.GetUserByID(userID); u != nil {
		memberName = u.Name
	}
	action := "clear_left_at"
	details := memberName
	if req.LeftAt != nil {
		action = "set_left_at"
		details = memberName + " left_at=" + *req.LeftAt
	}
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "team", teamID, action, details)
		slog.Info("admin.team."+action, "actor", currentUser.Email, "team_id", teamID, "member", memberName)
	}
	metrics.AdminOpsTotal.WithLabelValues("team", "set_left_at", "success").Inc()
	jsonOK(w, map[string]string{"status": "ok"})
}

// GetTeamLeadersAPI returns the user IDs designated as leaders of a team.
// GET /api/admin/teams/{id}/leaders
func (h *AdminHandler) GetTeamLeadersAPI(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, "Invalid ID", http.StatusBadRequest)
		return
	}
	userIDs, err := h.DB.GetTeamLeaderIDs(id)
	if err != nil {
		slog.Error("admin.team.leaders.get", "error", err)
		jsonError(w, "Server error", http.StatusInternalServerError)
		return
	}
	if userIDs == nil {
		userIDs = []int64{}
	}
	jsonOK(w, map[string]interface{}{"user_ids": userIDs})
}

// SetTeamLeadersAPI replaces the full leader list of a team.
// PUT /api/admin/teams/{id}/leaders
func (h *AdminHandler) SetTeamLeadersAPI(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser != nil && !currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal) {
		metrics.AdminOpsTotal.WithLabelValues("team", "set_leaders", "failure").Inc()
		jsonError(w, "Access denied", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, "Invalid ID", http.StatusBadRequest)
		return
	}
	var req struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if err := h.DB.SetTeamLeaders(id, req.UserIDs); err != nil {
		slog.Error("admin.team.leaders.set", "error", err)
		jsonError(w, "Server error", http.StatusInternalServerError)
		return
	}
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "team", id, "set_leaders", fmt.Sprintf("count=%d", len(req.UserIDs)))
		slog.Info("admin.team.leaders.set", "actor", currentUser.Email, "team_id", id, "count", len(req.UserIDs))
	}
	metrics.AdminOpsTotal.WithLabelValues("team", "set_leaders", "success").Inc()
	jsonOK(w, map[string]string{"status": "ok"})
}

// --- Status management ---

// StatusesPage renders the status management page.
func (h *AdminHandler) StatusesPage(w http.ResponseWriter, r *http.Request) {
	statuses, _ := h.DB.ListStatuses()
	h.Render(w, r, "admin_statuses", map[string]interface{}{
		"Statuses": statuses,
	})
}

// CreateStatus adds a new status.
func (h *AdminHandler) CreateStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Color     string `json:"color"`
		Billable  bool   `json:"billable"`
		OnSite    bool   `json:"on_site"`
		SortOrder int    `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		metrics.AdminOpsTotal.WithLabelValues("status", "create", "failure").Inc()
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Color == "" {
		metrics.AdminOpsTotal.WithLabelValues("status", "create", "failure").Inc()
		jsonError(w, "Name and color are required", http.StatusBadRequest)
		return
	}
	id, err := h.DB.CreateStatus(models.Status{Name: req.Name, Color: req.Color, Billable: req.Billable, OnSite: req.OnSite, SortOrder: req.SortOrder})
	if err != nil {
		metrics.AdminOpsTotal.WithLabelValues("status", "create", "failure").Inc()
		jsonError(w, "Error creating status", http.StatusInternalServerError)
		return
	}
	currentUser := middleware.GetUser(r)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "status", id, "create", req.Name)
		slog.Info("admin.status.create", "actor", currentUser.Email, "status", req.Name, "status_id", id)
	}
	metrics.AdminOpsTotal.WithLabelValues("status", "create", "success").Inc()
	jsonOK(w, map[string]interface{}{"id": id, "status": "ok"})
}

// UpdateStatus modifies a status.
func (h *AdminHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Name      string `json:"name"`
		Color     string `json:"color"`
		Billable  bool   `json:"billable"`
		OnSite    bool   `json:"on_site"`
		SortOrder int    `json:"sort_order"`
	}
	json.NewDecoder(r.Body).Decode(&req)                                                                                                             //nolint:errcheck
	h.DB.UpdateStatus(models.Status{ID: id, Name: req.Name, Color: req.Color, Billable: req.Billable, OnSite: req.OnSite, SortOrder: req.SortOrder}) //nolint:errcheck
	currentUser := middleware.GetUser(r)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "status", id, "update", req.Name)
		slog.Info("admin.status.update", "actor", currentUser.Email, "status", req.Name, "status_id", id)
	}
	metrics.AdminOpsTotal.WithLabelValues("status", "update", "success").Inc()
	jsonOK(w, map[string]string{"status": "ok"})
}

// DeleteStatus removes a status.
func (h *AdminHandler) DeleteStatus(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	statusName := h.DB.GetStatusName(id)
	currentUser := middleware.GetUser(r)
	if err := h.DB.DeleteStatus(id); err != nil {
		if err.Error() == "status_in_use" {
			if currentUser != nil {
				slog.Warn("admin.status.delete_rejected", "actor", currentUser.Email, "status", statusName, "status_id", id, "reason", "in_use")
			}
			metrics.AdminOpsTotal.WithLabelValues("status", "delete", "failure").Inc()
			jsonError(w, "statuses.delete_in_use", http.StatusConflict)
		} else {
			if currentUser != nil {
				slog.Error("admin.status.delete_error", "actor", currentUser.Email, "status", statusName, "status_id", id, "error", err)
			}
			metrics.AdminOpsTotal.WithLabelValues("status", "delete", "failure").Inc()
			jsonError(w, "Error deleting status", http.StatusInternalServerError)
		}
		return
	}
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "status", id, "delete", statusName)
		slog.Info("admin.status.delete", "actor", currentUser.Email, "status", statusName, "status_id", id)
	}
	metrics.AdminOpsTotal.WithLabelValues("status", "delete", "success").Inc()
	jsonOK(w, map[string]string{"status": "ok"})
}

// ToggleStatusDisabled enables or disables a status.
func (h *AdminHandler) ToggleStatusDisabled(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	statusName := h.DB.GetStatusName(id)
	if err := h.DB.SetStatusDisabled(id, req.Disabled); err != nil {
		slog.Error("admin.status.toggle_disabled_error", "status_id", id, "error", err)
		metrics.AdminOpsTotal.WithLabelValues("status", "toggle_disabled", "failure").Inc()
		jsonError(w, "Error updating status", http.StatusInternalServerError)
		return
	}
	currentUser := middleware.GetUser(r)
	action := "enable"
	if req.Disabled {
		action = "disable"
	}
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "status", id, action, statusName)
		slog.Info("admin.status.toggle_disabled", "actor", currentUser.Email, "status", statusName, "status_id", id, "disabled", req.Disabled)
	}
	metrics.AdminOpsTotal.WithLabelValues("status", "toggle_disabled", "success").Inc()
	jsonOK(w, map[string]string{"status": "ok"})
}

// --- Users / Roles management ---

// UsersAPI returns the user list as JSON.
func (h *AdminHandler) UsersAPI(w http.ResponseWriter, r *http.Request) {
	users, _ := h.DB.ListUsers()
	jsonOK(w, users)
}

// UpdateUserRoles updates a user's roles.
func (h *AdminHandler) UpdateUserRoles(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Roles []string `json:"roles"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	roles := strings.Join(req.Roles, ",")
	if err := h.DB.UpdateUserRoles(id, roles); err != nil {
		metrics.AdminOpsTotal.WithLabelValues("role", "update_user_roles", "failure").Inc()
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	currentUser := middleware.GetUser(r)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "user", id, "update_roles", roles)
		slog.Info("admin.user.roles", "actor", currentUser.Email, "target_id", id, "roles", roles)
	}
	metrics.AdminOpsTotal.WithLabelValues("role", "update_user_roles", "success").Inc()
	jsonOK(w, map[string]string{"status": "ok"})
}
