// Package api exposes the hub over HTTP: ingestion on the write side and
// querying on the read side.
package api

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"gorm.io/gorm"

	"github.com/jovandeginste/commhub/pkg/ingest"
)

// Config holds the API settings that are not database related.
type Config struct {
	// Tokens enables bearer authentication when non-empty.
	Tokens []string
	// BatchSize is the chunk size for NDJSON streams.
	BatchSize int
}

// Server is the HTTP front end of the hub.
type Server struct {
	echo   *echo.Echo
	db     *gorm.DB
	ingest *ingest.Service
	config Config
	logger *slog.Logger
}

// New creates the API server. It does not start listening.
func New(db *gorm.DB, ingester *ingest.Service, cfg Config, logger *slog.Logger) *Server {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 500
	}

	server := &Server{
		echo:   echo.New(),
		db:     db,
		ingest: ingester,
		config: cfg,
		logger: logger,
	}

	server.echo.HTTPErrorHandler = server.handleError
	server.echo.Logger = logger
	server.echo.Use(middleware.Recover())
	server.echo.Use(middleware.RequestLogger())
	server.echo.Use(middleware.BodyLimit(64 << 20)) // 64 MiB per request

	server.echo.GET("/healthz", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	api := server.echo.Group("/api/v1", server.authenticate)

	api.POST("/messages", server.ingestSingle)
	api.POST("/messages/batch", server.ingestBatch)
	api.POST("/messages/stream", server.ingestStream)
	api.GET("/messages", server.listMessages)
	api.GET("/messages/:id", server.getMessage)
	api.PATCH("/messages/:id", server.patchMessage)

	api.GET("/ingest/batches", server.listBatches)

	api.GET("/sources", server.listSources)
	api.PATCH("/accounts/:id", server.patchAccount)
	api.GET("/channels", server.listChannels)
	api.GET("/correspondents", server.listCorrespondents)
	api.POST("/correspondents", server.createCorrespondent)
	api.POST("/correspondents/:id/identities", server.addCorrespondentIdentity)
	api.GET("/spheres", server.listSpheres)
	api.POST("/spheres", server.createSphere)
	api.GET("/tags", server.listTags)
	api.GET("/stats", server.stats)

	return server
}

// Handler returns the underlying http.Handler.
func (s *Server) Handler() http.Handler { return s.echo }

// Start listens and serves until the process is interrupted.
func (s *Server) Start(address string) error {
	return s.echo.Start(address)
}

func (s *Server) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if len(s.config.Tokens) == 0 {
			return next(c)
		}

		header := c.Request().Header.Get("Authorization")

		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || !s.validToken(strings.TrimSpace(token)) {
			return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
		}

		return next(c)
	}
}

func (s *Server) validToken(token string) bool {
	valid := 0

	for _, candidate := range s.config.Tokens {
		// Compare every token to keep timing independent of the match.
		valid |= subtle.ConstantTimeCompare([]byte(token), []byte(candidate))
	}

	return valid == 1
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
