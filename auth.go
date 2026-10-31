package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/idtoken"
)

const (
	sessionCookie = "session"
	stateCookie   = "oauth_state"
	nextCookie    = "oauth_next"
	sessionTTL    = 30 * 24 * time.Hour
	loginTTL      = 10 * time.Minute
)

var errSignInRequired = errors.New("sign in required")

// Auth handles Google sign-in. A nil Auth means sign-in is not configured, and
// nobody can upload.
type Auth struct {
	config *oauth2.Config
	secure bool
}

// NewAuth returns nil when no client ID is set. baseURL is the address users
// reach the site on, and must match a redirect URI on the OAuth client.
func NewAuth(clientID, clientSecret, baseURL string) (*Auth, error) {
	if clientID == "" {
		return nil, nil
	}
	if clientSecret == "" || baseURL == "" {
		return nil, errors.New("GOOGLE_CLIENT_SECRET and BASE_URL are required with GOOGLE_CLIENT_ID")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &Auth{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  baseURL + "/auth/google/callback",
			Endpoint:     google.Endpoint,
			Scopes:       []string{"openid", "email", "profile"},
		},
		secure: strings.HasPrefix(baseURL, "https://"),
	}, nil
}

func (a *Auth) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   a.secure,
		// Lax keeps the cookie off cross-site POSTs, while the redirect back from
		// Google still carries it.
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) clearCookie(w http.ResponseWriter, name string) {
	a.setCookie(w, name, "", -time.Second)
}

// Only the hash is stored, so reading the table does not reveal live sessions.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// safeNext only allows paths on this site, so a sign-in link cannot send people
// somewhere else.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		return "/"
	}
	return next
}

func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeError(w, http.StatusServiceUnavailable, "sign in is not configured")
		return
	}
	state := rand.Text()
	s.auth.setCookie(w, stateCookie, state, loginTTL)
	s.auth.setCookie(w, nextCookie, safeNext(r.URL.Query().Get("next")), loginTTL)
	http.Redirect(w, r, s.auth.config.AuthCodeURL(state), http.StatusFound)
}

func (s *Server) signInCallback(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeError(w, http.StatusServiceUnavailable, "sign in is not configured")
		return
	}
	state, err := r.Cookie(stateCookie)
	got := r.URL.Query().Get("state")
	if err != nil || got == "" || subtle.ConstantTimeCompare([]byte(state.Value), []byte(got)) != 1 {
		writeError(w, http.StatusBadRequest, "sign in expired, please try again")
		return
	}
	s.auth.clearCookie(w, stateCookie)

	next := "/"
	if c, err := r.Cookie(nextCookie); err == nil {
		next = safeNext(c.Value)
	}
	s.auth.clearCookie(w, nextCookie)

	// The person pressed cancel on Google's screen.
	if r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, next, http.StatusFound)
		return
	}

	user, err := s.exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		slog.Error("google sign in", "err", err)
		writeError(w, http.StatusBadGateway, "could not sign in with Google")
		return
	}
	if err := s.store.UpsertUser(r.Context(), user); err != nil {
		internalError(w, "save user", err)
		return
	}
	token := rand.Text()
	if err := s.store.CreateSession(r.Context(), hashToken(token), user.ID, time.Now().Add(sessionTTL)); err != nil {
		internalError(w, "create session", err)
		return
	}
	s.auth.setCookie(w, sessionCookie, token, sessionTTL)
	http.Redirect(w, r, next, http.StatusFound)
}

// exchange trades the code for an ID token and checks it was issued by Google
// for this client.
func (s *Server) exchange(ctx context.Context, code string) (*User, error) {
	token, err := s.auth.config.Exchange(ctx, code)
	if err != nil {
		return nil, err
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("no id_token in response")
	}
	payload, err := idtoken.Validate(ctx, raw, s.auth.config.ClientID)
	if err != nil {
		return nil, err
	}
	email, _ := payload.Claims["email"].(string)
	name, _ := payload.Claims["name"].(string)
	picture, _ := payload.Claims["picture"].(string)
	return &User{GoogleSub: payload.Subject, Email: email, Name: name, Picture: picture}, nil
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.store.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			internalError(w, "delete session", err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user, err := s.currentUser(r)
	if errors.Is(err, errSignInRequired) {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		internalError(w, "load user", err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// currentUser returns errSignInRequired without a valid session.
func (s *Server) currentUser(r *http.Request) (*User, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, errSignInRequired
	}
	return s.store.UserBySession(r.Context(), hashToken(c.Value))
}
