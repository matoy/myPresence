// Package testhelper provides utilities for functional tests.
// It sets up a real in-memory SQLite database + a minimal HTTP server
// wired with the full middleware stack so tests exercise real code paths.
package testhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"presence-app/internal/config"
	"presence-app/internal/db"
	"presence-app/internal/models"
)

// Env is a complete test environment with a real DB, a test server and an HTTP client.
type Env struct {
	DB     *db.DB
	Cfg    *config.Config
	Server *httptest.Server
	Client *http.Client
}

// NewEnv opens an isolated SQLite DB in a temp directory and seeds it with defaults.
// The caller is responsible for registering routes onto Mux before calling this.
func NewEnv(t *testing.T) *Env {
	t.Helper()
	dir := t.TempDir()

	database, err := db.Open(dir)
	if err != nil {
		t.Fatalf("testhelper: open db: %v", err)
	}

	cfg := &config.Config{
		AdminUser:     "admin",
		AdminPassword: "admin",
		DataDir:       dir,
		DefaultLang:   "en",
		SecretKey:     "test-secret-32-chars-padded-here",
	}

	if err := database.SeedDefaults(cfg.AdminUser, cfg.AdminPassword); err != nil {
		t.Fatalf("testhelper: seed: %v", err)
	}

	t.Cleanup(func() { database.Close() })

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Follow redirects automatically (up to 10)
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	return &Env{
		DB:     database,
		Cfg:    cfg,
		Client: client,
	}
}

// StartServer starts a test HTTP server with the provided handler and updates
// the client so it targets the right base URL.
func (e *Env) StartServer(t *testing.T, handler http.Handler) {
	t.Helper()
	e.Server = httptest.NewServer(handler)
	t.Cleanup(e.Server.Close)
}

// URL returns an absolute URL for the given path.
func (e *Env) URL(path string) string {
	return e.Server.URL + path
}

// Do sends an HTTP request and returns the response. Body is automatically closed
// after the test; the caller must not read the body after the test ends.
func (e *Env) Do(method, path string, body io.Reader) *http.Response {
	req, err := http.NewRequestWithContext(context.Background(), method, e.URL(path), body)
	if err != nil {
		panic(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.Client.Do(req)
	if err != nil {
		panic(err)
	}
	return resp
}

// DoForm sends a POST form request (application/x-www-form-urlencoded).
func (e *Env) DoForm(method, path string, body io.Reader) *http.Response {
	req, err := http.NewRequestWithContext(context.Background(), method, e.URL(path), body)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.Client.Do(req)
	if err != nil {
		panic(err)
	}
	return resp
}

// DoJSON sends a request with a JSON body and returns the response.
func (e *Env) DoJSON(method, path string, payload interface{}) *http.Response {
	b, _ := json.Marshal(payload)
	return e.Do(method, path, bytes.NewReader(b))
}

// MustDecodeJSON decodes the JSON response body into v; fails the test on error.
func MustDecodeJSON(t *testing.T, resp *http.Response, v interface{}) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

// LoginSession logs in as admin and stores the session cookie in the client jar.
// Subsequent requests from the client will be authenticated.
func (e *Env) LoginSession(t *testing.T, username, password string) {
	t.Helper()
	body := bytes.NewBufferString("username=" + username + "&password=" + password)
	resp := e.DoForm("POST", "/login", body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login failed: status %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// InjectUser creates a fake session directly in the DB for the given user
// and stores the session cookie in the client jar. Useful to bypass login form.
func (e *Env) InjectUser(t *testing.T, user *models.User) string {
	t.Helper()
	token, err := e.DB.CreateSession(user.ID)
	if err != nil {
		t.Fatalf("injectUser: create session: %v", err)
	}
	// Store the cookie so the client sends it automatically
	if e.Server != nil {
		serverURL := e.Server.URL
		jar := e.Client.Jar
		req, _ := http.NewRequest("GET", serverURL+"/", nil)
		jar.SetCookies(req.URL, []*http.Cookie{{
			Name:  "session",
			Value: token,
			Path:  "/",
		}})
	}
	return token
}

// CreateBasicUser inserts a user with "basic" role and returns it.
func (e *Env) CreateBasicUser(t *testing.T, email, name, password string) *models.User {
	t.Helper()
	id, err := e.DB.CreateLocalUser(email, name, password)
	if err != nil {
		t.Fatalf("CreateBasicUser: %v", err)
	}
	u, err := e.DB.GetUserByID(id)
	if err != nil {
		t.Fatalf("CreateBasicUser GetUserByID: %v", err)
	}
	return u
}

// CreateAdminUser returns the existing seeded admin user (global role).
func (e *Env) GetAdminUser(t *testing.T) *models.User {
	t.Helper()
	u, err := e.DB.GetUserByEmail(e.Cfg.AdminUser)
	if err != nil {
		t.Fatalf("GetAdminUser: %v", err)
	}
	return u
}

// WithUserInContext returns a new request with the given user set in context.
// It uses context.WithValue with a plain string key – only useful when the test
// handler itself calls testhelper.GetUser() (not middleware.GetUser()).
// For integration tests that run through the real middleware, prefer InjectUser.
func WithUserInContext(r *http.Request, u *models.User) *http.Request {
	ctx := context.WithValue(r.Context(), ctxTestUserKey{}, u)
	return r.WithContext(ctx)
}

// ctxTestUserKey is the context key used by WithUserInContext / GetTestUser.
type ctxTestUserKey struct{}

// GetTestUser retrieves a user injected via WithUserInContext.
func GetTestUser(r *http.Request) *models.User {
	u, _ := r.Context().Value(ctxTestUserKey{}).(*models.User)
	return u
}
