package imip

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"

	goical "github.com/emersion/go-ical"

	"github.com/furryfoxcorp/calshare/internal/ical"
	"github.com/furryfoxcorp/calshare/internal/storage"
)

// Outcomes recorded for inbound messages.
const (
	OutcomeApplied   = "applied"
	OutcomeNoMatch   = "no_match"
	OutcomeMalformed = "malformed"
	OutcomeDuplicate = "duplicate"
)

// ApplyReply parses a raw RFC 5322 message, finds an iTIP REPLY, and updates
// the matching event's attendee state. It returns the outcome and the event
// UID (when found). It is the testable core of the IMAP receiver.
func ApplyReply(ctx context.Context, db *storage.DB, raw []byte) (outcome, uid string, err error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return OutcomeMalformed, "", err
	}
	calBytes, err := findCalendarPart(msg.Header.Get("Content-Type"), msg.Body)
	if err != nil || calBytes == nil {
		return OutcomeMalformed, "", err
	}

	cal, err := goical.NewDecoder(bytes.NewReader(calBytes)).Decode()
	if err != nil {
		return OutcomeMalformed, "", err
	}
	method := ""
	if p := cal.Props.Get("METHOD"); p != nil {
		method = strings.ToUpper(p.Value)
	}
	if method != "REPLY" {
		return OutcomeMalformed, "", errors.New("not a REPLY")
	}

	var event *goical.Component
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent {
			event = c
			break
		}
	}
	if event == nil {
		return OutcomeMalformed, "", errors.New("no VEVENT in reply")
	}
	if p := event.Props.Get("UID"); p != nil {
		uid = p.Value
	}
	if uid == "" {
		return OutcomeMalformed, "", errors.New("reply missing UID")
	}

	// The replying attendee carries the new PARTSTAT.
	var email, partstat string
	if p := event.Props.Get("ATTENDEE"); p != nil {
		email = mailtoAddr(p.Value)
		if p.Params != nil {
			partstat = p.Params.Get("PARTSTAT")
		}
	}
	if email == "" || partstat == "" {
		return OutcomeMalformed, uid, errors.New("reply missing attendee or partstat")
	}

	obj, err := db.ObjectByUIDAny(ctx, uid)
	if err != nil {
		return OutcomeNoMatch, uid, nil
	}
	if err := db.UpdateAttendeePartstat(ctx, obj.ID, email, partstat, "2.0"); err != nil {
		// No such attendee on the event is still a "no match" for our purposes.
		return OutcomeNoMatch, uid, nil
	}

	// Deposit the REPLY into the organizer's Inbox so their client sees it.
	if ownerID, oerr := db.CalendarOwner(ctx, obj.CalendarID); oerr == nil {
		_ = db.CreateInboxObject(ctx, &storage.InboxObject{
			UserID:      ownerID,
			Href:        sanitizeHref(uid) + "-reply.ics",
			ETag:        ical.ETag(calBytes),
			Blob:        calBytes,
			Method:      "REPLY",
			UID:         uid,
			OriginEmail: email,
		})
	}
	return OutcomeApplied, uid, nil
}

// findCalendarPart walks a MIME body (recursing through multipart containers)
// and returns the first text/calendar part, decoded per its transfer encoding.
func findCalendarPart(contentType string, body io.Reader) ([]byte, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			found, err := findCalendarPart(part.Header.Get("Content-Type"), decodePart(part))
			if err == nil && found != nil {
				return found, nil
			}
		}
		return nil, nil
	}
	if mediaType == "text/calendar" {
		return io.ReadAll(body)
	}
	return nil, nil
}

// decodePart wraps a MIME part reader to decode its Content-Transfer-Encoding.
func decodePart(part *multipart.Part) io.Reader {
	switch strings.ToLower(part.Header.Get("Content-Transfer-Encoding")) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, part)
	case "quoted-printable":
		return quotedprintable.NewReader(part)
	default:
		return part
	}
}

func mailtoAddr(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 7 && strings.EqualFold(v[:7], "mailto:") {
		v = v[7:]
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func sanitizeHref(uid string) string {
	var b strings.Builder
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "reply"
	}
	return b.String()
}
