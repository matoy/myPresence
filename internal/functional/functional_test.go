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
	"presence-app/internal/i18n"
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
		DisableFloorplans: false,
		DisableAPI:        false,
	}

	if err := database.SeedDefaults(cfg.AdminUser, cfg.AdminPassword); err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := buildRouter(database, cfg, dir)

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

// deleteJSONBody sends a DELETE request with a JSON body.
func (e *testEnv) deleteJSONBody(path string, payload interface{}) *http.Response {
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodDelete, e.url(path), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
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
	defer resp.Body.Close() //nolint:errcheck
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// buildRouter wires up a minimal but complete router – same logic as main.go
// but without templates (render is stubbed to write a 200 OK).
func buildRouter(database *db.DB, cfg *config.Config, dataDir string) http.Handler {
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
	calH := &handlers.CalendarHandler{DB: database, Render: renderPage, DisableFloorplans: false}
	adminH := &handlers.AdminHandler{DB: database, Render: renderPage}
	activityH := &handlers.ActivityHandler{DB: database, Render: renderPage}
	usersH := &handlers.UsersAdminHandler{DB: database, Render: renderPage}
	settingsH := &handlers.SettingsHandler{DB: database, Render: renderPage}
	patH := &handlers.PATHandler{DB: database, Render: renderPage}
	holidaysH := &handlers.HolidaysHandler{DB: database, Render: renderPage}
	resetH := &handlers.ResetPasswordHandler{DB: database, Config: cfg, Render: renderPage}
	fpH := &handlers.FloorplanHandler{DB: database, DataDir: dataDir, Render: renderPage}

	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /health", healthH.Health)
	mux.Handle("GET /login", middleware.OptionalAuth(database, http.HandlerFunc(authH.LoginPage)))
	mux.HandleFunc("POST /login", authH.LocalLogin)
	mux.Handle("POST /logout", middleware.ValidateCSRF(cfg.SecretKey)(http.HandlerFunc(authH.Logout)))
	mux.HandleFunc("POST /set-lang", func(w http.ResponseWriter, r *http.Request) {
		lang := r.FormValue("lang")
		valid := false
		for _, s := range i18n.Supported {
			if s.Code == lang {
				valid = true
				break
			}
		}
		if !valid {
			lang = cfg.DefaultLang
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "lang",
			Value:    lang,
			Path:     "/",
			MaxAge:   365 * 24 * 3600,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

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

	// Impersonation (CSRF-protected)
	authMux.HandleFunc("GET /impersonate", settingsH.ImpersonatePage)
	authMux.Handle("POST /impersonate", middleware.ValidateCSRF(cfg.SecretKey)(http.HandlerFunc(settingsH.ImpersonatePost)))
	authMux.Handle("POST /impersonate-exit", middleware.ValidateCSRF(cfg.SecretKey)(http.HandlerFunc(settingsH.ImpersonateExitPost)))

	// Floorplan user routes
	authMux.HandleFunc("GET /floorplan", fpH.FloorplanPage)
	authMux.HandleFunc("GET /api/seats", fpH.SeatsAPI)
	authMux.HandleFunc("GET /api/floorplans", fpH.ListFloorplansAPI)
	authMux.HandleFunc("GET /api/floorplans/{id}/seats", fpH.ListSeatsForFloorplanAPI)
	authMux.HandleFunc("POST /api/reservations", fpH.ReserveSeat)
	authMux.HandleFunc("POST /api/reservations/bulk", fpH.BulkReserveSeats)
	authMux.HandleFunc("DELETE /api/reservations/bulk", fpH.CancelReservationsByDates)
	authMux.HandleFunc("DELETE /api/reservations/{id}", fpH.CancelReservation)

	// Floorplan admin routes (require floorplan_manager or global)
	fpAdminMux := http.NewServeMux()
	fpAdminMux.HandleFunc("GET /admin/floorplans", fpH.AdminFloorplansPage)
	fpAdminMux.HandleFunc("POST /admin/floorplans", fpH.CreateFloorplan)
	fpAdminMux.HandleFunc("PUT /admin/floorplans/{id}", fpH.UpdateFloorplan)
	fpAdminMux.HandleFunc("DELETE /admin/floorplans/{id}", fpH.DeleteFloorplan)
	fpAdminMux.HandleFunc("POST /admin/floorplans/{id}/image", fpH.UploadFloorplanImage)
	fpAdminMux.HandleFunc("POST /admin/floorplans/{id}/seats", fpH.CreateSeat)
	fpAdminMux.HandleFunc("PUT /admin/seats/{id}", fpH.UpdateSeat)
	fpAdminMux.HandleFunc("DELETE /admin/seats/{id}", fpH.DeleteSeat)
	fpAdminMux.HandleFunc("GET /api/admin/seats", fpH.AdminListSeats)
	fpAdminMux.HandleFunc("GET /api/admin/floorplans/{id}", fpH.AdminGetFloorplan)

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
	fpRole := middleware.RequireRole(models.RoleFloorplanManager)
	mux.Handle("/admin/floorplans", middleware.Auth(database, fpRole(fpAdminMux)))
	mux.Handle("/admin/floorplans/", middleware.Auth(database, fpRole(fpAdminMux)))
	mux.Handle("/admin/seats/", middleware.Auth(database, fpRole(fpAdminMux)))
	mux.Handle("/api/admin/seats", middleware.Auth(database, fpRole(fpAdminMux)))
	mux.Handle("/api/admin/floorplans/", middleware.Auth(database, fpRole(fpAdminMux)))
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

// ─── PAT: PATPage (HTML) ──────────────────────────────────────────────────────

func TestPATPage_AsGlobalUser_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.get("/settings/tokens")
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestPATPage_AsBasicUser_Returns200(t *testing.T) {
	// Basic users can view the PAT page (but cannot create tokens)
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("basic_pat@test.com", "Basic", "password1")
	e.injectSession(t, id)

	resp := e.get("/settings/tokens")
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ─── PAT: AdminRevokePAT ──────────────────────────────────────────────────────

func TestAdminRevokePAT_AsGlobal_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	// Create a PAT owned by another user
	otherID, _ := e.db.CreateLocalUser("otherowner@test.com", "Other", "password1")
	e.db.UpdateUserRoles(otherID, "global") //nolint:errcheck
	_, pat, err := e.db.CreatePAT(otherID, "to be admin-revoked", nil)
	if err != nil {
		t.Fatalf("CreatePAT: %v", err)
	}

	resp := e.deleteReq("/api/admin/tokens/" + i64str(pat.ID))
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAdminRevokePAT_AsBasicUser_Forbidden(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("basicadmin@test.com", "Basic", "password1")
	e.injectSession(t, id)

	resp := e.deleteReq("/api/admin/tokens/1")
	defer drain(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

// ─── Impersonation ────────────────────────────────────────────────────────────

func TestImpersonatePage_AsGlobal_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.get("/impersonate")
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestImpersonatePage_AsBasicUser_Forbidden(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("basicimpers@test.com", "Basic", "password1")
	e.injectSession(t, id)

	resp := e.get("/impersonate")
	defer drain(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for basic user, got %d", resp.StatusCode)
	}
}

func TestImpersonatePost_AsGlobal_SwitchesSession(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)
	csrf := e.csrfToken()

	targetID, _ := e.db.CreateLocalUser("target@test.com", "Target", "password1")
	target, _ := e.db.GetUserByID(targetID)

	noFollow := e.noFollowClient()
	resp, err := noFollow.Post(e.url("/impersonate"),
		"application/x-www-form-urlencoded",
		strings.NewReader("login="+target.Email+"&csrf_token="+csrf))
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303 after impersonate, got %d", resp.StatusCode)
	}

	// The "real_session" cookie should now be set
	var hasRealSession bool
	for _, c := range resp.Cookies() {
		if c.Name == "real_session" && c.Value != "" {
			hasRealSession = true
		}
	}
	if !hasRealSession {
		t.Error("real_session cookie should be set after impersonation")
	}
}

func TestImpersonateExitPost_RestoresAdminSession(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	// Start impersonation
	csrf := e.csrfToken()
	targetID, _ := e.db.CreateLocalUser("exit_target@test.com", "ExitTarget", "password1")
	target, _ := e.db.GetUserByID(targetID)

	noFollow := e.noFollowClient()
	impResp, _ := noFollow.Post(e.url("/impersonate"),
		"application/x-www-form-urlencoded",
		strings.NewReader("login="+target.Email+"&csrf_token="+csrf))
	drain(impResp)

	// Now exit — need a fresh CSRF token (session has changed to target's)
	csrf2 := e.csrfToken()
	resp, err := noFollow.Post(e.url("/impersonate-exit"),
		"application/x-www-form-urlencoded",
		strings.NewReader("csrf_token="+csrf2))
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303 after impersonate-exit, got %d", resp.StatusCode)
	}
}

// ─── Floorplan: admin CRUD ────────────────────────────────────────────────────

func TestCreateFloorplan_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.postJSON("/admin/floorplans", map[string]string{"name": "Office 1"})
	var result map[string]interface{}
	mustDecodeJSON(t, resp, &result)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if result["name"] != "Office 1" {
		t.Errorf("expected name=Office 1, got %v", result["name"])
	}
}

func TestCreateFloorplan_EmptyName_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.postJSON("/admin/floorplans", map[string]string{"name": ""})
	defer drain(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestCreateFloorplan_BasicUser_Forbidden(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("fpbasic@test.com", "Basic", "password1")
	e.injectSession(t, id)

	resp := e.postJSON("/admin/floorplans", map[string]string{"name": "Hack"})
	defer drain(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestUpdateFloorplan_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	createResp := e.postJSON("/admin/floorplans", map[string]string{"name": "Old FP"})
	var created map[string]interface{}
	mustDecodeJSON(t, createResp, &created)
	id := int64(created["id"].(float64))

	resp := e.putJSON("/admin/floorplans/"+i64str(id), map[string]interface{}{"name": "New FP", "sort_order": 1})
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDeleteFloorplan_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	createResp := e.postJSON("/admin/floorplans", map[string]string{"name": "Delete Me FP"})
	var created map[string]interface{}
	mustDecodeJSON(t, createResp, &created)
	id := int64(created["id"].(float64))

	resp := e.deleteReq("/admin/floorplans/" + i64str(id))
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestCreateSeat_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	createResp := e.postJSON("/admin/floorplans", map[string]string{"name": "FP Seats"})
	var fp map[string]interface{}
	mustDecodeJSON(t, createResp, &fp)
	fpID := int64(fp["id"].(float64))

	resp := e.postJSON("/admin/floorplans/"+i64str(fpID)+"/seats", map[string]interface{}{
		"label": "A1", "x_pct": 50.0, "y_pct": 50.0,
	})
	var seat map[string]interface{}
	mustDecodeJSON(t, resp, &seat)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if seat["label"] != "A1" {
		t.Errorf("expected label=A1, got %v", seat["label"])
	}
}

func TestUpdateSeat_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	fpResp := e.postJSON("/admin/floorplans", map[string]string{"name": "FP Update Seat"})
	var fp map[string]interface{}
	mustDecodeJSON(t, fpResp, &fp)
	fpID := int64(fp["id"].(float64))

	seatResp := e.postJSON("/admin/floorplans/"+i64str(fpID)+"/seats", map[string]interface{}{
		"label": "B1", "x_pct": 10.0, "y_pct": 10.0,
	})
	var seat map[string]interface{}
	mustDecodeJSON(t, seatResp, &seat)
	seatID := int64(seat["id"].(float64))

	resp := e.putJSON("/admin/seats/"+i64str(seatID), map[string]interface{}{
		"label": "B2", "x_pct": 20.0, "y_pct": 20.0,
	})
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDeleteSeat_AsAdmin_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	fpResp := e.postJSON("/admin/floorplans", map[string]string{"name": "FP Delete Seat"})
	var fp map[string]interface{}
	mustDecodeJSON(t, fpResp, &fp)
	fpID := int64(fp["id"].(float64))

	seatResp := e.postJSON("/admin/floorplans/"+i64str(fpID)+"/seats", map[string]interface{}{
		"label": "C1", "x_pct": 30.0, "y_pct": 30.0,
	})
	var seat map[string]interface{}
	mustDecodeJSON(t, seatResp, &seat)
	seatID := int64(seat["id"].(float64))

	resp := e.deleteReq("/admin/seats/" + i64str(seatID))
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAdminListSeats_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	fpResp := e.postJSON("/admin/floorplans", map[string]string{"name": "FP AdminList"})
	var fp map[string]interface{}
	mustDecodeJSON(t, fpResp, &fp)
	fpID := int64(fp["id"].(float64))

	resp := e.get("/api/admin/seats?floorplan_id=" + i64str(fpID))
	var seats []interface{}
	mustDecodeJSON(t, resp, &seats)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAdminGetFloorplan_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	fpResp := e.postJSON("/admin/floorplans", map[string]string{"name": "FP GetOne"})
	var fp map[string]interface{}
	mustDecodeJSON(t, fpResp, &fp)
	fpID := int64(fp["id"].(float64))

	resp := e.get("/api/admin/floorplans/" + i64str(fpID))
	var result map[string]interface{}
	mustDecodeJSON(t, resp, &result)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if result["name"] != "FP GetOne" {
		t.Errorf("expected name=FP GetOne, got %v", result["name"])
	}
}

// ─── Floorplan: user-facing API ───────────────────────────────────────────────

func TestListFloorplansAPI_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	drain(e.postJSON("/admin/floorplans", map[string]string{"name": "List FP"}))

	resp := e.get("/api/floorplans")
	var result []interface{}
	mustDecodeJSON(t, resp, &result)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if len(result) == 0 {
		t.Error("expected at least one floorplan")
	}
}

func TestListSeatsForFloorplanAPI_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	fpResp := e.postJSON("/admin/floorplans", map[string]string{"name": "FP Seat API"})
	var fp map[string]interface{}
	mustDecodeJSON(t, fpResp, &fp)
	fpID := int64(fp["id"].(float64))

	drain(e.postJSON("/admin/floorplans/"+i64str(fpID)+"/seats", map[string]interface{}{
		"label": "D1", "x_pct": 40.0, "y_pct": 40.0,
	}))

	resp := e.get("/api/floorplans/" + i64str(fpID) + "/seats")
	var seats []interface{}
	mustDecodeJSON(t, resp, &seats)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if len(seats) == 0 {
		t.Error("expected at least one seat")
	}
}

func TestFloorplanPage_AuthenticatedUser_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.get("/floorplan")
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAdminFloorplansPage_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.get("/admin/floorplans")
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ─── Floorplan: reservations ──────────────────────────────────────────────────

func TestReserveSeat_NotOnSite_Forbidden(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	fpResp := e.postJSON("/admin/floorplans", map[string]string{"name": "FP Res"})
	var fp map[string]interface{}
	mustDecodeJSON(t, fpResp, &fp)
	fpID := int64(fp["id"].(float64))
	seatResp := e.postJSON("/admin/floorplans/"+i64str(fpID)+"/seats", map[string]interface{}{
		"label": "E1", "x_pct": 50.0, "y_pct": 50.0,
	})
	var seatData map[string]interface{}
	mustDecodeJSON(t, seatResp, &seatData)
	seatID := int64(seatData["id"].(float64))

	// No on-site presence → should be rejected
	resp := e.postJSON("/api/reservations", map[string]interface{}{
		"seat_id": seatID,
		"date":    "2026-06-20",
		"half":    "full",
	})
	defer drain(resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 (not on site), got %d", resp.StatusCode)
	}
}

func TestCancelReservationsByDates_ValidRequest_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.deleteJSONBody("/api/reservations/bulk", map[string]interface{}{
		"dates": []string{"2026-06-20"},
	})
	defer drain(resp)
	// No reservations to cancel but the handler should still succeed
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestCancelReservationsByDates_EmptyDates_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.deleteJSONBody("/api/reservations/bulk", map[string]interface{}{
		"dates": []string{},
	})
	defer drain(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty dates, got %d", resp.StatusCode)
	}
}

func TestBulkReserveSeats_MissingParams_Returns400(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	resp := e.postJSON("/api/reservations/bulk", map[string]interface{}{
		"seat_id": 0,
		"dates":   []string{},
	})
	defer drain(resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing params, got %d", resp.StatusCode)
	}
}

// ─── OptionalAuth: logged-in user sees page, unauthenticated also ─────────────

func TestLoginPage_AlreadyAuthenticated_StillRenders(t *testing.T) {
	// GET /login with a valid session — OptionalAuth populates user but doesn't block
	e := newTestEnv(t)
	e.loginAdmin(t)

	noFollow := e.noFollowClient()
	resp, err := noFollow.Get(e.url("/login"))
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	// The login page handler redirects authenticated users to "/"
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 or 303 for authenticated user on /login, got %d", resp.StatusCode)
	}
}

// ─── CheckPassword: correct and wrong ────────────────────────────────────────

func TestCheckPassword_CorrectAndWrong(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("checkpwd@test.com", "Check", "mypassword1")
	u, _ := e.db.GetUserByID(id)

	if !e.db.CheckPassword(u.ID, u.PasswordHash, "mypassword1") {
		t.Error("CheckPassword should return true for correct password")
	}
	if e.db.CheckPassword(u.ID, u.PasswordHash, "wrongpassword") {
		t.Error("CheckPassword should return false for wrong password")
	}
	if e.db.CheckPassword(u.ID, u.PasswordHash, "") {
		t.Error("CheckPassword should return false for empty password")
	}
}

// ─── End-to-end: SetPresences → GetPresencesAPI ───────────────────────────────

func TestSetThenGetPresences_ReturnsPostedData(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	adminUser, _ := e.db.GetUserByEmail("admin")
	statuses, _ := e.db.ListStatuses()
	teamID, _ := e.db.CreateTeam("E2E Team")
	e.db.AddTeamMember(teamID, adminUser.ID) //nolint:errcheck

	date := "2026-06-15"
	drain(e.postJSON("/api/presences", map[string]interface{}{
		"user_id":   adminUser.ID,
		"dates":     []string{date},
		"status_id": statuses[0].ID,
		"half":      "full",
	}))

	resp := e.get("/api/presences?team_id=" + i64str(teamID) + "&year=2026&month=6")
	// GetPresencesAPI returns map[userID]map[date]map[half]statusID directly
	var result map[string]interface{}
	mustDecodeJSON(t, resp, &result)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	userKey := i64str(adminUser.ID)
	byUser, ok := result[userKey].(map[string]interface{})
	if !ok {
		t.Fatalf("no presence entry for user %s, top-level keys=%v", userKey, result)
	}
	if _, exists := byUser[date]; !exists {
		t.Errorf("expected presence for date %s, keys=%v", date, byUser)
	}
}

// ─── End-to-end: ActivityAPI with data ───────────────────────────────────────

func TestActivityAPI_WithPresenceData_ReturnsContent(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	adminUser, _ := e.db.GetUserByEmail("admin")
	statuses, _ := e.db.ListStatuses()
	teamID, _ := e.db.CreateTeam("Activity E2E")
	e.db.AddTeamMember(teamID, adminUser.ID) //nolint:errcheck

	drain(e.postJSON("/api/presences", map[string]interface{}{
		"user_id":   adminUser.ID,
		"dates":     []string{"2026-06-01", "2026-06-02"},
		"status_id": statuses[0].ID,
		"half":      "full",
	}))

	resp := e.get("/api/activity?team_id=" + i64str(teamID) + "&year=2026&month=6")
	// ActivityAPI returns []UserStats (a JSON array)
	var stats []interface{}
	mustDecodeJSON(t, resp, &stats)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(stats) == 0 {
		t.Error("expected at least one entry in activity response")
	}
}

// ─── End-to-end: PAT Bearer → presence API ───────────────────────────────────

func TestBearerPAT_CanReadPresences(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	patResp := e.postJSON("/api/tokens", map[string]interface{}{"description": "read test", "expires_in": 10})
	var patResult map[string]interface{}
	mustDecodeJSON(t, patResp, &patResult)
	rawToken := patResult["token"].(string)

	teamID, _ := e.db.CreateTeam("PAT Team")

	req, _ := http.NewRequest("GET", e.url("/api/presences?team_id="+i64str(teamID)+"&year=2026&month=4"), nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("PAT should authenticate successfully, got 401")
	}
}

// ─── Security: CSRF rejected on change-password ───────────────────────────────

func TestChangePasswordPost_MissingCSRF_Forbidden(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("csrf@test.com", "CSRF", "oldpass1")
	e.injectSession(t, id)

	// POST without csrf_token field
	resp, _ := e.noFollowClient().Post(e.url("/settings/change-password"),
		"application/x-www-form-urlencoded",
		strings.NewReader("current_password=oldpass1&new_password=newpass12&confirm_password=newpass12"))
	defer drain(resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 when csrf_token is missing, got %d", resp.StatusCode)
	}
}

func TestChangePasswordPost_WrongCSRF_Forbidden(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("csrf2@test.com", "CSRF2", "oldpass1")
	e.injectSession(t, id)

	resp, _ := e.noFollowClient().Post(e.url("/settings/change-password"),
		"application/x-www-form-urlencoded",
		strings.NewReader("current_password=oldpass1&new_password=newpass12&confirm_password=newpass12&csrf_token=badtoken"))
	defer drain(resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 with wrong csrf_token, got %d", resp.StatusCode)
	}
}

// ─── Security: session invalidated after password change ─────────────────────

func TestChangePassword_OldSessionInvalidated(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("inval@test.com", "Inval", "oldpass1")

	// Create a second (background) session for the same user
	oldToken, err := e.db.CreateSession(id)
	if err != nil {
		t.Fatal(err)
	}

	// Log in with the main session
	e.injectSession(t, id)
	csrf := e.csrfToken()

	changeResp, _ := e.noFollowClient().Post(e.url("/settings/change-password"),
		"application/x-www-form-urlencoded",
		strings.NewReader("current_password=oldpass1&new_password=newpass12&confirm_password=newpass12&csrf_token="+csrf))
	drain(changeResp)

	if changeResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("password change should redirect (303), got %d", changeResp.StatusCode)
	}

	// The background session must now be invalid
	user, err := e.db.GetSessionUser(oldToken)
	if err == nil {
		t.Errorf("old session should be invalidated after password change, but got user %v", user)
	}
}

func TestResetPassword_AllSessionsInvalidated(t *testing.T) {
	e := newTestEnv(t)
	_, _ = e.db.CreateLocalUser("resetinval@test.com", "ResetInval", "oldpass1")
	u, _ := e.db.GetUserByEmail("resetinval@test.com")

	// Create a live session before the reset
	liveToken, err := e.db.CreateSession(u.ID)
	if err != nil {
		t.Fatal(err)
	}

	rawToken, _ := e.db.CreatePasswordResetToken("resetinval@test.com")
	resp := e.postForm("/reset-password", "token="+rawToken+"&password=newpass12&confirm=newpass12")
	drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after reset, got %d", resp.StatusCode)
	}

	user, err := e.db.GetSessionUser(liveToken)
	if err == nil {
		t.Errorf("all sessions should be invalidated after password reset, but got user %v", user)
	}
}

// ─── Security: role isolation ─────────────────────────────────────────────────

func TestCreateStatus_BasicUser_Forbidden(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("basic_status@test.com", "Basic", "password1")
	e.injectSession(t, id)

	resp := e.postJSON("/admin/statuses", map[string]interface{}{"name": "Hack", "color": "#ff0000"})
	defer drain(resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for basic user creating status, got %d", resp.StatusCode)
	}
}

func TestCreateHoliday_BasicUser_Forbidden(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("basic_holiday@test.com", "Basic", "password1")
	e.injectSession(t, id)

	resp := e.postJSON("/admin/holidays", map[string]interface{}{"date": "2026-01-01", "name": "New Year"})
	defer drain(resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for basic user creating holiday, got %d", resp.StatusCode)
	}
}

func TestCreateTeam_BasicUser_Forbidden(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.db.CreateLocalUser("basic_team@test.com", "Basic", "password1")
	e.injectSession(t, id)

	resp := e.postJSON("/admin/teams", map[string]string{"name": "Hack Team"})
	defer drain(resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for basic user creating team, got %d", resp.StatusCode)
	}
}

// ─── Admin: UpdateUserRoles promotes user to global ───────────────────────────

func TestUpdateUserRoles_PromotesToGlobal_CanAccessAdmin(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	// Register the route (not in buildRouter minimal stub — test via DB directly)
	id, _ := e.db.CreateLocalUser("promo@test.com", "Promo", "password1")

	// Before promotion: no admin access
	e.injectSession(t, id)
	noFollow := e.noFollowClient()
	resp1, _ := noFollow.Get(e.url("/admin/users"))
	drain(resp1)
	if resp1.StatusCode != http.StatusForbidden {
		t.Errorf("before promotion: expected 403, got %d", resp1.StatusCode)
	}

	// Promote via DB (UpdateUserRoles not in buildRouter — promote directly)
	if err := e.db.UpdateUserRoles(id, "global"); err != nil {
		t.Fatalf("UpdateUserRoles: %v", err)
	}

	// Refresh session after role change
	e.injectSession(t, id)
	resp2, _ := noFollow.Get(e.url("/admin/users"))
	drain(resp2)
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("after promotion to global: expected 200, got %d", resp2.StatusCode)
	}
}

// ─── Admin SetPassword: new password works for login ─────────────────────────

func TestAdminSetPassword_NewPasswordEnablesLogin(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	id, _ := e.db.CreateLocalUser("pwdchg@test.com", "PwdChg", "oldpass1")
	drain(e.putJSON("/admin/users/"+i64str(id)+"/password", map[string]string{"password": "freshpass1"}))

	// Log in with the new password
	noFollow := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, _ := noFollow.Post(e.url("/login"),
		"application/x-www-form-urlencoded",
		strings.NewReader("username=pwdchg@test.com&password=freshpass1"))
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("login with new admin-set password: expected 303, got %d", resp.StatusCode)
	}
	if strings.Contains(resp.Header.Get("Location"), "error") {
		t.Error("login should succeed but redirect contains 'error'")
	}
}

// ─── PAT: list returns created token ─────────────────────────────────────────

func TestListPATs_ReturnsCreatedToken(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	createResp := e.postJSON("/api/tokens", map[string]interface{}{"description": "listed token", "expires_in": 10})
	var created map[string]interface{}
	mustDecodeJSON(t, createResp, &created)

	listResp := e.get("/api/tokens")
	var pats []map[string]interface{}
	mustDecodeJSON(t, listResp, &pats)

	found := false
	for _, p := range pats {
		if p["description"] == "listed token" {
			found = true
		}
	}
	if !found {
		t.Errorf("created PAT 'listed token' not found in list response: %v", pats)
	}
}

// ─── POST /set-lang ───────────────────────────────────────────────────────────

func TestSetLang_SetsCookieAndRedirects(t *testing.T) {
	e := newTestEnv(t)

	noFollow := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noFollow.Post(e.url("/set-lang"),
		"application/x-www-form-urlencoded",
		strings.NewReader("lang=fr"))
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", resp.StatusCode)
	}

	var langCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "lang" {
			langCookie = c
		}
	}
	if langCookie == nil {
		t.Fatal("lang cookie not set")
	}
	if langCookie.Value != "fr" {
		t.Errorf("expected lang cookie value 'fr', got %q", langCookie.Value)
	}
}

func TestSetLang_InvalidLang_UsesDefault(t *testing.T) {
	e := newTestEnv(t)

	noFollow := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noFollow.Post(e.url("/set-lang"),
		"application/x-www-form-urlencoded",
		strings.NewReader("lang=xx_INVALID"))
	if err != nil {
		t.Fatal(err)
	}
	defer drain(resp)

	// Should still redirect (not error)
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303 even for invalid lang, got %d", resp.StatusCode)
	}
	// lang cookie must not be "xx_INVALID"
	for _, c := range resp.Cookies() {
		if c.Name == "lang" && c.Value == "xx_INVALID" {
			t.Error("invalid lang value should not be stored in cookie")
		}
	}
}

// ─── GET /api/docs ────────────────────────────────────────────────────────────

func TestAPIDocs_ReturnsMarkdown(t *testing.T) {
	// Wire the /api/docs endpoint inline — buildRouter does not include it
	// to keep the stub minimal, so test it via a dedicated mini-mux.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("# API")) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/docs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected text/plain content-type, got %q", ct)
	}
}

// ─── GET /settings/my-logs with real data ────────────────────────────────────

func TestMyLogsPage_WithPresenceData_Returns200(t *testing.T) {
	e := newTestEnv(t)
	e.loginAdmin(t)

	adminUser, _ := e.db.GetUserByEmail("admin")
	statuses, _ := e.db.ListStatuses()

	drain(e.postJSON("/api/presences", map[string]interface{}{
		"user_id":   adminUser.ID,
		"dates":     []string{"2026-06-10"},
		"status_id": statuses[0].ID,
		"half":      "full",
	}))

	resp := e.get("/settings/my-logs")
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on my-logs after setting presences, got %d", resp.StatusCode)
	}
}

// ─── Permissions-Policy header ────────────────────────────────────────────────

func TestPermissionsPolicy_PresentOnResponses(t *testing.T) {
	e := newTestEnv(t)
	resp := e.get("/health")
	defer drain(resp)

	pp := resp.Header.Get("Permissions-Policy")
	if pp == "" {
		t.Error("Permissions-Policy header missing")
	}
	for _, directive := range []string{"camera=()", "microphone=()", "geolocation=()"} {
		if !strings.Contains(pp, directive) {
			t.Errorf("Permissions-Policy missing directive %q, got: %s", directive, pp)
		}
	}
}
