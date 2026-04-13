package config

import "os"

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

	// Footer
	AppVersion string
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

		AppVersion: getEnv("APP_VERSION", "dev"),
		HideFooter: getEnvBool("HIDE_FOOTER", false),

		AdminUser:     getEnv("ADMIN_USER", "admin"),
		AdminPassword: getEnv("ADMIN_PASSWORD", "admin"),

		SAMLIDPMetadataURL: getEnv("SAML_IDP_METADATA_URL", ""),
		SAMLEntityID:       getEnv("SAML_ENTITY_ID", ""),
		SAMLRootURL:        getEnv("SAML_ROOT_URL", ""),
		SAMLCertFile:       getEnv("SAML_SP_CERT_FILE", ""),
		SAMLKeyFile:        getEnv("SAML_SP_KEY_FILE", ""),
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
