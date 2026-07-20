package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	authCookieName    = "easy_temp_host_session"
	authSessionTTL    = 7 * 24 * time.Hour
	maxAuthFailures   = 5
	authFailureWindow = 5 * time.Minute
)

type authFailure struct {
	first    time.Time
	attempts int
}

// auth holds process-local authentication state. Its signing key is generated
// on each startup, intentionally invalidating sessions and file links on restart.
type auth struct {
	passwordHash [sha256.Size]byte
	signingKey   [32]byte
	mu           sync.Mutex
	failures     map[string]authFailure
}

func newAuth(password string) (*auth, error) {
	a := &auth{passwordHash: sha256.Sum256([]byte(password)), failures: map[string]authFailure{}}
	if _, err := readRand(a.signingKey[:]); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *auth) validPassword(password string) bool {
	candidate := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(candidate[:], a.passwordHash[:]) == 1
}

func (a *auth) newSessionCookie(now time.Time) *http.Cookie {
	expires := now.Add(authSessionTTL)
	payload := strconv.FormatInt(expires.Unix(), 10)
	return &http.Cookie{
		Name:     authCookieName,
		Value:    payload + "." + a.signature("session:"+payload),
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(authSessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a *auth) validSession(value string, now time.Time) bool {
	payload, signature, ok := strings.Cut(value, ".")
	if !ok || !hmac.Equal([]byte(signature), []byte(a.signature("session:"+payload))) {
		return false
	}
	expires, err := strconv.ParseInt(payload, 10, 64)
	return err == nil && now.Before(time.Unix(expires, 0))
}

func (a *auth) fileKey(id string) string { return a.signature("file:" + id) }

func (a *auth) validFileKey(id, key string) bool {
	return key != "" && hmac.Equal([]byte(key), []byte(a.fileKey(id)))
}

func (a *auth) signature(payload string) string {
	mac := hmac.New(sha256.New, a.signingKey[:])
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *auth) allowAttempt(client string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	failure, ok := a.failures[client]
	if !ok || now.Sub(failure.first) >= authFailureWindow {
		delete(a.failures, client)
		return true
	}
	return failure.attempts < maxAuthFailures
}

func (a *auth) failedAttempt(client string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	failure, ok := a.failures[client]
	if !ok || now.Sub(failure.first) >= authFailureWindow {
		failure = authFailure{first: now}
	}
	failure.attempts++
	a.failures[client] = failure
	return failure.attempts >= maxAuthFailures
}

func (a *auth) clearAttempts(client string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, client)
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *service) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(authCookieName)
		if err != nil || !s.auth.validSession(cookie.Value, time.Now()) {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *service) requirePasswordQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client := clientAddress(r)
		now := time.Now()
		if !s.auth.allowAttempt(client, now) {
			writeError(w, http.StatusTooManyRequests, "too many failed authentication attempts")
			return
		}
		if !s.auth.validPassword(r.URL.Query().Get("pwd")) {
			if s.auth.failedAttempt(client, now) {
				writeError(w, http.StatusTooManyRequests, "too many failed authentication attempts")
				return
			}
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		s.auth.clearAttempts(client)
		next.ServeHTTP(w, r)
	})
}

func (s *service) login(w http.ResponseWriter, r *http.Request) {
	client := clientAddress(r)
	now := time.Now()
	if !s.auth.allowAttempt(client, now) {
		writeError(w, http.StatusTooManyRequests, "too many failed authentication attempts")
		return
	}
	if !s.auth.validPassword(r.FormValue("password")) {
		if s.auth.failedAttempt(client, now) {
			writeError(w, http.StatusTooManyRequests, "too many failed authentication attempts")
			return
		}
		s.loginPage(w, http.StatusUnauthorized)
		return
	}
	s.auth.clearAttempts(client)
	http.SetCookie(w, s.auth.newSessionCookie(now))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *service) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: authCookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
