package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/matoy/mypresence/internal/config"
	"github.com/matoy/mypresence/internal/middleware"
)

// -----------------------------------------------------------------------
// ResetPasswordPost handler
// -----------------------------------------------------------------------

func TestResetPasswordPost_EmptyToken(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	cfg := &config.Config{AppName: "Test"}
	var rendered string
	h := &ResetPasswordHandler{DB: d, Config: cfg, Render: func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
		rendered = page
	}}
	body := []byte("token=&password=newpass12&confirm=newpass12")
	req := httptest.NewRequest(http.MethodPost, "/reset-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	h.ResetPasswordPost(w, req)
	if rendered != "reset_password" {
		t.Fatalf("expected reset_password render, got %q", rendered)
	}
}

func TestResetPasswordPost_InvalidToken(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	cfg := &config.Config{AppName: "Test"}
	var rendered string
	h := &ResetPasswordHandler{DB: d, Config: cfg, Render: func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
		rendered = page
	}}
	body := []byte("token=invalidtoken&password=newpass12&confirm=newpass12")
	req := httptest.NewRequest(http.MethodPost, "/reset-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	h.ResetPasswordPost(w, req)
	if rendered != "reset_password" {
		t.Fatalf("expected reset_password render, got %q", rendered)
	}
}

func TestResetPasswordPost_PasswordMismatch(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	cfg := &config.Config{AppName: "Test"}
	var rendered string
	h := &ResetPasswordHandler{DB: d, Config: cfg, Render: func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
		rendered = page
	}}
	body := []byte("token=sometoken&password=newpass12&confirm=different")
	req := httptest.NewRequest(http.MethodPost, "/reset-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	h.ResetPasswordPost(w, req)
	if rendered != "reset_password" {
		t.Fatalf("expected reset_password render, got %q", rendered)
	}
}

func TestResetPasswordPost_Success(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	cfg := &config.Config{AppName: "Test", SMTPURL: "smtp://127.0.0.1:1"}
	var rendered string
	h := &ResetPasswordHandler{DB: d, Config: cfg, Render: func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
		rendered = page
	}}
	_, _ = d.CreateLocalUser("resetok@test.com", "ResetOK", "oldpass12")
	rawToken, _ := d.CreatePasswordResetToken("resetok@test.com")
	if rawToken == "" {
		t.Skip("could not generate reset token")
	}
	body := []byte("token=" + rawToken + "&password=newpass12&confirm=newpass12")
	req := httptest.NewRequest(http.MethodPost, "/reset-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	h.ResetPasswordPost(w, req)
	if rendered != "reset_password" {
		t.Fatalf("expected reset_password render, got %q", rendered)
	}
}

// -----------------------------------------------------------------------
// ChangePasswordPost handler
// -----------------------------------------------------------------------

func TestChangePasswordPost_SuccessPath(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &SettingsHandler{DB: d, Render: noRender}

	uid, _ := d.CreateLocalUser("changepwd@test.com", "ChangePwd", "oldpass12")
	tok, _ := d.CreateSession(uid)

	body := []byte("current_password=oldpass12&new_password=newpass99&confirm_password=newpass99")
	req := httptest.NewRequest(http.MethodPost, "/settings/change-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.ChangePasswordPost)).ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect on success, got %d: %s", w.Code, w.Body.String())
	}
}

func TestChangePasswordPost_WrongPassword(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &SettingsHandler{DB: d, Render: noRender}

	uid, _ := d.CreateLocalUser("wrongpwd@test.com", "WrongPwd", "correctpass")
	tok, _ := d.CreateSession(uid)

	body := []byte("current_password=wrongpass&new_password=newpass99&confirm_password=newpass99")
	req := httptest.NewRequest(http.MethodPost, "/settings/change-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: tok})
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.ChangePasswordPost)).ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if w.Header().Get("Location") != "/settings/change-password?error=Mot+de+passe+actuel+incorrect" {
		t.Fatalf("expected wrong password error, got %s", w.Header().Get("Location"))
	}
}

// -----------------------------------------------------------------------
// ImpersonatePost handler
// -----------------------------------------------------------------------

func TestImpersonatePost_Success(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &SettingsHandler{DB: d, Render: noRender}

	_, _ = d.CreateLocalUser("impersonatetgt@test.com", "Target", "password1")

	req := createAdminReq(t, d, http.MethodPost, "/admin/impersonate", []byte("login=impersonatetgt%40test.com"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.ImpersonatePost)).ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", w.Code, w.Body.String())
	}
}

func TestImpersonatePost_EmptyLogin(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &SettingsHandler{DB: d, Render: noRender}

	req := createAdminReq(t, d, http.MethodPost, "/admin/impersonate", []byte("login="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.ImpersonatePost)).ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
}

// -----------------------------------------------------------------------
// AdminProjectsPage — extra path
// -----------------------------------------------------------------------

func TestAdminProjectsPage_WithTeamID(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &ProjectsHandler{DB: d, Render: noRender}

	tid, _ := d.CreateTeam("Proj Team Extra")
	req := createAdminReq(t, d, http.MethodGet, "/admin/projects?team_id="+strconvI64(tid), nil)
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.AdminProjectsPage)).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// -----------------------------------------------------------------------
// SetProjectTime — valid project path
// -----------------------------------------------------------------------

func TestSetProjectTime_ValidProject(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &ProjectsHandler{DB: d, Render: noRender}

	projID, _ := d.CreateProject("Test Project X", "TPX01", 0, true, "2026-01-01", "2030-12-31")

	bodyBytes, _ := json.Marshal(map[string]interface{}{"project_id": projID, "year": 2026, "month": 6, "days": 0.0})
	req := createAdminReq(t, d, http.MethodPost, "/api/project-time", bodyBytes)
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.SetProjectTime)).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSetProjectTime_NegativeDays(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &ProjectsHandler{DB: d, Render: noRender}

	projID, _ := d.CreateProject("Test Project Y", "TPY01", 0, true, "2026-01-01", "2030-12-31")

	bodyBytes, _ := json.Marshal(map[string]interface{}{"project_id": projID, "year": 2026, "month": 6, "days": -1.0})
	req := createAdminReq(t, d, http.MethodPost, "/api/project-time", bodyBytes)
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.SetProjectTime)).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative days, got %d", w.Code)
	}
}

// -----------------------------------------------------------------------
// ProjectsAPI and ProjectTimeAPI
// -----------------------------------------------------------------------

func TestProjectsAPI_Success(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &ProjectsHandler{DB: d, Render: noRender}

	req := createAdminReq(t, d, http.MethodGet, "/api/projects?year=2026&month=6", nil)
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.ProjectsAPI)).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProjectTimeAPI_Success(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &ProjectsHandler{DB: d, Render: noRender}

	req := createAdminReq(t, d, http.MethodGet, "/api/project-time?year=2026&month=6", nil)
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.ProjectTimeAPI)).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// -----------------------------------------------------------------------
// CreateUser handler
// -----------------------------------------------------------------------

func TestCreateUser_Success(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &UsersAdminHandler{DB: d, Render: noRender}

	bodyBytes, _ := json.Marshal(map[string]interface{}{
		"email":    "newuser@test.com",
		"name":     "New User",
		"role":     "basic",
		"password": "password123",
	})
	req := createAdminReq(t, d, http.MethodPost, "/api/admin/users", bodyBytes)
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.CreateUser)).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &UsersAdminHandler{DB: d, Render: noRender}

	d.CreateLocalUser("dup@test.com", "Dup", "password1") //nolint:errcheck
	bodyBytes, _ := json.Marshal(map[string]interface{}{
		"email":    "dup@test.com",
		"name":     "Dup Again",
		"role":     "basic",
		"password": "password123",
	})
	req := createAdminReq(t, d, http.MethodPost, "/api/admin/users", bodyBytes)
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.CreateUser)).ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError && w.Code != http.StatusConflict {
		t.Fatalf("expected error for duplicate email, got %d: %s", w.Code, w.Body.String())
	}
}

// -----------------------------------------------------------------------
// SetPassword handler
// -----------------------------------------------------------------------

func TestSetPassword_Success(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &UsersAdminHandler{DB: d, Render: noRender}

	uid, _ := d.CreateLocalUser("setpwd@test.com", "SetPwd", "oldpass1")
	sessToken, err := d.CreateSession(uid)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := d.GetSessionUser(sessToken); err != nil {
		t.Fatalf("expected session to be valid before password reset: %v", err)
	}

	bodyBytes, _ := json.Marshal(map[string]interface{}{
		"password": "newpass123",
	})
	req := createAdminReq(t, d, http.MethodPut, "/api/admin/users/"+strconvI64(uid)+"/password", bodyBytes)
	req.SetPathValue("id", strconvI64(uid))
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.SetPassword)).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Active session for the reset user must be revoked
	if _, err := d.GetSessionUser(sessToken); err == nil {
		t.Fatalf("expected session for user %d to be deleted after admin SetPassword", uid)
	}
}

func TestSetPassword_AdminResetOwnPassword_PreservesCurrentSession(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &UsersAdminHandler{DB: d, Render: noRender}

	adminID, _ := d.CreateLocalUser("admin_self@test.com", "Admin Self", "adminpass1")
	_ = d.UpdateUserRoles(adminID, "global")

	sessToken, err := d.CreateSession(adminID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	otherSessToken, err := d.CreateSession(adminID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	bodyBytes, _ := json.Marshal(map[string]interface{}{
		"password": "newadminpass123",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/admin/users/"+strconvI64(adminID)+"/password", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: sessToken})
	req.SetPathValue("id", strconvI64(adminID))

	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.SetPassword)).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Current session should be preserved
	if _, err := d.GetSessionUser(sessToken); err != nil {
		t.Fatalf("expected current admin session to be preserved: %v", err)
	}
	// Other session on another device should be revoked
	if _, err := d.GetSessionUser(otherSessToken); err == nil {
		t.Fatalf("expected other admin session to be revoked")
	}
}

// -----------------------------------------------------------------------
// SetDisabled handler
// -----------------------------------------------------------------------

func TestSetDisabled_Success(t *testing.T) {
	d := newExtraTestDB(t)
	d.SetBcryptCost(4)
	h := &UsersAdminHandler{DB: d, Render: noRender}

	uid, _ := d.CreateLocalUser("setdis@test.com", "SetDis", "password1")
	bodyBytes, _ := json.Marshal(map[string]interface{}{"disabled": true})
	req := createAdminReq(t, d, http.MethodPut, "/api/admin/users/"+strconvI64(uid)+"/disabled", bodyBytes)
	req.SetPathValue("id", strconvI64(uid))
	w := httptest.NewRecorder()
	w.Body = new(bytes.Buffer)
	middleware.Auth(d, http.HandlerFunc(h.SetDisabled)).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
