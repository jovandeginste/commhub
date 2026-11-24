// Package blobs stores attachment bytes on disk, addressed by their SHA-256
// checksum. The same file is never written twice, so several messages can share
// one blob.
package blobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrInvalidChecksum is returned when a checksum is not a lowercase hex SHA-256.
var ErrInvalidChecksum = errors.New("invalid sha256 checksum")

// ErrNotFound is returned when a blob does not exist.
var ErrNotFound = errors.New("attachment not found")

var checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Store keeps blobs below one directory.
type Store struct {
	dir string
}

// New returns a store rooted at dir. The directory is created on first write.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Dir returns the root directory of the store.
func (s *Store) Dir() string {
	return s.dir
}

// Path returns the file path of a blob. Blobs are spread over two levels of
// two hex characters, e.g. ab/cd/<sha256>.
func (s *Store) Path(checksum string) (string, error) {
	checksum = strings.ToLower(strings.TrimSpace(checksum))

	if !checksumPattern.MatchString(checksum) {
		return "", ErrInvalidChecksum
	}

	return filepath.Join(s.dir, checksum[0:2], checksum[2:4], checksum), nil
}

// Exists reports whether the blob is stored.
func (s *Store) Exists(checksum string) bool {
	path, err := s.Path(checksum)
	if err != nil {
		return false
	}

	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

// Put streams r into the store under checksum and returns the path. The data is
// verified against the checksum; an existing blob is reused as-is.
func (s *Store) Put(checksum string, r io.Reader) (string, error) {
	path, err := s.Path(checksum)
	if err != nil {
		return "", err
	}

	if s.Exists(checksum) {
		return path, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create blob directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return "", fmt.Errorf("create temp blob: %w", err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	hasher := sha256.New()

	if _, err := io.Copy(io.MultiWriter(tmp, hasher), r); err != nil {
		_ = tmp.Close()

		return "", fmt.Errorf("write blob: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp blob: %w", err)
	}

	if got := hex.EncodeToString(hasher.Sum(nil)); got != checksum {
		return "", fmt.Errorf("%w: body hashes to %s", ErrInvalidChecksum, got)
	}

	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", fmt.Errorf("chmod blob: %w", err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("store blob: %w", err)
	}

	return path, nil
}

// Open returns the content of a blob.
func (s *Store) Open(checksum string) (*os.File, error) {
	path, err := s.Path(checksum)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("open blob: %w", err)
	}

	return file, nil
}
