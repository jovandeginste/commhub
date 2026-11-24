// Package api exposes the hub over HTTP: ingestion on the write side and
// querying on the read side.
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"gorm.io/gorm"

	"github.com/jovandeginste/commhub/pkg/blobs"
	"github.com/jovandeginste/commhub/pkg/config"
	"github.com/jovandeginste/commhub/pkg/ingest"
)

// maxBodySize caps the size of one request, including attachment uploads.
const maxBodySize = 64 << 20

// Config holds the API settings that are not database related.
type Config struct {
	// Tokens enables bearer authentication when non-empty. Entries may carry a
	// scope prefix, see config.ParseTokenSpec.
	Tokens []string
	// BatchSize is the chunk size for NDJSON streams.
	BatchSize int
	// Blobs stores uploaded attachments. When nil the attachment endpoints are
	// disabled.
	Blobs *blobs.Store
}

// Server is the HTTP front end of the hub.
type Server struct {
	echo   *echo.Echo
	db     *gorm.DB
	reader *gorm.DB
	ingest *ingest.Service
	blobs  *blobs.Store
	tokens []config.TokenSpec
	config Config
	logger *slog.Logger
}

// New creates the API server. It does not start listening. db is the writer
// connection; reader is a read-only pool used by the query endpoints (db itself
// when nil).
func New(db, reader *gorm.DB, ingester *ingest.Service, cfg Config, logger *slog.Logger) *Server {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 500
	}

	if reader == nil {
		reader = db
	}

	if cfg.Blobs != nil && ingester != nil {
		ingester.SetBlobs(cfg.Blobs)
	}

	tokens := make([]config.TokenSpec, 0, len(cfg.Tokens))

	for _, raw := range cfg.Tokens {
		if spec := config.ParseTokenSpec(raw); spec.Value != "" {
			tokens = append(tokens, spec)
		}
	}

	server := &Server{
		echo:   echo.New(),
		db:     db,
		reader: reader,
		ingest: ingester,
		blobs:  cfg.Blobs,
		tokens: tokens,
		config: cfg,
		logger: logger,
	}

	server.routes()

	return server
}

// routes wires the middleware and every endpoint.
func (s *Server) routes() {
	s.echo.HTTPErrorHandler = s.handleError
	s.echo.Logger = s.logger
	s.echo.Use(middleware.Recover())
	s.echo.Use(middleware.RequestLogger())
	s.echo.Use(middleware.BodyLimit(maxBodySize))

	s.echo.GET("/healthz", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	api := s.echo.Group("/api/v1", s.authenticate)

	api.POST("/messages", s.ingestSingle)
	api.POST("/messages/batch", s.ingestBatch)
	api.POST("/messages/stream", s.ingestStream)
	api.GET("/messages", s.listMessages)
	api.GET("/messages/:id", s.getMessage)
	api.PATCH("/messages/:id", s.patchMessage)
	api.DELETE("/messages/:id", s.deleteMessage)

	api.PUT("/attachments/:sha256", s.putAttachment)
	api.GET("/attachments/:sha256", s.getAttachment)

	api.GET("/cursors", s.listCursors)
	api.GET("/cursors/:key", s.getCursor)
	api.PUT("/cursors/:key", s.putCursor)
	api.DELETE("/cursors/:key", s.deleteCursor)

	api.GET("/ingest/batches", s.listBatches)

	api.GET("/sources", s.listSources)
	api.PATCH("/accounts/:id", s.patchAccount)
	api.GET("/channels", s.listChannels)
	api.GET("/correspondents", s.listCorrespondents)
	api.POST("/correspondents", s.createCorrespondent)
	api.POST("/correspondents/:id/identities", s.addCorrespondentIdentity)
	api.GET("/spheres", s.listSpheres)
	api.POST("/spheres", s.createSphere)
	api.GET("/tags", s.listTags)
	api.GET("/stats", s.stats)
}

// Handler returns the underlying http.Handler.
func (s *Server) Handler() http.Handler { return s.echo }

// Start listens and serves until the process is interrupted. A unix listen
// address ("unix:/run/user/1000/commhub.sock") creates a socket that only the
// current user can reach.
func (s *Server) Start(address string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return s.StartContext(ctx, address)
}

// StartContext serves until ctx is cancelled.
func (s *Server) StartContext(ctx context.Context, address string) error {
	if path, ok := config.UnixSocketPath(address); ok {
		return s.startUnix(ctx, address, path)
	}

	return echo.StartConfig{ //nolint:exhaustruct_v5 // defaults are fine
		Address:    address,
		HideBanner: true,
	}.Start(ctx, s.echo)
}

func (s *Server) startUnix(ctx context.Context, address, path string) error {
	// A socket left behind by a previous run makes Listen fail.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket %q: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", path, err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()

		return fmt.Errorf("chmod socket %q: %w", path, err)
	}

	return echo.StartConfig{ //nolint:exhaustruct_v5 // defaults are fine
		Address:    address,
		Listener:   listener,
		HideBanner: true,
	}.Start(ctx, s.echo)
}

func (s *Server) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if len(s.tokens) == 0 {
			return next(c)
		}

		header := c.Request().Header.Get("Authorization")

		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok {
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		spec, ok := s.tokenSpec(strings.TrimSpace(token))
		if !ok {
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		if !tokenAllows(spec, c.Request().Method) {
			return echo.NewHTTPError(http.StatusForbidden,
				"token lacks the "+scopeName(c.Request().Method)+" scope")
		}

		return next(c)
	}
}

func (s *Server) tokenSpec(token string) (config.TokenSpec, bool) {
	var (
		found config.TokenSpec
		valid int
	)

	for _, candidate := range s.tokens {
		// Compare every token to keep timing independent of the match.
		match := subtle.ConstantTimeCompare([]byte(token), []byte(candidate.Value))
		valid |= match

		if match == 1 {
			found = candidate
		}
	}

	return found, valid == 1
}

// tokenAllows reports whether a token may use a method: reads need the read
// scope, everything else needs the write scope.
func tokenAllows(spec config.TokenSpec, method string) bool {
	if scopeName(method) == "read" {
		return spec.Read
	}

	return spec.Write
}

func scopeName(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return "read"
	default:
		return "write"
	}
}

func (s *Server) handleError(c *echo.Context, err error) {
	// The request logger already rendered the error and committed the
	// response; the error is bubbled up for upstream middlewares.
	if response, unwrapErr := echo.UnwrapResponse(c.Response()); unwrapErr == nil && response.Committed {
		return
	}

	var (
		validation *ingest.ValidationError
		httpError  *echo.HTTPError
	)

	switch {
	case errors.As(err, &validation):
		s.writeError(c, http.StatusBadRequest, validation.Error())
	case errors.Is(err, gorm.ErrRecordNotFound):
		s.writeError(c, http.StatusNotFound, "not found")
	case errors.As(err, &httpError):
		s.writeError(c, httpError.StatusCode(), httpError.Message)
	default:
		s.logger.Error("request failed",
			"method", c.Request().Method,
			"path", c.Request().URL.Path,
			"error", err)
		s.writeError(c, http.StatusInternalServerError, "internal server error")
	}
}

// writeError renders an error response. A failure to write can only mean the
// client went away, so it is logged and otherwise ignored.
func (s *Server) writeError(c *echo.Context, status int, message string) {
	if err := c.JSON(status, errorBody(message)); err != nil {
		s.logger.Debug("write error response", "error", err)
	}
}

func errorBody(message string) map[string]string {
	return map[string]string{"error": message}
}

func badRequest(format string, args ...any) error {
	return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf(format, args...))
}
