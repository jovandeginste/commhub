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
