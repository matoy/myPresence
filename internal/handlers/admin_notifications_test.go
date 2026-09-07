package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/matoy/mypresence/internal/middleware"
	"github.com/matoy/mypresence/internal/models"
)

func TestAdminNotificationsPage_GlobalAdmin(t *testing.T) {
	d := newCRUDTestDB(t)
	var renderedPage string
	var renderedData map[string]interface{}
	h := &NotificationsHandler{
		DB: d,
		Render: func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
			renderedPage = page
			if m, ok := data.(map[string]interface{}); ok {
				renderedData = m
			}
			w.WriteHeader(http.StatusOK)
		},
	}

	adminID, _ := d.CreateLocalUser("admin_notif@test.com", "Admin Notif", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	token, _ := d.CreateSession(adminID)

	u1, _ := d.CreateLocalUser("user1_notif@test.com", "User One", "pass12345")
	_, _ = d.CreateNotification(u1, adminID, "info", "Test Hello", "Message body", "/calendar")

	req := httptest.NewRequest(http.MethodGet, "/admin/notifications?success=1", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminNotificationsPage)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if renderedPage != "admin_notifications" {
		t.Errorf("expected rendered page 'admin_notifications', got %q", renderedPage)
	}
	if renderedData == nil {
		t.Fatal("expected rendered data to be non-nil")
	}
	users, ok := renderedData["Users"].([]models.User)
	if !ok || len(users) < 2 {
		t.Errorf("expected Users in rendered data, got %v", renderedData["Users"])
	}
	teams, ok := renderedData["Teams"].([]models.Team)
	if !ok {
		t.Errorf("expected Teams in rendered data, got %v", renderedData["Teams"])
	}
	_ = teams
	sites, ok := renderedData["Sites"].([]*models.Site)
	if !ok {
		t.Errorf("expected Sites in rendered data, got %v", renderedData["Sites"])
	}
	_ = sites
	notifs, ok := renderedData["Notifications"].([]models.Notification)
	if !ok || len(notifs) < 1 {
		t.Errorf("expected Notifications in rendered data, got %v", renderedData["Notifications"])
	}
	if renderedData["Success"] != "1" {
		t.Errorf("expected Success '1', got %v", renderedData["Success"])
	}
}

func TestAdminNotificationsPage_ForbiddenForNonAdmin(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	userID, _ := d.CreateLocalUser("basic_user@test.com", "Basic User", "pass12345")
	token, _ := d.CreateSession(userID)

	// Authenticated basic user
	req := httptest.NewRequest(http.MethodGet, "/admin/notifications", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rec := httptest.NewRecorder()
	middleware.Auth(d, http.HandlerFunc(h.AdminNotificationsPage)).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for basic user, got %d", rec.Code)
	}

	// Unauthenticated request
	reqUnauth := httptest.NewRequest(http.MethodGet, "/admin/notifications", nil)
	recUnauth := httptest.NewRecorder()
	h.AdminNotificationsPage(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for unauthenticated user, got %d", recUnauth.Code)
	}
}

func TestAdminSendNotification_SingleUser_JSON(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin_snd@test.com", "Admin Sender", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	targetID, _ := d.CreateLocalUser("target_user@test.com", "Target User", "pass12345")

	payload := map[string]interface{}{
		"recipient": strconv.FormatInt(targetID, 10),
		"type":      "warning",
		"title":     "Maintenance Alert",
		"message":   "The system will restart tonight at 23:00.",
		"link":      "/calendar",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminSendNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", resp["status"])
	}

	// Check DB
	notifs, err := d.GetUnreadNotifications(targetID)
	if err != nil {
		t.Fatalf("GetUnreadNotifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 unread notification, got %d", len(notifs))
	}
	n := notifs[0]
	if n.Title != "Maintenance Alert" {
		t.Errorf("expected title 'Maintenance Alert', got %q", n.Title)
	}
	if n.Message != "The system will restart tonight at 23:00." {
		t.Errorf("expected message match, got %q", n.Message)
	}
	if n.Type != "warning" {
		t.Errorf("expected type 'warning', got %q", n.Type)
	}
	if n.Link != "/calendar" {
		t.Errorf("expected link '/calendar', got %q", n.Link)
	}
	if n.ActorID != adminID {
		t.Errorf("expected actor ID %d, got %d", adminID, n.ActorID)
	}
}

func TestAdminSendNotification_SingleUser_Form(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin_form@test.com", "Admin Form", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	targetID, _ := d.CreateLocalUser("target_form@test.com", "Target Form", "pass12345")

	form := url.Values{}
	form.Set("recipient", strconv.FormatInt(targetID, 10))
	form.Set("type", "alert")
	form.Set("title", "Urgent Form Title")
	form.Set("message", "Urgent form message content")
	form.Set("link", "https://example.com/status")

	req := httptest.NewRequest(http.MethodPost, "/admin/notifications", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminSendNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 See Other redirect, got %d", rec.Code)
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "/admin/notifications?success=1") {
		t.Errorf("expected redirect to success page, got %q", location)
	}

	notifs, err := d.GetUnreadNotifications(targetID)
	if err != nil {
		t.Fatalf("GetUnreadNotifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 unread notification, got %d", len(notifs))
	}
	if notifs[0].Title != "Urgent Form Title" {
		t.Errorf("expected title 'Urgent Form Title', got %q", notifs[0].Title)
	}
}

func TestAdminSendNotification_AllUsers(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin_all@test.com", "Admin All", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	u1, _ := d.CreateLocalUser("all_u1@test.com", "User One", "pass12345")
	u2, _ := d.CreateLocalUser("all_u2@test.com", "User Two", "pass12345")
	u3, _ := d.CreateLocalUser("all_u3@test.com", "User Disabled", "pass12345")
	_ = d.SetUserDisabled(u3, true)

	payload := map[string]interface{}{
		"recipient": "all",
		"type":      "info",
		"title":     "Broadcast Message",
		"message":   "Hello to everyone in the team!",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminSendNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Active users u1 and u2 and admin should have received it; u3 (disabled) should not
	for _, uid := range []int64{u1, u2, adminID} {
		notifs, err := d.GetUnreadNotifications(uid)
		if err != nil {
			t.Fatalf("GetUnreadNotifications for %d: %v", uid, err)
		}
		if len(notifs) != 1 {
			t.Errorf("expected 1 notification for active user %d, got %d", uid, len(notifs))
		}
	}

	disabledNotifs, _ := d.GetUnreadNotifications(u3)
	if len(disabledNotifs) != 0 {
		t.Errorf("expected 0 notifications for disabled user %d, got %d", u3, len(disabledNotifs))
	}
}

func TestAdminSendNotification_ValidationErrors(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin_val@test.com", "Admin Val", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	targetID, _ := d.CreateLocalUser("val_target@test.com", "Val Target", "pass12345")

	tests := []struct {
		name      string
		recipient string
		title     string
		message   string
	}{
		{"missing recipient", "", "Valid Title", "Valid Message"},
		{"missing title", strconv.FormatInt(targetID, 10), "", "Valid Message"},
		{"missing message", strconv.FormatInt(targetID, 10), "Valid Title", ""},
		{"invalid recipient id", "not-a-number", "Valid Title", "Valid Message"},
		{"negative recipient id", "-5", "Valid Title", "Valid Message"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]interface{}{
				"recipient": tc.recipient,
				"title":     tc.title,
				"message":   tc.message,
			}
			body, _ := json.Marshal(payload)

			req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
			rec := httptest.NewRecorder()

			middleware.Auth(d, http.HandlerFunc(h.AdminSendNotification)).ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400 Bad Request, got %d (%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAdminSendNotification_ForbiddenForNonAdmin(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	userID, _ := d.CreateLocalUser("non_admin_snd@test.com", "Non Admin", "pass12345")
	token, _ := d.CreateSession(userID)

	payload := map[string]interface{}{
		"recipient": "all",
		"title":     "Unauthorized broadcast",
		"message":   "I am not an admin",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminSendNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", rec.Code)
	}
}

func TestAdminSendNotification_Team_JSON(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin_team@test.com", "Admin Team", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	teamID, _ := d.CreateTeam("Engineering")
	u1, _ := d.CreateLocalUser("eng1@test.com", "Engineer One", "pass12345")
	u2, _ := d.CreateLocalUser("eng2@test.com", "Engineer Two", "pass12345")
	u3, _ := d.CreateLocalUser("other@test.com", "Other Person", "pass12345")

	_ = d.AddTeamMember(teamID, u1)
	_ = d.AddTeamMember(teamID, u2)

	payload := map[string]interface{}{
		"recipient": "team:" + strconv.FormatInt(teamID, 10),
		"type":      "team_added",
		"title":     "Team Sprint Kickoff",
		"message":   "Sprint starts tomorrow morning.",
		"link":      "/teams",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminSendNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", rec.Code, rec.Body.String())
	}

	for _, uid := range []int64{u1, u2} {
		notifs, err := d.GetUnreadNotifications(uid)
		if err != nil {
			t.Fatalf("GetUnreadNotifications for %d: %v", uid, err)
		}
		if len(notifs) != 1 {
			t.Errorf("expected 1 notification for team member %d, got %d", uid, len(notifs))
		}
	}

	otherNotifs, _ := d.GetUnreadNotifications(u3)
	if len(otherNotifs) != 0 {
		t.Errorf("expected 0 notifications for non-team member %d, got %d", u3, len(otherNotifs))
	}
}

func TestAdminSendNotification_UserPrefix_JSON(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin_pref@test.com", "Admin Pref", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	targetID, _ := d.CreateLocalUser("pref_target@test.com", "Pref Target", "pass12345")

	payload := map[string]interface{}{
		"recipient": "user:" + strconv.FormatInt(targetID, 10),
		"type":      "success",
		"title":     "Great job!",
		"message":   "Your profile is complete.",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminSendNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", rec.Code, rec.Body.String())
	}

	notifs, _ := d.GetUnreadNotifications(targetID)
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification for target user %d, got %d", targetID, len(notifs))
	}
}

func TestAdminDeleteNotification_JSON(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin_del@test.com", "Admin Del", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	targetID, _ := d.CreateLocalUser("del_target@test.com", "Del Target", "pass12345")
	notifID, err := d.CreateNotification(targetID, adminID, "info", "To be deleted", "Msg", "")
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}

	// Delete as admin via JSON
	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications/"+strconv.FormatInt(notifID, 10)+"/delete", nil)
	req.SetPathValue("id", strconv.FormatInt(notifID, 10))
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminDeleteNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Verify it was deleted from DB
	allNotifs, _ := d.GetAllNotifications(10)
	if len(allNotifs) != 0 {
		t.Fatalf("expected 0 notifications after delete, got %d", len(allNotifs))
	}
}

func TestAdminDeleteNotification_Forbidden(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	u1, _ := d.CreateLocalUser("user_del@test.com", "User Del", "pass12345")
	token, _ := d.CreateSession(u1)

	notifID, _ := d.CreateNotification(u1, 0, "info", "Test", "Msg", "")

	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications/"+strconv.FormatInt(notifID, 10)+"/delete", nil)
	req.SetPathValue("id", strconv.FormatInt(notifID, 10))
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminDeleteNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d", rec.Code)
	}
}

func TestAdminSendNotification_MultipleUsers_JSON(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin_multi_u@test.com", "Admin Multi U", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	u1, _ := d.CreateLocalUser("multi1@test.com", "Multi 1", "pass12345")
	u2, _ := d.CreateLocalUser("multi2@test.com", "Multi 2", "pass12345")
	u3, _ := d.CreateLocalUser("multi3@test.com", "Multi 3", "pass12345")

	payload := map[string]interface{}{
		"recipient": fmt.Sprintf("user:%d,%d", u1, u2),
		"user_ids":  []int64{u1, u2},
		"type":      "info",
		"title":     "Multi User Announcement",
		"message":   "Hello to both of you!",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminSendNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", rec.Code, rec.Body.String())
	}

	for _, uid := range []int64{u1, u2} {
		notifs, err := d.GetUnreadNotifications(uid)
		if err != nil || len(notifs) != 1 {
			t.Fatalf("expected 1 notification for user %d, got %d (err: %v)", uid, len(notifs), err)
		}
		if notifs[0].Title != "Multi User Announcement" {
			t.Errorf("expected title 'Multi User Announcement', got %q", notifs[0].Title)
		}
	}

	u3Notifs, _ := d.GetUnreadNotifications(u3)
	if len(u3Notifs) != 0 {
		t.Errorf("expected 0 notifications for u3, got %d", len(u3Notifs))
	}
}

func TestAdminSendNotification_MultipleTeams_Deduplicated(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin_multi_t@test.com", "Admin Multi T", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	team1, _ := d.CreateTeam("Team Alpha")
	team2, _ := d.CreateTeam("Team Beta")

	commonUser, _ := d.CreateLocalUser("common@test.com", "Common User", "pass12345")
	alphaOnly, _ := d.CreateLocalUser("alpha@test.com", "Alpha Only", "pass12345")
	betaOnly, _ := d.CreateLocalUser("beta@test.com", "Beta Only", "pass12345")
	outsider, _ := d.CreateLocalUser("outsider@test.com", "Outsider", "pass12345")

	// Common user is in BOTH teams
	_ = d.AddTeamMember(team1, commonUser)
	_ = d.AddTeamMember(team2, commonUser)

	_ = d.AddTeamMember(team1, alphaOnly)
	_ = d.AddTeamMember(team2, betaOnly)

	payload := map[string]interface{}{
		"recipient": fmt.Sprintf("team:%d,%d", team1, team2),
		"team_ids":  []int64{team1, team2},
		"type":      "success",
		"title":     "Cross-Team Event",
		"message":   "Joint event this Friday",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminSendNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["count"] != float64(3) {
		t.Errorf("expected count 3 (deduplicated), got %v", resp["count"])
	}

	// Common user must have received EXACTLY 1 notification (not 2)
	notifsCommon, err := d.GetUnreadNotifications(commonUser)
	if err != nil || len(notifsCommon) != 1 {
		t.Fatalf("expected exactly 1 notification for common user, got %d", len(notifsCommon))
	}

	for _, uid := range []int64{alphaOnly, betaOnly} {
		notifs, _ := d.GetUnreadNotifications(uid)
		if len(notifs) != 1 {
			t.Errorf("expected 1 notification for %d, got %d", uid, len(notifs))
		}
	}

	notifsOutsider, _ := d.GetUnreadNotifications(outsider)
	if len(notifsOutsider) != 0 {
		t.Errorf("expected 0 notifications for outsider, got %d", len(notifsOutsider))
	}
}

func TestAdminSendNotification_Site_JSON(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin_site@test.com", "Admin Site", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	siteID, _ := d.CreateSite(models.Site{Name: "Paris Campus", CountryCode: "FR"})
	siteOtherID, _ := d.CreateSite(models.Site{Name: "Lyon Campus", CountryCode: "FR"})

	u1, _ := d.CreateLocalUser("paris1@test.com", "Paris User 1", "pass12345")
	u2, _ := d.CreateLocalUser("paris2@test.com", "Paris User 2", "pass12345")
	uDisabled, _ := d.CreateLocalUser("paris_dis@test.com", "Paris Disabled", "pass12345")
	_ = d.SetUserDisabled(uDisabled, true)
	uOther, _ := d.CreateLocalUser("lyon1@test.com", "Lyon User", "pass12345")
	uNoSite, _ := d.CreateLocalUser("nosite@test.com", "No Site", "pass12345")

	_ = d.UpdateUserSite(u1, siteID)
	_ = d.UpdateUserSite(u2, siteID)
	_ = d.UpdateUserSite(uDisabled, siteID)
	_ = d.UpdateUserSite(uOther, siteOtherID)

	payload := map[string]interface{}{
		"recipient": "site:" + strconv.FormatInt(siteID, 10),
		"type":      "warning",
		"title":     "Building Maintenance",
		"message":   "Fire drill scheduled at 14:00 today in Paris.",
		"link":      "/floorplan",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminSendNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["count"] != float64(2) {
		t.Errorf("expected count 2, got %v", resp["count"])
	}

	// u1 and u2 must have received it
	for _, uid := range []int64{u1, u2} {
		notifs, err := d.GetUnreadNotifications(uid)
		if err != nil {
			t.Fatalf("GetUnreadNotifications for %d: %v", uid, err)
		}
		if len(notifs) != 1 {
			t.Errorf("expected 1 notification for site member %d, got %d", uid, len(notifs))
		}
		if notifs[0].Title != "Building Maintenance" {
			t.Errorf("expected title 'Building Maintenance', got %q", notifs[0].Title)
		}
	}

	// Disabled user must NOT have received it
	notifsDis, _ := d.GetUnreadNotifications(uDisabled)
	if len(notifsDis) != 0 {
		t.Errorf("expected 0 notifications for disabled user, got %d", len(notifsDis))
	}

	// Other site user and no site user must NOT have received it
	for _, uid := range []int64{uOther, uNoSite} {
		notifs, _ := d.GetUnreadNotifications(uid)
		if len(notifs) != 0 {
			t.Errorf("expected 0 notifications for user %d, got %d", uid, len(notifs))
		}
	}
}

func TestAdminSendNotification_MultipleSites_JSON(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin_multisite@test.com", "Admin Multi", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	s1, _ := d.CreateSite(models.Site{Name: "Site Alpha", CountryCode: "FR"})
	s2, _ := d.CreateSite(models.Site{Name: "Site Beta", CountryCode: "US"})
	s3, _ := d.CreateSite(models.Site{Name: "Site Gamma", CountryCode: "DE"})

	uA, _ := d.CreateLocalUser("user_a@test.com", "User A", "pass12345")
	uB, _ := d.CreateLocalUser("user_b@test.com", "User B", "pass12345")
	uC, _ := d.CreateLocalUser("user_c@test.com", "User C", "pass12345")

	_ = d.UpdateUserSite(uA, s1)
	_ = d.UpdateUserSite(uB, s2)
	_ = d.UpdateUserSite(uC, s3)

	payload := map[string]interface{}{
		"recipient": fmt.Sprintf("site:%d,%d", s1, s2),
		"site_ids":  []int64{s1, s2},
		"type":      "info",
		"title":     "Dual Site Update",
		"message":   "Notice for Alpha and Beta",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.AdminSendNotification)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (%s)", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["count"] != float64(2) {
		t.Errorf("expected count 2, got %v", resp["count"])
	}

	// uA and uB must have received it
	for _, uid := range []int64{uA, uB} {
		notifs, err := d.GetUnreadNotifications(uid)
		if err != nil || len(notifs) != 1 {
			t.Errorf("expected 1 notification for user %d, got %d", uid, len(notifs))
		}
	}

	// uC from Site Gamma must NOT have received it
	notifsC, _ := d.GetUnreadNotifications(uC)
	if len(notifsC) != 0 {
		t.Errorf("expected 0 notifications for user C, got %d", len(notifsC))
	}
}

