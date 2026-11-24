package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jovandeginste/commhub/pkg/ingest"
	"github.com/jovandeginste/commhub/pkg/model"
)

const (
	sortSentAt     = "sent_at"
	sortIngestedAt = "ingested_at"
	sortCreatedAt  = "created_at"
	sortImportance = "importance"

	orderAsc  = "asc"
	orderDesc = "desc"
)

// messageSort is the validated sort order for a message list query.
type messageSort struct {
	Field string
	Order string
}

func (s messageSort) column() string {
	column, _ := sortColumn(s.Field)

	return column
}

func (s messageSort) orderClause() string {
	direction := "DESC"
	if s.Order == orderAsc {
		direction = "ASC"
	}

	return s.column() + " " + direction + ", id " + direction
}

func (s messageSort) value(message model.Message) string {
	switch s.Field {
	case sortIngestedAt:
		return message.IngestedAt.UTC().Format(time.RFC3339Nano)
	case sortCreatedAt:
		return message.CreatedAt.UTC().Format(time.RFC3339Nano)
	case sortImportance:
		return strconv.Itoa(message.Importance)
	default:
		return message.SentAt.UTC().Format(time.RFC3339Nano)
	}
}

func sortColumn(field string) (string, bool) {
	switch field {
	case "sent_at":
		return "sent_at", true
	case sortIngestedAt:
		return "ingested_at", true
	case sortCreatedAt:
		return "created_at", true
	case sortImportance:
		return "importance", true
	default:
		return "", false
	}
}

func parseMessageSort(query url.Values) (messageSort, error) {
	field := strings.ToLower(textParam(query, "sort"))
	if field == "" {
		field = sortSentAt
	}

	if _, ok := sortColumn(field); !ok {
		return messageSort{}, badRequest("invalid sort %q (use sent_at, ingested_at, created_at or importance)", field)
	}

	order := strings.ToLower(textParam(query, "order"))
	if order == "" {
		order = orderDesc
	}

	if order != orderAsc && order != orderDesc {
		return messageSort{}, badRequest("invalid order %q (use asc or desc)", order)
	}

	return messageSort{Field: field, Order: order}, nil
}

// filteredMessages applies every supported query parameter. Natural keys are
// resolved with subqueries so callers never need internal ids for the common
// filters.
func (s *Server) filteredMessages(c *echo.Context) (*gorm.DB, messageSort, error) {
	query := c.QueryParams()

	messageOrder, err := parseMessageSort(query)
	if err != nil {
		return nil, messageSort{}, err
	}

	q := s.reader.WithContext(c.Request().Context()).Model(&model.Message{})

	q, err = s.applyEntityFilters(query, q)
	if err != nil {
		return nil, messageSort{}, err
	}

	q, err = applyMessageFilters(query, q)
	if err != nil {
		return nil, messageSort{}, err
	}

	q, err = applyTimeFilters(query, q)
	if err != nil {
		return nil, messageSort{}, err
	}

	q, err = applyStateFilters(query, q)
	if err != nil {
		return nil, messageSort{}, err
	}

	q, err = applyCursor(query, q, messageOrder)
	if err != nil {
		return nil, messageSort{}, err
	}

	return q, messageOrder, nil
}

func (s *Server) applyEntityFilters(query url.Values, q *gorm.DB) (*gorm.DB, error) {
	q = s.applySourceAccountFilters(query, q)
	q = s.applyChannelFilters(query, q)
	q = s.applyIdentityFilters(query, q)
	q = s.applySphereFilters(query, q)

	return applyTagFilters(query, q)
}

func (s *Server) applySourceAccountFilters(query url.Values, q *gorm.DB) *gorm.DB {
	source := ingest.Slugify(textParam(query, "source"))

	if id := textParam(query, "source_id"); id != "" {
		q = q.Where("source_id = ?", id)
	}

	if source != "" {
		q = q.Where("source_id IN (?)", s.sourceIDs(source))
	}

	if kind := strings.ToLower(textParam(query, "source_kind")); kind != "" {
		q = q.Where("source_id IN (?)",
			s.reader.Model(&model.Source{}).Select("id").Where("kind = ?", kind))
	}

	if id := textParam(query, "account_id"); id != "" {
		q = q.Where("account_id = ?", id)
	}

	if account := ingest.Slugify(textParam(query, "account")); account != "" {
		accountIDs := s.reader.Model(&model.Account{}).Select("id").Where("slug = ?", account)
		if source != "" {
			accountIDs = accountIDs.Where("source_id IN (?)", s.sourceIDs(source))
		}

		q = q.Where("account_id IN (?)", accountIDs)
	}

	return q
}

func (s *Server) sourceIDs(slug string) *gorm.DB {
	return s.reader.Model(&model.Source{}).Select("id").Where("slug = ?", slug)
}

func (s *Server) applyChannelFilters(query url.Values, q *gorm.DB) *gorm.DB {
	if id := textParam(query, "channel_id"); id != "" {
		q = q.Where("channel_id = ?", id)
	}

	channelIDs := s.reader.Model(&model.Channel{}).Select("id")
	filtered := false

	if externalID := firstTextParam(query, "channel", "channel_external_id"); externalID != "" {
		channelIDs = channelIDs.Where("external_id = ?", externalID)
		filtered = true
	}

	if name := textParam(query, "channel_name"); name != "" {
		channelIDs = channelIDs.Where("name LIKE ?", likePattern(name))
		filtered = true
	}

	if kind := strings.ToLower(textParam(query, "channel_kind")); kind != "" {
		channelIDs = channelIDs.Where("kind = ?", kind)
		filtered = true
	}

	if filtered {
		q = q.Where("channel_id IN (?)", channelIDs)
	}

	return q
}

func (s *Server) applyIdentityFilters(query url.Values, q *gorm.DB) *gorm.DB {
	if id := textParam(query, "sender_id"); id != "" {
		q = q.Where("sender_id = ?", id)
	}

	if value := textParam(query, "sender"); value != "" {
		q = q.Where("sender_id IN (?)", identityIDs(s.reader, textParam(query, "sender_kind"), value))
	}

	if correspondent := textParam(query, "sender_correspondent"); correspondent != "" {
		q = q.Where("sender_id IN (?)", correspondentIdentityIDs(s.reader, correspondent))
	}

	q = s.applyRecipientFilters(query, q)

	if correspondent := textParam(query, "correspondent_id"); correspondent != "" {
		q = q.Where(
			`sender_id IN (SELECT id FROM identities WHERE correspondent_id = ?)
			 OR id IN (
				SELECT message_id FROM message_recipients
				WHERE identity_id IN (SELECT id FROM identities WHERE correspondent_id = ?)
			 )`, correspondent, correspondent)
	}

	return q
}

func (s *Server) applyRecipientFilters(query url.Values, q *gorm.DB) *gorm.DB {
	if id := textParam(query, "recipient_id"); id != "" {
		q = q.Where("id IN (SELECT message_id FROM message_recipients WHERE identity_id = ?)", id)
	}

	if correspondent := textParam(query, "recipient_correspondent"); correspondent != "" {
		q = q.Where(`id IN (
			SELECT message_id FROM message_recipients
			WHERE identity_id IN (SELECT id FROM identities WHERE correspondent_id = ?)
		)`, correspondent)
	}

	value := textParam(query, "recipient")
	if value == "" {
		return q
	}

	identitySubquery := identityIDs(s.reader, textParam(query, "recipient_kind"), value)

	if role := strings.ToLower(textParam(query, "recipient_role")); role != "" {
		return q.Where(`id IN (
			SELECT message_id FROM message_recipients
			WHERE role = ? AND identity_id IN (?)
		)`, role, identitySubquery)
	}

	return q.Where("id IN (SELECT message_id FROM message_recipients WHERE identity_id IN (?))",
		identitySubquery)
}

func (s *Server) applySphereFilters(query url.Values, q *gorm.DB) *gorm.DB {
	if slug := ingest.Slugify(textParam(query, "sphere")); slug != "" {
		q = q.Where("sphere_id IN (?)",
			s.reader.Model(&model.Sphere{}).Select("id").Where("slug = ?", slug))
	}

	if id := textParam(query, "sphere_id"); id != "" {
		q = q.Where("sphere_id = ?", id)
	}

	if isTrue(query.Get("no_sphere")) {
		q = q.Where("sphere_id IS NULL")
	}

	return q
}

func identityIDs(db *gorm.DB, kind, value string) *gorm.DB {
	subquery := db.Model(&model.Identity{}).Select("id")

	if kind != "" {
		kind = strings.ToLower(strings.TrimSpace(kind))

		return subquery.Where("kind = ? AND normalized_value = ?",
			kind, ingest.NormalizeIdentity(kind, value))
	}

	return subquery.Where("LOWER(normalized_value) = LOWER(?)", strings.TrimSpace(value))
}

func correspondentIdentityIDs(db *gorm.DB, correspondentID string) *gorm.DB {
	return db.Model(&model.Identity{}).Select("id").Where("correspondent_id = ?", correspondentID)
}

func applyTagFilters(query url.Values, q *gorm.DB) (*gorm.DB, error) {
	slugs := tagSlugs(query)
	if len(slugs) == 0 {
		return q, nil
	}

	sort.Strings(slugs)

	mode := strings.ToLower(textParam(query, "tag_mode"))
	if mode == "" {
		mode = "any"
	}

	switch mode {
	case "any":
		q = q.Where(`id IN (
			SELECT message_id FROM message_tags
			WHERE tag_id IN (SELECT id FROM tags WHERE slug IN ?)
		)`, slugs)
	case "all":
		q = q.Where(`id IN (
			SELECT mt.message_id FROM message_tags mt
			JOIN tags t ON t.id = mt.tag_id
			WHERE t.slug IN ?
			GROUP BY mt.message_id
			HAVING COUNT(DISTINCT t.slug) = ?
		)`, slugs, len(slugs))
	default:
		return nil, badRequest("invalid tag_mode %q (use any or all)", mode)
	}

	return q, nil
}

func tagSlugs(query url.Values) []string {
	seen := make(map[string]struct{})
	slugs := make([]string, 0)

	for _, key := range []string{"tag", "tags"} {
		for _, raw := range query[key] {
			for part := range strings.SplitSeq(raw, ",") {
				slug := ingest.Slugify(part)
				if slug == "" {
					continue
				}

				if _, ok := seen[slug]; ok {
					continue
				}

				seen[slug] = struct{}{}
				slugs = append(slugs, slug)
			}
		}
	}

	return slugs
}

func applyMessageFilters(query url.Values, q *gorm.DB) (*gorm.DB, error) {
	q = applyIdentityFieldFilters(query, q)
	q = applyContentFilters(query, q)

	return applyNumberFilters(query, q)
}

func applyIdentityFieldFilters(query url.Values, q *gorm.DB) *gorm.DB {
	if id := textParam(query, "id"); id != "" {
		q = q.Where("id = ?", id)
	}

	if externalID := textParam(query, "external_id"); externalID != "" {
		q = q.Where("external_id = ?", externalID)
	}

	if threadID := textParam(query, "thread_id"); threadID != "" {
		q = q.Where("thread_id = ?", threadID)
	}

	if parentID := textParam(query, "parent_id"); parentID != "" {
		q = q.Where("parent_id = ?", parentID)
	}

	if hash := textParam(query, "content_hash"); hash != "" {
		q = q.Where("content_hash = ?", hash)
	}

	return q
}

func applyContentFilters(query url.Values, q *gorm.DB) *gorm.DB {
	if direction := strings.ToLower(textParam(query, "direction")); direction != "" {
		q = q.Where("direction = ?", direction)
	}

	if kind := strings.ToLower(textParam(query, "kind")); kind != "" {
		q = q.Where("kind = ?", kind)
	}

	if format := strings.ToLower(textParam(query, "body_format")); format != "" {
		q = q.Where("body_format = ?", format)
	}

	if language := strings.ToLower(textParam(query, "language")); language != "" {
		q = q.Where("language = ?", language)
	}

	if subject := textParam(query, "subject"); subject != "" {
		q = q.Where("subject LIKE ?", likePattern(subject))
	}

	if body := textParam(query, "body"); body != "" {
		q = q.Where("body LIKE ?", likePattern(body))
	}

	if search := textParam(query, "q"); search != "" {
		pattern := likePattern(search)
		q = q.Where("subject LIKE ? OR body LIKE ? OR preview LIKE ? OR url LIKE ?",
			pattern, pattern, pattern, pattern)
	}

	return q
}

func applyNumberFilters(query url.Values, q *gorm.DB) (*gorm.DB, error) {
	if raw := textParam(query, "importance_min"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return nil, badRequest("invalid importance_min %q", raw)
		}

		q = q.Where("importance >= ?", value)
	}

	if raw := textParam(query, "importance_max"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return nil, badRequest("invalid importance_max %q", raw)
		}

		q = q.Where("importance <= ?", value)
	}

	return q, nil
}

func applyTimeFilters(query url.Values, q *gorm.DB) (*gorm.DB, error) {
	var err error

	if q, err = applyTimeRange(query, q, "sent_at", "since", "until"); err != nil {
		return nil, err
	}

	if q, err = applyTimeRange(query, q, "received_at", "received_since", "received_until"); err != nil {
		return nil, err
	}

	return applyTimeRange(query, q, "ingested_at", "ingested_since", "ingested_until")
}

func applyTimeRange(query url.Values, q *gorm.DB, column, sinceParam, untilParam string) (*gorm.DB, error) {
	if raw := textParam(query, sinceParam); raw != "" {
		value, err := parseQueryTime(raw)
		if err != nil {
			return nil, badRequest("invalid %s: %v", sinceParam, err)
		}

		q = q.Where(clause.Gte{Column: clause.Column{Name: column}, Value: value})
	}

	if raw := textParam(query, untilParam); raw != "" {
		value, err := parseQueryTime(raw)
		if err != nil {
			return nil, badRequest("invalid %s: %v", untilParam, err)
		}

		q = q.Where(clause.Lte{Column: clause.Column{Name: column}, Value: value})
	}

	return q, nil
}

func applyStateFilters(query url.Values, q *gorm.DB) (*gorm.DB, error) {
	// unread and starred predate the explicit is_* filters.
	if isTrue(query.Get("unread")) {
		q = q.Where("is_read = ?", false)
	}

	if isTrue(query.Get("starred")) {
		q = q.Where("is_starred = ?", true)
	}

	q, err := applyBoolFilters(query, q)
	if err != nil {
		return nil, err
	}

	q, err = applyDeletedFilter(query, q)
	if err != nil {
		return nil, err
	}

	q, err = applyAttachmentPresenceFilter(query, q)
	if err != nil {
		return nil, err
	}

	return applyAttachmentFilters(query, q), nil
}

func applyBoolFilters(query url.Values, q *gorm.DB) (*gorm.DB, error) {
	filters := []struct {
		param  string
		column string
	}{
		{"is_read", "is_read"},
		{"is_starred", "is_starred"},
		{"is_archived", "is_archived"},
	}

	for _, filter := range filters {
		value, err := boolParam(query, filter.param)
		if err != nil {
			return nil, err
		}

		if value != nil {
			q = q.Where(filter.column+" = ?", *value)
		}
	}

	return q, nil
}

func applyDeletedFilter(query url.Values, q *gorm.DB) (*gorm.DB, error) {
	deleted, err := boolParam(query, "deleted")
	if err != nil {
		return nil, err
	}

	if deleted == nil {
		return q, nil
	}

	if *deleted {
		return q.Where("deleted_at IS NOT NULL"), nil
	}

	return q.Where("deleted_at IS NULL"), nil
}

func applyAttachmentPresenceFilter(query url.Values, q *gorm.DB) (*gorm.DB, error) {
	hasAttachments, err := boolParam(query, "has_attachments")
	if err != nil {
		return nil, err
	}

	if hasAttachments == nil {
		return q, nil
	}

	condition := "EXISTS (SELECT 1 FROM attachments WHERE attachments.message_id = messages.id)"
	if !*hasAttachments {
		condition = "NOT " + condition
	}

	return q.Where(condition), nil
}

func applyAttachmentFilters(query url.Values, q *gorm.DB) *gorm.DB {
	if mime := textParam(query, "attachment_mime"); mime != "" {
		q = q.Where(`EXISTS (
			SELECT 1 FROM attachments
			WHERE attachments.message_id = messages.id AND attachments.mime_type LIKE ?
		)`, likePattern(mime))
	}

	if filename := textParam(query, "attachment_filename"); filename != "" {
		q = q.Where(`EXISTS (
			SELECT 1 FROM attachments
			WHERE attachments.message_id = messages.id AND attachments.filename LIKE ?
		)`, likePattern(filename))
	}

	if sha := textParam(query, "attachment_sha256"); sha != "" {
		q = q.Where(`EXISTS (
			SELECT 1 FROM attachments
			WHERE attachments.message_id = messages.id AND attachments.sha256 = ?
		)`, sha)
	}

	return q
}

func parseQueryTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)

	if value, err := time.Parse(time.RFC3339, raw); err == nil {
		return value.UTC(), nil
	}

	if value, err := time.Parse("2006-01-02", raw); err == nil {
		return value.UTC(), nil
	}

	return time.Time{}, errors.New("expected RFC 3339 or YYYY-MM-DD")
}

func textParam(query url.Values, name string) string {
	return strings.TrimSpace(query.Get(name))
}

func firstTextParam(query url.Values, names ...string) string {
	for _, name := range names {
		if value := textParam(query, name); value != "" {
			return value
		}
	}

	return ""
}

func likePattern(value string) string {
	return "%" + value + "%"
}

func isTrue(value string) bool {
	return value != "" && value != "false" && value != "0"
}

func boolParam(query url.Values, name string) (*bool, error) {
	raw := textParam(query, name)
	if raw == "" {
		//nolint:nilnil // an absent parameter means "do not filter".
		return nil, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, badRequest("%s: invalid boolean %q", name, raw)
	}

	return &value, nil
}

// cursorPayload is an opaque position in a sorted message list. It contains the
// sort/order it belongs to, so a cursor cannot silently be used with another
// ordering.
type cursorPayload struct {
	Field string `json:"field"`
	Order string `json:"order"`
	Value string `json:"value"`
	ID    string `json:"id"`
}

func encodeCursor(messageOrder messageSort, message model.Message) (string, error) {
	payload := cursorPayload{
		Field: messageOrder.Field,
		Order: messageOrder.Order,
		Value: messageOrder.value(message),
		ID:    message.ID,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(raw string) (cursorPayload, error) {
	var payload cursorPayload

	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return payload, err
	}

	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, err
	}

	if payload.ID == "" || payload.Value == "" {
		return payload, errors.New("malformed cursor")
	}

	if _, ok := sortColumn(payload.Field); !ok {
		return payload, errors.New("invalid cursor sort field")
	}

	if payload.Order != orderAsc && payload.Order != orderDesc {
		return payload, errors.New("invalid cursor order")
	}

	return payload, nil
}

func applyCursor(query url.Values, q *gorm.DB, messageOrder messageSort) (*gorm.DB, error) {
	raw := textParam(query, "cursor")
	if raw == "" {
		return q, nil
	}

	payload, err := decodeCursor(raw)
	if err != nil {
		return nil, badRequest("invalid cursor: %v", err)
	}

	if payload.Field != messageOrder.Field || payload.Order != messageOrder.Order {
		return nil, badRequest("cursor does not match sort and order")
	}

	column, _ := sortColumn(payload.Field)

	operator := "<"
	if messageOrder.Order == orderAsc {
		operator = ">"
	}

	var value any

	if payload.Field == sortImportance {
		number, err := strconv.Atoi(payload.Value)
		if err != nil {
			return nil, badRequest("invalid cursor value")
		}

		value = number
	} else {
		timestamp, err := time.Parse(time.RFC3339Nano, payload.Value)
		if err != nil {
			return nil, badRequest("invalid cursor value")
		}

		value = timestamp.UTC()
	}

	condition := fmt.Sprintf("(%s %s ? OR (%s = ? AND id %s ?))",
		column, operator, column, operator)

	return q.Where(condition, value, value, payload.ID), nil
}
