package main

import (
	"testing"
	"time"
)

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
