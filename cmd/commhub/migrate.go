package main

import (
	"github.com/spf13/cobra"

	"github.com/jovandeginste/commhub/pkg/config"
)

func newMigrateCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Create or update the database schema",
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

			logger.Info("database is up to date", "database", cfg.Database)

			return nil
		},
	}
}
