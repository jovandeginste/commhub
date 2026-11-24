package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "commhub:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:           "commhub",
		Short:         "Local-first communication hub",
		Long:          "CommHub stores messages from many sources in one local database.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&configPath, "config", "",
		"config file (default: ./commhub.yaml, ~/.config/commhub/commhub.yaml)")

	root.AddCommand(
		newServeCmd(&configPath),
		newMigrateCmd(&configPath),
		newIngestCmd(&configPath),
		newQueryCmd(&configPath),
		newStatsCmd(&configPath),
		newGetCmd(&configPath),
		newVersionCmd(),
	)

	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(*cobra.Command, []string) {
			fmt.Println("commhub", version)
		},
	}
}
