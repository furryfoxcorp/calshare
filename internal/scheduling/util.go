package scheduling

import (
	"crypto/rand"
	"encoding/base64"
	"strconv"
	"strings"
)

// generateMessageID returns a unique RFC 5322 Message-ID bound to a UID, used
// to correlate iMIP replies back to the originating event.
func generateMessageID(uid string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	rnd := base64.RawURLEncoding.EncodeToString(b)
	return "<" + rnd + "." + sanitizeHref(uid) + "@calshare>"
}

func atoi(s string) (int, error) { return strconv.Atoi(strings.TrimSpace(s)) }
func itoa(n int) string          { return strconv.Itoa(n) }

// normalizeEmail lowercases and trims an email for comparison.
func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// mailtoAddress extracts the bare address from a "mailto:foo@bar" value
// (case-insensitive scheme), lowercased.
func mailtoAddress(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 7 && strings.EqualFold(v[:7], "mailto:") {
		v = v[7:]
	}
	return normalizeEmail(v)
}

// equalFoldMailto reports whether two mailto values point at the same address.
func equalFoldMailto(a, b string) bool {
	return mailtoAddress(a) == mailtoAddress(b)
}
