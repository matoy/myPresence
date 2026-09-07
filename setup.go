package main

import (
	"crypto/subtle"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/matoy/mypresence/internal/config"
	"github.com/matoy/mypresence/internal/db"
	"github.com/matoy/mypresence/internal/i18n"
	"github.com/matoy/mypresence/internal/middleware"
	"github.com/matoy/mypresence/internal/models"
)

// buildTemplateFuncMap constructs the FuncMap used by all HTML templates.
func buildTemplateFuncMap(cfg *config.Config) template.FuncMap {
	return template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"flagFor": models.FlagForCountry,
		// safehtml marks a string as safe HTML so html/template does not escape it.
		// Only use with strings originating from our own controlled i18n data.
		"safehtml": func(s string) template.HTML { return template.HTML(s) }, //nolint:gosec
		// safeNewsContent escapes the content and converts [text](url) syntax to safe anchor tags.
		// Only http/https URLs are allowed.
		"safeNewsContent": safeNewsContent,
		"newsBgColor":     tmplNewsBgColor,
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
		"statusBillable": func(statuses []models.Status, id int64) bool {
			for _, s := range statuses {
				if s.ID == id {
					return s.Billable
				}
			}
			return false
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
		"getCount":    func(m map[int64]int, key int64) int { return m[key] },
		"getStrCount": func(m map[string]int, key string) int { return m[key] },
		"sumMap": func(m map[int64]int) int {
			total := 0
			for _, v := range m {
				total += v
			}
			return total
		},
		// Float64 variants for half-day support
		"getCountF":    tmplGetCountF,
		"getStrCountF": tmplGetStrCountF,
		"sumMapF":      tmplSumMapF,
		"fmtF":         tmplFmtF,
		"percentF":     tmplPercentF,
		"i2f":          tmplI2F,
		"subF":         tmplSubF,
		"activityRocket": func(notSet, onSiteDays, billableDays, projectActivity float64) bool {
			return tmplActivitySummaryRocket(notSet, onSiteDays, billableDays, projectActivity, cfg.OnsiteRatioThreshold)
		},
		// Presence half-day helpers for templates
		"presenceHalf":     tmplPresenceHalf,
		"hasDatePresence":  tmplHasDatePresence,
		"presenceOverride": tmplPresenceOverride,
		"dict": func(pairs ...interface{}) map[string]interface{} {
			d := make(map[string]interface{})
			for i := 0; i < len(pairs)-1; i += 2 {
				d[pairs[i].(string)] = pairs[i+1]
			}
			return d
		},
		"intToInt64": func(i int) int64 { return int64(i) },
		"int": func(v interface{}) int {
			switch n := v.(type) {
			case int:
				return n
			case int64:
				return int(n)
			case float64:
				return int(n)
			case float32:
				return int(n)
			default:
				return 0
			}
		},
		"upper":   strings.ToUpper,
		"percent":    tmplPercent,
		"hasRole": func(user *models.User, role string) bool {
			if user == nil {
				return false
			}
			return user.HasRole(role)
		},
	}
}

// loadTemplates parses all page templates with the given FuncMap.
// Calls log.Fatalf if any template fails to parse.
func loadTemplates(funcMap template.FuncMap) map[string]*template.Template {
	pages := []string{
		"login", "calendar", "admin_teams", "admin_domains", "admin_statuses", "admin_activity",
		"admin_holidays", "admin_users", "admin_user_logs", "floorplan", "admin_floorplans", "admin_sites",
		"pat", "settings_change_password", "settings_passkeys", "forgot_password", "reset_password",
		"impersonate", "projects", "admin_projects", "admin_projects_report",
		"admin_general_settings", "admin_news", "admin_notifications", "admin_sites_report",
	}
	templates := make(map[string]*template.Template)
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
	return templates
}

// newRenderPage returns a render function that resolves the current user,
// language, CSRF token and impersonation state before executing the named template.
func newRenderPage(cfg *config.Config, database *db.DB, templates map[string]*template.Template) func(http.ResponseWriter, *http.Request, string, interface{}) {
	return func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
		user := middleware.GetUser(r)
		lang := i18n.LangFromRequest(r, cfg.DefaultLang)
		if user != nil && database != nil && user.Language != lang {
			_ = database.UpdateUserLanguage(user.ID, lang)
			user.Language = lang
		}

		// Check if a logo file exists in the data directory.
		logoExists := false
		logoFile := "logo.png"
		if cfg.LogoPath != "" {
			logoFile = cfg.LogoPath
		}
		if _, err := os.Stat(filepath.Join(cfg.DataDir, logoFile)); err == nil {
			logoExists = true
		}

		var csrfToken string
		if cookie, err := r.Cookie("session"); err == nil {
			csrfToken = middleware.GenerateCSRFToken(cfg.SecretKey, cookie.Value)
		}

		// Detect impersonation: check if a real_session cookie is present and valid.
		var realAdmin *models.User
		if realCookie, err := r.Cookie("real_session"); err == nil {
			if adminUser, err := database.GetSessionUser(realCookie.Value); err == nil && adminUser.HasRole(models.RoleGlobal) {
				realAdmin = adminUser
			}
		}

		// Determine whether the user's /projects page shows the daily-tasks
		// form (member of a team with "Timesheets managed manually" enabled).
		var userTasksMode bool
		if user != nil {
			if teams, err := database.GetUserTeams(user.ID); err == nil {
				for _, t := range teams {
					if t.TimesheetsManagedManually {
						userTasksMode = true
						break
					}
				}
			}
		}

		// Domain managers and team leaders get scoped access to Activity/Projects Report nav
		// links even without any other role.
		var isDomainManager bool
		var isTeamLeader bool
		if user != nil {
			isDomainManager, _ = database.IsDomainManager(user.ID)
			isTeamLeader, _ = database.IsTeamLeader(user.ID)
		}

		pd := models.PageData{
			Config: map[string]string{
				"AppName":        cfg.AppName,
				"PrimaryColor":   cfg.PrimaryColor,
				"SecondaryColor": cfg.SecondaryColor,
				"AccentColor":    cfg.AccentColor,
				"FontURL":        cfg.FontURL,
				"FontFamily":     cfg.FontFamily,
				"FontFamilyMono": cfg.FontFamilyMono,
			},
			User:              user,
			Page:              page,
			Data:              data,
			SAMLEnabled:       cfg.SAMLEnabled,
			SMTPEnabled:       cfg.SMTPURL != "",
			HideFooter:        cfg.HideFooter,
			AppVersion:        config.Version,
			DisableFloorplans: cfg.DisableFloorplans,
			DisableAPI:        cfg.DisableAPI,
			DisableProjects:   cfg.DisableProjects, PasskeysEnabled: cfg.EnablePasskeys, T: i18n.T(lang),
			Lang:                       lang,
			SupportedLangs:             i18n.Supported,
			CSRFToken:                  csrfToken,
			RealAdmin:                  realAdmin,
			UserTasksMode:              userTasksMode,
			IsDomainManager:            isDomainManager,
			IsTeamLeader:               isTeamLeader,
			TeamCalendarRefreshMinutes: cfg.TeamCalendarRefreshMinutes,
		}
		// Fetch active news banners and unread notifications for authenticated users.
		if user != nil {
			if activeNews, err := database.GetActiveNewsMessages(); err == nil {
				pd.ActiveNewsMessages = activeNews
			}
			if unreadNotifs, err := database.GetUnreadNotifications(user.ID); err == nil {
				tr := i18n.T(lang)
				for i := range unreadNotifs {
					unreadNotifs[i].Localize(tr)
				}
				pd.Notifications = unreadNotifs
			}
		}
		if logoExists {
			pd.Config.(map[string]string)["LogoURL"] = "/data/logo.png"
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
}

// safeNewsContent escapes all HTML in s, then converts [text](url) markdown-link
// syntax to safe <a> anchor tags. Only http:// and https:// URLs are accepted.
// The returned value is marked as template.HTML so it renders without escaping.
var newsLinkRE = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)]+)\)`)

func safeNewsContent(s string) template.HTML {
	// First escape all HTML to prevent XSS.
	escaped := template.HTMLEscapeString(s)
	// Then replace [text](url) with safe anchor tags.
	result := newsLinkRE.ReplaceAllStringFunc(escaped, func(match string) string {
		parts := newsLinkRE.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		text := parts[1] // already HTML-escaped by template.HTMLEscapeString above
		rawURL := parts[2]
		// Validate scheme (must be http or https).
		if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
			return match
		}
		urlEscaped := template.HTMLEscapeString(rawURL)
		return `<a href="` + urlEscaped + `" target="_blank" rel="noopener noreferrer" class="underline">` + text + `</a>`
	})
	return template.HTML(result) //nolint:gosec
}

// floorplanImgHandler returns a handler that serves floorplan image files from
// dataDir. Only files whose name starts with "floorplan_" and have an allowed
// image extension are served.
func floorplanImgHandler(dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		if !strings.HasPrefix(name, "floorplan_") {
			http.NotFound(w, r)
			return
		}
		ext := strings.ToLower(filepath.Ext(name))
		allowed := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}
		if !allowed[ext] {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dataDir, name))
	}
}

// dataFileHandler returns a handler that serves an allowlisted set of logo
// files from dataDir (logo.png, logo.svg, logo.jpg).
func dataFileHandler(dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		allowed := map[string]bool{"logo.png": true, "logo.svg": true, "logo.jpg": true}
		if !allowed[name] {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dataDir, name))
	}
}

// metricsHandler returns a handler that exposes Prometheus metrics behind a
// Bearer-token check. Returns 404 when metricsToken is empty (metrics disabled).
func metricsHandler(metricsToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if metricsToken == "" {
			http.Error(w, "Metrics not enabled", http.StatusNotFound)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(metricsToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mypresence-metrics"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		promhttp.Handler().ServeHTTP(w, r)
	}
}

// langSwitcherHandler returns a handler that sets the "lang" cookie to a
// supported language code, persists it to the user profile if authenticated, and redirects back.
func langSwitcherHandler(defaultLang string, database ...*db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lang := r.FormValue("lang")
		valid := false
		for _, s := range i18n.Supported {
			if s.Code == lang {
				valid = true
				break
			}
		}
		if !valid {
			lang = defaultLang
		}
		if len(database) > 0 && database[0] != nil {
			if u := middleware.GetUser(r); u != nil {
				_ = database[0].UpdateUserLanguage(u.ID, lang)
			}
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "lang",
			Value:    lang,
			Path:     "/",
			MaxAge:   365 * 24 * 3600,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		})
		// Prevent open redirect: only allow same-origin redirects.
		target := "/"
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err == nil {
				if p := u.RequestURI(); strings.HasPrefix(p, "/") {
					target = p
				}
			}
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	}
}

