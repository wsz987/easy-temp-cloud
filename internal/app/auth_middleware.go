package app

import (
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"easy-temp-cloud/internal/auth"
)

// requireBearer guards a handler behind a valid Authorization Bearer token.
func (s *service) requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t") || !s.auth.ValidToken(token, time.Now()) {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// issueToken validates a form password, rate-limits failures by client IP, and
// returns a seven-day Bearer token on success.
func (s *service) issueToken(w http.ResponseWriter, r *http.Request) {
	client := auth.ClientAddress(r)
	now := time.Now()
	if !s.auth.AllowAttempt(client, now) {
		writeError(w, http.StatusTooManyRequests, "too many failed authentication attempts")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/x-www-form-urlencoded")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, auth.MaxLoginBody)
	if err := r.ParseForm(); err != nil {
		if errors.As(err, new(*http.MaxBytesError)) {
			writeError(w, http.StatusRequestEntityTooLarge, "login request exceeds maximum size")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid login form")
		return
	}
	if !s.auth.ValidPassword(r.PostForm.Get("password")) {
		if s.auth.FailedAttempt(client, now) {
			writeError(w, http.StatusTooManyRequests, "too many failed authentication attempts")
			return
		}
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	s.auth.ClearAttempts(client)
	token, expiresAt, err := s.auth.NewToken(now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue authentication token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expiresAt": expiresAt})
}
