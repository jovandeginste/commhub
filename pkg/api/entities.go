package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jovandeginste/commhub/pkg/ingest"
	"github.com/jovandeginste/commhub/pkg/model"
)

func (s *Server) listSources(c *echo.Context) error {
	var sources []model.Source

	if err := s.db.WithContext(c.Request().Context()).
		Preload("Accounts").
		Order("slug ASC").
		Find(&sources).Error; err != nil {
		return err
	}

	if sources == nil {
		sources = []model.Source{}
	}

	return c.JSON(http.StatusOK, map[string]any{"sources": sources})
}

func (s *Server) listChannels(c *echo.Context) error {
	q := s.db.WithContext(c.Request().Context()).Model(&model.Channel{})

	if source := strings.TrimSpace(c.QueryParam("source")); source != "" {
		q = q.Where("source_id IN (?)",
			s.db.Model(&model.Source{}).Select("id").Where("slug = ?", source))
	}

	if account := strings.TrimSpace(c.QueryParam("account")); account != "" {
		q = q.Where("account_id IN (?)",
			s.db.Model(&model.Account{}).Select("id").Where("slug = ?", account))
	}

	if sphere := strings.TrimSpace(c.QueryParam("sphere")); sphere != "" {
		q = q.Where("sphere_id IN (?)",
			s.db.Model(&model.Sphere{}).Select("id").Where("slug = ?", sphere))
	}

	limit, err := queryLimit(c.QueryParam("limit"), 500, 2000)
	if err != nil {
		return err
	}

	var channels []model.Channel

	if err := q.Preload("Sphere").Order("name ASC").Limit(limit).Find(&channels).Error; err != nil {
		return err
	}

	if channels == nil {
		channels = []model.Channel{}
	}

	return c.JSON(http.StatusOK, map[string]any{"channels": channels})
}

func (s *Server) listCorrespondents(c *echo.Context) error {
	q := s.db.WithContext(c.Request().Context()).Model(&model.Correspondent{})

	if search := strings.TrimSpace(c.QueryParam("q")); search != "" {
		q = q.Where("display_name LIKE ?", "%"+search+"%")
	}

	if sphere := strings.TrimSpace(c.QueryParam("sphere")); sphere != "" {
		q = q.Where("sphere_id IN (?)",
			s.db.Model(&model.Sphere{}).Select("id").Where("slug = ?", sphere))
	}

	limit, err := queryLimit(c.QueryParam("limit"), 500, 2000)
	if err != nil {
		return err
	}

	var correspondents []model.Correspondent

	if err := q.Preload("Identities").Preload("Sphere").
		Order("display_name ASC").Limit(limit).Find(&correspondents).Error; err != nil {
		return err
	}

	if correspondents == nil {
		correspondents = []model.Correspondent{}
	}

	return c.JSON(http.StatusOK, map[string]any{"correspondents": correspondents})
}

type correspondentIdentityRequest struct {
	Source      string `json:"source"`
	Kind        string `json:"kind"`
	Value       string `json:"value"`
	DisplayName string `json:"display_name"`
}

type createCorrespondentRequest struct {
	DisplayName string          `json:"display_name"`
	SortName    string          `json:"sort_name"`
	Sphere      string          `json:"sphere"`
	IsSelf      bool            `json:"is_self"`
	Notes       string          `json:"notes"`
	AvatarURL   string          `json:"avatar_url"`
	Metadata    json.RawMessage `json:"metadata"`

	Identities []correspondentIdentityRequest `json:"identities"`
}

func (s *Server) createCorrespondent(c *echo.Context) error {
	var request createCorrespondentRequest

	if err := c.Bind(&request); err != nil {
		return badRequest("invalid JSON: %v", err)
	}

	request.DisplayName = strings.TrimSpace(request.DisplayName)
	if request.DisplayName == "" {
		return badRequest("display_name: required")
	}

	sphereID, err := s.findSphereID(c.Request().Context(), request.Sphere)
	if err != nil {
		return err
	}

	correspondent := model.Correspondent{
		DisplayName: request.DisplayName,
		SortName:    request.SortName,
		SphereID:    sphereID,
		IsSelf:      request.IsSelf,
		Notes:       request.Notes,
		AvatarURL:   request.AvatarURL,
		Metadata:    model.JSON(request.Metadata),
	}

	err = s.db.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit(clause.Associations).Create(&correspondent).Error; err != nil {
			return err
		}

		for i := range request.Identities {
			if err := createCorrespondentIdentity(tx, correspondent.ID, request.Identities[i]); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	var created model.Correspondent

	if err := s.db.WithContext(c.Request().Context()).
		Preload("Identities").Preload("Sphere").
		Where("id = ?", correspondent.ID).First(&created).Error; err != nil {
		return err
	}

	return c.JSON(http.StatusCreated, created)
}

func createCorrespondentIdentity(tx *gorm.DB, correspondentID string, input correspondentIdentityRequest) error {
	sourceSlug := ingest.Slugify(input.Source)
	if sourceSlug == "" {
		return ingest.Invalid("identities.source", "required")
	}

	var source model.Source

	err := tx.Where("slug = ?", sourceSlug).First(&source).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ingest.Invalid("identities.source", "unknown source "+sourceSlug)
	}

	if err != nil {
		return err
	}

	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	if kind == "" {
		kind = ingest.DefaultIdentityKind(sourceSlug)
	}

	value := strings.TrimSpace(input.Value)
	if value == "" {
		return ingest.Invalid("identities.value", "required")
	}

	identity := model.Identity{
		CorrespondentID: correspondentID,
		SourceID:        source.ID,
		Kind:            kind,
		Value:           value,
		NormalizedValue: ingest.NormalizeIdentity(kind, value),
		DisplayName:     input.DisplayName,
	}

	if err := tx.Omit(clause.Associations).Create(&identity).Error; err != nil {
		return translateConflict(err, "identity already exists")
	}

	return nil
}

// addCorrespondentIdentity attaches another platform handle to an existing
// correspondent. This is how the hub merges one person across sources.
func (s *Server) addCorrespondentIdentity(c *echo.Context) error {
	var request correspondentIdentityRequest

	if err := c.Bind(&request); err != nil {
		return badRequest("invalid JSON: %v", err)
	}

	var correspondent model.Correspondent

	if err := s.db.WithContext(c.Request().Context()).
		Where("id = ?", c.Param("id")).First(&correspondent).Error; err != nil {
		return err
	}

	err := s.db.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		return createCorrespondentIdentity(tx, correspondent.ID, request)
	})
	if err != nil {
		return err
	}

	if err := s.db.WithContext(c.Request().Context()).
		Preload("Identities").Preload("Sphere").
		Where("id = ?", correspondent.ID).First(&correspondent).Error; err != nil {
		return err
	}

	return c.JSON(http.StatusCreated, correspondent)
}

func (s *Server) listSpheres(c *echo.Context) error {
	var spheres []model.Sphere

	if err := s.db.WithContext(c.Request().Context()).
		Order("sort_order ASC, name ASC").Find(&spheres).Error; err != nil {
		return err
	}

	if spheres == nil {
		spheres = []model.Sphere{}
	}

	return c.JSON(http.StatusOK, map[string]any{"spheres": spheres})
}

type createSphereRequest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
	SortOrder   int    `json:"sort_order"`
}

func (s *Server) createSphere(c *echo.Context) error {
	var request createSphereRequest

	if err := c.Bind(&request); err != nil {
		return badRequest("invalid JSON: %v", err)
	}

	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		return badRequest("name: required")
	}

	slug := ingest.Slugify(request.Slug)
	if slug == "" {
		slug = ingest.Slugify(request.Name)
	}

	sphere := model.Sphere{
		Slug:        slug,
		Name:        request.Name,
		Description: request.Description,
		Color:       request.Color,
		SortOrder:   request.SortOrder,
	}

	if err := s.db.WithContext(c.Request().Context()).
		Omit(clause.Associations).Create(&sphere).Error; err != nil {
		return translateConflict(err, "sphere already exists")
	}

	return c.JSON(http.StatusCreated, sphere)
}

func (s *Server) listTags(c *echo.Context) error {
	var tags []model.Tag

	if err := s.db.WithContext(c.Request().Context()).
		Order("slug ASC").Find(&tags).Error; err != nil {
		return err
	}

	if tags == nil {
		tags = []model.Tag{}
	}

	return c.JSON(http.StatusOK, map[string]any{"tags": tags})
}

type patchAccountRequest struct {
	Name             *string         `json:"name"`
	ExternalUser     *string         `json:"external_user"`
	ExternalUserKind *string         `json:"external_user_kind"`
	SecretRef        *string         `json:"secret_ref"`
	Enabled          *bool           `json:"enabled"`
	Config           json.RawMessage `json:"config"`
}

func (s *Server) patchAccount(c *echo.Context) error {
	var request patchAccountRequest

	if err := c.Bind(&request); err != nil {
		return badRequest("invalid JSON: %v", err)
	}

	updates := map[string]any{}

	if request.Name != nil {
		updates["name"] = strings.TrimSpace(*request.Name)
	}

	if request.ExternalUser != nil {
		updates["external_user"] = strings.TrimSpace(*request.ExternalUser)
	}

	if request.ExternalUserKind != nil {
		kind := strings.ToLower(strings.TrimSpace(*request.ExternalUserKind))
		if kind == "" {
			return badRequest("external_user_kind: must not be empty")
		}

		updates["external_user_kind"] = kind
	}

	if request.SecretRef != nil {
		updates["secret_ref"] = strings.TrimSpace(*request.SecretRef)
	}

	if request.Enabled != nil {
		updates["enabled"] = *request.Enabled
	}

	if request.Config != nil {
		updates["config"] = model.JSON(request.Config)
	}

	if len(updates) == 0 {
		return badRequest("nothing to update")
	}

	result := s.db.WithContext(c.Request().Context()).Model(&model.Account{}).
		Where("id = ?", c.Param("id")).Updates(updates)
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	var account model.Account

	if err := s.db.WithContext(c.Request().Context()).
		Where("id = ?", c.Param("id")).First(&account).Error; err != nil {
		return err
	}

	return c.JSON(http.StatusOK, account)
}

type sourceStats struct {
	Source   string `json:"source"`
	Messages int64  `json:"messages"`
}

type statsResponse struct {
	Messages       int64         `json:"messages"`
	Unread         int64         `json:"unread"`
	Sources        int64         `json:"sources"`
	Accounts       int64         `json:"accounts"`
	Channels       int64         `json:"channels"`
	Correspondents int64         `json:"correspondents"`
	Identities     int64         `json:"identities"`
	Tags           int64         `json:"tags"`
	BySource       []sourceStats `json:"by_source"`
	EarliestSentAt *time.Time    `json:"earliest_sent_at,omitempty"`
	LatestSentAt   *time.Time    `json:"latest_sent_at,omitempty"`
}

func (s *Server) stats(c *echo.Context) error {
	ctx := c.Request().Context()
	response := statsResponse{BySource: []sourceStats{}}

	if err := s.statCounts(ctx, &response); err != nil {
		return err
	}

	if err := s.db.WithContext(ctx).Table("messages").
		Select("sources.slug AS source, COUNT(*) AS messages").
		Joins("JOIN sources ON sources.id = messages.source_id").
		Group("sources.slug").
		Order("messages DESC").
		Scan(&response.BySource).Error; err != nil {
		return err
	}

	earliest, latest, err := s.messageTimeSpan(ctx)
	if err != nil {
		return err
	}

	response.EarliestSentAt = earliest
	response.LatestSentAt = latest

	return c.JSON(http.StatusOK, response)
}

func (s *Server) statCounts(ctx context.Context, response *statsResponse) error {
	models := []struct {
		model any
		dest  *int64
	}{
		{&model.Message{}, &response.Messages},
		{&model.Source{}, &response.Sources},
		{&model.Account{}, &response.Accounts},
		{&model.Channel{}, &response.Channels},
		{&model.Correspondent{}, &response.Correspondents},
		{&model.Identity{}, &response.Identities},
		{&model.Tag{}, &response.Tags},
	}

	for _, entry := range models {
		if err := s.db.WithContext(ctx).Model(entry.model).Count(entry.dest).Error; err != nil {
			return err
		}
	}

	return s.db.WithContext(ctx).Model(&model.Message{}).
		Where("is_read = ?", false).Count(&response.Unread).Error
}

func (s *Server) messageTimeSpan(ctx context.Context) (*time.Time, *time.Time, error) {
	var earliest model.Message

	if err := s.db.WithContext(ctx).Select("sent_at").
		Order("sent_at ASC").Limit(1).Find(&earliest).Error; err != nil {
		return nil, nil, err
	}

	var latest model.Message

	if err := s.db.WithContext(ctx).Select("sent_at").
		Order("sent_at DESC").Limit(1).Find(&latest).Error; err != nil {
		return nil, nil, err
	}

	var earliestAt, latestAt *time.Time

	if !earliest.SentAt.IsZero() {
		sentAt := earliest.SentAt
		earliestAt = &sentAt
	}

	if !latest.SentAt.IsZero() {
		sentAt := latest.SentAt
		latestAt = &sentAt
	}

	return earliestAt, latestAt, nil
}

// translateConflict maps a unique constraint violation to HTTP 409.
func translateConflict(err error, message string) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return echo.NewHTTPError(http.StatusConflict, message)
	}

	return err
}
