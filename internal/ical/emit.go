package ical

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"unicode/utf8"

	goical "github.com/emersion/go-ical"

	_ "time/tzdata" // bundle the IANA database so VTIMEZONE generation needs no host tzdata
)

const prodID = "-//furryfoxcorp//calshare//EN"

// Emit serializes a calendar to bytes with CRLF line endings, ensuring a
// PRODID is present and that content lines are folded to 75 octets (RFC 5545
// 3.1). go-ical does not fold, and strict clients such as Apple Calendar
// reject overly long lines, so we fold here.
func Emit(cal *goical.Calendar) ([]byte, error) {
	ensureProdID(cal)
	var buf bytes.Buffer
	if err := goical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, fmt.Errorf("ical: encode: %w", err)
	}
	return foldLines(buf.Bytes()), nil
}

// foldLines folds every CRLF-delimited content line to at most 75 octets,
// using a CRLF followed by a single space as the continuation, without
// splitting a multi-byte UTF-8 sequence across a fold.
func foldLines(data []byte) []byte {
	const limit = 75
	var out bytes.Buffer
	out.Grow(len(data) + len(data)/64 + 16)

	// Process each line (go-ical emits one property per CRLF-terminated line).
	for _, line := range bytes.Split(data, []byte("\r\n")) {
		if len(line) <= limit {
			out.Write(line)
			out.WriteString("\r\n")
			continue
		}
		col := 0
		for i := 0; i < len(line); {
			_, sz := utf8.DecodeRune(line[i:]) // sz >= 1 even for invalid bytes
			if col+sz > limit {
				out.WriteString("\r\n ")
				col = 1 // the leading space counts toward the limit
			}
			out.Write(line[i : i+sz])
			col += sz
			i += sz
		}
		out.WriteString("\r\n")
	}
	// bytes.Split on the trailing CRLF yields a final empty element, which added
	// one extra CRLF above; trim it back to a single trailing CRLF.
	b := out.Bytes()
	for bytes.HasSuffix(b, []byte("\r\n\r\n")) {
		b = b[:len(b)-2]
	}
	return b
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
