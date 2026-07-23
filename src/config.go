//go:build ignore

package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/joho/godotenv"
	str2duration "github.com/xhit/go-str2duration/v2"
)

var retentionPattern = regexp.MustCompile(`^[1-9][0-9]*[mhdw]$`)

// config holds every runtime knob. All fields are derived from environment
// variables in loadConfig and are immutable for the lifetime of the process.
type config struct {
	ListenAddr      string
	DataDir         string
	PublicBaseURL   string
	MaxUploadBytes  int64
	MaxStorageBytes int64
	Retention       time.Duration
	Driver          string
	AllowedTypes    string
	OSSEndpoint     string
	OSSBucket       string
	OSSAccessKeyID  string
	OSSAccessKey    string
	AuthPassword    string
}

// loadConfig reads and validates the environment configuration. It is the
// single source of truth for startup-time validation; a returned error aborts
// the process.
func loadConfig() (config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return config{}, fmt.Errorf("load .env: %w", err)
	}
	cfg := config{
		ListenAddr:      env("LISTEN_ADDR", ":8080"),
		DataDir:         env("DATA_DIR", "/data"),
		PublicBaseURL:   strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/"),
		MaxUploadBytes:  defaultMaxUploadBytes,
		MaxStorageBytes: defaultMaxUploadBytes,
		Retention:       24 * time.Hour,
		Driver:          strings.ToLower(env("STORAGE_DRIVER", "local")),
		AllowedTypes:    strings.TrimSpace(os.Getenv("ALLOWED_TYPES")),
		OSSEndpoint:     os.Getenv("OSS_ENDPOINT"),
		OSSBucket:       os.Getenv("OSS_BUCKET"),
		OSSAccessKeyID:  os.Getenv("OSS_ACCESS_KEY_ID"),
		OSSAccessKey:    os.Getenv("OSS_ACCESS_KEY_SECRET"),
		AuthPassword:    os.Getenv("AUTH_PASSWORD"),
	}
	if raw := os.Getenv("MAX_UPLOAD_BYTES"); raw != "" {
		value, err := parseBytes(raw)
		if err != nil || value <= 0 || value > defaultMaxUploadBytes {
			return config{}, fmt.Errorf("invalid MAX_UPLOAD_BYTES %q", raw)
		}
		cfg.MaxUploadBytes = value
	}
	if raw := os.Getenv("MAX_STORAGE_BYTES"); raw != "" {
		value, err := parseBytes(raw)
		if err != nil || value <= 0 || value > defaultMaxUploadBytes {
			return config{}, fmt.Errorf("invalid MAX_STORAGE_BYTES %q", raw)
		}
		cfg.MaxStorageBytes = value
	}
	if raw := os.Getenv("RETENTION"); raw != "" {
		retention, err := parseRetention(raw)
		if err != nil {
			return config{}, fmt.Errorf("invalid RETENTION %q", raw)
		}
		cfg.Retention = retention
	}
	if cfg.Driver != "local" && cfg.Driver != "oss" {
		return config{}, fmt.Errorf("STORAGE_DRIVER must be local or oss")
	}
	if cfg.Driver == "oss" {
		if cfg.OSSEndpoint == "" || cfg.OSSBucket == "" || cfg.OSSAccessKeyID == "" || cfg.OSSAccessKey == "" {
			return config{}, errors.New("OSS_ENDPOINT, OSS_BUCKET, OSS_ACCESS_KEY_ID, and OSS_ACCESS_KEY_SECRET are required for oss storage")
		}
	}
	if !validAuthPassword(cfg.AuthPassword) {
		return config{}, errors.New("AUTH_PASSWORD must be 6 to 16 ASCII characters")
	}
	return cfg, nil
}

func validAuthPassword(password string) bool {
	if len(password) < 6 || len(password) > 16 {
		return false
	}
	for _, char := range password {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

func parseRetention(raw string) (time.Duration, error) {
	if !retentionPattern.MatchString(raw) {
		return 0, errors.New("must be a positive integer followed by m, h, d, or w")
	}
	retention, err := str2duration.ParseDuration(raw)
	if err != nil || retention <= 0 {
		return 0, errors.New("invalid duration")
	}
	return retention, nil
}
