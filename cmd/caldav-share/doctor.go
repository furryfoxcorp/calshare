package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/smtp"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/furryfoxcorp/calshare/internal/config"
	"github.com/furryfoxcorp/calshare/internal/storage"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check configuration and dependencies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}

			ok := true
			check := func(name string, pass bool, detail string) {
				status := "ok"
				if !pass {
					status = "FAIL"
					ok = false
				}
				fmt.Fprintf(out, "[%s] %s%s\n", status, name, detail)
			}

			check("external url set", cfg.ExternalURL != "", note(cfg.ExternalURL))
			check("oidc configured", cfg.OIDCIssuer != "" && cfg.OIDCClientID != "", "")

			db, dbErr := storage.Open(cfg.DBPath)
			check("database open", dbErr == nil, note(cfg.DBPath))
			if dbErr == nil {
				defer db.Close()
				ver, verErr := db.SchemaVersion(cmd.Context())
				check("schema readable", verErr == nil, fmt.Sprintf(" (version %d)", ver))
			}

			_, tzErr := time.LoadLocation("America/New_York")
			check("bundled tzdata", tzErr == nil, "")

			if cfg.OIDCIssuer != "" {
				err := checkHTTPS(cfg.OIDCIssuer + "/.well-known/openid-configuration")
				check("oidc discovery reachable", err == nil, errNote(err))
			}

			if cfg.SMTPHost != "" {
				check("imip reply address", cfg.IMIPReplyAddress != "", "")
				err := checkSMTP(cfg)
				check("smtp reachable", err == nil, errNote(err))
			}

			if cfg.IMAPHost != "" {
				err := checkIMAP(cfg)
				check("imap login", err == nil, errNote(err))
			}

			if !ok {
				return fmt.Errorf("one or more checks failed")
			}
			return nil
		},
	}
}

func note(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

func errNote(err error) string {
	if err == nil {
		return ""
	}
	return " (" + err.Error() + ")"
}

// checkHTTPS verifies an HTTPS endpoint responds, confirming outbound
// reachability and that the OIDC issuer is up.
func checkHTTPS(url string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %s", resp.Status)
	}
	return nil
}

// checkSMTP opens a connection to the relay and greets it.
func checkSMTP(cfg *config.Config) error {
	addr := cfg.SMTPHost + ":" + strconv.Itoa(cfg.SMTPPort)
	var client *smtp.Client
	var err error
	if cfg.SMTPTLS == "implicit" {
		conn, derr := tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.SMTPHost})
		if derr != nil {
			return derr
		}
		client, err = smtp.NewClient(conn, cfg.SMTPHost)
	} else {
		client, err = smtp.Dial(addr)
	}
	if err != nil {
		return err
	}
	defer client.Close()
	if cfg.SMTPTLS == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: cfg.SMTPHost}); err != nil {
				return err
			}
		}
	}
	if cfg.SMTPUser != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPHost)); err != nil {
			return err
		}
	}
	return client.Quit()
}

// checkIMAP logs into the mailbox and out again.
func checkIMAP(cfg *config.Config) error {
	addr := cfg.IMAPHost + ":" + strconv.Itoa(cfg.IMAPPort)
	var client *imapclient.Client
	var err error
	switch cfg.IMAPTLS {
	case "none":
		client, err = imapclient.DialInsecure(addr, nil)
	case "starttls":
		client, err = imapclient.DialStartTLS(addr, nil)
	default:
		client, err = imapclient.DialTLS(addr, nil)
	}
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Login(cfg.IMAPUser, cfg.IMAPPass).Wait(); err != nil {
		return err
	}
	return client.Logout().Wait()
}
