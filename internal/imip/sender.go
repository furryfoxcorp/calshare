package imip

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/smtp"
	"strconv"
	"time"

	"github.com/furryfoxcorp/calshare/internal/storage"
)

// SMTPConfig describes the outbound relay.
type SMTPConfig struct {
	Host         string
	Port         int
	User         string
	Pass         string
	TLS          string // starttls, implicit, none
	FromOverride string
	ReplyAddress string
}

// Configured reports whether an SMTP relay is set.
func (c SMTPConfig) Configured() bool { return c.Host != "" }

const maxAttempts = 5

// Sender drains the outbound iMIP queue over SMTP. The transport is injectable
// for testing.
type Sender struct {
	db     *storage.DB
	cfg    SMTPConfig
	logger *slog.Logger
	send   func(cfg SMTPConfig, from, to string, msg []byte) error
	now    func() time.Time
}

// NewSender builds a Sender using a real SMTP transport.
func NewSender(db *storage.DB, cfg SMTPConfig, logger *slog.Logger) *Sender {
	return &Sender{
		db:     db,
		cfg:    cfg,
		logger: logger,
		send:   smtpSend,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// Run drains the queue on a ticker until ctx is cancelled.
func (s *Sender) Run(ctx context.Context) {
	if !s.cfg.Configured() {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if _, err := s.Drain(ctx); err != nil {
			s.logger.Error("imip drain", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Drain sends every due queued message and returns how many were sent.
func (s *Sender) Drain(ctx context.Context) (int, error) {
	due, err := s.db.DueIMIP(ctx, s.now(), 20)
	if err != nil {
		return 0, err
	}
	sent := 0
	for i := range due {
		m := due[i]
		if err := s.sendOne(ctx, &m); err != nil {
			s.handleFailure(ctx, &m, err)
			continue
		}
		_ = s.db.MarkIMIPSent(ctx, m.ID)
		sent++
	}
	return sent, nil
}

func (s *Sender) sendOne(ctx context.Context, m *storage.IMIPMessage) error {
	organizer, err := s.db.UserByID(ctx, m.FromUserID)
	if err != nil {
		return err
	}
	from := organizer.Email
	if s.cfg.FromOverride != "" {
		from = s.cfg.FromOverride
	}
	summary, _ := eventDetails(m.Body)
	env := Envelope{
		From:      from,
		To:        m.ToAddress,
		ReplyTo:   s.cfg.ReplyAddress,
		Subject:   Subject(m.Method, summary),
		MessageID: m.MessageID,
		InReplyTo: m.InReplyTo,
		Method:    m.Method,
		ICal:      m.Body,
		Date:      s.now(),
	}
	return s.send(s.cfg, from, m.ToAddress, Build(env))
}

func (s *Sender) handleFailure(ctx context.Context, m *storage.IMIPMessage, cause error) {
	if m.AttemptCount+1 >= maxAttempts {
		_ = s.db.MarkIMIPFinal(ctx, m.ID, cause.Error())
		s.logger.Warn("imip permanently failed", "to", m.ToAddress, "err", cause)
		return
	}
	backoff := time.Duration(1<<m.AttemptCount) * time.Minute
	_ = s.db.MarkIMIPRetry(ctx, m.ID, s.now().Add(backoff), cause.Error())
}

// smtpSend delivers one message over SMTP, honoring the TLS mode.
func smtpSend(cfg SMTPConfig, from, to string, msg []byte) error {
	addr := cfg.Host + ":" + strconv.Itoa(cfg.Port)
	var auth smtp.Auth
	if cfg.User != "" {
		auth = smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	}

	if cfg.TLS == "implicit" {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.Host})
		if err != nil {
			return err
		}
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return err
		}
		defer client.Close()
		return deliver(client, auth, from, to, msg)
	}

	client, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer client.Close()
	if cfg.TLS != "none" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
				return err
			}
		}
	}
	return deliver(client, auth, from, to, msg)
}

func deliver(client *smtp.Client, auth smtp.Auth, from, to string, msg []byte) error {
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(auth); err != nil {
				return err
			}
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
