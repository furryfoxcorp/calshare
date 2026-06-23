// Command caldav-share is a self-hosted CalDAV server with sharing and
// scheduling. See the README for setup. This file wires the subcommands;
// the work lives under internal/.
package main

import (
	"fmt"
	"os"

	"github.com/furryfoxcorp/calshare/internal/config"
	"github.com/furryfoxcorp/calshare/internal/version"
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
		newIMIPCmd(),
		newAdminCmd(),
		newTokenCmd(),
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

// loadConfig reads configuration using the --config flag, if set.
func loadConfig(cmd *cobra.Command) (*config.Config, error) {
	flags := map[string]string{}
	if v, _ := cmd.Flags().GetString("config"); v != "" {
		flags["config"] = v
	}
	return config.Load(flags)
}
