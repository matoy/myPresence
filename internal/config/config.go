package config

import (
	"os"
	"reflect"
	"strconv"
)

// Version is the application version, updated manually for each release (format: YYYYMMDD-X).
const Version = "20260907-1"

// Config holds all application configuration loaded from environment variables.
//
// Fields tagged `env:"..."` name the environment variable they are loaded
// from. Fields additionally tagged `live:"true"` can be changed at runtime
// via the general settings page (ApplyEnvOverride), with no other code to
// update — adding those two tags to a new field is enough to make it
// live-editable. Structural fields (server port, data directory, secret key,
// database connection, SAML) are intentionally left untagged for `live`
// since altering them without restarting the process would have no effect
// or would break active sessions.
type Config struct {
	// Server
	Port      string
	DataDir   string
	SecretKey string

	// Database backend
	DBDriver   string // sqlite (default), postgres, mysql, sqlserver
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
	DBSSLMode  string // postgres: disable|require|verify-full; mysql: true|false|skip-verify

	// Branding
	AppName        string `env:"APP_NAME" live:"true"`
	PrimaryColor   string `env:"PRIMARY_COLOR" live:"true"`
	SecondaryColor string `env:"SECONDARY_COLOR" live:"true"`
	AccentColor    string `env:"ACCENT_COLOR" live:"true"`
	LogoPath       string `env:"LOGO_PATH" live:"true"`

	// Fonts
	FontURL        string `env:"FONT_URL" live:"true"`
	FontFamily     string `env:"FONT_FAMILY" live:"true"`
	FontFamilyMono string `env:"FONT_FAMILY_MONO" live:"true"`

	// Footer
	HideFooter bool `env:"HIDE_FOOTER" live:"true"`

	// Local admin auth
	AdminUser     string `env:"ADMIN_USER" live:"true"`
	AdminPassword string `env:"ADMIN_PASSWORD" live:"true"`

	// SAML
	SAMLEnabled        bool
	SAMLAutoLogin      bool `env:"SAML_AUTO_LOGIN" live:"true"`
	SAMLIDPMetadataURL string
	SAMLEntityID       string
	SAMLRootURL        string
	SAMLCertFile       string
	SAMLKeyFile        string
	// SAML group → role mapping (Entra ID group Object IDs)
	SAMLGroupsClaim           string // claim URI that carries group values (default: Entra standard)
	SAMLGroupGlobal           string // group ID → global (admin) role
	SAMLGroupTeamManager      string // group ID → team_manager role
	SAMLGroupStatusManager    string // group ID → status_manager role
	SAMLGroupActivityViewer   string // group ID → activity_viewer role
	SAMLGroupFloorplanManager string // group ID → floorplan_manager role
	SAMLGroupProjectsManager  string // group ID → projects_manager role
	SAMLGroupProjectsViewer   string // group ID → projects_viewer role

	// Internationalisation
	DefaultLang string `env:"DEFAULT_LANG" live:"true"`

	// Observability
	MetricsToken string `env:"METRICS_TOKEN" live:"true"`

	// Features
	DisableFloorplans    bool    `env:"DISABLE_FLOORPLANS" live:"true"`
	DisableAPI           bool    `env:"DISABLE_API" live:"true"`
	DisableProjects      bool    `env:"DISABLE_PROJECTS" live:"true"`
	OnsiteRatioThreshold float64 `env:"ONSITE_RATIO_THRESHOLD" live:"true"` // minimum on-site % for the activity rocket (default 60)

	// TeamCalendarRefreshMinutes is how often (in minutes) the team calendar(s)
	// on the home page auto-refresh. 0 disables auto-refresh. Default: 3.
	TeamCalendarRefreshMinutes int `env:"TEAM_CALENDAR_REFRESH_MINUTES" live:"true"`

	// SMTP (password reset)
	SMTPURL  string `env:"SMTP_URL" live:"true"`
	SMTPFrom string `env:"SMTP_FROM" live:"true"`
	AppURL   string `env:"APP_URL" live:"true"`

	// Passkeys (WebAuthn)
	EnablePasskeys  bool
	PasskeyRPID     string `env:"PASSKEY_RP_ID" live:"true"`     // Relying Party ID (domain, e.g. "presence.example.com")
	PasskeyRPOrigin string `env:"PASSKEY_RP_ORIGIN" live:"true"` // Full origin (e.g. "https://presence.example.com")

	// Jira integration (used for team Jira space linkage)
	JiraEnabled bool   // true when JiraCloudID+JiraToken or JiraBaseURL+JiraEmail+JiraToken are set
	JiraCloudID string `env:"JIRA_CLOUD_ID" live:"true"` // Atlassian Cloud ID for scoped API token mode
	JiraBaseURL string `env:"JIRA_BASE_URL" live:"true"` // e.g. "https://your-domain.atlassian.net" (Legacy mode)
	JiraEmail   string `env:"JIRA_EMAIL" live:"true"`    // Atlassian account email used for API auth (Legacy mode)
	JiraToken   string `env:"JIRA_TOKEN" live:"true"`    // Atlassian API token or Scoped Bearer token
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	c := &Config{
		Port:      getEnv("PORT", "8080"),
		DataDir:   getEnv("DATA_DIR", "/data"),
		SecretKey: getEnv("SECRET_KEY", "change-me-in-production-use-random-32-chars"),

		DBDriver:   getEnv("DB_DRIVER", "sqlite"),
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", ""),
		DBName:     getEnv("DB_NAME", "mypresence"),
		DBUser:     getEnv("DB_USER", ""),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBSSLMode:  getEnv("DB_SSL_MODE", "disable"),

		AppName:        getEnv("APP_NAME", "Presence"),
		PrimaryColor:   getEnv("PRIMARY_COLOR", "#3b82f6"),
		SecondaryColor: getEnv("SECONDARY_COLOR", "#1e40af"),
		AccentColor:    getEnv("ACCENT_COLOR", "#f59e0b"),
		LogoPath:       getEnv("LOGO_PATH", ""),

		FontURL:        getEnv("FONT_URL", "https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap"),
		FontFamily:     getEnv("FONT_FAMILY", "'Inter', ui-sans-serif, system-ui, sans-serif"),
		FontFamilyMono: getEnv("FONT_FAMILY_MONO", "'JetBrains Mono', ui-monospace, monospace"),

		HideFooter: getEnvBool("HIDE_FOOTER", false),

		AdminUser:     getEnv("ADMIN_USER", "admin"),
		AdminPassword: getEnv("ADMIN_PASSWORD", "admin"),

		SAMLAutoLogin:      getEnvBool("SAML_AUTO_LOGIN", true),
		SAMLIDPMetadataURL: getEnv("SAML_IDP_METADATA_URL", ""),
		SAMLEntityID:       getEnv("SAML_ENTITY_ID", ""),
		SAMLRootURL:        getEnv("SAML_ROOT_URL", ""),
		SAMLCertFile:       getEnv("SAML_SP_CERT_FILE", ""),
		SAMLKeyFile:        getEnv("SAML_SP_KEY_FILE", ""),

		SAMLGroupsClaim:           getEnv("SAML_GROUPS_CLAIM", "http://schemas.microsoft.com/ws/2008/06/identity/claims/groups"),
		SAMLGroupGlobal:           getEnv("SAML_GROUP_GLOBAL", ""),
		SAMLGroupTeamManager:      getEnv("SAML_GROUP_TEAM_MANAGER", ""),
		SAMLGroupStatusManager:    getEnv("SAML_GROUP_STATUS_MANAGER", ""),
		SAMLGroupActivityViewer:   getEnv("SAML_GROUP_ACTIVITY_VIEWER", ""),
		SAMLGroupFloorplanManager: getEnv("SAML_GROUP_FLOORPLAN_MANAGER", ""),
		SAMLGroupProjectsManager:  getEnv("SAML_GROUP_PROJECTS_MANAGER", ""),
		SAMLGroupProjectsViewer:   getEnv("SAML_GROUP_PROJECTS_VIEWER", ""),

		DefaultLang: getEnv("DEFAULT_LANG", "en"),

		MetricsToken: getEnv("METRICS_TOKEN", ""),

		DisableFloorplans:    getEnvBool("DISABLE_FLOORPLANS", false),
		DisableAPI:           getEnvBool("DISABLE_API", false),
		DisableProjects:      getEnvBool("DISABLE_PROJECTS", false),
		OnsiteRatioThreshold: getEnvFloat("ONSITE_RATIO_THRESHOLD", 60.0),

		TeamCalendarRefreshMinutes: getEnvInt("TEAM_CALENDAR_REFRESH_MINUTES", 3),

		SMTPURL:  getEnv("SMTP_URL", ""),
		SMTPFrom: getEnv("SMTP_FROM", "noreply@presence.local"),
		AppURL:   getEnv("APP_URL", ""),

		EnablePasskeys:  getEnvBool("ENABLE_PASSKEYS", false),
		PasskeyRPID:     getEnv("PASSKEY_RP_ID", ""),
		PasskeyRPOrigin: getEnv("PASSKEY_RP_ORIGIN", ""),

		JiraCloudID: getEnv("JIRA_CLOUD_ID", ""),
		JiraBaseURL: getEnv("JIRA_BASE_URL", ""),
		JiraEmail:   getEnv("JIRA_EMAIL", ""),
		JiraToken:   getEnv("JIRA_TOKEN", ""),
	}
	c.SAMLEnabled = c.SAMLIDPMetadataURL != "" && c.SAMLEntityID != ""
	c.JiraEnabled = (c.JiraCloudID != "" && c.JiraToken != "") || (c.JiraBaseURL != "" && c.JiraEmail != "" && c.JiraToken != "")
	return c
}

// liveField returns the Config struct field tagged `env:"key" live:"true"`, if any.
// Adding those two tags to a Config field is all that's needed to make a new
// environment variable editable at runtime — no other code has to change.
func liveField(key string) (reflect.StructField, bool) {
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Tag.Get("env") == key && f.Tag.Get("live") == "true" {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// IsLiveEditable returns true if key is allowed to be changed at runtime via ApplyEnvOverride.
func IsLiveEditable(key string) bool {
	_, ok := liveField(key)
	return ok
}

// ApplyEnvOverride updates the in-memory field matching the given environment
// variable name (and calls os.Setenv so any code reading it later stays
// consistent). It only accepts keys tagged `live:"true"` on the Config struct.
// Returns true if the key was recognized and applied.
func (c *Config) ApplyEnvOverride(key, value string) bool {
	field, ok := liveField(key)
	if !ok {
		return false
	}
	os.Setenv(key, value) //nolint:errcheck
	fv := reflect.ValueOf(c).Elem().FieldByIndex(field.Index)
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(value)
	case reflect.Bool:
		fv.SetBool(parseBool(value, fv.Bool()))
	case reflect.Float64:
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			fv.SetFloat(f)
		}
	case reflect.Int:
		if i, err := strconv.Atoi(value); err == nil {
			fv.SetInt(int64(i))
		}
	}
	if key == "JIRA_BASE_URL" || key == "JIRA_EMAIL" || key == "JIRA_TOKEN" || key == "JIRA_CLOUD_ID" {
		c.JiraEnabled = (c.JiraCloudID != "" && c.JiraToken != "") || (c.JiraBaseURL != "" && c.JiraEmail != "" && c.JiraToken != "")
	}
	return true
}

// parseBool parses "true"/"1"/"yes" and "false"/"0"/"no" (case-sensitive, matching
// getEnvBool), falling back to the current value for anything else.
func parseBool(v string, fallback bool) bool {
	if v == "true" || v == "1" || v == "yes" {
		return true
	}
	if v == "false" || v == "0" || v == "no" {
		return false
	}
	return fallback
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "true" || v == "1" || v == "yes" {
		return true
	}
	if v == "false" || v == "0" || v == "no" {
		return false
	}
	return fallback
}
