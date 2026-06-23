package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/furryfoxcorp/calshare/internal/audit"
	"github.com/furryfoxcorp/calshare/internal/caldav"
	"github.com/furryfoxcorp/calshare/internal/imip"
	"github.com/furryfoxcorp/calshare/internal/oidc"
	"github.com/furryfoxcorp/calshare/internal/scheduling"
	"github.com/furryfoxcorp/calshare/internal/share"
	"github.com/furryfoxcorp/calshare/internal/sources"
	"github.com/furryfoxcorp/calshare/internal/storage"
	"github.com/furryfoxcorp/calshare/internal/web"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd)
		},
	}
	cmd.Flags().String("listen-addr", "", "address to listen on (overrides config)")
	return cmd
}

func runServe(cmd *cobra.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	if v, _ := cmd.Flags().GetString("listen-addr"); v != "" {
		cfg.ListenAddr = v
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel)

	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if cfg.AutoMigrate {
		n, err := db.Migrate(cmd.Context())
		if err != nil {
			return err
		}
		if n > 0 {
			logger.Info("applied migrations", "count", n)
		}
	}

	sessionKey, err := db.SessionKey(cmd.Context(), deriveKey(cfg.SessionKey))
	if err != nil {
		return err
	}
	secure := strings.HasPrefix(cfg.ExternalURL, "https://")
	sessions := oidc.NewManager(db, sessionKey, secure)

	var auth *oidc.Authenticator
	a, authErr := oidc.New(cmd.Context(), db, sessions, oidc.Config{
		Issuer:       cfg.OIDCIssuer,
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		RedirectURL:  cfg.OIDCRedirectURL,
		Scopes:       strings.Fields(cfg.OIDCScopes),
		AdminEmails:  cfg.AdminEmailSet(),
	})
	if authErr != nil {
		logger.Warn("OIDC provider unavailable; web sign-in disabled until reachable", "err", authErr)
	} else {
		auth = a
	}

	var sched *scheduling.Scheduler
	if cfg.SchedulingEnabled {
		sched = scheduling.New(db)
	}

	mux := http.NewServeMux()
	caldav.NewServer(db, "/dav", cfg.TrustProxyHeaders, sched).Register(mux)
	share.NewServer(db).Register(mux)
	web.NewServer(db, sessions, auth, audit.New(db, logger), cfg.ExternalURL).Register(mux)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           requestLogger(logger, mux),
		ReadHeaderTimeout: 30 * time.Second,
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Poll external ICS feeds in the background.
	go sources.New(db, cfg.ICSDefaultPollInterval, logger).Run(ctx)

	// Send queued external invitations over SMTP, and poll for replies.
	smtpCfg := imip.SMTPConfig{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Pass: cfg.SMTPPass,
		TLS: cfg.SMTPTLS, FromOverride: cfg.IMIPFromOverride, ReplyAddress: cfg.IMIPReplyAddress,
	}
	if smtpCfg.Configured() {
		go imip.NewSender(db, smtpCfg, logger).Run(ctx)
	} else {
		logger.Info("SMTP not configured; external invitations will be queued but not sent")
	}
	if cfg.IMAPHost != "" {
		imapCfg := imip.IMAPConfig{
			Host: cfg.IMAPHost, Port: cfg.IMAPPort, User: cfg.IMAPUser, Pass: cfg.IMAPPass,
			TLS: cfg.IMAPTLS, Folder: cfg.IMAPFolder, PollInterval: cfg.IMAPPollInterval,
			ProcessedFolder: cfg.IMAPProcessedDir,
		}
		go imip.NewReceiver(db, imapCfg, logger).Run(ctx)
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr, "external_url", cfg.ExternalURL)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// deriveKey turns a configured key string into a stable 32-byte key, or nil
// when unset so storage generates and persists one.
func deriveKey(s string) []byte {
	if s == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

// requestLogger logs one line per HTTP request.
func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		logger.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"dur_ms", time.Since(start).Milliseconds(),
			"remote_ip", r.RemoteAddr,
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
