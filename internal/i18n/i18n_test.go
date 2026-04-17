package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestT_KnownLanguages(t *testing.T) {
	langs := []string{"en", "fr", "de", "es", "it"}
	for _, lang := range langs {
		m := T(lang)
		if m == nil {
			t.Errorf("T(%q) returned nil", lang)
		}
	}
}

func TestT_FallsBackToEnglish(t *testing.T) {
	m := T("xx")
	if m == nil {
		t.Fatal("T(unknown) should fall back to English map, got nil")
	}
	// English map must be non-empty
	if len(m) == 0 {
		t.Error("English fallback map is empty")
	}
}

func TestT_EnglishAndFrenchDiffer(t *testing.T) {
	en := T("en")
	fr := T("fr")
	if len(en) == 0 || len(fr) == 0 {
		t.Fatal("translation maps should not be empty")
	}
	// At least one key should differ between English and French
	different := false
	for k, vEN := range en {
		if vFR, ok := fr[k]; ok && vFR != vEN {
			different = true
			break
		}
	}
	if !different {
		t.Error("English and French translations appear identical")
	}
}

func TestLangFromRequest_CookieOverrides(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: "fr"})
	got := LangFromRequest(req, "en")
	if got != "fr" {
		t.Errorf("expected 'fr' from cookie, got %q", got)
	}
}

func TestLangFromRequest_InvalidCookieFallsToDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: "xx"})
	got := LangFromRequest(req, "de")
	if got != "de" {
		t.Errorf("invalid cookie lang should fall back to default 'de', got %q", got)
	}
}

func TestLangFromRequest_NoCookie_ValidDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	got := LangFromRequest(req, "it")
	if got != "it" {
		t.Errorf("no cookie: expected default 'it', got %q", got)
	}
}

func TestLangFromRequest_NoCookie_InvalidDefault_FallsToEnglish(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	got := LangFromRequest(req, "zz")
	if got != "en" {
		t.Errorf("invalid default lang should fall back to 'en', got %q", got)
	}
}

func TestLangFromRequest_AllSupportedLangs(t *testing.T) {
	for _, s := range Supported {
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(&http.Cookie{Name: "lang", Value: s.Code})
		got := LangFromRequest(req, "en")
		if got != s.Code {
			t.Errorf("supported lang %q from cookie: got %q", s.Code, got)
		}
	}
}
