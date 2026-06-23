package main

import (
	"fmt"

	"github.com/furryfoxcorp/calshare/internal/imip"
	"github.com/furryfoxcorp/calshare/internal/storage"
	"github.com/spf13/cobra"
)

func newIMIPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "imip",
		Short: "Inspect and run the iMIP email queue",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "drain",
		Short: "Send one pass of the outbound iMIP queue",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			db, err := storage.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer db.Close()

			smtpCfg := imip.SMTPConfig{
				Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Pass: cfg.SMTPPass,
				TLS: cfg.SMTPTLS, FromOverride: cfg.IMIPFromOverride, ReplyAddress: cfg.IMIPReplyAddress,
			}
			if !smtpCfg.Configured() {
				return fmt.Errorf("SMTP is not configured (set CALDAV_SMTP_HOST)")
			}
			n, err := imip.NewSender(db, smtpCfg, newLogger(cfg.LogLevel)).Drain(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sent %d message(s)\n", n)
			return nil
		},
	})
	return cmd
}
