package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newAuthTestService(t *testing.T) *service {
	t.Helper()
	svc, err := newService(config{
		DataDir:         t.TempDir(),
		PublicBaseURL:   "http://files.test",
		MaxUploadBytes:  1024,
		MaxStorageBytes: 1024,
		Retention:       24 * time.Hour,
		Driver:          "local",
		AuthPassword:    "short1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestProtectedClientConfigRequiresSession(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	newRouter(newAuthTestService(t)).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestUnauthenticatedRootServesLoginForm(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	newRouter(newAuthTestService(t)).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `name="password"`) {
		t.Fatal("root response does not contain a password form")
	}
}

func TestUploadRejectsIncorrectPasswordQuery(t *testing.T) {
	request := uploadRequest(t, "/api/upload?pwd=wrong")
	response := httptest.NewRecorder()
	newRouter(newAuthTestService(t)).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestLoginCreatesSessionForProtectedRoutes(t *testing.T) {
	svc := newAuthTestService(t)
	form := url.Values{"password": {"short1"}}
	login := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResponse := httptest.NewRecorder()
	newRouter(svc).ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d", loginResponse.Code, http.StatusSeeOther)
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authCookieName || !cookies[0].HttpOnly {
		t.Fatalf("login cookie = %#v", cookies)
	}
	config := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	config.AddCookie(cookies[0])
	configResponse := httptest.NewRecorder()
	newRouter(svc).ServeHTTP(configResponse, config)
	if configResponse.Code != http.StatusOK {
		t.Fatalf("authenticated config status = %d", configResponse.Code)
	}
}

func TestUploadWithPasswordReturnsFileSpecificShareLink(t *testing.T) {
	svc := newAuthTestService(t)
	router := newRouter(svc)
	uploadResponse := httptest.NewRecorder()
	router.ServeHTTP(uploadResponse, uploadRequest(t, "/api/upload?pwd=short1"))
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload status = %d: %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	shareURL, err := url.Parse(payload.URL)
	if err != nil {
		t.Fatal(err)
	}
	if shareURL.Query().Get("key") == "" || shareURL.Query().Get("pwd") != "" {
		t.Fatalf("unexpected share URL %q", payload.URL)
	}
	fileResponse := httptest.NewRecorder()
	router.ServeHTTP(fileResponse, httptest.NewRequest(http.MethodGet, shareURL.RequestURI(), nil))
	if fileResponse.Code != http.StatusOK {
		t.Fatalf("share link status = %d", fileResponse.Code)
	}
	shareURL.RawQuery = "key=invalid"
	invalidResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidResponse, httptest.NewRequest(http.MethodGet, shareURL.RequestURI(), nil))
	if invalidResponse.Code != http.StatusUnauthorized {
		t.Fatalf("invalid share link status = %d", invalidResponse.Code)
	}
}

func TestAuthenticationCredentialsExpireAcrossServiceRestart(t *testing.T) {
	directory := t.TempDir()
	cfg := config{DataDir: directory, MaxUploadBytes: 1024, MaxStorageBytes: 1024, Retention: time.Hour, Driver: "local", AuthPassword: "short1"}
	first, err := newService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cookie := first.auth.newSessionCookie(time.Now())
	key := first.auth.fileKey("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	second, err := newService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if second.auth.validSession(cookie.Value, time.Now()) {
		t.Fatal("session from previous process was accepted")
	}
	if second.auth.validFileKey("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", key) {
		t.Fatal("file key from previous process was accepted")
	}
}

func TestAuthenticationFailuresAreRateLimited(t *testing.T) {
	router := newRouter(newAuthTestService(t))
	for attempt := 1; attempt <= maxAuthFailures; attempt++ {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, uploadRequest(t, "/api/upload?pwd=wrong"))
		want := http.StatusUnauthorized
		if attempt == maxAuthFailures {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.Code, want)
		}
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, uploadRequest(t, "/api/upload?pwd=short1"))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("limited request status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
}

func uploadRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "image.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(tinyPNG); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
