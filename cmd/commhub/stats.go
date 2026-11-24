package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jovandeginste/commhub/pkg/client"
	"github.com/jovandeginste/commhub/pkg/config"
)

func newStatsCmd(configPath *string) *cobra.Command {
	var (
		apiURL  string
		token   string
		jsonOut bool
	)

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show hub statistics through the REST API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}

			stats, err := client.New(resolveAPIURL(cfg, apiURL), resolveToken(cfg, token)).
				Stats(cmd.Context())
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			return printStats(cmd.OutOrStdout(), stats)
		},
	}

	cmd.Flags().StringVar(&apiURL, "url", "", "API base URL (default from config)")
	cmd.Flags().StringVar(&token, "token", "", "API bearer token (default from config)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the raw API response")

	return cmd
}

func printStats(w io.Writer, stats *client.Stats) error {
	counts := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	headers := []string{
		"MESSAGES", "UNREAD", "SOURCES", "ACCOUNTS",
		"CHANNELS", "CORRESPONDENTS", "IDENTITIES", "TAGS",
	}
	values := fmt.Sprintf("%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d",
		stats.Messages, stats.Unread, stats.Sources, stats.Accounts,
		stats.Channels, stats.Correspondents, stats.Identities, stats.Tags)

	if _, err := fmt.Fprintln(counts, strings.Join(headers, "\t")); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(counts, values); err != nil {
		return err
	}

	if err := counts.Flush(); err != nil {
		return err
	}

	if len(stats.BySource) > 0 {
		sources := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

		if _, err := fmt.Fprintln(sources, "SOURCE\tMESSAGES"); err != nil {
			return err
		}

		for _, source := range stats.BySource {
			if _, err := fmt.Fprintf(sources, "%s\t%d\n", source.Source, source.Messages); err != nil {
				return err
			}
		}

		if err := sources.Flush(); err != nil {
			return err
		}
	}

	fmt.Fprintf(w, "earliest: %s\nlatest:   %s\n",
		formatOptionalTime(stats.EarliestSentAt), formatOptionalTime(stats.LatestSentAt))

	return nil
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return "-"
	}

	return value.Local().Format(time.RFC3339)
}
