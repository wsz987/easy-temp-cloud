package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigLoadsDotenvFile(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("AUTH_PASSWORD=dotenv1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(workingDirectory) })

	previousPassword, hadPassword := os.LookupEnv("AUTH_PASSWORD")
	if err := os.Unsetenv("AUTH_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadPassword {
			_ = os.Setenv("AUTH_PASSWORD", previousPassword)
			return
		}
		_ = os.Unsetenv("AUTH_PASSWORD")
	})

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.AuthPassword != "dotenv1" {
		t.Fatalf("AuthPassword = %q, want value from .env", cfg.AuthPassword)
	}
}

func TestLoadConfigParsesRetention(t *testing.T) {
	t.Setenv("AUTH_PASSWORD", "short1")
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "default", raw: "", want: 24 * time.Hour},
		{name: "minutes", raw: "1m", want: time.Minute},
		{name: "hours", raw: "5h", want: 5 * time.Hour},
		{name: "days", raw: "24d", want: 24 * 24 * time.Hour},
		{name: "weeks", raw: "1w", want: 7 * 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("RETENTION", tt.raw)

			cfg, err := loadConfig()
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if cfg.Retention != tt.want {
				t.Fatalf("Retention = %v, want %v", cfg.Retention, tt.want)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidRetention(t *testing.T) {
	t.Setenv("AUTH_PASSWORD", "short1")
	for _, raw := range []string{"0m", "-1h", "1.5h", "1H", "1M", "1d2h", "2day"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("RETENTION", raw)

			if _, err := loadConfig(); err == nil {
				t.Fatalf("loadConfig(%q) succeeded", raw)
			}
		})
	}
}

func TestLoadConfigRequiresValidAuthPassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "missing", wantErr: true},
		{name: "too short", password: "short", wantErr: true},
		{name: "valid minimum", password: "short1"},
		{name: "valid maximum", password: "1234567890abcdef"},
		{name: "too long", password: "1234567890abcdefg", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AUTH_PASSWORD", tt.password)
			_, err := loadConfig()
			if (err != nil) != tt.wantErr {
				t.Fatalf("loadConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
