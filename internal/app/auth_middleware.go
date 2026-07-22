package app

import (
	"errors"
	"mime"
	"net/http"
	"time"

	"easy-temp-cloud/internal/auth"
)

// requireSession guards a handler behind a valid browser session.
func (s *service) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.CookieName)
		if err != nil || !s.auth.ValidSession(cookie.Value, time.Now()) {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requirePasswordQuery guards a handler behind a ?pwd= password query parameter
// and rate-limits failures per client IP.
func (s *service) requirePasswordQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client := auth.ClientAddress(r)
		now := time.Now()
		if !s.auth.AllowAttempt(client, now) {
			writeError(w, http.StatusTooManyRequests, "too many failed authentication attempts")
			return
		}
		if !s.auth.ValidPassword(r.URL.Query().Get("pwd")) {
			if s.auth.FailedAttempt(client, now) {
				writeError(w, http.StatusTooManyRequests, "too many failed authentication attempts")
				return
			}
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		s.auth.ClearAttempts(client)
		next.ServeHTTP(w, r)
	})
}

func (s *service) login(w http.ResponseWriter, r *http.Request) {
	client := auth.ClientAddress(r)
	now := time.Now()
	if !s.auth.AllowAttempt(client, now) {
		s.loginPage(w, http.StatusTooManyRequests, "登录尝试过于频繁，请 5 分钟后再试。")
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
			s.loginPage(w, http.StatusTooManyRequests, "登录尝试过于频繁，请 5 分钟后再试。")
			return
		}
		s.loginPage(w, http.StatusUnauthorized, "密码不正确，请重试。")
		return
	}
	s.auth.ClearAttempts(client)
	http.SetCookie(w, s.auth.NewSessionCookie(now))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *service) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
