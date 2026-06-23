package main

import (
	"fmt"
	"strconv"

	"github.com/furryfoxcorp/calshare/internal/storage"
	"github.com/spf13/cobra"
)

func withDB(cmd *cobra.Command, fn func(db *storage.DB) error) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return fn(db)
}

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "admin", Short: "Manage administrators"}
	cmd.AddCommand(&cobra.Command{
		Use:   "grant <email>",
		Short: "Grant admin to a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withDB(cmd, func(db *storage.DB) error {
				if err := db.SetAdmin(cmd.Context(), args[0], true); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s is now an admin\n", args[0])
				return nil
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "revoke <email>",
		Short: "Revoke admin from a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withDB(cmd, func(db *storage.DB) error {
				if err := db.SetAdmin(cmd.Context(), args[0], false); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s is no longer an admin\n", args[0])
				return nil
			})
		},
	})
	return cmd
}

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "token", Short: "Inspect and revoke share-link tokens"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all share tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withDB(cmd, func(db *storage.DB) error {
				tokens, err := db.ListAllShareTokens(cmd.Context())
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				for _, t := range tokens {
					state := "active"
					if t.RevokedAt != nil {
						state = "revoked"
					}
					fmt.Fprintf(out, "%d\tview=%d\t%s\t%s\tfetched=%d\n", t.ID, t.ViewID, t.Label, state, t.AccessCount)
				}
				return nil
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke a share token by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %w", err)
			}
			return withDB(cmd, func(db *storage.DB) error {
				if err := db.RevokeShareToken(cmd.Context(), id); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "token %d revoked\n", id)
				return nil
			})
		},
	})
	return cmd
}
