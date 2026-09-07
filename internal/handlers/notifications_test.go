package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/matoy/mypresence/internal/middleware"
	"github.com/matoy/mypresence/internal/models"
)

func TestNotificationsHandler_Acknowledge(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	uID, err := d.CreateLocalUser("notifuser@test.com", "Notif User", "pass12345")
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	uToken, err := d.CreateSession(uID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	adminID, err := d.CreateLocalUser("admin@test.com", "Admin User", "pass12345")
	if err != nil {
		t.Fatalf("CreateLocalUser admin: %v", err)
	}

	notifID, err := d.CreateNotification(uID, adminID, "team_added", "Ajout à une équipe", "Vous avez été ajouté à l'équipe Dev par Admin User.", "")
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}

	// 1. Unauthorized request
	reqUnauth := httptest.NewRequest(http.MethodPost, "/api/notifications/"+strconv.FormatInt(notifID, 10)+"/ack", nil)
	reqUnauth.SetPathValue("id", strconv.FormatInt(notifID, 10))
	recUnauth := httptest.NewRecorder()
	h.AcknowledgeNotification(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", recUnauth.Code)
	}

	// 2. Invalid notification ID
	reqInvalid := httptest.NewRequest(http.MethodPost, "/api/notifications/invalid/ack", nil)
	reqInvalid.AddCookie(&http.Cookie{Name: "session", Value: uToken})
	reqInvalid.SetPathValue("id", "invalid")
	recInvalid := httptest.NewRecorder()
	middleware.Auth(d, http.HandlerFunc(h.AcknowledgeNotification)).ServeHTTP(recInvalid, reqInvalid)
	if recInvalid.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", recInvalid.Code)
	}

	// 3. Successful acknowledge
	reqSuccess := httptest.NewRequest(http.MethodPost, "/api/notifications/"+strconv.FormatInt(notifID, 10)+"/ack", nil)
	reqSuccess.AddCookie(&http.Cookie{Name: "session", Value: uToken})
	reqSuccess.SetPathValue("id", strconv.FormatInt(notifID, 10))
	recSuccess := httptest.NewRecorder()
	middleware.Auth(d, http.HandlerFunc(h.AcknowledgeNotification)).ServeHTTP(recSuccess, reqSuccess)
	if recSuccess.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", recSuccess.Code)
	}

	// 4. Verify unread API returns 0
	reqUnread := httptest.NewRequest(http.MethodGet, "/api/notifications/unread", nil)
	reqUnread.AddCookie(&http.Cookie{Name: "session", Value: uToken})
	recUnread := httptest.NewRecorder()
	middleware.Auth(d, http.HandlerFunc(h.GetUnreadNotificationsAPI)).ServeHTTP(recUnread, reqUnread)
	if recUnread.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recUnread.Code)
	}
	var unreadList []models.Notification
	if err := json.NewDecoder(recUnread.Body).Decode(&unreadList); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(unreadList) != 0 {
		t.Errorf("expected 0 unread notifications, got %d", len(unreadList))
	}
}

func TestTeamAddMember_GeneratesNotification(t *testing.T) {
	d := newCRUDTestDB(t)
	adminH := &AdminHandler{DB: d}

	adminID, _ := d.CreateLocalUser("admin@teamnotif.com", "Admin Boss", "pass12345")
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	memberID, _ := d.CreateLocalUser("member@teamnotif.com", "New Member", "pass12345")
	teamID, _ := d.CreateTeam("Engineering")

	// Add member via handler
	body, _ := json.Marshal(map[string]int64{"user_id": memberID})
	req := httptest.NewRequest(http.MethodPost, "/admin/teams/"+strconv.FormatInt(teamID, 10)+"/members", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
	req.SetPathValue("id", strconv.FormatInt(teamID, 10))
	rec := httptest.NewRecorder()
	middleware.Auth(d, http.HandlerFunc(adminH.AddTeamMember)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("AddTeamMember failed: %d (%s)", rec.Code, rec.Body.String())
	}

	// Verify notification was created for member with ActorName and message
	notifs, err := d.GetUnreadNotifications(memberID)
	if err != nil {
		t.Fatalf("GetUnreadNotifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 unread notification, got %d", len(notifs))
	}
	n := notifs[0]
	if n.Type != "team_added" {
		t.Errorf("expected type 'team_added', got %q", n.Type)
	}
	if n.ActorID != adminID {
		t.Errorf("expected actor ID %d, got %d", adminID, n.ActorID)
	}
	if n.ActorName != "Admin Boss" {
		t.Errorf("expected ActorName 'Admin Boss', got %q", n.ActorName)
	}
	if n.Link != "Engineering" {
		t.Errorf("expected Link 'Engineering', got %q", n.Link)
	}
}

func TestGetUnreadNotificationsAPI_Localization(t *testing.T) {
	d := newCRUDTestDB(t)
	h := &NotificationsHandler{DB: d}

	uID, err := d.CreateLocalUser("frenchuser@test.com", "French User", "pass12345")
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	uToken, err := d.CreateSession(uID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, _ = d.CreateNotification(uID, 0, "reminder_presence", "Presence declaration reminder", "Please declare your presence for the next 5 working day(s).", "/calendar")
	_, _ = d.CreateNotification(uID, 0, "reminder_activity", "Activity declaration reminder", "Please declare your project activities for your 3 billable day(s).", "/projects")

	// Request with French language cookie
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/unread", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: uToken})
	req.AddCookie(&http.Cookie{Name: "lang", Value: "fr"})
	rec := httptest.NewRecorder()

	middleware.Auth(d, http.HandlerFunc(h.GetUnreadNotificationsAPI)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var notifs []models.Notification
	if err := json.NewDecoder(rec.Body).Decode(&notifs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(notifs) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(notifs))
	}

	// First is reminder_activity (newest)
	if notifs[0].Type == "reminder_activity" {
		if notifs[0].Title != "Rappel : déclaration d'activité" {
			t.Errorf("expected French activity title, got %q", notifs[0].Title)
		}
		if notifs[0].Message != "Vous avez des déclarations d'activité (projets/tâches) incomplètes sur 3 jour(s) ouvré(s) passé(s)." {
			t.Errorf("expected French activity message, got %q", notifs[0].Message)
		}
	} else {
		t.Errorf("expected first notif to be reminder_activity, got %s", notifs[0].Type)
	}

	// Second is reminder_presence
	if notifs[1].Type == "reminder_presence" {
		if notifs[1].Title != "Rappel : déclaration de présence" {
			t.Errorf("expected French presence title, got %q", notifs[1].Title)
		}
		if notifs[1].Message != "Pensez à déclarer votre présence pour les 5 prochain(s) jour(s) ouvré(s)." {
			t.Errorf("expected French presence message, got %q", notifs[1].Message)
		}
	} else {
		t.Errorf("expected second notif to be reminder_presence, got %s", notifs[1].Type)
	}
}

