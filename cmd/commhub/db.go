package main

import (
	"log/slog"
	"os"

	"gorm.io/gorm"

	"github.com/jovandeginste/commhub/pkg/config"
	"github.com/jovandeginste/commhub/pkg/store"
)

func newLogger(cfg *config.Config) *slog.Logger {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.Level(),
	}))
	slog.SetDefault(logger)

	return logger
}

func openDB(cfg *config.Config, logger *slog.Logger) (*gorm.DB, error) {
	db, err := store.Open(cfg.Database, logger)
	if err != nil {
		return nil, err
	}

	if err := prepare(db); err != nil {
		return nil, err
	}

	return db, nil
}

// openHubDB opens a writer plus the read-only pool used by the query endpoints.
// Both handles are prepared before the API starts.
func openHubDB(cfg *config.Config, logger *slog.Logger) (*gorm.DB, *gorm.DB, error) {
	writer, reader, err := store.OpenWithReader(cfg.Database, logger)
	if err != nil {
		return nil, nil, err
	}

	if err := prepare(writer); err != nil {
		return nil, nil, err
	}

	return writer, reader, nil
}

func prepare(db *gorm.DB) error {
	if err := store.Migrate(db); err != nil {
		return err
	}

	return store.Seed(db)
}

// closeDatabase closes the connection pool behind a GORM handle.
func closeDatabase(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
