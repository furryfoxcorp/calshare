package main

import (
	"fmt"

	"github.com/furryfoxcorp/calshare/internal/storage"
	"github.com/spf13/cobra"
)

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending database migrations",
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
			n, err := db.Migrate(cmd.Context())
			if err != nil {
				return err
			}
			ver, _ := db.SchemaVersion(cmd.Context())
			fmt.Fprintf(cmd.OutOrStdout(), "applied %d migration(s); schema version %d\n", n, ver)
			return nil
		},
	}
}
