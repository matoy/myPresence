package handlers

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"presence-app/internal/config"
	"presence-app/internal/db"
	"presence-app/internal/metrics"
	"presence-app/internal/middleware"
	"presence-app/internal/models"

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

	log.Printf("SAML SSO enabled (Entity ID: %s)", h.Config.SAMLEntityID)
	return nil
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
	h.Render(w, r, "login", map[string]interface{}{
		"Flash": flash,
	})
}

// LocalLogin handles local admin login.
func (h *AuthHandler) LocalLogin(w http.ResponseWriter, r *http.Request) {
	// Rate limit: block IPs with too many recent failures
	if h.RateLimiter != nil && !h.RateLimiter.Allow(r) {
		metrics.AuthLoginsTotal.WithLabelValues("local", "failure").Inc()
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
	}

	var userID int64

	if username == h.Config.AdminUser {
		// Admin credential check (plain-text against config value)
		if password != h.Config.AdminPassword {
			recordFailure()
			http.Redirect(w, r, "/login?error=Invalid+credentials", http.StatusSeeOther)
			return
		}
		user, err := h.DB.GetUserByEmail(username)
		if err != nil {
			recordFailure()
			http.Redirect(w, r, "/login?error=Internal+error", http.StatusSeeOther)
			return
		}
		userID = user.ID
	} else {
		// Try DB local user with bcrypt-aware comparison
		user, err := h.DB.GetUserByEmail(username)
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

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 30,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Logout clears the session.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		h.DB.DeleteSession(cookie.Value) //nolint:errcheck
	}
	metrics.AuthLogoutsTotal.Inc()
	http.SetCookie(w, &http.Cookie{Name: "session", MaxAge: -1, Path: "/"})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
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
	authReq, err := h.SP.MakeAuthenticationRequest(
		h.SP.GetSSOBindingLocation(saml.HTTPRedirectBinding),
		saml.HTTPRedirectBinding,
		saml.HTTPPostBinding,
	)
	if err != nil {
		log.Printf("SAML AuthnRequest error: %v", err)
		http.Redirect(w, r, "/login?error=Erreur+SSO", http.StatusSeeOther)
		return
	}

	redirectURL, err := authReq.Redirect("", h.SP)
	if err != nil {
		log.Printf("SAML redirect error: %v", err)
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

	// Collect all non-expired pending request IDs and clean up stale ones.
	now := time.Now()
	var pendingIDs []string
	h.pendingSAMLRequests.Range(func(key, value interface{}) bool {
		if now.Before(value.(time.Time)) {
			pendingIDs = append(pendingIDs, key.(string))
		} else {
			h.pendingSAMLRequests.Delete(key)
		}
		return true
	})
	assertion, err := h.SP.ParseResponse(r, pendingIDs)
	if err != nil {
		log.Printf("SAML Response error: %v", err)
		http.Redirect(w, r, "/login?error=Authentification+SSO+échouée", http.StatusSeeOther)
		return
	}

	// Extract user attributes from SAML assertion
	email := getAttributeValue(assertion, "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress")
	if email == "" {
		email = getAttributeValue(assertion, "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name")
	}
	if email == "" && assertion.Subject != nil && assertion.Subject.NameID != nil {
		email = assertion.Subject.NameID.Value
	}

	displayName := getAttributeValue(assertion, "http://schemas.microsoft.com/identity/claims/displayname")
	if displayName == "" {
		first := getAttributeValue(assertion, "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/givenname")
		last := getAttributeValue(assertion, "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/surname")
		if first != "" || last != "" {
			displayName = first + " " + last
		}
	}
	if displayName == "" {
		displayName = email
	}

	if email == "" {
		http.Redirect(w, r, "/login?error=Aucun+email+dans+la+réponse+SAML", http.StatusSeeOther)
		return
	}

	// Auto-provision or update user
	user, err := h.DB.UpsertUser(email, displayName)
	if err != nil {
		log.Printf("User provisioning error: %v", err)
		http.Redirect(w, r, "/login?error=Erreur+provisionnement", http.StatusSeeOther)
		return
	}

	// Apply RBAC: map IDP groups to application roles if group mapping is configured.
	if h.Config.SAMLGroupGlobal != "" || h.Config.SAMLGroupTeamManager != "" ||
		h.Config.SAMLGroupTeamLeader != "" || h.Config.SAMLGroupStatusManager != "" ||
		h.Config.SAMLGroupActivityViewer != "" || h.Config.SAMLGroupFloorplanManager != "" {
		groups := getAttributeValues(assertion, h.Config.SAMLGroupsClaim)
		groupSet := make(map[string]bool, len(groups))
		for _, g := range groups {
			groupSet[g] = true
		}
		var roles []string
		for _, mapping := range []struct{ groupID, role string }{
			{h.Config.SAMLGroupGlobal, models.RoleGlobal},
			{h.Config.SAMLGroupTeamManager, models.RoleTeamManager},
			{h.Config.SAMLGroupTeamLeader, models.RoleTeamLeader},
			{h.Config.SAMLGroupStatusManager, models.RoleStatusManager},
			{h.Config.SAMLGroupActivityViewer, models.RoleActivityViewer},
			{h.Config.SAMLGroupFloorplanManager, models.RoleFloorplanManager},
		} {
			if mapping.groupID != "" && groupSet[mapping.groupID] {
				roles = append(roles, mapping.role)
			}
		}
		if len(roles) == 0 {
			roles = []string{models.RoleBasic}
		}
		if err := h.DB.UpdateUserRoles(user.ID, strings.Join(roles, ",")); err != nil {
			log.Printf("SAML role sync error: %v", err)
		}
	}

	token, err := h.DB.CreateSession(user.ID)
	if err != nil {
		metrics.AuthLoginsTotal.WithLabelValues("saml", "failure").Inc()
		http.Redirect(w, r, "/login?error=Erreur+session", http.StatusSeeOther)
		return
	}

	metrics.AuthLoginsTotal.WithLabelValues("saml", "success").Inc()
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 30,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			if attr.Name == name {
				vals := make([]string, 0, len(attr.Values))
				for _, v := range attr.Values {
					vals = append(vals, v.Value)
				}
				return vals
			}
		}
	}
	return nil
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
