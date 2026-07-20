package main

import (
	"fmt"
	"sort"
	"strings"
)

// typePolicy decides whether a detected MIME type may be stored.
type typePolicy struct {
	allowAll bool
	exact    map[string]struct{}
	prefixes []string
	raw      string
}

// presetGroups expands named aliases to their constituent MIME types.
var presetGroups = map[string][]string{
	"images": {"image/jpeg", "image/png", "image/gif", "image/webp"},
	"videos": {"video/mp4", "video/webm", "video/quicktime", "video/x-matroska", "video/x-msvideo", "video/mpeg"},
	"audio":  {"audio/mpeg", "audio/ogg", "audio/wav", "audio/x-wav", "audio/webm", "audio/aac", "audio/flac"},
	"docs":   {"application/pdf", "text/plain", "text/markdown", "text/html"},
}

// parseTypePolicy builds a typePolicy from the ALLOWED_TYPES env value.
// Empty / "all" => accept anything; "images" => the default image set;
// otherwise a comma-separated list that may mix aliases (images, videos),
// prefix wildcards (image/*, video/*), and exact MIME types.
func parseTypePolicy(raw string) (typePolicy, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		raw = "all"
	}
	policy := typePolicy{exact: map[string]struct{}{}, raw: raw}
	if raw == "all" {
		policy.allowAll = true
		return policy, nil
	}
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if strings.HasSuffix(token, "/*") {
			policy.prefixes = append(policy.prefixes, strings.TrimSuffix(token, "/*"))
			continue
		}
		if group, ok := presetGroups[token]; ok {
			for _, mt := range group {
				policy.exact[mt] = struct{}{}
			}
			continue
		}
		if !validMIME(token) {
			return typePolicy{}, fmt.Errorf("invalid MIME type %q in ALLOWED_TYPES", token)
		}
		policy.exact[token] = struct{}{}
	}
	if len(policy.exact) == 0 && len(policy.prefixes) == 0 {
		return typePolicy{}, fmt.Errorf("ALLOWED_TYPES %q contains no usable types", raw)
	}
	return policy, nil
}

// validMIME checks that token looks like a "type/subtype" MIME type.
func validMIME(token string) bool {
	slash := strings.IndexByte(token, '/')
	if slash <= 0 || slash == len(token)-1 {
		return false
	}
	for _, r := range token[:slash] {
		if !isMIMERune(r, false) {
			return false
		}
	}
	for _, r := range token[slash+1:] {
		if !isMIMERune(r, true) {
			return false
		}
	}
	return true
}

// isMIMERune restricts tokens to the RFC 6838 MIME grammar (lowercased).
func isMIMERune(r rune, subtype bool) bool {
	if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' {
		return true
	}
	return subtype && r == '+'
}

func (p typePolicy) allows(contentType string) bool {
	if p.allowAll {
		return true
	}
	if _, ok := p.exact[contentType]; ok {
		return true
	}
	for _, prefix := range p.prefixes {
		if strings.HasPrefix(contentType, prefix+"/") {
			return true
		}
	}
	return false
}

func (p typePolicy) String() string {
	if p.allowAll {
		return "all"
	}
	parts := make([]string, 0, len(p.prefixes)+len(p.exact))
	for _, prefix := range p.prefixes {
		parts = append(parts, prefix+"/*")
	}
	for mt := range p.exact {
		parts = append(parts, mt)
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return p.raw
	}
	return strings.Join(parts, ", ")
}

// normalizeContentType lowercases the detected MIME and drops any parameters
// (e.g. "; charset=utf-8") so stored and compared types are canonical.
func normalizeContentType(contentType string) string {
	contentType = strings.TrimSpace(strings.ToLower(contentType))
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = strings.TrimSpace(contentType[:i])
	}
	return contentType
}
