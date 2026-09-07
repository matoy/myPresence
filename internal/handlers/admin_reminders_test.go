package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/matoy/mypresence/internal/middleware"
	"github.com/matoy/mypresence/internal/models"
)

type mockReminderRunner struct {
	calledWithTeamID int64
	calledWithForce  bool
	presenceSent     int
	activitySent     int
	err              error
}

func (m *mockReminderRunner) CheckAndSendReminders(now time.Time, teamIDFilter int64, force bool) (presenceSent, activitySent int, err error) {
	m.calledWithTeamID = teamIDFilter
	m.calledWithForce = force
	return m.presenceSent, m.activitySent, m.err
}

func TestTriggerTeamReminders(t *testing.T) {
	d := newCRUDTestDB(t)

	adminID, err := d.CreateLocalUser("globaladmin@example.com", "Global Admin", "pass12345")
	if err != nil {
		t.Fatalf("create global admin: %v", err)
	}
	_ = d.UpdateUserRoles(adminID, models.RoleGlobal)
	adminToken, _ := d.CreateSession(adminID)

	regularID, err := d.CreateLocalUser("regularuser@example.com", "Regular User", "pass12345")
	if err != nil {
		t.Fatalf("create regular user: %v", err)
	}
	regularToken, _ := d.CreateSession(regularID)

	teamMgrID, err := d.CreateLocalUser("teammgr@example.com", "Team Manager", "pass12345")
	if err != nil {
		t.Fatalf("create team manager: %v", err)
	}
	_ = d.UpdateUserRoles(teamMgrID, models.RoleTeamManager)
	teamMgrToken, _ := d.CreateSession(teamMgrID)

	teamID, err := d.CreateTeam("Engineering")
	if err != nil {
		t.Fatalf("create team: %v", err)
	}

	mockRunner := &mockReminderRunner{
		presenceSent: 2,
		activitySent: 3,
	}

	h := &AdminHandler{
		DB:               d,
		RemindersService: mockRunner,
	}

	t.Run("Forbidden for anonymous", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/teams/1/trigger-reminders", nil)
		req.SetPathValue("id", "1")
		rec := httptest.NewRecorder()

		middleware.Auth(d, http.HandlerFunc(h.TriggerTeamReminders)).ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusForbidden {
			t.Errorf("expected 303 or 403, got %d", rec.Code)
		}
	})

	t.Run("Forbidden for regular user", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/teams/1/trigger-reminders", nil)
		req.SetPathValue("id", "1")
		req.AddCookie(&http.Cookie{Name: "session", Value: regularToken})
		rec := httptest.NewRecorder()

		middleware.Auth(d, http.HandlerFunc(h.TriggerTeamReminders)).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden, got %d", rec.Code)
		}
	})

	t.Run("Forbidden for team manager (global admin only)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/teams/1/trigger-reminders", nil)
		req.SetPathValue("id", "1")
		req.AddCookie(&http.Cookie{Name: "session", Value: teamMgrToken})
		rec := httptest.NewRecorder()

		middleware.Auth(d, http.HandlerFunc(h.TriggerTeamReminders)).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden, got %d", rec.Code)
		}
	})

	t.Run("Invalid team ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/teams/abc/trigger-reminders", nil)
		req.SetPathValue("id", "abc")
		req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
		rec := httptest.NewRecorder()

		middleware.Auth(d, http.HandlerFunc(h.TriggerTeamReminders)).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 403 or 400, got %d", rec.Code)
		}
	})

	t.Run("Service unavailable if nil", func(t *testing.T) {
		hNilService := &AdminHandler{DB: d, RemindersService: nil}
		req := httptest.NewRequest(http.MethodPost, "/api/admin/teams/1/trigger-reminders", nil)
		req.SetPathValue("id", "1")
		req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
		rec := httptest.NewRecorder()

		middleware.Auth(d, http.HandlerFunc(hNilService.TriggerTeamReminders)).ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("expected 503 Service Unavailable, got %d", rec.Code)
		}
	})

	t.Run("Service error returns 500", func(t *testing.T) {
		errRunner := &mockReminderRunner{err: errors.New("database locked")}
		hErr := &AdminHandler{DB: d, RemindersService: errRunner}
		req := httptest.NewRequest(http.MethodPost, "/api/admin/teams/1/trigger-reminders", nil)
		req.SetPathValue("id", "1")
		req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
		rec := httptest.NewRecorder()

		middleware.Auth(d, http.HandlerFunc(hErr.TriggerTeamReminders)).ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 Internal Server Error, got %d", rec.Code)
		}
	})

	t.Run("Success for global admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/teams/1/trigger-reminders", nil)
		req.SetPathValue("id", "1")
		req.AddCookie(&http.Cookie{Name: "session", Value: adminToken})
		rec := httptest.NewRecorder()

		middleware.Auth(d, http.HandlerFunc(h.TriggerTeamReminders)).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if resp["status"] != "ok" {
			t.Errorf("expected status 'ok', got %v", resp["status"])
		}
		if int(resp["presence_sent"].(float64)) != 2 {
			t.Errorf("expected presence_sent 2, got %v", resp["presence_sent"])
		}
		if int(resp["activity_sent"].(float64)) != 3 {
			t.Errorf("expected activity_sent 3, got %v", resp["activity_sent"])
		}

		if mockRunner.calledWithTeamID != 1 {
			t.Errorf("expected calledWithTeamID 1, got %d", mockRunner.calledWithTeamID)
		}
		if !mockRunner.calledWithForce {
			t.Errorf("expected calledWithForce true, got false")
		}
		_ = teamID
	})
}
