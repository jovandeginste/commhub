package main

import (
	"github.com/spf13/cobra"

	"github.com/jovandeginste/commhub/pkg/api"
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

			db, err := openDB(cfg, logger)
			if err != nil {
				return err
			}

			sqlDB, err := db.DB()
			if err != nil {
				return err
			}

			defer func() { _ = sqlDB.Close() }()

			ingester := ingest.New(db, logger)
			server := api.New(db, ingester, api.Config{
				Tokens:    cfg.Tokens(),
				BatchSize: cfg.BatchSize,
			}, logger)

			if listen == "" {
				listen = cfg.Listen
			}

			logger.Info("starting commhub",
				"address", listen,
				"database", cfg.Database,
				"auth", len(cfg.Tokens()) > 0)

			return server.Start(listen)
		},
	}

	cmd.Flags().StringVar(&listen, "listen", "", "listen address (overrides config)")

	return cmd
}
