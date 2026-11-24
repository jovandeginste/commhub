package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"

	"github.com/jovandeginste/commhub/pkg/ingest"
	"github.com/jovandeginste/commhub/pkg/model"
)

type messageList struct {
	Messages   []model.Message `json:"messages"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func (s *Server) ingestSingle(c *echo.Context) error {
	var in ingest.MessageInput

	if err := c.Bind(&in); err != nil {
		return badRequest("invalid JSON: %v", err)
	}

	result, err := s.ingest.Ingest(c.Request().Context(), &in)
	if err != nil {
		return err
	}

	status := http.StatusOK
	if result.Status == model.ResultCreated {
		status = http.StatusCreated
	}

	return c.JSON(status, result)
}

func (s *Server) ingestBatch(c *echo.Context) error {
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return badRequest("read body: %v", err)
	}

	messages, err := decodeBatch(body)
	if err != nil {
		return badRequest("%v", err)
	}

	result, err := s.ingest.IngestBatch(c.Request().Context(), messages, model.IngestModeBatch)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, result)
}

func (s *Server) ingestStream(c *echo.Context) error {
	result, err := s.ingest.IngestStream(c.Request().Context(), c.Request().Body, s.config.BatchSize)
	if err != nil {
		return badRequest("ingest stream: %v", err)
	}

	return c.JSON(http.StatusOK, result)
}

// decodeBatch accepts either a bare array of messages or {"messages": [...]}.
func decodeBatch(body []byte) ([]ingest.MessageInput, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, errors.New("empty request body")
	}

	if trimmed[0] == '[' {
		var messages []ingest.MessageInput
		if err := json.Unmarshal(body, &messages); err != nil {
			return nil, fmt.Errorf("invalid JSON array: %w", err)
		}

		return messages, nil
	}

	var wrapper struct {
		Messages []ingest.MessageInput `json:"messages"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("invalid JSON object: %w", err)
	}

	if wrapper.Messages == nil {
		return nil, errors.New(`missing "messages" array`)
	}

	return wrapper.Messages, nil
}

func (s *Server) listMessages(c *echo.Context) error {
	q, messageOrder, err := s.filteredMessages(c)
	if err != nil {
		return err
	}

	limit, err := queryLimit(c.QueryParam("limit"), 50, 500)
	if err != nil {
		return err
	}

	var messages []model.Message

	if err := q.
		Omit("raw").
		Preload("Source").
		Preload("Account").
		Preload("Channel").
		Preload("Sender").
		Preload("Sphere").
		Preload("Tags").
		Order(messageOrder.orderClause()).
		Limit(limit + 1).
		Find(&messages).Error; err != nil {
		return err
	}

	nextCursor := ""

	if len(messages) > limit {
		messages = messages[:limit]
		last := messages[len(messages)-1]

		nextCursor, err = encodeCursor(messageOrder, last)
		if err != nil {
			return err
		}
	}

	if messages == nil {
		messages = []model.Message{}
	}

	return c.JSON(http.StatusOK, messageList{Messages: messages, NextCursor: nextCursor})
}

func queryLimit(raw string, fallback, maximum int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 0, badRequest("invalid limit")
	}

	return min(parsed, maximum), nil
}

func (s *Server) getMessage(c *echo.Context) error {
	var message model.Message

	if err := s.messageQuery(c).
		Where("id = ?", c.Param("id")).
		First(&message).Error; err != nil {
		return err
	}

	return c.JSON(http.StatusOK, message)
}

type patchMessageRequest struct {
	IsRead     *bool     `json:"is_read"`
	IsStarred  *bool     `json:"is_starred"`
	IsArchived *bool     `json:"is_archived"`
	Sphere     *string   `json:"sphere"`
	Tags       *[]string `json:"tags"`
}

func (s *Server) patchMessage(c *echo.Context) error {
	var request patchMessageRequest

	if err := c.Bind(&request); err != nil {
		return badRequest("invalid JSON: %v", err)
	}

	var message model.Message

	if err := s.db.WithContext(c.Request().Context()).
		Where("id = ?", c.Param("id")).First(&message).Error; err != nil {
		return err
	}

	updates := map[string]any{}

	if request.IsRead != nil {
		updates["is_read"] = *request.IsRead
	}

	if request.IsStarred != nil {
		updates["is_starred"] = *request.IsStarred
	}

	if request.IsArchived != nil {
		updates["is_archived"] = *request.IsArchived
	}

	if request.Sphere != nil {
		sphereID, err := s.sphereID(c, *request.Sphere)
		if err != nil {
			return err
		}

		if sphereID == nil {
			updates["sphere_id"] = nil
		} else {
			updates["sphere_id"] = *sphereID
		}
	}

	if len(updates) > 0 {
		if err := s.db.WithContext(c.Request().Context()).Model(&model.Message{}).
			Where("id = ?", message.ID).Updates(updates).Error; err != nil {
			return err
		}
	}

	if request.Tags != nil {
		if err := s.ingest.SetTags(c.Request().Context(), message.ID, *request.Tags); err != nil {
			return err
		}
	}

	if err := s.messageQuery(c).
		Where("id = ?", message.ID).
		First(&message).Error; err != nil {
		return err
	}

	return c.JSON(http.StatusOK, message)
}

// sphereID resolves a sphere slug. An empty slug means "clear the sphere" and
// returns (nil, nil).
func (s *Server) sphereID(c *echo.Context, slug string) (*string, error) {
	return s.findSphereID(c.Request().Context(), slug)
}

func (s *Server) findSphereID(ctx context.Context, slug string) (*string, error) {
	slug = ingest.Slugify(slug)
	if slug == "" {
		//nolint:nilnil // an empty slug explicitly clears the sphere.
		return nil, nil
	}

	var sphere model.Sphere

	err := s.db.WithContext(ctx).
		Where("slug = ?", slug).First(&sphere).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, badRequest("unknown sphere %q", slug)
	}

	if err != nil {
		return nil, err
	}

	return &sphere.ID, nil
}

func (s *Server) messageQuery(c *echo.Context) *gorm.DB {
	return s.db.WithContext(c.Request().Context()).
		Preload("Source").
		Preload("Account").
		Preload("Channel").
		Preload("Sender.Correspondent").
		Preload("Sphere").
		Preload("Recipients.Identity.Correspondent").
		Preload("Attachments").
		Preload("Tags")
}

func (s *Server) listBatches(c *echo.Context) error {
	limit, err := queryLimit(c.QueryParam("limit"), 20, 200)
	if err != nil {
		return err
	}

	var batches []model.IngestBatch

	if err := s.db.WithContext(c.Request().Context()).
		Preload("Errors").
		Order("started_at DESC").
		Limit(limit).
		Find(&batches).Error; err != nil {
		return err
	}

	if batches == nil {
		batches = []model.IngestBatch{}
	}

	return c.JSON(http.StatusOK, map[string]any{"batches": batches})
}
