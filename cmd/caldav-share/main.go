// Command caldav-share is a self-hosted CalDAV server with sharing and
// scheduling. See the README for setup. This file wires the subcommands;
// the work lives under internal/.
package main

import (
	"fmt"
	"os"

	"github.com/slowestnetwork/caldav-share/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "caldav-share",
		Short:         "Self-hosted CalDAV server with sharing and scheduling",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	var configPath string
	root.PersistentFlags().StringVar(&configPath, "config", "", "path to TOML config file")

	root.AddCommand(
		newVersionCmd(),
		newServeCmd(),
		newMigrateCmd(),
		newDoctorCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version and exit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version.Version)
			return nil
		},
	}
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP server",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("serve is not wired up yet")
		},
	}
}

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending database migrations",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("migrate is not wired up yet")
		},
	}
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check configuration and dependencies",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("doctor is not wired up yet")
		},
	}
}
