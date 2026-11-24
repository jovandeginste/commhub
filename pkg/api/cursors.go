package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"

	"github.com/jovandeginste/commhub/pkg/model"
)

// maxCursorKeyLength keeps connector keys sane and index friendly.
const maxCursorKeyLength = 200

type cursorList struct {
	Cursors []model.Cursor `json:"cursors"`
}

type cursorBody struct {
	Value string `json:"value"`
}

func (s *Server) listCursors(c *echo.Context) error {
	var cursors []model.Cursor

	if err := s.reader.WithContext(c.Request().Context()).
		Order("key ASC").Find(&cursors).Error; err != nil {
		return err
	}

	if cursors == nil {
		cursors = []model.Cursor{}
	}

	return c.JSON(http.StatusOK, cursorList{Cursors: cursors})
}

func (s *Server) getCursor(c *echo.Context) error {
	var cursor model.Cursor

	if err := s.reader.WithContext(c.Request().Context()).
		Where("key = ?", cursorKey(c)).First(&cursor).Error; err != nil {
		return err
	}

	return c.JSON(http.StatusOK, cursor)
}

func (s *Server) putCursor(c *echo.Context) error {
	var body cursorBody

	if err := c.Bind(&body); err != nil {
		return badRequest("invalid JSON: %v", err)
	}

	key := cursorKey(c)
	if key == "" || len(key) > maxCursorKeyLength {
		return badRequest("cursor key must be 1-%d characters", maxCursorKeyLength)
	}

	db := s.db.WithContext(c.Request().Context())

	var existing model.Cursor

	err := db.Where("key = ?", key).First(&existing).Error

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		cursor := model.Cursor{Key: key, Value: body.Value}

		if err := db.Create(&cursor).Error; err != nil {
			return err
		}

		return c.JSON(http.StatusCreated, cursor)
	case err != nil:
		return err
	}

	if err := db.Model(&existing).Update("value", body.Value).Error; err != nil {
		return err
	}

	if err := db.Where("key = ?", key).First(&existing).Error; err != nil {
		return err
	}

	return c.JSON(http.StatusOK, existing)
}

func (s *Server) deleteCursor(c *echo.Context) error {
	result := s.db.WithContext(c.Request().Context()).
		Where("key = ?", cursorKey(c)).Delete(&model.Cursor{})
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	return c.NoContent(http.StatusNoContent)
}

func cursorKey(c *echo.Context) string {
	key, err := url.PathUnescape(strings.TrimSpace(c.Param("key")))
	if err != nil {
		return ""
	}

	return key
}
