package main

import (
	"github.com/spf13/cobra"

	"github.com/jovandeginste/commhub/pkg/api"
	"github.com/jovandeginste/commhub/pkg/blobs"
	"github.com/jovandeginste/commhub/pkg/config"
	"github.com/jovandeginste/commhub/pkg/ingest"
)

func newServeCmd(configPath *string) *cobra.Command {
	var listen string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the hub daemon",
		RunE: func(*cobra.Command, []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}

			logger := newLogger(cfg)

			db, reader, err := openHubDB(cfg, logger)
			if err != nil {
				return err
			}

			defer closeDatabase(db)

			if reader != db {
				defer closeDatabase(reader)
			}

			ingester := ingest.New(db, logger)

			attachments := blobStore(cfg)

			server := api.New(db, reader, ingester, api.Config{
				Tokens:    config.SplitTokens(cfg.APITokens),
				BatchSize: cfg.BatchSize,
				Blobs:     attachments,
			}, logger)

			if listen == "" {
				listen = cfg.Listen
			}

			logger.Info("starting commhub",
				"address", listen,
				"database", cfg.Database,
				"attachments", cfg.BlobDir(),
				"auth", len(cfg.Tokens()) > 0)

			return server.Start(listen)
		},
	}

	cmd.Flags().StringVar(&listen, "listen", "", "listen address (overrides config)")

	return cmd
}

// blobStore returns the attachment store, or nil when the configuration has no
// place to put one (e.g. an in-memory database).
func blobStore(cfg *config.Config) *blobs.Store {
	if dir := cfg.BlobDir(); dir != "" {
		return blobs.New(dir)
	}

	return nil
}
