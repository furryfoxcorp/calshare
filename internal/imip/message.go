// Package imip handles email delivery of scheduling messages (RFC 6047): it
// builds the RFC 5322 MIME envelope around an iTIP body, drains the outbound
// queue over SMTP, and polls IMAP for replies from external attendees.
package imip

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	goical "github.com/emersion/go-ical"
)

// Envelope holds the addressing and content for one iMIP message.
type Envelope struct {
	From      string
	To        string
	ReplyTo   string
	Subject   string
	MessageID string // including angle brackets
	InReplyTo string // including angle brackets, optional
	Method    string // REQUEST, REPLY, CANCEL
	ICal      []byte
	Date      time.Time
}

// Build renders the envelope into RFC 5322 bytes: a multipart/mixed message
// with a text summary, the iCalendar body as text/calendar, and the same
// content attached as invite.ics for maximum client compatibility.
func Build(e Envelope) []byte {
	boundary := "calshare-" + base64.RawURLEncoding.EncodeToString([]byte(e.MessageID))
	if len(boundary) > 60 {
		boundary = boundary[:60]
	}

	var b bytes.Buffer
	header := func(k, v string) {
		if v != "" {
			// Strip CR/LF so a value derived from event data (a SUMMARY can be
			// attacker-controlled and may carry newlines) cannot inject headers.
			fmt.Fprintf(&b, "%s: %s\r\n", k, headerSafe(v))
		}
	}
	header("From", e.From)
	header("To", e.To)
	header("Reply-To", e.ReplyTo)
	header("Subject", e.Subject)
	header("Message-ID", e.MessageID)
	header("In-Reply-To", e.InReplyTo)
	header("Date", e.Date.Format(time.RFC1123Z))
	header("MIME-Version", "1.0")
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", boundary)

	// Human-readable part.
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(textSummary(e))
	b.WriteString("\r\n")

	// Inline calendar part with the iTIP method.
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	fmt.Fprintf(&b, "Content-Type: text/calendar; method=%s; charset=utf-8\r\n", e.Method)
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.Write(e.ICal)
	b.WriteString("\r\n")

	// The same body as an attachment.
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: application/ics; name=\"invite.ics\"\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"invite.ics\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(wrap76(base64.StdEncoding.EncodeToString(e.ICal)))
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func textSummary(e Envelope) string {
	verb := "invited you to"
	switch strings.ToUpper(e.Method) {
	case "CANCEL":
		verb = "cancelled"
	case "REPLY":
		verb = "replied about"
	}
	summary, when := eventDetails(e.ICal)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s an event.\r\n\r\n", e.From, verb)
	if summary != "" {
		fmt.Fprintf(&b, "Event: %s\r\n", summary)
	}
	if when != "" {
		fmt.Fprintf(&b, "When: %s\r\n", when)
	}
	b.WriteString("\r\nOpen the attached invitation in your calendar app to respond.\r\n")
	return b.String()
}

// eventDetails pulls a SUMMARY and a human start time out of an iCalendar body
// for the text part and subject line.
func eventDetails(ics []byte) (summary, when string) {
	cal, err := goical.NewDecoder(bytes.NewReader(ics)).Decode()
	if err != nil {
		return "", ""
	}
	for _, c := range cal.Children {
		if c.Name != "VEVENT" {
			continue
		}
		if p := c.Props.Get("SUMMARY"); p != nil {
			summary = p.Value
		}
		if p := c.Props.Get("DTSTART"); p != nil {
			if t, err := c.Props.DateTime("DTSTART", time.UTC); err == nil {
				when = t.Format("Mon, 2 Jan 2006 15:04 MST")
			} else {
				when = p.Value
			}
		}
		break
	}
	return summary, when
}

// Subject builds the subject line for a method and event summary.
func Subject(method, summary string) string {
	if summary == "" {
		summary = "an event"
	}
	switch strings.ToUpper(method) {
	case "CANCEL":
		return "Cancelled: " + summary
	case "REPLY":
		return "Re: " + summary
	default:
		return "Invitation: " + summary
	}
}

// headerSafe removes CR and LF from a header value to prevent header injection.
func headerSafe(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

func wrap76(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76])
		b.WriteString("\r\n")
		s = s[76:]
	}
	b.WriteString(s)
	return b.String()
}
