// Package auth holds process-local authentication state: password checking,
// session-cookie signing, and per-client failure rate limiting.
//
// Its signing key is generated on each startup, intentionally invalidating
// sessions and file links on restart.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
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
	CookieName    = "et_session"
	SessionTTL    = 7 * 24 * time.Hour
	MaxFailures   = 5
	FailureWindow = 5 * time.Minute
	MaxClients    = 1024
	MaxLoginBody  = 8 * 1024
)

type failure struct {
	first    time.Time
	attempts int
}

// Auth holds process-local authentication state. Its signing key is generated
// on each startup, intentionally invalidating sessions and file links on restart.
type Auth struct {
	passwordHash [sha256.Size]byte
	signingKey   [32]byte
	mu           sync.Mutex
	failures     map[string]failure
}

// New creates authentication state for the configured password.
func New(password string) (*Auth, error) {
	a := &Auth{passwordHash: sha256.Sum256([]byte(password)), failures: map[string]failure{}}
	if _, err := ReadRand(a.signingKey[:]); err != nil {
		return nil, err
	}
	return a, nil
}

// ReadRand fills b with cryptographically random bytes. Wrapped so tests and
// callers don't import crypto/rand directly and so failures surface uniformly.
func ReadRand(b []byte) (int, error) {
	return rand.Read(b)
}

// ValidPassword reports whether password matches the configured one, using a
// constant-time comparison.
func (a *Auth) ValidPassword(password string) bool {
	candidate := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(candidate[:], a.passwordHash[:]) == 1
}

// NewSessionCookie builds the session cookie for the given instant.
func (a *Auth) NewSessionCookie(now time.Time) *http.Cookie {
	expires := now.Add(SessionTTL)
	payload := strconv.FormatInt(expires.Unix(), 10)
	return &http.Cookie{
		Name:     CookieName,
		Value:    payload + "." + a.signature("session:"+payload),
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(SessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// ValidSession reports whether the cookie value is a currently-valid session.
func (a *Auth) ValidSession(value string, now time.Time) bool {
	payload, signature, ok := strings.Cut(value, ".")
	if !ok || !hmac.Equal([]byte(signature), []byte(a.signature("session:"+payload))) {
		return false
	}
	expires, err := strconv.ParseInt(payload, 10, 64)
	return err == nil && now.Before(time.Unix(expires, 0))
}

// FileKey returns the share-link key for a file id.
func (a *Auth) FileKey(id string) string { return a.signature("file:" + id) }

// ValidFileKey reports whether key is the correct share-link key for id.
func (a *Auth) ValidFileKey(id, key string) bool {
	return key != "" && hmac.Equal([]byte(key), []byte(a.fileKey(id)))
}

func (a *Auth) fileKey(id string) string { return a.signature("file:" + id) }

func (a *Auth) signature(payload string) string {
	mac := hmac.New(sha256.New, a.signingKey[:])
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// AllowAttempt reports whether the client may still try to authenticate.
func (a *Auth) AllowAttempt(client string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneFailuresLocked(now)
	failure, ok := a.failures[client]
	if !ok {
		return true
	}
	return failure.attempts < MaxFailures
}

// FailedAttempt records a failed authentication and reports whether the client
// has now hit the rate limit.
func (a *Auth) FailedAttempt(client string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneFailuresLocked(now)
	state, ok := a.failures[client]
	if !ok {
		if len(a.failures) >= MaxClients {
			a.dropOldestFailureLocked()
		}
		state = failure{first: now}
	}
	state.attempts++
	a.failures[client] = state
	return state.attempts >= MaxFailures
}

func (a *Auth) pruneFailuresLocked(now time.Time) {
	for client, failure := range a.failures {
		if now.Sub(failure.first) >= FailureWindow {
			delete(a.failures, client)
		}
	}
}

func (a *Auth) dropOldestFailureLocked() {
	var oldestClient string
	var oldest time.Time
	for client, failure := range a.failures {
		if oldestClient == "" || failure.first.Before(oldest) {
			oldestClient = client
			oldest = failure.first
		}
	}
	if oldestClient != "" {
		delete(a.failures, oldestClient)
	}
}

// ClearAttempts forgets a client's failure history (on a successful login).
func (a *Auth) ClearAttempts(client string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, client)
}

// Failures returns the number of currently tracked clients. For tests.
func (a *Auth) Failures() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.failures)
}

// ClientAddress extracts the remote IP from a request, stripping any port.
func ClientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
