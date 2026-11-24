// Package store opens and prepares the hub's SQLite database.
package store

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	slogGorm "github.com/orandin/slog-gorm"
	"gorm.io/gorm"

	"github.com/jovandeginste/commhub/pkg/model"
)

const (
	// readerConns is the size of the read-only pool.
	readerConns = 4
	// memoryDatabase is the special file name for an in-memory SQLite database.
	memoryDatabase = ":memory:"
)

// Open opens the SQLite database at path with a single serialized writer. Use
// OpenWithReader when reads should not queue behind writes.
func Open(path string, log *slog.Logger) (*gorm.DB, error) {
	writer, _, err := OpenWithReader(path, log)

	return writer, err
}

// OpenWithReader opens the database twice: one writer connection and a small
// read-only pool. SQLite serves concurrent readers next to one writer in WAL
// mode, so long ingest batches no longer block the query endpoints. An
// in-memory database returns the same handle twice. A missing parent directory
// is created with mode 0700, so the daemon can run from a systemd unit without a
// separate preparation step.
func OpenWithReader(path string, log *slog.Logger) (*gorm.DB, *gorm.DB, error) {
	if err := ensureParentDir(path); err != nil {
		return nil, nil, fmt.Errorf("prepare database directory: %w", err)
	}

	writer, err := open(path, log, 1, false)
	if err != nil {
		return nil, nil, err
	}

	if path == memoryDatabase {
		return writer, writer, nil
	}

	reader, err := open(path, log, readerConns, true)
	if err != nil {
		if sqlDB, dbErr := writer.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}

		return nil, nil, err
	}

	return writer, reader, nil
}

// open creates one connection pool. readOnly pools refuse writes with
// PRAGMA query_only, so a read handler can never take the write lock by
// accident.
func open(path string, log *slog.Logger, maxConns int, readOnly bool) (*gorm.DB, error) {
	gormLog := slogGorm.New(
		slogGorm.WithHandler(log.Handler()),
		slogGorm.SetLogLevel(slogGorm.DefaultLogType, slog.LevelDebug),
		slogGorm.WithSlowThreshold(200*time.Millisecond),
	)

	db, err := gorm.Open(sqlite.Open(dsn(path, readOnly)), &gorm.Config{
		Logger:         gormLog,
		NowFunc:        func() time.Time { return time.Now().UTC() },
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open database %q: %w", path, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get database handle: %w", err)
	}

	// SQLite has one writer. Serializing its connections removes SQLITE_BUSY
	// retries and keeps ingestion deterministic; batches amortize the cost.
	sqlDB.SetMaxOpenConns(maxConns)
	sqlDB.SetMaxIdleConns(maxConns)
	sqlDB.SetConnMaxLifetime(0)

	return db, nil
}

// Migrate creates or updates the schema.
func Migrate(db *gorm.DB) error {
	if err := model.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	return nil
}

// Seed creates the default spheres if they do not exist yet.
func Seed(db *gorm.DB) error {
	defaults := []model.Sphere{
		{Slug: "work", Name: "Work", Color: "#2563eb", SortOrder: 10},
		{Slug: "personal", Name: "Personal", Color: "#16a34a", SortOrder: 20},
		{Slug: "hockey", Name: "Hockey", Color: "#ea580c", SortOrder: 30},
		{Slug: "school", Name: "School", Color: "#7c3aed", SortOrder: 40},
	}

	for i := range defaults {
		if err := db.Where("slug = ?", defaults[i].Slug).
			FirstOrCreate(&defaults[i]).Error; err != nil {
			return fmt.Errorf("seed sphere %q: %w", defaults[i].Slug, err)
		}
	}

	return nil
}

func dsn(path string, readOnly bool) string {
	pragmas := "?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=temp_store(MEMORY)"

	if readOnly {
		pragmas += "&_pragma=query_only(1)"
	}

	if path == memoryDatabase {
		return "file::memory:" + pragmas
	}

	return "file:" + path + pragmas
}

// ensureParentDir creates the database directory when path is a plain file
// path. DSNs and in-memory databases are left alone.
func ensureParentDir(path string) error {
	if path == memoryDatabase || strings.HasPrefix(path, "file:") {
		return nil
	}

	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	return nil
}
