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

// AdminHandler handles all admin pages and API endpoints.
type AdminHandler struct {
	DB     *db.DB
	Render func(w http.ResponseWriter, r *http.Request, page string, data interface{})
}

// --- Team management ---

// TeamsPage renders the team management page.
func (h *AdminHandler) TeamsPage(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	teams, _ := h.DB.ListTeams()
	users, _ := h.DB.ListUsers()

	canManageTeams := currentUser != nil && currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal)
	isTeamLeader := currentUser != nil && currentUser.HasRole(models.RoleTeamLeader) && !canManageTeams

	myTeamIDs := map[int64]bool{}
	if isTeamLeader {
		myTeams, _ := h.DB.GetUserTeams(currentUser.ID)
		for _, t := range myTeams {
			myTeamIDs[t.ID] = true
		}
	}

	type TeamWithMembers struct {
		Team    models.Team
		Members []models.User
		CanEdit bool
	}

	var teamsList []TeamWithMembers
	for _, t := range teams {
		if isTeamLeader && !myTeamIDs[t.ID] {
			continue
		}
		members, _ := h.DB.GetTeamMembers(t.ID)
		canEdit := canManageTeams || myTeamIDs[t.ID]
		teamsList = append(teamsList, TeamWithMembers{Team: t, Members: members, CanEdit: canEdit})
	}

	h.Render(w, r, "admin_teams", map[string]interface{}{
		"Teams":          teamsList,
		"Users":          users,
		"CanManageTeams": canManageTeams,
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
		jsonError(w, "Access denied", http.StatusForbidden)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		jsonError(w, "name required", http.StatusBadRequest)
		return
	}
	id, err := h.DB.CreateTeam(strings.TrimSpace(req.Name))
	if err != nil {
		jsonError(w, "Erreur création équipe", http.StatusInternalServerError)
		return
	}
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "team", id, "create", req.Name)
	}
	jsonOK(w, map[string]interface{}{"id": id, "status": "ok"})
}

// DeleteTeam deletes a team.
func (h *AdminHandler) DeleteTeam(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser != nil && !currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal) {
		jsonError(w, "Access denied", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	teamName := h.DB.GetTeamName(id)
	h.DB.DeleteTeam(id)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "team", id, "delete", teamName)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// UpdateTeam renames a team.
func (h *AdminHandler) UpdateTeam(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser != nil && !currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal) {
		jsonError(w, "Access denied", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	h.DB.UpdateTeam(id, req.Name)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "team", id, "update", req.Name)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// AddTeamMember adds a user to a team.
func (h *AdminHandler) AddTeamMember(w http.ResponseWriter, r *http.Request) {
	teamID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	currentUser := middleware.GetUser(r)
	if currentUser != nil && currentUser.HasRole(models.RoleTeamLeader) && !currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal) {
		if !h.isUserInTeam(currentUser.ID, teamID) {
			jsonError(w, "Access denied", http.StatusForbidden)
			return
		}
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	memberName := strconv.FormatInt(req.UserID, 10)
	if u, _ := h.DB.GetUserByID(req.UserID); u != nil {
		memberName = u.Name
	}
	h.DB.AddTeamMember(teamID, req.UserID)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "team", teamID, "add_member", memberName)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// RemoveTeamMember removes a user from a team.
func (h *AdminHandler) RemoveTeamMember(w http.ResponseWriter, r *http.Request) {
	teamID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	currentUser := middleware.GetUser(r)
	if currentUser != nil && currentUser.HasRole(models.RoleTeamLeader) && !currentUser.HasAnyRole(models.RoleTeamManager, models.RoleGlobal) {
		if !h.isUserInTeam(currentUser.ID, teamID) {
			jsonError(w, "Access denied", http.StatusForbidden)
			return
		}
	}
	userID, _ := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	memberName := strconv.FormatInt(userID, 10)
	if u, _ := h.DB.GetUserByID(userID); u != nil {
		memberName = u.Name
	}
	h.DB.RemoveTeamMember(teamID, userID)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "team", teamID, "remove_member", memberName)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// isUserInTeam checks whether a user is a member of the given team.
func (h *AdminHandler) isUserInTeam(userID, teamID int64) bool {
	myTeams, _ := h.DB.GetUserTeams(userID)
	for _, t := range myTeams {
		if t.ID == teamID {
			return true
		}
	}
	return false
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
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Color == "" {
		jsonError(w, "Name and color are required", http.StatusBadRequest)
		return
	}
	id, err := h.DB.CreateStatus(models.Status{Name: req.Name, Color: req.Color, Billable: req.Billable, OnSite: req.OnSite, SortOrder: req.SortOrder})
	if err != nil {
		jsonError(w, "Error creating status", http.StatusInternalServerError)
		return
	}
	currentUser := middleware.GetUser(r)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "status", id, "create", req.Name)
	}
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
	json.NewDecoder(r.Body).Decode(&req)
	h.DB.UpdateStatus(models.Status{ID: id, Name: req.Name, Color: req.Color, Billable: req.Billable, OnSite: req.OnSite, SortOrder: req.SortOrder})
	currentUser := middleware.GetUser(r)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "status", id, "update", req.Name)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// DeleteStatus removes a status.
func (h *AdminHandler) DeleteStatus(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	statusName := h.DB.GetStatusName(id)
	h.DB.DeleteStatus(id)
	currentUser := middleware.GetUser(r)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "status", id, "delete", statusName)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// --- Users / Roles management ---

// UsersAPI returns the user list as JSON.
func (h *AdminHandler) UsersAPI(w http.ResponseWriter, r *http.Request) {
	users, _ := h.DB.ListUsers()
	jsonOK(w, users)
}

// RolesPage renders the role management page.
func (h *AdminHandler) RolesPage(w http.ResponseWriter, r *http.Request) {
	users, _ := h.DB.ListUsers()
	h.Render(w, r, "admin_roles", map[string]interface{}{
		"Users":    users,
		"AllRoles": models.AllRoles,
	})
}

// UpdateUserRoles updates a user's roles.
func (h *AdminHandler) UpdateUserRoles(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Roles []string `json:"roles"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	roles := strings.Join(req.Roles, ",")
	if err := h.DB.UpdateUserRoles(id, roles); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	currentUser := middleware.GetUser(r)
	if currentUser != nil {
		h.DB.LogAdminAction(currentUser.ID, "user", id, "update_roles", roles)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}
