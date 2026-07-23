package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"easy-temp-cloud/internal/auth"
	"easy-temp-cloud/internal/config"
	"easy-temp-cloud/internal/policy"
)

func TestTokenEndpointIssuesBearerToken(t *testing.T) {
	a, err := auth.New("eztCloud@")
	if err != nil {
		t.Fatal(err)
	}
	svc := &service{auth: a}
	router := NewRouter(svc)
	form := url.Values{"password": {"eztCloud@"}}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if response.Header().Get("Set-Cookie") != "" {
		t.Fatal("token login must not set a session cookie")
	}
	var payload struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token == "" {
		t.Fatal("token must not be empty")
	}
	if until := time.Until(payload.ExpiresAt); until < 6*24*time.Hour || until > 7*24*time.Hour {
		t.Fatalf("expiresAt has remaining duration %s, want about 7 days", until)
	}
}

func addBearer(t *testing.T, router http.Handler, password string, request *http.Request) {
	t.Helper()
	form := url.Values{"password": []string{password}}
	login := httptest.NewRequest(http.MethodPost, "/api/auth/token", strings.NewReader(form.Encode()))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, login)
	if response.Code != http.StatusOK {
		t.Fatalf("token status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token == "" {
		t.Fatal("token must not be empty")
	}
	request.Header.Set("Authorization", "Bearer "+payload.Token)
}

func TestClientConfigDoesNotExposeAPIPassword(t *testing.T) {
	pol, err := policy.Parse("all")
	if err != nil {
		t.Fatal(err)
	}
	svc := &service{
		config: config.Config{AuthPassword: "eztCloud@"},
		policy: pol,
	}
	response := httptest.NewRecorder()

	svc.clientConfig(response, httptest.NewRequest(http.MethodGet, "/api/config", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["apiPassword"]; exists {
		t.Fatal("config must not expose apiPassword")
	}
}

func TestRootServesApplicationAndLoginHasOwnRoute(t *testing.T) {
	SetWebFS(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("application")},
		"login.html": &fstest.MapFile{Data: []byte("login")},
	})
	router := NewRouter(&service{})

	root := httptest.NewRecorder()
	router.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	if root.Code != http.StatusOK || root.Body.String() != "application" {
		t.Fatalf("root response = %d %q, want application", root.Code, root.Body.String())
	}

	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/login", nil))
	if login.Code != http.StatusOK || login.Body.String() != "login" {
		t.Fatalf("login response = %d %q, want login", login.Code, login.Body.String())
	}
}
