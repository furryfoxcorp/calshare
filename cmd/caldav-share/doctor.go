package main

import (
	"fmt"
	"time"

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

			if cfg.SMTPHost != "" {
				check("imip reply address", cfg.IMIPReplyAddress != "", "")
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
