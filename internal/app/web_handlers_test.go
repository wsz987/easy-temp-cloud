package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"easy-temp-cloud/internal/config"
	"easy-temp-cloud/internal/policy"
)

func TestClientConfigIncludesAPIPassword(t *testing.T) {
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
	var payload struct {
		APIPassword string `json:"apiPassword"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.APIPassword != "eztCloud@" {
		t.Fatalf("apiPassword = %q, want %q", payload.APIPassword, "eztCloud@")
	}
}
