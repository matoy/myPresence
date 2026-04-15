package config

import "os"

// Version is the application version, updated manually for each release.
const Version = "0.1.8"

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Server
	Port      string
	DataDir   string
	SecretKey string

	// Branding
	AppName        string
	PrimaryColor   string
	SecondaryColor string
	AccentColor    string
	LogoPath       string

	// Fonts
	FontURL        string
	FontFamily     string
	FontFamilyMono string

	// Footer
	HideFooter bool

	// Local admin auth
	AdminUser     string
	AdminPassword string

	// SAML
	SAMLEnabled        bool
	SAMLIDPMetadataURL string
	SAMLEntityID       string
	SAMLRootURL        string
	SAMLCertFile       string
	SAMLKeyFile        string

	// Internationalisation
	DefaultLang string

	// Observability
	MetricsToken string

	// Features
	DisableFloorplans bool
	DisableAPI        bool

	// SMTP (password reset)
	SMTPURL  string
	SMTPFrom string
	AppURL   string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	c := &Config{
		Port:      getEnv("PORT", "8080"),
		DataDir:   getEnv("DATA_DIR", "/data"),
		SecretKey: getEnv("SECRET_KEY", "change-me-in-production-use-random-32-chars"),

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

		SAMLIDPMetadataURL: getEnv("SAML_IDP_METADATA_URL", ""),
		SAMLEntityID:       getEnv("SAML_ENTITY_ID", ""),
		SAMLRootURL:        getEnv("SAML_ROOT_URL", ""),
		SAMLCertFile:       getEnv("SAML_SP_CERT_FILE", ""),
		SAMLKeyFile:        getEnv("SAML_SP_KEY_FILE", ""),

		DefaultLang: getEnv("DEFAULT_LANG", "en"),

		MetricsToken: getEnv("METRICS_TOKEN", ""),

		DisableFloorplans: getEnvBool("DISABLE_FLOORPLANS", false),
		DisableAPI:        getEnvBool("DISABLE_API", false),

		SMTPURL:  getEnv("SMTP_URL", ""),
		SMTPFrom: getEnv("SMTP_FROM", "noreply@presence.local"),
		AppURL:   getEnv("APP_URL", ""),
	}
	c.SAMLEnabled = c.SAMLIDPMetadataURL != "" && c.SAMLEntityID != ""
	return c
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
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
