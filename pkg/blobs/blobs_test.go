package blobs_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jovandeginste/commhub/pkg/blobs"
)

func checksum(content string) string {
	sum := sha256.Sum256([]byte(content))

	return hex.EncodeToString(sum[:])
}

func TestPutAndOpen(t *testing.T) {
	store := blobs.New(t.TempDir())
	content := "hello attachment"
	sum := checksum(content)

	path, err := store.Put(sum, strings.NewReader(content))
	require.NoError(t, err)

	assert.FileExists(t, path)
	assert.Equal(t, filepath.Join(store.Dir(), sum[0:2], sum[2:4], sum), path)
	assert.True(t, store.Exists(sum))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	file, err := store.Open(sum)
	require.NoError(t, err)

	data, err := io.ReadAll(file)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestPutIsIdempotent(t *testing.T) {
	store := blobs.New(t.TempDir())
	content := "same bytes"
	sum := checksum(content)

	first, err := store.Put(sum, strings.NewReader(content))
	require.NoError(t, err)

	second, err := store.Put(sum, strings.NewReader(content))
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestPutRejectsWrongChecksum(t *testing.T) {
	store := blobs.New(t.TempDir())
	sum := checksum("expected")

	_, err := store.Put(sum, strings.NewReader("something else"))
	require.ErrorIs(t, err, blobs.ErrInvalidChecksum)
	assert.False(t, store.Exists(sum))
}

func TestInvalidChecksum(t *testing.T) {
	store := blobs.New(t.TempDir())

	_, err := store.Path("not-a-checksum")
	require.ErrorIs(t, err, blobs.ErrInvalidChecksum)

	_, err = store.Open(strings.Repeat("a", 64))
	require.ErrorIs(t, err, blobs.ErrNotFound)
}
