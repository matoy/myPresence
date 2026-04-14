package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNewUserPage_RendersTemplate verifies that NewUserPage calls the render
// function with the "admin_user_new" template and responds 200.
func TestNewUserPage_RendersTemplate(t *testing.T) {
	rendered := ""
	h := &UsersAdminHandler{
		Render: func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
			rendered = page
			w.WriteHeader(http.StatusOK)
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/users/new", nil)
	w := httptest.NewRecorder()
	h.NewUserPage(w, req)

	if rendered != "admin_user_new" {
		t.Errorf("expected template %q, got %q", "admin_user_new", rendered)
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// TestNewUserPage_PassesErrorParam verifies that the error query param is
// forwarded to the template data.
func TestNewUserPage_PassesErrorParam(t *testing.T) {
	var capturedData interface{}
	h := &UsersAdminHandler{
		Render: func(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
			capturedData = data
			w.WriteHeader(http.StatusOK)
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/users/new?error=Email+already+in+use", nil)
	w := httptest.NewRecorder()
	h.NewUserPage(w, req)

	m, ok := capturedData.(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be map[string]interface{}")
	}
	if m["Error"] != "Email already in use" {
		t.Errorf("expected Error=%q, got %q", "Email already in use", m["Error"])
	}
}
