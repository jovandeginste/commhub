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

	if err := store.Migrate(db); err != nil {
		return nil, err
	}

	if err := store.Seed(db); err != nil {
		return nil, err
	}

	return db, nil
}
