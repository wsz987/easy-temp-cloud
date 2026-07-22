package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// ParseBytes parses a human-friendly byte count such as "10GiB", "500MB",
// "100m", or a raw integer. Suffixes are case-insensitive; binary and decimal
// units are both supported (GiB/GB, MiB/MB, g, m).
func ParseBytes(raw string) (int64, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	multiplier := int64(1)
	for suffix, value := range map[string]int64{"gib": 1024 * 1024 * 1024, "gb": 1000 * 1000 * 1000, "g": 1024 * 1024 * 1024, "mib": 1024 * 1024, "mb": 1000 * 1000, "m": 1024 * 1024} {
		if strings.HasSuffix(raw, suffix) {
			raw = strings.TrimSuffix(raw, suffix)
			multiplier = value
			break
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value > ((1<<63-1)-MaxMultipartOverhead)/multiplier {
		return 0, errors.New("invalid byte count")
	}
	return value * multiplier, nil
}

// env returns the named environment variable or fallback when unset/empty.
func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// FileExists reports whether path names an existing filesystem entry.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
