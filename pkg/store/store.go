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

// Open opens the SQLite database at path and configures it for concurrent reads
// and a single serialized writer. Use ":memory:" for tests. A missing parent
// directory is created with mode 0700, so the daemon can run from a systemd
// unit without a separate preparation step.
func Open(path string, log *slog.Logger) (*gorm.DB, error) {
	if err := ensureParentDir(path); err != nil {
		return nil, fmt.Errorf("prepare database directory: %w", err)
	}

	gormLog := slogGorm.New(
		slogGorm.WithHandler(log.Handler()),
		slogGorm.SetLogLevel(slogGorm.DefaultLogType, slog.LevelDebug),
		slogGorm.WithSlowThreshold(200*time.Millisecond),
	)

	db, err := gorm.Open(sqlite.Open(dsn(path)), &gorm.Config{
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

	// SQLite has one writer. Serializing connections removes SQLITE_BUSY
	// retries and keeps ingestion deterministic; batches amortize the cost.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
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

func dsn(path string) string {
	pragmas := "?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=temp_store(MEMORY)"

	if path == ":memory:" {
		return "file::memory:" + pragmas
	}

	return "file:" + path + pragmas
}

// ensureParentDir creates the database directory when path is a plain file
// path. DSNs and in-memory databases are left alone.
func ensureParentDir(path string) error {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
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
