// Package functional contains end-to-end HTTP tests that spin up the full
// router (middleware + real SQLite DB) against an httptest.Server.
// No mocks are used: every test touches real handler + real DB code paths.
package functional

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"presence-app/internal/config"
	"presence-app/internal/db"
	"presence-app/internal/handlers"
	"presence-app/internal/middleware"
	"presence-app/internal/models"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// testEnv holds a running test server with the full router wired up.
type testEnv struct {
	db     *db.DB
	cfg    *config.Config
	srv    *httptest.Server
	client *http.Client
}

// newTestEnv creates a fresh isolated DB, seeds it, builds the router and
// starts an httptest.Server. It registers cleanup automatically.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	dir := t.TempDir()
	database, err := db.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	cfg := &config.Config{
		AdminUser:         "admin",
		AdminPassword:     "adminpass1",
		DataDir:           dir,
		DefaultLang:       "en",
		SecretKey:         "test-secret-32-chars-padded-here!",
		DisableFloorplans: true, // keep tests simple
		DisableAPI:        false,
	}

	if err := database.SeedDefaults(cfg.AdminUser, cfg.AdminPassword); err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := buildRouter(database, cfg)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &testEnv{db: database, cfg: cfg, srv: srv, client: client}
}

// url returns an absolute URL for the given path.
func (e *testEnv) url(path string) string { return e.srv.URL + path }

// get sends a GET and returns the response (caller must close body).
func (e *testEnv) get(path string) *http.Response {
	resp, err := e.client.Get(e.url(path))
	if err != nil {
		panic(err)
	}
	return resp
}

// postForm sends a POST with form-encoded body.
func (e *testEnv) postForm(path, body string) *http.Response {
	resp, err := e.client.Post(e.url(path), "application/x-www-form-urlencoded", strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	return resp
}

// postJSON sends a POST with a JSON body.
func (e *testEnv) postJSON(path string, payload interface{}) *http.Response {
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, e.url(path), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		panic(err)
	}
	return resp
}

// deleteJSON sends a DELETE request (no body needed for most endpoints).
func (e *testEnv) deleteReq(path string) *http.Response {
	req, _ := http.NewRequest(http.MethodDelete, e.url(path), nil)
	resp, err := e.client.Do(req)
	if err != nil {
		panic(err)
	}
	return resp
}

// putJSON sends a PUT with a JSON body.
func (e *testEnv) putJSON(path string, payload interface{}) *http.Response {
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPut, e.url(path), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		panic(err)
	}
	return resp
}

// noFollowClient returns a client that shares the session jar but never follows redirects.
func (e *testEnv) noFollowClient() *http.Client {
	return &http.Client{
		Jar:           e.client.Jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// csrfToken derives the CSRF token from the current session cookie in the shared jar.
func (e *testEnv) csrfToken() string {
	u, _ := url.Parse(e.srv.URL)
	for _, c := range e.client.Jar.Cookies(u) {
		if c.Name == "session" {
			return middleware.GenerateCSRFToken(e.cfg.SecretKey, c.Value)
		}
	}
	return ""
}

// drain reads and discards the response body.
func drain(resp *http.Response) { io.Copy(io.Discard, resp.Body); resp.Body.Close() } //nolint:errcheck

// loginAdmin logs the client in as the seeded admin user.
func (e *testEnv) loginAdmin(t *testing.T) {
	t.Helper()
	resp := e.postForm("/login", "username=admin&password=adminpass1")
	drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("loginAdmin: expected 200 after redirect, got %d", resp.StatusCode)
	}
}

// injectSession creates a DB session for userID and sets the cookie in the jar.
func (e *testEnv) injectSession(t *testing.T, userID int64) {
	t.Helper()
	token, err := e.db.CreateSession(userID)
	if err != nil {
		t.Fatalf("injectSession: %v", err)
	}
	parsed, _ := http.NewRequest("GET", e.url("/"), nil)
	e.client.Jar.SetCookies(parsed.URL, []*http.Cookie{{
		Name: "session", Value: token, Path: "/",
	}})
}

// mustDecodeJSON decodes JSON from resp.Body into v; fails on error.
func mustDecodeJSON(t *testing.T, resp *http.Response, v interface{}) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// buildRouter wires up a minimal but complete router – same logic as main.go
// but without templates (render is stubbed to write a 200 OK).
func buildRouter(database *db.DB, cfg *config.Config) http.Handler {
	// Stub renderer: just returns 200 with a plain-text page name.
	renderPage := func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("page:" + page)) //nolint:errcheck
	}

	healthH := &handlers.HealthHandler{DB: database, StartedAt: time.Now()}
	authH := &handlers.AuthHandler{
		DB:          database,
		Config:      cfg,
		Render:      renderPage,
		RateLimiter: middleware.NewLoginRateLimiter(),
	}
	calH := &handlers.CalendarHandler{DB: database, Render: renderPage, DisableFloorplans: true}
	adminH := &handlers.AdminHandler{DB: database, Render: renderPage}
	activityH := &handlers.ActivityHandler{DB: database, Render: renderPage}
	usersH := &handlers.UsersAdminHandler{DB: database, Render: renderPage}
	settingsH := &handlers.SettingsHandler{DB: database, Render: renderPage}
	patH := &handlers.PATHandler{DB: database, Render: renderPage}
	holidaysH := &handlers.HolidaysHandler{DB: database, Render: renderPage}
	resetH := &handlers.ResetPasswordHandler{DB: database, Config: cfg, Render: renderPage}

	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /health", healthH.Health)
	mux.Handle("GET /login", middleware.OptionalAuth(database, http.HandlerFunc(authH.LoginPage)))
	mux.HandleFunc("POST /login", authH.LocalLogin)
	mux.Handle("POST /logout", middleware.ValidateCSRF(cfg.SecretKey)(http.HandlerFunc(authH.Logout)))

	// Reset password (always active in test env; production gates on SMTP)
	mux.HandleFunc("GET /forgot-password", resetH.ForgotPasswordPage)
	mux.HandleFunc("POST /forgot-password", resetH.ForgotPasswordPost)
	mux.HandleFunc("GET /reset-password", resetH.ResetPasswordPage)
	mux.HandleFunc("POST /reset-password", resetH.ResetPasswordPost)

	// Protected – authenticated users only
	authMux := http.NewServeMux()
	authMux.HandleFunc("GET /", calH.CalendarPage)
	authMux.HandleFunc("GET /{$}", calH.CalendarPage)
	authMux.HandleFunc("POST /api/presences", calH.SetPresences)
	authMux.HandleFunc("POST /api/presences/clear", calH.ClearPresences)
	authMux.HandleFunc("GET /api/presences", calH.GetPresencesAPI)
	authMux.HandleFunc("GET /settings/my-logs", settingsH.MyLogsPage)
	authMux.HandleFunc("GET /settings/change-password", settingsH.ChangePasswordPage)
	authMux.Handle("POST /settings/change-password", middleware.ValidateCSRF(cfg.SecretKey)(http.HandlerFunc(settingsH.ChangePasswordPost)))
	authMux.HandleFunc("GET /settings/tokens", patH.PATPage)
	authMux.HandleFunc("GET /api/tokens", patH.ListPATs)
	authMux.HandleFunc("POST /api/tokens", patH.CreatePAT)
	authMux.HandleFunc("DELETE /api/tokens/{id}", patH.RevokePAT)
	authMux.HandleFunc("DELETE /api/admin/tokens/{id}", patH.AdminRevokePAT)

	// Admin sub-routes
	teamMux := http.NewServeMux()
	teamMux.HandleFunc("GET /api/teams", adminH.ListTeamsAPI)
	teamMux.HandleFunc("POST /admin/teams", adminH.CreateTeam)
	teamMux.HandleFunc("PUT /admin/teams/{id}", adminH.UpdateTeam)
	teamMux.HandleFunc("DELETE /admin/teams/{id}", adminH.DeleteTeam)
	teamMux.HandleFunc("POST /admin/teams/{id}/members", adminH.AddTeamMember)
	teamMux.HandleFunc("DELETE /admin/teams/{id}/members/{userId}", adminH.RemoveTeamMember)

	statusMux := http.NewServeMux()
	statusMux.HandleFunc("POST /admin/statuses", adminH.CreateStatus)
	statusMux.HandleFunc("PUT /admin/statuses/{id}", adminH.UpdateStatus)
	statusMux.HandleFunc("DELETE /admin/statuses/{id}", adminH.DeleteStatus)

	holidaysMux := http.NewServeMux()
	holidaysMux.HandleFunc("POST /admin/holidays", holidaysH.CreateHoliday)
	holidaysMux.HandleFunc("PUT /admin/holidays/{id}", holidaysH.UpdateHoliday)
	holidaysMux.HandleFunc("DELETE /admin/holidays/{id}", holidaysH.DeleteHoliday)

	usersMux := http.NewServeMux()
	usersMux.HandleFunc("GET /admin/users", usersH.UsersPage)
	usersMux.HandleFunc("POST /admin/users", usersH.CreateUser)
	usersMux.HandleFunc("PUT /admin/users/{id}", usersH.UpdateUser)
	usersMux.HandleFunc("PUT /admin/users/{id}/password", usersH.SetPassword)
	usersMux.HandleFunc("PUT /admin/users/{id}/disabled", usersH.SetDisabled)
	usersMux.HandleFunc("DELETE /admin/users/{id}", usersH.DeleteUser)

	activityMux := http.NewServeMux()
	activityMux.HandleFunc("GET /api/activity", activityH.ActivityAPI)

	mux.Handle("/api/teams", middleware.Auth(database, middleware.RequireRole(models.RoleTeamManager)(teamMux)))
	mux.Handle("/api/teams/", middleware.Auth(database, middleware.RequireRole(models.RoleTeamManager)(teamMux)))
	mux.Handle("/admin/teams", middleware.Auth(database, middleware.RequireRole(models.RoleTeamManager)(teamMux)))
	mux.Handle("/admin/teams/", middleware.Auth(database, middleware.RequireRole(models.RoleTeamManager)(teamMux)))
	mux.Handle("/admin/statuses", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(statusMux)))
	mux.Handle("/admin/statuses/", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(statusMux)))
	mux.Handle("/admin/holidays", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(holidaysMux)))
	mux.Handle("/admin/holidays/", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(holidaysMux)))
	mux.Handle("/admin/users", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(usersMux)))
	mux.Handle("/admin/users/", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(usersMux)))
	mux.Handle("/api/activity", middleware.Auth(database, middleware.RequireRole(models.RoleActivityViewer)(activityMux)))
	mux.Handle("/", middleware.AuthWithOptions(database, true, authMux))

	return middleware.SecurityHeaders(mux)
}

// ─── Health ──────────────────────────────────────────────────────────────────

func TestHealth_ReturnsOK(t *testing.T) {
	e := newTestEnv(t)
	resp := e.get("/health")
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		// body already drained by drain() – that's fine, we already checked status
		return
	}
}

func TestHealth_ReturnsJSON(t *testing.T) {
	e := newTestEnv(t)
	resp := e.get("/health")

	var body map[string]interface{}
	mustDecodeJSON(t, resp, &body)

	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body["status"])
	}
}

// ─── Auth: Login ─────────────────────────────────────────────────────────────

func TestLogin_ValidCredentials_RedirectsToHome(t *testing.T) {
	e := newTestEnv(t)
	jar, _ := cookiejar.New(nil)
	// Use a non-following client to inspect the redirect itself
	noFollowClient := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noFollowClient.Post(e.url("/login"), "application/x-www-form-urlencoded",
		strings.NewReader("username=admin&password=adminpass1"))
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}
	// Session cookie must be set
	cookies := resp.Cookies()
	var hasSess bool
	for _, c := range cookies {
		if c.Name == "session" && c.Value != "" {
			hasSess = true
		}
	}
	if !hasSess {
		t.Error("expected session cookie to be set after login")
	}
}

func TestLogin_InvalidCredentials_RedirectsToLoginWithError(t *testing.T) {
	e := newTestEnv(t)
	noFollowClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noFollowClient.Post(e.url("/login"), "application/x-www-form-urlencoded",
		strings.NewReader("username=admin&password=wrongpassword"))
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/login") {
		t.Errorf("expected redirect back to /login, got %q", loc)
	}
	if !strings.Contains(loc, "error=") {
		t.Errorf("expected error param in redirect, got %q", loc)
	}
}

func TestLogin_LocalUser_ValidCredentials(t *testing.T) {
	e := newTestEnv(t)
	// Create a local user with a plain-text password — bcrypt is now applied by CreateLocalUser
	_, err := e.db.CreateLocalUser("user@test.com", "Test User", "mypassword")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	noFollowClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noFollowClient.Post(e.url("/login"), "application/x-www-form-urlencoded",
		strings.NewReader("username=user@test.com&password=mypassword"))
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303 redirect to /, got %d", resp.StatusCode)
	}
}

func TestLogin_DisabledUser_Rejected(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("disabled@test.com", "Disabled", "password1")
	e.db.SetUserDisabled(id, true) //nolint:errcheck

	noFollowClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noFollowClient.Post(e.url("/login"), "application/x-www-form-urlencoded",
		strings.NewReader("username=disabled@test.com&password=password1"))
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("expected error on login with disabled account, got Location=%q", loc)
	}
}

// ─── Auth: Logout ─────────────────────────────────────────────────────────────

func TestLogout_ClearsSessionAndRedirects(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	// Verify we can access a protected route
	resp := e.get("/")
	drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on / after login, got %d", resp.StatusCode)
	}

	// Logout via POST with CSRF token
	noFollowClient := &http.Client{
		Jar:           e.client.Jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	csrf := e.csrfToken()
	logoutForm := url.Values{}
	logoutForm.Set("csrf_token", csrf)
	logoutReq, _ := http.NewRequest("POST", e.url("/logout"), strings.NewReader(logoutForm.Encode()))
	logoutReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp2, err := noFollowClient.Do(logoutReq)
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp2)

	if resp2.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303 on logout, got %d", resp2.StatusCode)
	}
	loc := resp2.Header.Get("Location")
	if !strings.Contains(loc, "/login") {
		t.Errorf("expected redirect to /login after logout, got %q", loc)
	}

	// After logout, accessing / should redirect to login
	// Build a client that does NOT follow so we see the 303
	noFollowClient2 := &http.Client{
		Jar:           e.client.Jar, // same jar (session cookie cleared)
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp3, _ := noFollowClient2.Get(e.url("/"))
	defer drain(resp3)
	if resp3.StatusCode != http.StatusSeeOther {
		t.Errorf("after logout / should redirect to login (303), got %d", resp3.StatusCode)
	}
}

// ─── Middleware: unauthenticated access ──────────────────────────────────────

func TestProtectedRoutes_WithoutSession_RedirectToLogin(t *testing.T) {
	e := newTestEnv(t)
	// Fresh client with no cookies
	noAuthClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	routes := []string{"/", "/settings/my-logs", "/settings/change-password", "/settings/tokens"}
	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			resp, err := noAuthClient.Get(e.url(route))
			if err != nil {
				t.Fatal(err)
			}
			defer drain(resp)
			if resp.StatusCode != http.StatusSeeOther {
				t.Errorf("route %s: expected 303 redirect, got %d", route, resp.StatusCode)
			}
			loc := resp.Header.Get("Location")
			if !strings.Contains(loc, "/login") {
				t.Errorf("route %s: expected redirect to /login, got %q", route, loc)
			}
		})
	}
}

func TestAdminRoutes_WithoutAdminRole_Forbidden(t *testing.T) {
	e := newTestEnv(t)

	// Create a basic user with no admin role
	id, err := e.db.CreateLocalUser("basic@test.com", "Basic User", "basicpass1")
	if err != nil {
		t.Fatal(err)
	}
	e.injectSession(t, id)

	noFollowClient := &http.Client{
		Jar:           e.client.Jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	resp, err := noFollowClient.Get(e.url("/admin/users"))
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("basic user on /admin/users: expected 403, got %d", resp.StatusCode)
	}
}

func TestAdminRoutes_BearerPAT_WithoutToken_Unauthorized(t *testing.T) {
	e := newTestEnv(t)
	req, _ := http.NewRequest("GET", e.url("/api/presences"), nil)
	req.Header.Set("Authorization", "Bearer invalidtoken")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 with invalid PAT, got %d", resp.StatusCode)
	}
}

// ─── Calendar – authenticated ─────────────────────────────────────────────────

func TestCalendarPage_AuthenticatedUser_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.get("/")
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on /, got %d", resp.StatusCode)
	}
}

func TestCalendarPage_WithYearMonthParams(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.get("/?year=2026&month=3")
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ─── Presences API ────────────────────────────────────────────────────────────

func TestSetPresences_ValidRequest_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	// Find the admin user
	adminUser, err := e.db.GetUserByEmail("admin")
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}

	// Find first status
	statuses, err := e.db.ListStatuses()
	if err != nil || len(statuses) == 0 {
		t.Fatal("no statuses found")
	}

	payload := map[string]interface{}{
		"user_id":   adminUser.ID,
		"dates":     []string{"2026-04-07"},
		"status_id": statuses[0].ID,
		"half":      "full",
	}
	resp := e.postJSON("/api/presences", payload)
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSetPresences_InvalidDateFormat_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	adminUser, _ := e.db.GetUserByEmail("admin")
	statuses, _ := e.db.ListStatuses()

	payload := map[string]interface{}{
		"user_id":   adminUser.ID,
		"dates":     []string{"not-a-date"},
		"status_id": statuses[0].ID,
	}
	resp := e.postJSON("/api/presences", payload)
	defer drain(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid date, got %d", resp.StatusCode)
	}
}

func TestSetPresences_BasicUserEditingOtherUser_Forbidden(t *testing.T) {
	e := newTestEnv(t)

	// Create a basic user
	otherID, _ := e.db.CreateLocalUser("other@test.com", "Other", "password1")
	basicID, _ := e.db.CreateLocalUser("basic@test.com", "Basic", "password2")
	e.injectSession(t, basicID)

	statuses, _ := e.db.ListStatuses()
	payload := map[string]interface{}{
		"user_id":   otherID, // basic user trying to edit another user
		"dates":     []string{"2026-04-07"},
		"status_id": statuses[0].ID,
		"half":      "full",
	}
	resp := e.postJSON("/api/presences", payload)
	defer drain(resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 when basic user edits other user, got %d", resp.StatusCode)
	}
}

func TestClearPresences_ValidRequest_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	adminUser, _ := e.db.GetUserByEmail("admin")
	statuses, _ := e.db.ListStatuses()

	// First set a presence
	setPayload := map[string]interface{}{
		"user_id":   adminUser.ID,
		"dates":     []string{"2026-04-10"},
		"status_id": statuses[0].ID,
		"half":      "full",
	}
	drain(e.postJSON("/api/presences", setPayload))

	// Then clear it
	clearPayload := map[string]interface{}{
		"user_id": adminUser.ID,
		"dates":   []string{"2026-04-10"},
		"half":    "full",
	}
	resp := e.postJSON("/api/presences/clear", clearPayload)
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGetPresencesAPI_MissingParams_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.get("/api/presences") // no team_id, year, month
	defer drain(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestGetPresencesAPI_ValidParams_ReturnsJSON(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	// Create a team
	teamID, err := e.db.CreateTeam("Dev")
	if err != nil {
		t.Fatalf("create team: %v", err)
	}

	resp := e.get("/api/presences?team_id=" + i64str(teamID) + "&year=2026&month=4")
	var result interface{}
	mustDecodeJSON(t, resp, &result)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ─── Admin: Teams API ─────────────────────────────────────────────────────────

func TestListTeamsAPI_AsAdmin_ReturnsJSON(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	// Create a couple of teams
	e.db.CreateTeam("Alpha") //nolint:errcheck
	e.db.CreateTeam("Beta")  //nolint:errcheck

	resp := e.get("/api/teams")
	var teams []map[string]interface{}
	mustDecodeJSON(t, resp, &teams)

	if len(teams) < 2 {
		t.Errorf("expected at least 2 teams, got %d", len(teams))
	}
}

func TestCreateTeam_AsAdmin_Creates(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	payload := map[string]string{"name": "Gamma Team"}
	resp := e.postJSON("/admin/teams", payload)
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	teams, _ := e.db.ListTeams()
	found := false
	for _, tm := range teams {
		if tm.Name == "Gamma Team" {
			found = true
		}
	}
	if !found {
		t.Error("team 'Gamma Team' not found in DB after creation")
	}
}

func TestCreateTeam_EmptyName_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	payload := map[string]string{"name": ""}
	resp := e.postJSON("/admin/teams", payload)
	defer drain(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty team name, got %d", resp.StatusCode)
	}
}

// ─── Admin: Users API ─────────────────────────────────────────────────────────

func TestAdminUsers_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.get("/admin/users")
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestCreateUser_AsAdmin_Creates(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	payload := map[string]string{
		"email":    "newuser@example.com",
		"name":     "New User",
		"password": "securepass",
	}
	resp := e.postJSON("/admin/users", payload)
	var result map[string]interface{}
	mustDecodeJSON(t, resp, &result)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if result["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", result["status"])
	}
}

func TestCreateUser_DuplicateEmail_Returns409(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	e.db.CreateLocalUser("dup@example.com", "Dup", "pass") //nolint:errcheck

	payload := map[string]string{
		"email":    "dup@example.com",
		"name":     "Dup2",
		"password": "pass",
	}
	resp := e.postJSON("/admin/users", payload)
	defer drain(resp)

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409 for duplicate email, got %d", resp.StatusCode)
	}
}

// ─── Personal Access Tokens ───────────────────────────────────────────────────

func TestCreatePAT_AsGlobalUser_ReturnsToken(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	payload := map[string]interface{}{
		"description": "my integration test token",
		"expires_in":  30,
	}
	resp := e.postJSON("/api/tokens", payload)
	var result map[string]interface{}
	mustDecodeJSON(t, resp, &result)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	token, _ := result["token"].(string)
	if !strings.HasPrefix(token, "mpa_") {
		t.Errorf("expected token to start with mpa_, got %q", token)
	}
}

func TestCreatePAT_BasicUser_Forbidden(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("basic2@test.com", "Basic", "password1")
	e.injectSession(t, id)

	payload := map[string]interface{}{
		"description": "should fail",
		"expires_in":  0,
	}
	resp := e.postJSON("/api/tokens", payload)
	defer drain(resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for basic user creating PAT, got %d", resp.StatusCode)
	}
}

func TestCreatePAT_EmptyDescription_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	payload := map[string]interface{}{
		"description": "",
		"expires_in":  30,
	}
	resp := e.postJSON("/api/tokens", payload)
	defer drain(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestRevokePAT_AsOwner_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	// Create a PAT first
	payload := map[string]interface{}{"description": "to revoke", "expires_in": 10}
	resp := e.postJSON("/api/tokens", payload)
	var result map[string]interface{}
	mustDecodeJSON(t, resp, &result)
	id := int64(result["id"].(float64))

	resp2 := e.deleteReq("/api/tokens/" + i64str(id))
	defer drain(resp2)

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on revoke, got %d", resp2.StatusCode)
	}
}

// ─── Activity API ─────────────────────────────────────────────────────────────

func TestActivityAPI_MissingParams_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	// Give admin the activity_viewer role (seeded admin has global role which satisfies everything)
	resp := e.get("/api/activity")
	defer drain(resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 with missing params, got %d", resp.StatusCode)
	}
}

func TestActivityAPI_ValidParams_ReturnsJSON(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	teamID, _ := e.db.CreateTeam("Activity Team")

	resp := e.get("/api/activity?team_id=" + i64str(teamID) + "&year=2026&month=4")
	var result interface{}
	mustDecodeJSON(t, resp, &result)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ─── Settings ─────────────────────────────────────────────────────────────────

func TestMyLogsPage_AuthenticatedUser_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.get("/settings/my-logs")
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestChangePasswordPage_NonLocalUser_Redirects(t *testing.T) {
	e := newTestEnv(t)

	// Create a user that looks like a SAML user (no password_hash = not local)
	_, err := e.db.UpsertUser("saml@corp.com", "SAML User")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := e.db.GetUserByEmail("saml@corp.com")
	e.injectSession(t, u.ID)

	noFollow := &http.Client{
		Jar:           e.client.Jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, _ := noFollow.Get(e.url("/settings/change-password"))
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("non-local user on change-password: expected 303 redirect, got %d", resp.StatusCode)
	}
}

// ─── PAT Bearer authentication ────────────────────────────────────────────────

func TestBearerPAT_ValidToken_AccessesProtectedRoute(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	// Create PAT
	payload := map[string]interface{}{"description": "bearer test", "expires_in": 10}
	resp := e.postJSON("/api/tokens", payload)
	var result map[string]interface{}
	mustDecodeJSON(t, resp, &result)
	rawToken := result["token"].(string)

	// Use a brand new client (no session cookie)
	req, _ := http.NewRequest("GET", e.url("/api/presences?team_id=1&year=2026&month=4"), nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	freshClient := &http.Client{}
	resp2, err := freshClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp2)

	// Should not be 401 – even if params cause 400 the auth itself passed
	if resp2.StatusCode == http.StatusUnauthorized {
		t.Error("expected PAT authentication to succeed (not 401)")
	}
}

// ─── Settings: ChangePassword ─────────────────────────────────────────────────

func TestChangePasswordPost_ValidChange_Redirects(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("local@test.com", "Local", "oldpass1")
	e.injectSession(t, id)
	csrf := e.csrfToken()

	resp, _ := e.noFollowClient().Post(e.url("/settings/change-password"),
		"application/x-www-form-urlencoded",
		strings.NewReader("current_password=oldpass1&new_password=newpass12&confirm_password=newpass12&csrf_token="+csrf))
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "success") {
		t.Errorf("expected success in redirect, got %q", resp.Header.Get("Location"))
	}
}

func TestChangePasswordPost_WrongCurrent_RedirectsWithError(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("local2@test.com", "Local2", "correctpass")
	e.injectSession(t, id)
	csrf := e.csrfToken()

	resp, _ := e.noFollowClient().Post(e.url("/settings/change-password"),
		"application/x-www-form-urlencoded",
		strings.NewReader("current_password=wrongpass&new_password=newpass12&confirm_password=newpass12&csrf_token="+csrf))
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "error") {
		t.Errorf("expected error in redirect, got %q", resp.Header.Get("Location"))
	}
}

func TestChangePasswordPost_TooShort_RedirectsWithError(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("local3@test.com", "Local3", "oldpass1")
	e.injectSession(t, id)
	csrf := e.csrfToken()

	resp, _ := e.noFollowClient().Post(e.url("/settings/change-password"),
		"application/x-www-form-urlencoded",
		strings.NewReader("current_password=oldpass1&new_password=short&confirm_password=short&csrf_token="+csrf))
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "error") {
		t.Errorf("expected error in redirect, got %q", resp.Header.Get("Location"))
	}
}

func TestChangePasswordPost_Mismatch_RedirectsWithError(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("local4@test.com", "Local4", "oldpass1")
	e.injectSession(t, id)
	csrf := e.csrfToken()

	resp, _ := e.noFollowClient().Post(e.url("/settings/change-password"),
		"application/x-www-form-urlencoded",
		strings.NewReader("current_password=oldpass1&new_password=newpass12&confirm_password=different1&csrf_token="+csrf))
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "error") {
		t.Errorf("expected error in redirect, got %q", resp.Header.Get("Location"))
	}
}

// ─── Reset Password ───────────────────────────────────────────────────────────

func TestForgotPassword_EmptyEmail_ReturnsSentPage(t *testing.T) {
	e := newTestEnv(t)
	resp := e.postForm("/forgot-password", "email=")
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestForgotPassword_UnknownEmail_ReturnsSentPage(t *testing.T) {
	e := newTestEnv(t)
	resp := e.postForm("/forgot-password", "email=nobody%40example.com")
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 (no enumeration), got %d", resp.StatusCode)
	}
}

func TestResetPassword_InvalidToken_RendersErrorPage(t *testing.T) {
	e := newTestEnv(t)
	resp := e.postForm("/reset-password", "token=invalid&password=newpass12&confirm=newpass12")
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 error page, got %d", resp.StatusCode)
	}
}

func TestResetPassword_ValidToken_SetsNewPassword(t *testing.T) {
	e := newTestEnv(t)
	_, err := e.db.CreateLocalUser("reset@test.com", "Reset User", "oldpass1")
	if err != nil {
		t.Fatal(err)
	}
	rawToken, err := e.db.CreatePasswordResetToken("reset@test.com")
	if err != nil || rawToken == "" {
		t.Fatalf("create reset token: err=%v token=%q", err, rawToken)
	}
	resp := e.postForm("/reset-password",
		"token="+rawToken+"&password=newpass12&confirm=newpass12")
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 success page, got %d", resp.StatusCode)
	}
}

func TestResetPassword_TooShortPassword_RendersError(t *testing.T) {
	e := newTestEnv(t)
	_, _ = e.db.CreateLocalUser("reset2@test.com", "Reset2", "oldpass1")
	rawToken, _ := e.db.CreatePasswordResetToken("reset2@test.com")
	resp := e.postForm("/reset-password",
		"token="+rawToken+"&password=short&confirm=short")
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 error page, got %d", resp.StatusCode)
	}
}

// ─── Admin: Teams CRUD ────────────────────────────────────────────────────────

func TestDeleteTeam_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	id, _ := e.db.CreateTeam("ToDelete")
	resp := e.deleteReq("/admin/teams/" + i64str(id))
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUpdateTeam_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	id, _ := e.db.CreateTeam("OldName")
	resp := e.putJSON("/admin/teams/"+i64str(id), map[string]string{"name": "NewName"})
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAddTeamMember_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	teamID, _ := e.db.CreateTeam("TeamWithMember")
	userID, _ := e.db.CreateLocalUser("member@test.com", "Member", "memberpass1")
	resp := e.postJSON("/admin/teams/"+i64str(teamID)+"/members",
		map[string]int64{"user_id": userID})
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRemoveTeamMember_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	teamID, _ := e.db.CreateTeam("TeamRemove")
	userID, _ := e.db.CreateLocalUser("rmember@test.com", "RMember", "rmemberpass1")
	e.db.AddTeamMember(teamID, userID) //nolint:errcheck
	resp := e.deleteReq("/admin/teams/" + i64str(teamID) + "/members/" + i64str(userID))
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ─── Admin: Statuses CRUD ─────────────────────────────────────────────────────

func TestCreateStatus_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	resp := e.postJSON("/admin/statuses", map[string]interface{}{
		"name": "Remote", "color": "#ff0000", "billable": false, "on_site": false, "sort_order": 10,
	})
	var result map[string]interface{}
	mustDecodeJSON(t, resp, &result)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestCreateStatus_MissingFields_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	resp := e.postJSON("/admin/statuses", map[string]interface{}{"name": ""})
	defer drain(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestUpdateStatus_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	statuses, _ := e.db.ListStatuses()
	id := statuses[0].ID
	resp := e.putJSON("/admin/statuses/"+i64str(id), map[string]interface{}{
		"name": "UpdatedStatus", "color": "#00ff00",
	})
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDeleteStatus_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	// Create via API to get the ID
	createResp := e.postJSON("/admin/statuses", map[string]interface{}{
		"name": "ToDeleteStatus", "color": "#aabbcc",
	})
	var created map[string]interface{}
	mustDecodeJSON(t, createResp, &created)
	id := int64(created["id"].(float64))

	resp := e.deleteReq("/admin/statuses/" + i64str(id))
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ─── Admin: Holidays CRUD ─────────────────────────────────────────────────────

func TestCreateHoliday_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	resp := e.postJSON("/admin/holidays", map[string]interface{}{
		"date": "2026-05-01", "name": "Labour Day", "allow_imputed": false,
	})
	var result map[string]interface{}
	mustDecodeJSON(t, resp, &result)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestCreateHoliday_MissingFields_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	resp := e.postJSON("/admin/holidays", map[string]interface{}{"date": ""})
	defer drain(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestUpdateHoliday_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	id, _ := e.db.CreateHoliday("2026-07-14", "Bastille Day", false)
	resp := e.putJSON("/admin/holidays/"+i64str(id), map[string]interface{}{
		"date": "2026-07-14", "name": "Bastille Day Updated", "allow_imputed": true,
	})
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDeleteHoliday_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	id, _ := e.db.CreateHoliday("2026-08-15", "Assumption Day", false)
	resp := e.deleteReq("/admin/holidays/" + i64str(id))
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ─── Admin: Users CRUD ────────────────────────────────────────────────────────

func TestUpdateUser_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	id, _ := e.db.CreateLocalUser("upd@test.com", "Old Name", "password1")
	resp := e.putJSON("/admin/users/"+i64str(id),
		map[string]string{"email": "upd@test.com", "name": "New Name"})
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSetPassword_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	id, _ := e.db.CreateLocalUser("setpwd@test.com", "SetPwd", "password1")
	resp := e.putJSON("/admin/users/"+i64str(id)+"/password",
		map[string]string{"password": "newpassword1"})
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSetPassword_EmptyPassword_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	id, _ := e.db.CreateLocalUser("setpwd2@test.com", "SetPwd2", "password1")
	resp := e.putJSON("/admin/users/"+i64str(id)+"/password",
		map[string]string{"password": ""})
	defer drain(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestSetDisabled_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	id, _ := e.db.CreateLocalUser("disable@test.com", "Disable", "password1")
	resp := e.putJSON("/admin/users/"+i64str(id)+"/disabled",
		map[string]bool{"disabled": true})
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSetDisabled_Self_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	adminUser, _ := e.db.GetUserByEmail("admin")
	resp := e.putJSON("/admin/users/"+i64str(adminUser.ID)+"/disabled",
		map[string]bool{"disabled": true})
	defer drain(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 when disabling self, got %d", resp.StatusCode)
	}
}

func TestDeleteUser_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	id, _ := e.db.CreateLocalUser("todelete@test.com", "ToDelete", "password1")
	resp := e.deleteReq("/admin/users/" + i64str(id))
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDeleteUser_Self_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	adminUser, _ := e.db.GetUserByEmail("admin")
	resp := e.deleteReq("/admin/users/" + i64str(adminUser.ID))
	defer drain(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 when deleting self, got %d", resp.StatusCode)
	}
}

// ─── PAT: List ────────────────────────────────────────────────────────────────

func TestListPATs_AsAdmin_ReturnsJSON(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	resp := e.get("/api/tokens")
	var result interface{}
	mustDecodeJSON(t, resp, &result)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ─── Rate Limiter ─────────────────────────────────────────────────────────────

func TestRateLimiter_BlocksAfterMaxFailures(t *testing.T) {
	e := newTestEnv(t)
	noFollow := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	badLogin := func() *http.Response {
		resp, err := noFollow.Post(e.url("/login"),
			"application/x-www-form-urlencoded",
			strings.NewReader("username=admin&password=wrongpassword"))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// Exhaust the 5-failure budget
	for i := 0; i < 5; i++ {
		drain(badLogin())
	}
	// Next attempt must be rate-limited
	resp := badLogin()
	defer drain(resp)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "many") && !strings.Contains(loc, "Many") {
		t.Errorf("expected rate-limit redirect, got location %q", loc)
	}
}

// ─── Security Headers ─────────────────────────────────────────────────────────

func TestSecurityHeaders_PresentOnResponses(t *testing.T) {
	e := newTestEnv(t)
	resp := e.get("/health")
	defer drain(resp)

	checks := map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
	}
	for header, want := range checks {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s: want %q, got %q", header, want, got)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); csp == "" {
		t.Error("Content-Security-Policy header missing")
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors") {
		t.Error("CSP missing frame-ancestors directive")
	}
}

// ─── small utility ────────────────────────────────────────────────────────────

func i64str(n int64) string {
	return strconv.FormatInt(n, 10)
}
