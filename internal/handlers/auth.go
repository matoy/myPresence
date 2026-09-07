package handlers

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/matoy/mypresence/internal/config"
	"github.com/matoy/mypresence/internal/db"
	"github.com/matoy/mypresence/internal/metrics"
	"github.com/matoy/mypresence/internal/middleware"
	"github.com/matoy/mypresence/internal/models"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
)

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	DB          *db.DB
	Config      *config.Config
	Render      func(w http.ResponseWriter, r *http.Request, page string, data interface{})
	SP          *saml.ServiceProvider
	RateLimiter *middleware.LoginRateLimiter
	// pendingSAMLRequests stores in-flight SAML request IDs (id -> expiry time).
	// Used to validate InResponseTo in the ACS response.
	pendingSAMLRequests sync.Map
	// WebAuthn instance (nil when passkeys are disabled).
	WebAuthn *webauthn.WebAuthn
	// passkeySessions stores in-flight WebAuthn ceremonies keyed by a random session token.
	passkeySessions sync.Map
}

// InitSAML initializes the SAML service provider if configured.
func (h *AuthHandler) InitSAML() error {
	if !h.Config.SAMLEnabled {
		return nil
	}

	rootURL, err := url.Parse(h.Config.SAMLRootURL)
	if err != nil {
		return fmt.Errorf("invalid SAML_ROOT_URL: %w", err)
	}

	idpMetadataURL, err := url.Parse(h.Config.SAMLIDPMetadataURL)
	if err != nil {
		return fmt.Errorf("invalid SAML_IDP_METADATA_URL: %w", err)
	}

	// Fetch IdP metadata
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	idpMetadata, err := samlsp.FetchMetadata(
		context.Background(),
		httpClient,
		*idpMetadataURL,
	)
	if err != nil {
		return fmt.Errorf("fetch IdP metadata: %w", err)
	}

	// Load or generate SP key pair
	var keyPair tls.Certificate
	if h.Config.SAMLCertFile != "" && h.Config.SAMLKeyFile != "" {
		keyPair, err = tls.LoadX509KeyPair(h.Config.SAMLCertFile, h.Config.SAMLKeyFile)
		if err != nil {
			return fmt.Errorf("load SAML cert/key: %w", err)
		}
	} else {
		// Generate a self-signed key pair for SAML signing
		keyPair, err = generateSelfSignedCert()
		if err != nil {
			return fmt.Errorf("generate SAML cert: %w", err)
		}
	}

	keyPair.Leaf, err = x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse certificate: %w", err)
	}

	acsURL := *rootURL
	acsURL.Path = "/saml/acs"
	metadataURL := *rootURL
	metadataURL.Path = "/saml/metadata"

	h.SP = &saml.ServiceProvider{
		EntityID:    h.Config.SAMLEntityID,
		Key:         keyPair.PrivateKey.(*rsa.PrivateKey),
		Certificate: keyPair.Leaf,
		MetadataURL: metadataURL,
		AcsURL:      acsURL,
		IDPMetadata: idpMetadata,
	}

	slog.Info("saml.enabled", "entity_id", h.Config.SAMLEntityID)
	return nil
}

// sanitizeReturnURL ensures returnTo is a safe relative path.
func sanitizeReturnURL(target string) string {
	if target == "" {
		return ""
	}
	if strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") {
		return target
	}
	return ""
}

// LoginPage renders the login page.
func (h *AuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	// If already logged in, redirect to home
	user := middleware.GetUser(r)
	if user != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	flash := r.URL.Query().Get("error")
	local := r.URL.Query().Get("local")
	loggedOut := r.URL.Query().Get("logged_out")

	// If SAML is enabled and auto-login is active, and no bypass/error query parameters are present:
	// automatically initiate the SSO flow!
	if h.Config != nil && h.Config.SAMLEnabled && h.Config.SAMLAutoLogin && flash == "" && local == "" && loggedOut == "" {
		returnTo := sanitizeReturnURL(r.URL.Query().Get("return_to"))
		if returnTo == "" {
			returnTo = sanitizeReturnURL(r.URL.Query().Get("next"))
		}
		target := "/saml/login"
		if returnTo != "" {
			target += "?return_to=" + url.QueryEscape(returnTo)
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}

	h.Render(w, r, "login", map[string]interface{}{
		"Flash": flash,
	})
}

// LocalLogin handles local admin login.
func (h *AuthHandler) LocalLogin(w http.ResponseWriter, r *http.Request) {
	// Rate limit: block IPs with too many recent failures
	if h.RateLimiter != nil && !h.RateLimiter.Allow(r) {
		metrics.AuthLoginsTotal.WithLabelValues("local", "failure").Inc()
		slog.Warn("auth.login", "result", "blocked", "method", "local", "ip", clientIP(r))
		http.Redirect(w, r, "/login?error=Too+many+attempts%2C+please+wait", http.StatusSeeOther)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	recordFailure := func() {
		if h.RateLimiter != nil {
			h.RateLimiter.RecordFailure(r)
		}
		metrics.AuthLoginsTotal.WithLabelValues("local", "failure").Inc()
		slog.Warn("auth.login", "result", "failure", "user", username, "method", "local", "ip", clientIP(r))
	}

	var userID int64
	var user *models.User

	if username == h.Config.AdminUser {
		// Admin credential check (plain-text against config value)
		if password != h.Config.AdminPassword {
			recordFailure()
			http.Redirect(w, r, "/login?error=Invalid+credentials", http.StatusSeeOther)
			return
		}
		var err error
		user, err = h.DB.GetUserByEmail(username)
		if err != nil {
			recordFailure()
			http.Redirect(w, r, "/login?error=Internal+error", http.StatusSeeOther)
			return
		}
		userID = user.ID
	} else {
		// Try DB local user with bcrypt-aware comparison
		var err error
		user, err = h.DB.GetUserByEmail(username)
		if err != nil || !h.DB.CheckPassword(user.ID, user.PasswordHash, password) {
			recordFailure()
			http.Redirect(w, r, "/login?error=Invalid+credentials", http.StatusSeeOther)
			return
		}
		if user.Disabled {
			recordFailure()
			http.Redirect(w, r, "/login?error=Account+disabled", http.StatusSeeOther)
			return
		}
		userID = user.ID
	}

	token, err := h.DB.CreateSession(userID)
	if err != nil {
		recordFailure()
		http.Redirect(w, r, "/login?error=Internal+error", http.StatusSeeOther)
		return
	}

	if h.RateLimiter != nil {
		h.RateLimiter.Reset(r)
	}
	metrics.AuthLoginsTotal.WithLabelValues("local", "success").Inc()
	slog.Info("auth.login", "result", "success", "user", username, "method", "local", "ip", clientIP(r))

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 30,
	})

	// Synchronize language between cookie and user record
	if c, err := r.Cookie("lang"); err == nil && c.Value != "" {
		if user.Language != c.Value {
			_ = h.DB.UpdateUserLanguage(user.ID, c.Value)
			user.Language = c.Value
		}
	} else if user.Language != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     "lang",
			Value:    user.Language,
			Path:     "/",
			MaxAge:   365 * 24 * 3600,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		})
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Logout clears the session.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if u := middleware.GetUser(r); u != nil {
		slog.Info("auth.logout", "user", u.Email)
	}
	cookie, err := r.Cookie("session")
	if err == nil {
		h.DB.DeleteSession(cookie.Value) //nolint:errcheck
	}
	metrics.AuthLogoutsTotal.Inc()
	http.SetCookie(w, &http.Cookie{Name: "session", MaxAge: -1, Path: "/"})
	http.Redirect(w, r, "/login?logged_out=1", http.StatusSeeOther)
}

// SAMLMetadata serves the SP metadata XML.
func (h *AuthHandler) SAMLMetadata(w http.ResponseWriter, r *http.Request) {
	if h.SP == nil {
		http.Error(w, "SAML not configured", http.StatusNotFound)
		return
	}
	buf, _ := xml.MarshalIndent(h.SP.Metadata(), "", "  ")
	w.Header().Set("Content-Type", "application/xml")
	w.Write(buf) //nolint:errcheck
}

// SAMLLogin initiates the SAML SSO flow.
func (h *AuthHandler) SAMLLogin(w http.ResponseWriter, r *http.Request) {
	if h.SP == nil {
		http.Error(w, "SAML not configured", http.StatusNotFound)
		return
	}
	relayState := sanitizeReturnURL(r.URL.Query().Get("return_to"))
	if relayState == "" {
		relayState = sanitizeReturnURL(r.URL.Query().Get("next"))
	}

	authReq, err := h.SP.MakeAuthenticationRequest(
		h.SP.GetSSOBindingLocation(saml.HTTPRedirectBinding),
		saml.HTTPRedirectBinding,
		saml.HTTPPostBinding,
	)
	if err != nil {
		slog.Error("auth.saml.authn_request", "error", err)
		http.Redirect(w, r, "/login?error=Erreur+SSO", http.StatusSeeOther)
		return
	}

	redirectURL, err := authReq.Redirect(relayState, h.SP)
	if err != nil {
		slog.Error("auth.saml.redirect", "error", err)
		http.Redirect(w, r, "/login?error=Erreur+SSO", http.StatusSeeOther)
		return
	}
	// Store the request ID so we can validate InResponseTo in the ACS handler.
	h.pendingSAMLRequests.Store(authReq.ID, time.Now().Add(5*time.Minute))
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

// SAMLACS handles the SAML Assertion Consumer Service.
func (h *AuthHandler) SAMLACS(w http.ResponseWriter, r *http.Request) {
	if h.SP == nil {
		http.Error(w, "SAML not configured", http.StatusNotFound)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/login?error=Réponse+SAML+invalide", http.StatusSeeOther)
		return
	}

	pendingIDs := collectPendingSAMLIDs(&h.pendingSAMLRequests)
	assert, err := h.SP.ParseResponse(r, pendingIDs)
	if err != nil {
		slog.Warn("auth.saml.response", "error", err, "ip", clientIP(r))
		http.Redirect(w, r, "/login?error=Authentification+SSO+échouée", http.StatusSeeOther)
		return
	}

	email := extractSAMLEmail(assert)
	if email == "" {
		http.Redirect(w, r, "/login?error=Aucun+email+dans+la+réponse+SAML", http.StatusSeeOther)
		return
	}
	displayName := extractSAMLDisplayName(assert, email)

	// Auto-provision or update user
	user, err := h.DB.UpsertUser(email, displayName)
	if err != nil {
		slog.Error("auth.saml.provision", "error", err, "email", email)
		http.Redirect(w, r, "/login?error=Erreur+provisionnement", http.StatusSeeOther)
		return
	}

	// Apply RBAC: map IDP groups to application roles if group mapping is configured.
	h.syncSAMLGroupRoles(user, assert, email)

	token, err := h.DB.CreateSession(user.ID)
	if err != nil {
		metrics.AuthLoginsTotal.WithLabelValues("saml", "failure").Inc()
		http.Redirect(w, r, "/login?error=Erreur+session", http.StatusSeeOther)
		return
	}

	metrics.AuthLoginsTotal.WithLabelValues("saml", "success").Inc()
	slog.Info("auth.login", "result", "success", "user", email, "method", "saml", "ip", clientIP(r))
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 30,
	})
	relayState := sanitizeReturnURL(r.FormValue("RelayState"))
	target := "/"
	if relayState != "" {
		target = relayState
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// collectPendingSAMLIDs returns all non-expired pending SAML request IDs and
// removes stale entries from the map.
func collectPendingSAMLIDs(m *sync.Map) []string {
	now := time.Now()
	var ids []string
	m.Range(func(key, value interface{}) bool {
		if now.Before(value.(time.Time)) {
			ids = append(ids, key.(string))
		} else {
			m.Delete(key)
		}
		return true
	})
	return ids
}

// extractSAMLEmail resolves the user's email from a SAML assertion, trying
// the email-address claim, then the name claim, then the NameID.
func extractSAMLEmail(assertion *saml.Assertion) string {
	if v := getAttributeValue(assertion, "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress"); v != "" {
		return v
	}
	if v := getAttributeValue(assertion, "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name"); v != "" {
		return v
	}
	if assertion.Subject != nil && assertion.Subject.NameID != nil {
		return assertion.Subject.NameID.Value
	}
	return ""
}

// extractSAMLDisplayName resolves the display name from a SAML assertion.
// Falls back to combining given/surname claims, then to email.
func extractSAMLDisplayName(assertion *saml.Assertion, email string) string {
	if v := getAttributeValue(assertion, "http://schemas.microsoft.com/identity/claims/displayname"); v != "" {
		return v
	}
	first := getAttributeValue(assertion, "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/givenname")
	last := getAttributeValue(assertion, "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/surname")
	if first != "" || last != "" {
		return first + " " + last
	}
	if email != "" {
		return email
	}
	return ""
}

// syncSAMLGroupRoles maps IDP group memberships from the assertion to
// application roles and persists the result via UpdateUserRoles.
func (h *AuthHandler) syncSAMLGroupRoles(user *models.User, assertion *saml.Assertion, email string) {
	cfg := h.Config
	if !hasConfiguredSAMLGroupMappings(cfg) {
		return
	}
	groups, groupClaimPresent := getAttributeValuesWithPresence(assertion, cfg.SAMLGroupsClaim)
	if !groupClaimPresent {
		slog.Warn("auth.saml.groups_claim_missing",
			"email", email,
			"claim", cfg.SAMLGroupsClaim,
			"present_claims", listSAMLAttributeNames(assertion),
			"possible_entra_group_overage", hasPossibleEntraGroupOverage(assertion),
		)
	}
	groupSet := make(map[string]bool, len(groups))
	for _, g := range groups {
		groupSet[g] = true
	}
	var roles []string
	for _, m := range []struct{ groupID, role string }{
		{cfg.SAMLGroupGlobal, models.RoleGlobal},
		{cfg.SAMLGroupTeamManager, models.RoleTeamManager},
		{cfg.SAMLGroupStatusManager, models.RoleStatusManager},
		{cfg.SAMLGroupActivityViewer, models.RoleActivityViewer},
		{cfg.SAMLGroupFloorplanManager, models.RoleFloorplanManager},
		{cfg.SAMLGroupProjectsManager, models.RoleProjectsManager},
		{cfg.SAMLGroupProjectsViewer, models.RoleProjectsViewer},
	} {
		if m.groupID != "" && groupSet[m.groupID] {
			roles = append(roles, m.role)
		}
	}
	if len(roles) == 0 {
		roles = []string{models.RoleBasic}
	}
	if err := h.DB.UpdateUserRoles(user.ID, strings.Join(roles, ",")); err != nil {
		slog.Warn("auth.saml.role_sync", "error", err, "email", email)
	}
}

// getAttributeValue extracts an attribute value from a SAML assertion.
func getAttributeValue(assertion *saml.Assertion, name string) string {
	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			if attr.Name == name && len(attr.Values) > 0 {
				return attr.Values[0].Value
			}
		}
	}
	return ""
}

// getAttributeValues returns all values for the named attribute in a SAML assertion.
func getAttributeValues(assertion *saml.Assertion, name string) []string {
	vals, _ := getAttributeValuesWithPresence(assertion, name)
	return vals
}

func getAttributeValuesWithPresence(assertion *saml.Assertion, name string) ([]string, bool) {
	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			if attr.Name == name {
				vals := make([]string, 0, len(attr.Values))
				for _, v := range attr.Values {
					vals = append(vals, v.Value)
				}
				return vals, true
			}
		}
	}
	return nil, false
}

func hasConfiguredSAMLGroupMappings(cfg *config.Config) bool {
	return cfg.SAMLGroupGlobal != "" || cfg.SAMLGroupTeamManager != "" ||
		cfg.SAMLGroupStatusManager != "" || cfg.SAMLGroupActivityViewer != "" ||
		cfg.SAMLGroupFloorplanManager != "" || cfg.SAMLGroupProjectsManager != "" ||
		cfg.SAMLGroupProjectsViewer != ""
}

func listSAMLAttributeNames(assertion *saml.Assertion) []string {
	names := make([]string, 0)
	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			names = append(names, attr.Name)
		}
	}
	return names
}

func hasPossibleEntraGroupOverage(assertion *saml.Assertion) bool {
	for _, name := range listSAMLAttributeNames(assertion) {
		lowerName := strings.ToLower(name)
		if lowerName == "hasgroups" || strings.HasSuffix(lowerName, "/hasgroups") ||
			lowerName == "_claim_names" || strings.HasSuffix(lowerName, "/_claim_names") ||
			lowerName == "_claim_sources" || strings.HasSuffix(lowerName, "/_claim_sources") ||
			lowerName == "groups.link" || strings.HasSuffix(lowerName, "/groups.link") {
			return true
		}
	}
	return false
}

// generateSelfSignedCert creates a self-signed TLS certificate for SAML SP.
func generateSelfSignedCert() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(cryptorand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}

// clientIP returns the best-effort client IP address from a request,
// honouring X-Forwarded-For when present (first entry only).
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.SplitN(fwd, ",", 2)[0]
	}
	return r.RemoteAddr
}
