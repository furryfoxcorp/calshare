// Package oidc handles web authentication: the OIDC code flow with PKCE and
// server-side session cookies. CalDAV clients authenticate separately with app
// passwords (see internal/caldav).
package oidc

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// signValue returns "value.signature" where the signature is an HMAC of the
// value under key. It lets the server detect tampering with the cookie.
func signValue(key []byte, value string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value))
	return value + "." + hex.EncodeToString(mac.Sum(nil))
}

// verifyValue checks a signed value and returns the original value. The second
// result is false when the signature is missing or wrong.
func verifyValue(key []byte, signed string) (string, bool) {
	value, sig, ok := strings.Cut(signed, ".")
	if !ok {
		return "", false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value))
	expected := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return "", false
	}
	return value, true
}
