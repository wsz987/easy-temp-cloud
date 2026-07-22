package main

import (
	"bytes"
	"crypto/rand"
	"strconv"
)

// readRand fills b with cryptographically random bytes. Wrapped so tests and
// callers don't import crypto/rand directly and so failures surface uniformly.
func readRand(b []byte) (int, error) {
	return rand.Read(b)
}

// parseInt64 parses a base-10 int64. Thin wrapper for readability at call sites.
func parseInt64(raw string) (int64, error) {
	return strconv.ParseInt(raw, 10, 64)
}

// bytesReader returns a seekable reader over b for http.ServeContent.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
