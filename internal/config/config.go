// Package config loads and validates the runtime configuration from the
// environment. It is the single source of truth for startup-time validation;
// a returned error aborts the process.
package config

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

// Hard limits shared by single-shot and chunked uploads.
const (
	// DefaultMaxUploadBytes is the absolute per-file ceiling. Both MAX_UPLOAD_BYTES
	// and MAX_STORAGE_BYTES are clamped to it.
	DefaultMaxUploadBytes int64 = 10 * 1024 * 1024 * 1024

	// MaxMultipartOverhead is the slack allowed on top of the content length to
	// account for multipart encoding overhead.
	MaxMultipartOverhead int64 = 1024 * 1024
)

var retentionPattern = regexp.MustCompile(`^[1-9][0-9]*[mhdw]$`)

// Config holds every runtime knob. All fields are derived from environment
// variables in Load and are immutable for the lifetime of the process.
type Config struct {
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

// ChunkSize returns the chunk size to advertise to clients. It clamps the
// per-upload maximum to a safe default derived from the configured single-file
// cap, so very large files still produce a bounded number of chunks.
func (c Config) ChunkSize() int64 {
	const (
		maxChunkSize     int64 = 32 * 1024 * 1024
		defaultChunkSize int64 = 8 * 1024 * 1024
	)
	size := defaultChunkSize
	// Keep at most ~1024 chunks per file for sane progress reporting.
	if cap := c.MaxUploadBytes / 1024; cap > 0 && cap < size {
		size = cap
	}
	if size < 1<<20 {
		size = 1 << 20
	}
	if size > maxChunkSize {
		size = maxChunkSize
	}
	return size
}

// MaxChunkSize bounds a single chunk payload. 32 MiB keeps per-request memory
// bounded while giving XHR upload-progress events enough granularity.
const MaxChunkSize int64 = 32 * 1024 * 1024

// Load reads and validates the environment configuration.
func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}
	cfg := Config{
		ListenAddr:      env("LISTEN_ADDR", ":8080"),
		DataDir:         env("DATA_DIR", "/data"),
		PublicBaseURL:   strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/"),
		MaxUploadBytes:  DefaultMaxUploadBytes,
		MaxStorageBytes: DefaultMaxUploadBytes,
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
		value, err := ParseBytes(raw)
		if err != nil || value <= 0 || value > DefaultMaxUploadBytes {
			return Config{}, fmt.Errorf("invalid MAX_UPLOAD_BYTES %q", raw)
		}
		cfg.MaxUploadBytes = value
	}
	if raw := os.Getenv("MAX_STORAGE_BYTES"); raw != "" {
		value, err := ParseBytes(raw)
		if err != nil || value <= 0 || value > DefaultMaxUploadBytes {
			return Config{}, fmt.Errorf("invalid MAX_STORAGE_BYTES %q", raw)
		}
		cfg.MaxStorageBytes = value
	}
	if raw := os.Getenv("RETENTION"); raw != "" {
		retention, err := parseRetention(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid RETENTION %q", raw)
		}
		cfg.Retention = retention
	}
	if cfg.Driver != "local" && cfg.Driver != "oss" {
		return Config{}, fmt.Errorf("STORAGE_DRIVER must be local or oss")
	}
	if cfg.Driver == "oss" {
		if cfg.OSSEndpoint == "" || cfg.OSSBucket == "" || cfg.OSSAccessKeyID == "" || cfg.OSSAccessKey == "" {
			return Config{}, errors.New("OSS_ENDPOINT, OSS_BUCKET, OSS_ACCESS_KEY_ID, and OSS_ACCESS_KEY_SECRET are required for oss storage")
		}
	}
	if !ValidAuthPassword(cfg.AuthPassword) {
		return Config{}, errors.New("AUTH_PASSWORD must be 6 to 16 ASCII characters")
	}
	return cfg, nil
}

// ValidAuthPassword reports whether password meets the strength rules:
// 6 to 16 printable ASCII characters.
func ValidAuthPassword(password string) bool {
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
