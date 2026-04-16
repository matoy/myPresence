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
		AdminPassword:     "adminpass",
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

// drain reads and discards the response body.
func drain(resp *http.Response) { io.Copy(io.Discard, resp.Body); resp.Body.Close() } //nolint:errcheck

// loginAdmin logs the client in as the seeded admin user.
func (e *testEnv) loginAdmin(t *testing.T) {
	t.Helper()
	resp := e.postForm("/login", "username=admin&password=adminpass")
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
	authH := &handlers.AuthHandler{DB: database, Config: cfg, Render: renderPage}
	calH := &handlers.CalendarHandler{DB: database, Render: renderPage, DisableFloorplans: true}
	adminH := &handlers.AdminHandler{DB: database, Render: renderPage}
	activityH := &handlers.ActivityHandler{DB: database, Render: renderPage}
	usersH := &handlers.UsersAdminHandler{DB: database, Render: renderPage}
	settingsH := &handlers.SettingsHandler{DB: database, Render: renderPage}
	patH := &handlers.PATHandler{DB: database, Render: renderPage}

	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /health", healthH.Health)
	mux.Handle("GET /login", middleware.OptionalAuth(database, http.HandlerFunc(authH.LoginPage)))
	mux.HandleFunc("POST /login", authH.LocalLogin)
	mux.HandleFunc("GET /logout", authH.Logout)

	// Protected – authenticated users only
	authMux := http.NewServeMux()
	authMux.HandleFunc("GET /", calH.CalendarPage)
	authMux.HandleFunc("GET /{$}", calH.CalendarPage)
	authMux.HandleFunc("POST /api/presences", calH.SetPresences)
	authMux.HandleFunc("POST /api/presences/clear", calH.ClearPresences)
	authMux.HandleFunc("GET /api/presences", calH.GetPresencesAPI)
	authMux.HandleFunc("GET /settings/my-logs", settingsH.MyLogsPage)
	authMux.HandleFunc("GET /settings/change-password", settingsH.ChangePasswordPage)
	authMux.HandleFunc("POST /settings/change-password", settingsH.ChangePasswordPost)
	authMux.HandleFunc("GET /settings/tokens", patH.PATPage)
	authMux.HandleFunc("GET /api/tokens", patH.ListPATs)
	authMux.HandleFunc("POST /api/tokens", patH.CreatePAT)
	authMux.HandleFunc("DELETE /api/tokens/{id}", patH.RevokePAT)

	// Admin sub-routes
	teamMux := http.NewServeMux()
	teamMux.HandleFunc("GET /api/teams", adminH.ListTeamsAPI)
	teamMux.HandleFunc("POST /admin/teams", adminH.CreateTeam)
	teamMux.HandleFunc("DELETE /admin/teams/{id}", adminH.DeleteTeam)

	usersMux := http.NewServeMux()
	usersMux.HandleFunc("GET /admin/users", usersH.UsersPage)
	usersMux.HandleFunc("POST /admin/users", usersH.CreateUser)
	usersMux.HandleFunc("DELETE /admin/users/{id}", usersH.DeleteUser)

	activityMux := http.NewServeMux()
	activityMux.HandleFunc("GET /api/activity", activityH.ActivityAPI)

	mux.Handle("/api/teams", middleware.Auth(database, middleware.RequireRole(models.RoleTeamManager)(teamMux)))
	mux.Handle("/api/teams/", middleware.Auth(database, middleware.RequireRole(models.RoleTeamManager)(teamMux)))
	mux.Handle("/admin/teams", middleware.Auth(database, middleware.RequireRole(models.RoleTeamManager)(teamMux)))
	mux.Handle("/admin/teams/", middleware.Auth(database, middleware.RequireRole(models.RoleTeamManager)(teamMux)))
	mux.Handle("/admin/users", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(usersMux)))
	mux.Handle("/admin/users/", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(usersMux)))
	mux.Handle("/api/activity", middleware.Auth(database, middleware.RequireRole(models.RoleActivityViewer)(activityMux)))
	mux.Handle("/", middleware.AuthWithOptions(database, true, authMux))

	return mux
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
		strings.NewReader("username=admin&password=adminpass"))
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
	// Create a local user with a plain-text password (matches auth.go behaviour)
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
	id, _ := e.db.CreateLocalUser("disabled@test.com", "Disabled", "pass")
	e.db.SetUserDisabled(id, true) //nolint:errcheck

	noFollowClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noFollowClient.Post(e.url("/login"), "application/x-www-form-urlencoded",
		strings.NewReader("username=disabled@test.com&password=pass"))
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

	// Logout
	noFollowClient := &http.Client{
		Jar:           e.client.Jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp2, err := noFollowClient.Get(e.url("/logout"))
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
	id, err := e.db.CreateLocalUser("basic@test.com", "Basic User", "basicpass")
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
	otherID, _ := e.db.CreateLocalUser("other@test.com", "Other", "pass")
	basicID, _ := e.db.CreateLocalUser("basic@test.com", "Basic", "pass")
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
	id, _ := e.db.CreateLocalUser("basic2@test.com", "Basic", "pass")
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

// ─── small utility ────────────────────────────────────────────────────────────

func i64str(n int64) string {
	return strconv.FormatInt(n, 10)
}
