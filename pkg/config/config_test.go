package config_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jovandeginste/commhub/pkg/config"
)

func TestParseTokenSpec(t *testing.T) {
	tests := []struct {
		raw         string
		value       string
		read, write bool
	}{
		{"plain", "plain", true, true},
		{"read:abc", "abc", true, false},
		{"write:def", "def", false, true},
		{"rw:ghi", "ghi", true, true},
		{"all:jkl", "jkl", true, true},
		{"  write:with:colons ", "with:colons", false, true},
		{"weird:token", "weird:token", true, true},
	}

	for _, test := range tests {
		spec := config.ParseTokenSpec(test.raw)
		assert.Equal(t, test.value, spec.Value, test.raw)
		assert.Equal(t, test.read, spec.Read, test.raw)
		assert.Equal(t, test.write, spec.Write, test.raw)
	}
}

func TestSplitTokens(t *testing.T) {
	assert.Equal(t, []string{"read:a", "b", "write:c"},
		config.SplitTokens(" read:a , b ,, write:c "))
	assert.Empty(t, config.SplitTokens("  "))
}

func TestUnixSocketPath(t *testing.T) {
	path, ok := config.UnixSocketPath("unix:/run/user/1000/commhub.sock")
	assert.True(t, ok)
	assert.Equal(t, "/run/user/1000/commhub.sock", path)

	path, ok = config.UnixSocketPath("unix:///run/user/1000/commhub.sock")
	assert.True(t, ok)
	assert.Equal(t, "/run/user/1000/commhub.sock", path)

	_, ok = config.UnixSocketPath("127.0.0.1:8080")
	assert.False(t, ok)

	_, ok = config.UnixSocketPath("unix:")
	assert.False(t, ok)
}

func TestBlobDir(t *testing.T) {
	cfg := config.Config{Database: "data/commhub.db"}
	assert.Equal(t, filepath.Join("data", "attachments"), cfg.BlobDir())

	cfg = config.Config{Database: "data/commhub.db", AttachmentsDir: "~/blobs"}
	assert.Contains(t, cfg.BlobDir(), "blobs")

	cfg = config.Config{Database: ":memory:"}
	assert.Empty(t, cfg.BlobDir())
}

func TestAPIBaseURLWithUnixSocket(t *testing.T) {
	cfg := config.Config{Listen: "unix:/run/user/1000/commhub.sock"}
	assert.Equal(t, "unix:/run/user/1000/commhub.sock", cfg.APIBaseURL())

	cfg = config.Config{Listen: "0.0.0.0:8810"}
	assert.Equal(t, "http://127.0.0.1:8810", cfg.APIBaseURL())
}
