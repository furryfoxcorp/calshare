package ical

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	goical "github.com/emersion/go-ical"

	_ "time/tzdata" // bundle the IANA database so VTIMEZONE generation needs no host tzdata
)

const prodID = "-//furryfoxcorp//calshare//EN"

// Emit serializes a calendar to bytes with CRLF line endings, ensuring a
// PRODID is present.
func Emit(cal *goical.Calendar) ([]byte, error) {
	ensureProdID(cal)
	var buf bytes.Buffer
	if err := goical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, fmt.Errorf("ical: encode: %w", err)
	}
	return buf.Bytes(), nil
}

func ensureProdID(cal *goical.Calendar) {
	if cal.Props.Get("PRODID") == nil {
		cal.Props.SetText("PRODID", prodID)
	}
	if cal.Props.Get("VERSION") == nil {
		cal.Props.SetText("VERSION", "2.0")
	}
}

// ETag returns the strong ETag for a stored body: the first 16 bytes of its
// SHA-256, hex-encoded.
func ETag(blob []byte) string {
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:16])
}

// Canonicalize parses and re-emits a body into the server's canonical form
// (PRODID injected, line folding normalized, all properties preserved). The
// mutated flag reports whether the canonical form differs from the input, so
// callers can decide whether to omit the ETag on a PUT response.
//
// Canonicalize is idempotent: feeding its own output back in returns
// mutated=false.
func Canonicalize(blob []byte) (out []byte, mutated bool, err error) {
	cal, err := goical.NewDecoder(bytes.NewReader(blob)).Decode()
	if err != nil {
		return nil, false, fmt.Errorf("ical: decode: %w", err)
	}
	ensureProdID(cal)
	var buf bytes.Buffer
	if err := goical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, false, fmt.Errorf("ical: encode: %w", err)
	}
	out = buf.Bytes()
	return out, !bytes.Equal(out, blob), nil
}
