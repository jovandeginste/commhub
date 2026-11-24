package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/jovandeginste/commhub/pkg/config"
	"github.com/jovandeginste/commhub/pkg/ingest"
)

func newIngestCmd(configPath *string) *cobra.Command {
	var batchSize int

	cmd := &cobra.Command{
		Use:   "ingest <file|->",
		Short: "Ingest NDJSON messages from a file or stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIngest(cmd, args[0], *configPath, batchSize)
		},
	}

	cmd.Flags().IntVar(&batchSize, "batch-size", 0, "chunk size (default from config)")

	return cmd
}

func runIngest(cmd *cobra.Command, path, configPath string, batchSize int) error {
	cfg, err := config.Load(configPath)
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

	reader := io.Reader(os.Stdin)

	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}

		defer func() { _ = file.Close() }()

		reader = file
	}

	size := batchSize
	if size <= 0 {
		size = cfg.BatchSize
	}

	result, err := ingest.New(db, logger).
		IngestStream(context.Background(), reader, size)
	if result != nil {
		fmt.Fprintf(cmd.OutOrStdout(),
			"total=%d created=%d updated=%d unchanged=%d failed=%d\n",
			result.Total, result.Created, result.Updated,
			result.Unchanged, result.Failed)
	}

	if err != nil {
		return err
	}

	if result.Failed > 0 {
		return fmt.Errorf("%d messages failed", result.Failed)
	}

	return nil
}
