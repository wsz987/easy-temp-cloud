//go:build ignore

package main

import "regexp"

// sha256Pattern matches a lowercase 64-character hex SHA-256 digest. Used to
// validate object keys and route parameters before touching the filesystem.
var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
