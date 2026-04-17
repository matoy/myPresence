package config

import (
	"os"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	// Ensure no test env vars bleed in
	for _, k := range []string{
		"PORT", "DATA_DIR", "SECRET_KEY", "APP_NAME",
		"PRIMARY_COLOR", "SECONDARY_COLOR", "ACCENT_COLOR",
		"DEFAULT_LANG", "ADMIN_USER", "ADMIN_PASSWORD",
		"DISABLE_FLOORPLANS", "DISABLE_API", "HIDE_FOOTER",
		"METRICS_TOKEN", "SMTP_URL", "SMTP_FROM", "APP_URL",
	} {
		os.Unsetenv(k) //nolint:errcheck
	}

	c := Load()

	if c.Port != "8080" {
		t.Errorf("Port: want 8080, got %q", c.Port)
	}
	if c.DataDir != "/data" {
		t.Errorf("DataDir: want /data, got %q", c.DataDir)
	}
	if c.SecretKey != "change-me-in-production-use-random-32-chars" {
		t.Errorf("SecretKey default mismatch: %q", c.SecretKey)
	}
	if c.AppName != "Presence" {
		t.Errorf("AppName: want Presence, got %q", c.AppName)
	}
	if c.PrimaryColor != "#3b82f6" {
		t.Errorf("PrimaryColor: want #3b82f6, got %q", c.PrimaryColor)
	}
	if c.DefaultLang != "en" {
		t.Errorf("DefaultLang: want en, got %q", c.DefaultLang)
	}
	if c.AdminUser != "admin" {
		t.Errorf("AdminUser: want admin, got %q", c.AdminUser)
	}
	if c.DisableFloorplans {
		t.Error("DisableFloorplans should default to false")
	}
	if c.DisableAPI {
		t.Error("DisableAPI should default to false")
	}
	if c.HideFooter {
		t.Error("HideFooter should default to false")
	}
	if c.SAMLEnabled {
		t.Error("SAMLEnabled should be false when IDP URL and entity ID are empty")
	}
	if c.SMTPFrom != "noreply@presence.local" {
		t.Errorf("SMTPFrom default: want noreply@presence.local, got %q", c.SMTPFrom)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("DATA_DIR", "/tmp/mydata")
	t.Setenv("SECRET_KEY", "super-secret-key-for-testing-ok")
	t.Setenv("APP_NAME", "MyApp")
	t.Setenv("DEFAULT_LANG", "fr")
	t.Setenv("ADMIN_USER", "boss")
	t.Setenv("ADMIN_PASSWORD", "topSecret")
	t.Setenv("METRICS_TOKEN", "tok123")

	c := Load()

	if c.Port != "9090" {
		t.Errorf("Port: want 9090, got %q", c.Port)
	}
	if c.DataDir != "/tmp/mydata" {
		t.Errorf("DataDir: want /tmp/mydata, got %q", c.DataDir)
	}
	if c.SecretKey != "super-secret-key-for-testing-ok" {
		t.Errorf("SecretKey: want override, got %q", c.SecretKey)
	}
	if c.AppName != "MyApp" {
		t.Errorf("AppName: want MyApp, got %q", c.AppName)
	}
	if c.DefaultLang != "fr" {
		t.Errorf("DefaultLang: want fr, got %q", c.DefaultLang)
	}
	if c.AdminUser != "boss" {
		t.Errorf("AdminUser: want boss, got %q", c.AdminUser)
	}
	if c.MetricsToken != "tok123" {
		t.Errorf("MetricsToken: want tok123, got %q", c.MetricsToken)
	}
}

func TestLoad_BoolEnvVars(t *testing.T) {
	tests := []struct {
		val  string
		want bool
	}{
		{"true", true},
		{"1", true},
		{"yes", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"", false}, // fallback
	}
	for _, tt := range tests {
		t.Run("DISABLE_FLOORPLANS="+tt.val, func(t *testing.T) {
			if tt.val == "" {
				os.Unsetenv("DISABLE_FLOORPLANS") //nolint:errcheck
			} else {
				t.Setenv("DISABLE_FLOORPLANS", tt.val)
			}
			c := Load()
			if c.DisableFloorplans != tt.want {
				t.Errorf("DisableFloorplans with %q: got %v, want %v", tt.val, c.DisableFloorplans, tt.want)
			}
		})
	}
}

func TestLoad_SAMLEnabled_WhenBothSet(t *testing.T) {
	t.Setenv("SAML_IDP_METADATA_URL", "https://idp.example.com/metadata")
	t.Setenv("SAML_ENTITY_ID", "https://sp.example.com")

	c := Load()
	if !c.SAMLEnabled {
		t.Error("SAMLEnabled should be true when both SAML_IDP_METADATA_URL and SAML_ENTITY_ID are set")
	}
}

func TestLoad_SAMLEnabled_WhenOnlyURLSet(t *testing.T) {
	t.Setenv("SAML_IDP_METADATA_URL", "https://idp.example.com/metadata")
	os.Unsetenv("SAML_ENTITY_ID") //nolint:errcheck

	c := Load()
	if c.SAMLEnabled {
		t.Error("SAMLEnabled should be false when SAML_ENTITY_ID is missing")
	}
}

func TestGetEnv_EmptyFallsBack(t *testing.T) {
	os.Unsetenv("TEST_GET_ENV_KEY") //nolint:errcheck
	got := getEnv("TEST_GET_ENV_KEY", "fallback")
	if got != "fallback" {
		t.Errorf("getEnv fallback: want 'fallback', got %q", got)
	}
}

func TestGetEnv_ReturnsValue(t *testing.T) {
	t.Setenv("TEST_GET_ENV_KEY2", "hello")
	got := getEnv("TEST_GET_ENV_KEY2", "fallback")
	if got != "hello" {
		t.Errorf("getEnv: want 'hello', got %q", got)
	}
}

func TestGetEnvBool_UnknownValueFallsBack(t *testing.T) {
	t.Setenv("TEST_BOOL_KEY", "maybe")
	if got := getEnvBool("TEST_BOOL_KEY", true); !got {
		t.Error("unknown value should fall back to true")
	}
	if got := getEnvBool("TEST_BOOL_KEY", false); got {
		t.Error("unknown value should fall back to false")
	}
}
