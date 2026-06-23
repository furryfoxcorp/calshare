package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

// Config holds the OIDC client settings.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	AdminEmails  map[string]struct{}
}

// Authenticator runs the OIDC code flow with PKCE and bridges it to web
// sessions.
type Authenticator struct {
	db       *storage.DB
	sessions *Manager
	cfg      Config
	verifier *gooidc.IDTokenVerifier
	oauth2   oauth2.Config
}

// New discovers the provider and builds an Authenticator. It needs network
// access to the issuer's discovery endpoint.
func New(ctx context.Context, db *storage.DB, sessions *Manager, cfg Config) (*Authenticator, error) {
	provider, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{gooidc.ScopeOpenID, "profile", "email"}
	}
	return &Authenticator{
		db:       db,
		sessions: sessions,
		cfg:      cfg,
		verifier: provider.Verifier(&gooidc.Config{ClientID: cfg.ClientID}),
		oauth2: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
		},
	}, nil
}

// LoginStart begins the code flow: it stores PKCE state and redirects the
// browser to the provider.
func (a *Authenticator) LoginStart(w http.ResponseWriter, r *http.Request) {
	state, err := randToken()
	if err != nil {
		http.Error(w, "login error", http.StatusInternalServerError)
		return
	}
	nonce, err := randToken()
	if err != nil {
		http.Error(w, "login error", http.StatusInternalServerError)
		return
	}
	verifier := oauth2.GenerateVerifier()

	redirectTo := r.URL.Query().Get("next")
	if !strings.HasPrefix(redirectTo, "/") {
		redirectTo = "/"
	}

	flow := storage.OIDCFlow{State: state, CodeVerifier: verifier, Nonce: nonce, RedirectTo: redirectTo}
	if err := a.db.CreateOIDCFlow(r.Context(), flow, 10*time.Minute); err != nil {
		http.Error(w, "login error", http.StatusInternalServerError)
		return
	}

	url := a.oauth2.AuthCodeURL(state,
		gooidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// Callback completes the code flow, provisions the user, and starts a session.
func (a *Authenticator) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if errParam := q.Get("error"); errParam != "" {
		http.Error(w, "sign-in was cancelled or failed", http.StatusBadRequest)
		return
	}
	state := q.Get("state")
	flow, err := a.db.TakeOIDCFlow(r.Context(), state)
	if err != nil {
		http.Error(w, "login session expired, try again", http.StatusBadRequest)
		return
	}

	token, err := a.oauth2.Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(flow.CodeVerifier))
	if err != nil {
		http.Error(w, "could not complete sign-in", http.StatusBadGateway)
		return
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "provider did not return an id token", http.StatusBadGateway)
		return
	}
	idToken, err := a.verifier.Verify(r.Context(), rawID)
	if err != nil {
		http.Error(w, "could not verify sign-in", http.StatusBadGateway)
		return
	}

	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
		Nonce string `json:"nonce"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "could not read profile", http.StatusBadGateway)
		return
	}
	if claims.Nonce != flow.Nonce {
		http.Error(w, "sign-in could not be verified", http.StatusBadRequest)
		return
	}
	if claims.Email == "" {
		http.Error(w, "provider did not share an email address", http.StatusBadGateway)
		return
	}
	name := claims.Name
	if name == "" {
		name = claims.Email
	}

	user, err := a.db.UpsertUserOnLogin(r.Context(), claims.Sub, claims.Email, name)
	if err != nil {
		http.Error(w, "could not sign you in", http.StatusInternalServerError)
		return
	}
	if _, isAdmin := a.cfg.AdminEmails[strings.ToLower(claims.Email)]; isAdmin && !user.IsAdmin {
		_ = a.db.SetAdmin(r.Context(), claims.Email, true)
	}

	if err := a.sessions.StartSession(w, r, user.ID); err != nil {
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return
	}
	dest := flow.RedirectTo
	if dest == "" {
		dest = "/"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// Logout clears the session and redirects to the login page.
func (a *Authenticator) Logout(w http.ResponseWriter, r *http.Request) {
	a.sessions.ClearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// randToken returns a URL-safe random token (about 144 bits of entropy).
func randToken() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
