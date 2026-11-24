package store_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jovandeginste/commhub/pkg/model"
	"github.com/jovandeginste/commhub/pkg/store"
)

func TestOpenCreatesDatabaseDirectory(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	path := filepath.Join(root, "share", "commhub", "commhub.db")

	db, err := store.Open(path, logger)
	require.NoError(t, err)
	require.NoError(t, model.Migrate(db))

	info, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	info, err = os.Stat(path)
	require.NoError(t, err)
	assert.False(t, info.IsDir())

	// A second open on the same path must work (service restart).
	db2, err := store.Open(path, logger)
	require.NoError(t, err)
	require.NoError(t, model.Migrate(db2))
}

func TestOpenWithReader(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	path := filepath.Join(t.TempDir(), "commhub.db")

	writer, reader, err := store.OpenWithReader(path, logger)
	require.NoError(t, err)
	require.NotSame(t, writer, reader)

	require.NoError(t, model.Migrate(writer))

	// The reader sees what the writer committed.
	require.NoError(t, writer.Create(&model.Tag{Slug: "urgent", Name: "urgent"}).Error)

	var tag model.Tag
	require.NoError(t, reader.Where("slug = ?", "urgent").First(&tag).Error)
	assert.Equal(t, "urgent", tag.Name)

	// The read pool refuses writes, so a read handler can never take the write
	// lock by accident.
	err = reader.Create(&model.Tag{Slug: "nope", Name: "nope"}).Error
	require.Error(t, err)
}
