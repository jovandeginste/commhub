package api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"

	"github.com/jovandeginste/commhub/pkg/blobs"
)

// putAttachment stores an attachment blob. The body is verified against the
// checksum in the path, so connectors can retry without risk.
func (s *Server) putAttachment(c *echo.Context) error {
	if s.blobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "attachment storage is disabled")
	}

	checksum := strings.ToLower(strings.TrimSpace(c.Param("sha256")))

	path, err := s.blobs.Path(checksum)
	if err != nil {
		return badRequest("invalid sha256 checksum")
	}

	existed := s.blobs.Exists(checksum)

	if _, err := s.blobs.Put(checksum, c.Request().Body); err != nil {
		if errors.Is(err, blobs.ErrInvalidChecksum) {
			return badRequest("%v", err)
		}

		return err
	}

	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}

	return c.JSON(status, attachmentRef{
		SHA256:      checksum,
		StoragePath: path,
		Size:        blobSize(path),
	})
}

// getAttachment serves a stored blob.
func (s *Server) getAttachment(c *echo.Context) error {
	if s.blobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "attachment storage is disabled")
	}

	checksum := strings.ToLower(strings.TrimSpace(c.Param("sha256")))

	file, err := s.blobs.Open(checksum)
	if err != nil {
		if errors.Is(err, blobs.ErrNotFound) {
			return gorm.ErrRecordNotFound
		}

		return badRequest("%v", err)
	}

	defer func() { _ = file.Close() }()

	response := c.Response()

	response.Header().Set("ETag", `"`+checksum+`"`)
	response.Header().Set("Cache-Control", "private, max-age=31536000, immutable")

	return c.Stream(http.StatusOK, detectContentType(file), file)
}

type attachmentRef struct {
	SHA256      string `json:"sha256"`
	StoragePath string `json:"storage_path"`
	Size        int64  `json:"size"`
}

func blobSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}

	return info.Size()
}

// detectContentType sniffs the first bytes of a blob and rewinds the file, since
// blobs are stored without an extension.
func detectContentType(file *os.File) string {
	buffer := make([]byte, 512)

	read, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return "application/octet-stream"
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "application/octet-stream"
	}

	return http.DetectContentType(buffer[:read])
}
