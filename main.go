package main

import (
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"presence-app/internal/config"
	"presence-app/internal/db"
	"presence-app/internal/handlers"
	"presence-app/internal/middleware"
	"presence-app/internal/models"
)

//go:embed web/templates/*.html
var templateFS embed.FS

//go:embed web/static
var staticFS embed.FS

func main() {
	cfg := config.Load()

	// Ensure data directory exists
	os.MkdirAll(cfg.DataDir, 0755)

	// Open database
	database, err := db.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("Database error: %v", err)
	}
	defer database.Close()

	// Seed defaults
	if err := database.SeedDefaults(cfg.AdminUser, cfg.AdminPassword); err != nil {
		log.Fatalf("Seed error: %v", err)
	}

	// Clean expired sessions periodically
	database.CleanExpiredSessions()

	// Parse templates
	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i
			}
			return s
		},
		"json": func(v interface{}) template.JS {
			b, _ := json.Marshal(v)
			return template.JS(b)
		},
		"statusColor": func(statuses []models.Status, id int64) string {
			for _, s := range statuses {
				if s.ID == id {
					return s.Color
				}
			}
			return "#e5e7eb"
		},
		"statusName": func(statuses []models.Status, id int64) string {
			for _, s := range statuses {
				if s.ID == id {
					return s.Name
				}
			}
			return ""
		},
		"hasKey": func(m map[string]int64, key string) bool {
			_, ok := m[key]
			return ok
		},
		"getKey": func(m map[string]int64, key string) int64 {
			if m == nil {
				return 0
			}
			return m[key]
		},
		"getCount": func(m map[int64]int, key int64) int {
			return m[key]
		},
		"getStrCount": func(m map[string]int, key string) int {
			return m[key]
		},
		"sumMap": func(m map[int64]int) int {
			total := 0
			for _, v := range m {
				total += v
			}
			return total
		},
		"dict": func(pairs ...interface{}) map[string]interface{} {
			d := make(map[string]interface{})
			for i := 0; i < len(pairs)-1; i += 2 {
				d[pairs[i].(string)] = pairs[i+1]
			}
			return d
		},
		"intToInt64": func(i int) int64 { return int64(i) },
		"percent": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a * 100 / b
		},
		"hasRole": func(user *models.User, role string) bool {
			if user == nil {
				return false
			}
			return user.HasRole(role)
		},
	}

	templates := make(map[string]*template.Template)
	pages := []string{"login", "calendar", "admin_teams", "admin_statuses", "admin_activity", "admin_holidays", "admin_users", "admin_user_logs"}
	for _, page := range pages {
		t, err := template.New("").Funcs(funcMap).ParseFS(
			templateFS,
			"web/templates/layout.html",
			"web/templates/"+page+".html",
		)
		if err != nil {
			log.Fatalf("Template parse error (%s): %v", page, err)
		}
		templates[page] = t
	}

	// Render helper
	renderPage := func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
		user := middleware.GetUser(r)
		// Check if logo exists
		logoExists := false
		if cfg.LogoPath != "" {
			if _, err := os.Stat(filepath.Join(cfg.DataDir, cfg.LogoPath)); err == nil {
				logoExists = true
			}
		} else {
			if _, err := os.Stat(filepath.Join(cfg.DataDir, "logo.png")); err == nil {
				logoExists = true
			}
		}

		pd := models.PageData{
			Config: map[string]string{
				"AppName":        cfg.AppName,
				"PrimaryColor":   cfg.PrimaryColor,
				"SecondaryColor": cfg.SecondaryColor,
				"AccentColor":    cfg.AccentColor,
			},
			User:        user,
			Page:        page,
			Data:        data,
			SAMLEnabled: cfg.SAMLEnabled,
			HideFooter:  cfg.HideFooter,
			AppVersion:  config.Version,
		}
		_ = logoExists
		// Add logo flag to config map
		configMap := pd.Config.(map[string]string)
		if logoExists {
			configMap["LogoURL"] = "/data/logo.png"
		}

		tmpl, ok := templates[page]
		if !ok {
			http.Error(w, "Template not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "layout", pd); err != nil {
			log.Printf("Template render error: %v", err)
		}
	}

	// Initialize handlers
	healthHandler := &handlers.HealthHandler{DB: database, StartedAt: time.Now()}
	authHandler := &handlers.AuthHandler{DB: database, Config: cfg, Render: renderPage}
	calHandler := &handlers.CalendarHandler{DB: database, Render: renderPage}
	adminHandler := &handlers.AdminHandler{DB: database, Render: renderPage}
	activityHandler := &handlers.ActivityHandler{DB: database, Render: renderPage}
	holidaysHandler := &handlers.HolidaysHandler{DB: database, Render: renderPage}
	usersAdminHandler := &handlers.UsersAdminHandler{DB: database, Render: renderPage}

	// Initialize SAML if configured
	if cfg.SAMLEnabled {
		if err := authHandler.InitSAML(); err != nil {
			log.Printf("WARNING: SAML initialization failed: %v", err)
			log.Printf("SAML SSO will be disabled. Fix configuration and restart.")
			cfg.SAMLEnabled = false
		}
	}

	// Router
	mux := http.NewServeMux()

	// Static files (embedded)
	staticSub, _ := fs.Sub(staticFS, "web/static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticSub)))

	// Serve logo and data files
	mux.HandleFunc("GET /data/", func(w http.ResponseWriter, r *http.Request) {
		// Only serve specific safe files from data dir
		name := filepath.Base(r.URL.Path)
		allowed := map[string]bool{"logo.png": true, "logo.svg": true, "logo.jpg": true}
		if !allowed[name] {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(cfg.DataDir, name))
	})

	// Health check (public, no auth)
	mux.HandleFunc("GET /health", healthHandler.Health)

	// Auth routes (public)
	mux.Handle("GET /login", middleware.OptionalAuth(database, http.HandlerFunc(authHandler.LoginPage)))
	mux.HandleFunc("POST /login", authHandler.LocalLogin)
	mux.HandleFunc("GET /logout", authHandler.Logout)

	// SAML routes
	mux.HandleFunc("GET /saml/metadata", authHandler.SAMLMetadata)
	mux.HandleFunc("GET /saml/login", authHandler.SAMLLogin)
	mux.HandleFunc("POST /saml/acs", authHandler.SAMLACS)

	// Protected routes
	authMux := http.NewServeMux()

	// Calendar (main page)
	authMux.HandleFunc("GET /", calHandler.CalendarPage)
	authMux.HandleFunc("GET /{$}", calHandler.CalendarPage)

	// Presence API
	authMux.HandleFunc("POST /api/presences", calHandler.SetPresences)
	authMux.HandleFunc("POST /api/presences/clear", calHandler.ClearPresences)
	authMux.HandleFunc("GET /api/presences", calHandler.GetPresencesAPI)

	// Admin routes - each section guarded by its own role
	teamMux := http.NewServeMux()
	teamMux.HandleFunc("GET /admin/teams", adminHandler.TeamsPage)
	teamMux.HandleFunc("POST /admin/teams", adminHandler.CreateTeam)
	teamMux.HandleFunc("PUT /admin/teams/{id}", adminHandler.UpdateTeam)
	teamMux.HandleFunc("DELETE /admin/teams/{id}", adminHandler.DeleteTeam)
	teamMux.HandleFunc("POST /admin/teams/{id}/members", adminHandler.AddTeamMember)
	teamMux.HandleFunc("DELETE /admin/teams/{id}/members/{userId}", adminHandler.RemoveTeamMember)

	statusMux := http.NewServeMux()
	statusMux.HandleFunc("GET /admin/statuses", adminHandler.StatusesPage)
	statusMux.HandleFunc("POST /admin/statuses", adminHandler.CreateStatus)
	statusMux.HandleFunc("PUT /admin/statuses/{id}", adminHandler.UpdateStatus)
	statusMux.HandleFunc("DELETE /admin/statuses/{id}", adminHandler.DeleteStatus)

	activityMux := http.NewServeMux()
	activityMux.HandleFunc("GET /admin/activity", activityHandler.ActivityPage)
	activityMux.HandleFunc("GET /api/activity", activityHandler.ActivityAPI)

	holidaysMux := http.NewServeMux()
	holidaysMux.HandleFunc("GET /admin/holidays", holidaysHandler.HolidaysPage)
	holidaysMux.HandleFunc("POST /admin/holidays", holidaysHandler.CreateHoliday)
	holidaysMux.HandleFunc("PUT /admin/holidays/{id}", holidaysHandler.UpdateHoliday)
	holidaysMux.HandleFunc("DELETE /admin/holidays/{id}", holidaysHandler.DeleteHoliday)

	usersMux := http.NewServeMux()
	usersMux.HandleFunc("GET /admin/users", usersAdminHandler.UsersPage)
	usersMux.HandleFunc("POST /admin/users", usersAdminHandler.CreateUser)
	usersMux.HandleFunc("GET /admin/users/{id}/logs", usersAdminHandler.UserLogsPage)
	usersMux.HandleFunc("PUT /admin/users/{id}", usersAdminHandler.UpdateUser)
	usersMux.HandleFunc("PUT /admin/users/{id}/password", usersAdminHandler.SetPassword)
	usersMux.HandleFunc("PUT /admin/users/{id}/disabled", usersAdminHandler.SetDisabled)
	usersMux.HandleFunc("DELETE /admin/users/{id}", usersAdminHandler.DeleteUser)
	usersMux.HandleFunc("GET /api/users", adminHandler.UsersAPI)
	usersMux.HandleFunc("PUT /api/users/{id}/roles", adminHandler.UpdateUserRoles)
	usersMux.HandleFunc("GET /admin/roles", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/users", http.StatusMovedPermanently)
	})

	// Wire role-based middleware
	mux.Handle("/admin/teams", middleware.Auth(database, middleware.RequireRole(models.RoleTeamManager, models.RoleTeamLeader)(teamMux)))
	mux.Handle("/admin/teams/", middleware.Auth(database, middleware.RequireRole(models.RoleTeamManager, models.RoleTeamLeader)(teamMux)))
	mux.Handle("/admin/statuses", middleware.Auth(database, middleware.RequireRole(models.RoleStatusManager)(statusMux)))
	mux.Handle("/admin/statuses/", middleware.Auth(database, middleware.RequireRole(models.RoleStatusManager)(statusMux)))
	mux.Handle("/admin/activity", middleware.Auth(database, middleware.RequireRole(models.RoleActivityViewer, models.RoleTeamLeader)(activityMux)))
	mux.Handle("/api/activity", middleware.Auth(database, middleware.RequireRole(models.RoleActivityViewer, models.RoleTeamLeader)(activityMux)))
	mux.Handle("/admin/holidays", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(holidaysMux)))
	mux.Handle("/admin/holidays/", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(holidaysMux)))
	mux.Handle("/admin/roles", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(usersMux)))
	mux.Handle("/api/users", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(usersMux)))
	mux.Handle("/api/users/", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(usersMux)))
	mux.Handle("/admin/users", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(usersMux)))
	mux.Handle("/admin/users/", middleware.Auth(database, middleware.RequireRole(models.RoleGlobal)(usersMux)))
	mux.Handle("/", middleware.Auth(database, authMux))

	// Start server
	addr := ":" + cfg.Port
	log.Printf("🚀 %s démarré sur http://localhost%s", cfg.AppName, addr)
	log.Printf("   Admin: %s / %s", cfg.AdminUser, cfg.AdminPassword)
	if cfg.SAMLEnabled {
		log.Printf("   SAML SSO: activé (Entity ID: %s)", cfg.SAMLEntityID)
	}
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
