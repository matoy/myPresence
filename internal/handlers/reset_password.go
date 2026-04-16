package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"presence-app/internal/config"
	"presence-app/internal/db"
	"presence-app/internal/mailer"
)

// ResetPasswordHandler handles the forgot-password / reset-password flow.
type ResetPasswordHandler struct {
	DB     *db.DB
	Config *config.Config
	Render func(w http.ResponseWriter, r *http.Request, page string, data interface{})
}

// ForgotPasswordPage renders the "forgot password" form.
func (h *ResetPasswordHandler) ForgotPasswordPage(w http.ResponseWriter, r *http.Request) {
	h.Render(w, r, "forgot_password", map[string]interface{}{
		"Sent": false,
	})
}

// ForgotPasswordPost processes the email submission and sends a reset link.
func (h *ResetPasswordHandler) ForgotPasswordPost(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))

	// Always show the same neutral message to prevent user enumeration.
	renderSent := func() {
		h.Render(w, r, "forgot_password", map[string]interface{}{
			"Sent": true,
		})
	}

	if email == "" {
		renderSent()
		return
	}

	rawToken, err := h.DB.CreatePasswordResetToken(email)
	if err != nil {
		log.Printf("CreatePasswordResetToken: internal error")
		renderSent()
		return
	}
	if rawToken == "" {
		// No local account — return silently
		renderSent()
		return
	}

	resetURL := h.baseURL(r) + "/reset-password?token=" + rawToken
	body := fmt.Sprintf(
		"Hello,\n\nYou requested a password reset for your account (%s).\n\n"+
			"Click the link below to set a new password (valid 1 hour):\n\n%s\n\n"+
			"If you did not request this, you can safely ignore this email.\n",
		email, resetURL,
	)
	subject := "Password reset — " + h.Config.AppName

	go func() {
		if err := mailer.Send(h.Config.SMTPURL, h.Config.SMTPFrom, email, subject, body); err != nil {
			log.Printf("password reset email to %s: %v", email, err)
		}
	}()

	renderSent()
}

// ResetPasswordPage renders the "set new password" form for a valid token.
func (h *ResetPasswordHandler) ResetPasswordPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	h.Render(w, r, "reset_password", map[string]interface{}{
		"Token": token,
		"Error": "",
		"Done":  false,
	})
}

// ResetPasswordPost sets the new password if the token is valid.
func (h *ResetPasswordHandler) ResetPasswordPost(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.FormValue("token"))
	password := r.FormValue("password")
	confirm := r.FormValue("confirm")

	renderErr := func(msg string) {
		h.Render(w, r, "reset_password", map[string]interface{}{
			"Token": token,
			"Error": msg,
			"Done":  false,
		})
	}

	if token == "" {
		renderErr("invalid_token")
		return
	}
	if password == "" || confirm == "" {
		renderErr("fields_required")
		return
	}
	if len(password) < 8 {
		renderErr("password_too_short")
		return
	}
	if password != confirm {
		renderErr("passwords_mismatch")
		return
	}

	user, err := h.DB.UsePasswordResetToken(token)
	if err != nil {
		renderErr("invalid_token")
		return
	}

	if err := h.DB.SetUserPassword(user.ID, password); err != nil {
		renderErr("server_error")
		return
	}

	// Invalidate all sessions — user must log in again with the new password.
	h.DB.DeleteUserSessions(user.ID, "")

	h.Render(w, r, "reset_password", map[string]interface{}{
		"Token": "",
		"Error": "",
		"Done":  true,
	})
}

// baseURL returns the application base URL, derived from APP_URL config or the request Host.
func (h *ResetPasswordHandler) baseURL(r *http.Request) string {
	if h.Config.AppURL != "" {
		return strings.TrimRight(h.Config.AppURL, "/")
	}
	scheme := "http"
	if r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
