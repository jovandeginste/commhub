package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jovandeginste/commhub/pkg/blobs"
	"github.com/jovandeginste/commhub/pkg/model"
)

// Service implements ingestion. It is safe for concurrent use.
type Service struct {
	db     *gorm.DB
	logger *slog.Logger
	now    func() time.Time
	blobs  *blobs.Store
}

// New returns an ingestion service.
func New(db *gorm.DB, logger *slog.Logger) *Service {
	return &Service{
		db:     db,
		logger: logger,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// SetBlobs attaches an attachment store. When a connector uploads a blob before
// ingesting the message, the hub links the local copy instead of the
// connector's path.
func (s *Service) SetBlobs(store *blobs.Store) {
	s.blobs = store
}

// Result is the outcome for a single message.
type Result struct {
	ID     string             `json:"id"`
	Status model.ResultStatus `json:"status"`
}

// ItemResult is the outcome for one message inside a batch.
type ItemResult struct {
	Index      int                `json:"index"`
	ExternalID string             `json:"external_id,omitempty"`
	ID         string             `json:"id,omitempty"`
	Status     model.ResultStatus `json:"status,omitempty"`
	Error      string             `json:"error,omitempty"`
}

// BatchResult aggregates the outcomes of a batch or stream.
type BatchResult struct {
	BatchID   string       `json:"batch_id,omitempty"`
	Total     int          `json:"total"`
	Created   int          `json:"created"`
	Updated   int          `json:"updated"`
	Unchanged int          `json:"unchanged"`
	Failed    int          `json:"failed"`
	Results   []ItemResult `json:"results"`
}

// resolvedRecipient is an identity that was resolved during ingestion.
type resolvedRecipient struct {
	identity *model.Identity
	role     string
}

// Ingest stores one message. The whole message is one transaction.
func (s *Service) Ingest(ctx context.Context, in *MessageInput) (Result, error) {
	if err := in.Validate(); err != nil {
		return Result{}, err
	}

	var result Result

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error

		result, err = s.ingest(tx, in)

		return err
	})
	if err != nil {
		return Result{}, err
	}

	return result, nil
}

// IngestBatch stores messages best-effort: one bad item does not prevent the
// others from being stored. All items share one outer transaction with a
// savepoint per message.
func (s *Service) IngestBatch(ctx context.Context, inputs []MessageInput, mode model.IngestMode) (*BatchResult, error) {
	if len(inputs) > MaxBatchSize {
		return nil, Invalid("messages", fmt.Sprintf("at most %d messages per batch", MaxBatchSize))
	}

	result := &BatchResult{
		Total:   len(inputs),
		Results: make([]ItemResult, 0, len(inputs)),
	}

	if len(inputs) == 0 {
		return result, nil
	}

	batch := model.IngestBatch{
		Mode:      string(mode),
		Status:    string(model.IngestStatusRunning),
		Total:     len(inputs),
		StartedAt: s.now(),
	}
	if err := s.db.WithContext(ctx).Omit(clause.Associations).Create(&batch).Error; err != nil {
		return nil, fmt.Errorf("create ingest batch: %w", err)
	}

	result.BatchID = batch.ID

	ingestErrors, txErr := s.processBatch(ctx, inputs, result)

	s.finishBatch(ctx, batch.ID, result, ingestErrors, txErr)

	if txErr != nil {
		return result, fmt.Errorf("ingest batch: %w", txErr)
	}

	return result, nil
}

// processBatch ingests all items in one transaction with a savepoint per
// message, so a failure only rolls back that message.
func (s *Service) processBatch(ctx context.Context, inputs []MessageInput, result *BatchResult) ([]model.IngestError, error) {
	ingestErrors := make([]model.IngestError, 0)

	txErr := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range inputs {
			item, err := s.ingestItem(tx, &inputs[i], i)
			if err != nil {
				result.Failed++

				if payload, marshalErr := json.Marshal(&inputs[i]); marshalErr == nil {
					ingestErrors = append(ingestErrors, model.IngestError{
						BatchID:    result.BatchID,
						Position:   i,
						ExternalID: inputs[i].ExternalID,
						Error:      err.Error(),
						Payload:    model.JSON(payload),
					})
				}
			} else {
				switch item.Status {
				case model.ResultCreated:
					result.Created++
				case model.ResultUpdated:
					result.Updated++
				case model.ResultUnchanged:
					result.Unchanged++
				}
			}

			result.Results = append(result.Results, item)
		}

		return nil
	})

	return ingestErrors, txErr
}

func (s *Service) ingestItem(tx *gorm.DB, in *MessageInput, index int) (ItemResult, error) {
	item := ItemResult{Index: index, ExternalID: in.ExternalID}

	var result Result

	err := tx.Transaction(func(sub *gorm.DB) error {
		var err error

		result, err = s.ingest(sub, in)

		return err
	})
	if err != nil {
		item.Error = err.Error()

		return item, err
	}

	item.ID = result.ID
	item.Status = result.Status

	return item, nil
}

func (s *Service) finishBatch(ctx context.Context, batchID string, result *BatchResult, ingestErrors []model.IngestError, txErr error) {
	status := model.IngestStatusCompleted

	switch {
	case txErr != nil:
		status = model.IngestStatusFailed
	case result.Failed > 0 && result.Failed == result.Total:
		status = model.IngestStatusFailed
	case result.Failed > 0:
		status = model.IngestStatusPartial
	}

	updates := map[string]any{
		"status":      string(status),
		"created":     result.Created,
		"updated":     result.Updated,
		"unchanged":   result.Unchanged,
		"failed":      result.Failed,
		"finished_at": s.now(),
	}

	if txErr != nil {
		updates["error"] = txErr.Error()
	}

	if err := s.db.WithContext(ctx).Model(&model.IngestBatch{}).
		Where("id = ?", batchID).Updates(updates).Error; err != nil {
		s.logger.Error("update ingest batch", "batch", batchID, "error", err)
	}

	if len(ingestErrors) > 0 {
		if err := s.db.WithContext(ctx).Omit(clause.Associations).
			CreateInBatches(ingestErrors, 100).Error; err != nil {
			s.logger.Error("store ingest errors", "batch", batchID, "error", err)
		}
	}
}

// SetTags replaces the tags of an existing message.
func (s *Service) SetTags(ctx context.Context, messageID string, names []string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64

		if err := tx.Model(&model.Message{}).Where("id = ?", messageID).Count(&count).Error; err != nil {
			return err
		}

		if count == 0 {
			return gorm.ErrRecordNotFound
		}

		return syncTags(tx, messageID, names)
	})
}

// IngestStream reads NDJSON from r and ingests it in chunks. A malformed line
// stops the stream; the messages before it are returned in the result.
func (s *Service) IngestStream(ctx context.Context, r io.Reader, batchSize int) (*BatchResult, error) {
	if batchSize <= 0 || batchSize > MaxBatchSize {
		batchSize = MaxBatchSize
	}

	decoder := json.NewDecoder(r)
	flusher := &streamFlusher{
		service:   s,
		aggregate: &BatchResult{Results: make([]ItemResult, 0)},
	}
	chunk := make([]MessageInput, 0, batchSize)
	line := 0

	for {
		var in MessageInput

		err := decoder.Decode(&in)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			decodeErr := fmt.Errorf("decode NDJSON line %d: %w", line+1, err)

			if flushErr := flusher.flush(ctx, chunk); flushErr != nil {
				return flusher.aggregate, errors.Join(decodeErr, flushErr)
			}

			return flusher.aggregate, decodeErr
		}

		line++
		chunk = append(chunk, in)

		if len(chunk) >= batchSize {
			if err := flusher.flush(ctx, chunk); err != nil {
				return flusher.aggregate, err
			}

			chunk = chunk[:0]
		}
	}

	if err := flusher.flush(ctx, chunk); err != nil {
		return flusher.aggregate, err
	}

	return flusher.aggregate, nil
}

// streamFlusher ingests chunks and merges their results into one aggregate.
type streamFlusher struct {
	service   *Service
	aggregate *BatchResult
	offset    int
}

func (f *streamFlusher) flush(ctx context.Context, chunk []MessageInput) error {
	if len(chunk) == 0 {
		return nil
	}

	result, err := f.service.IngestBatch(ctx, chunk, model.IngestModeStream)
	if err != nil {
		return err
	}

	if f.aggregate.BatchID == "" {
		f.aggregate.BatchID = result.BatchID
	}

	f.aggregate.Total += result.Total
	f.aggregate.Created += result.Created
	f.aggregate.Updated += result.Updated
	f.aggregate.Unchanged += result.Unchanged
	f.aggregate.Failed += result.Failed

	for _, item := range result.Results {
		item.Index += f.offset
		f.aggregate.Results = append(f.aggregate.Results, item)
	}

	f.offset += len(chunk)

	return nil
}

// messageContext is everything that is resolved before the message row is
// created or updated.
type messageContext struct {
	source     *model.Source
	account    *model.Account
	channel    *model.Channel
	sphereID   *string
	sender     *model.Identity
	recipients []resolvedRecipient
	direction  model.Direction
	sentAt     time.Time
	hash       string
}

func (s *Service) ingest(tx *gorm.DB, in *MessageInput) (Result, error) {
	resolved, err := s.resolveMessage(tx, in)
	if err != nil {
		return Result{}, err
	}

	var existing model.Message

	err = tx.Where("account_id = ? AND external_id = ?", resolved.account.ID, in.ExternalID).
		First(&existing).Error

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if in.DeletedInSourceAt != nil {
			// The connector reports a deletion before we ever saw the message.
			// Nothing to flag; the next sync will not bring it back either.
			return Result{Status: model.ResultUnchanged}, nil
		}

		return s.createMessage(tx, in, resolved)
	case err != nil:
		return Result{}, fmt.Errorf("lookup message: %w", err)
	}

	if in.DeletedInSourceAt != nil {
		if err := tx.Model(&existing).
			Update("deleted_at", in.DeletedInSourceAt.UTC()).Error; err != nil {
			return Result{}, fmt.Errorf("mark message deleted: %w", err)
		}

		return Result{ID: existing.ID, Status: model.ResultUpdated}, nil
	}

	if existing.ContentHash == resolved.hash {
		return Result{ID: existing.ID, Status: model.ResultUnchanged}, nil
	}

	return s.updateMessage(tx, &existing, in, resolved)
}

// resolveMessage resolves or creates every entity a message refers to.
func (s *Service) resolveMessage(tx *gorm.DB, in *MessageInput) (*messageContext, error) {
	source, account, err := s.resolveSource(tx, in)
	if err != nil {
		return nil, err
	}

	channel, err := s.resolveChannel(tx, source, account, in)
	if err != nil {
		return nil, err
	}

	sphereID := channel.SphereID

	if in.Sphere != "" {
		sphere, err := resolveSphere(tx, in.Sphere)
		if err != nil {
			return nil, err
		}

		sphereID = &sphere.ID
	}

	var sender *model.Identity

	if in.Sender != nil {
		sender, err = s.resolveIdentity(tx, source, account, in.Sender)
		if err != nil {
			return nil, err
		}
	}

	senderIsSelf := sender != nil &&
		accountIsSelf(account, sender.Kind, sender.NormalizedValue)

	recipients, err := s.resolveRecipients(tx, source, account, in.Recipients)
	if err != nil {
		return nil, err
	}

	sentAt := s.now()
	if in.SentAt != nil && !in.SentAt.IsZero() {
		sentAt = in.SentAt.UTC()
	}

	return &messageContext{
		source:     source,
		account:    account,
		channel:    channel,
		sphereID:   sphereID,
		sender:     sender,
		recipients: recipients,
		direction:  deriveDirection(in, senderIsSelf),
		sentAt:     sentAt,
		hash:       contentHash(in, sender, sentAt),
	}, nil
}

func (s *Service) resolveRecipients(tx *gorm.DB, source *model.Source, account *model.Account, inputs []RecipientInput) ([]resolvedRecipient, error) {
	recipients := make([]resolvedRecipient, 0, len(inputs))

	for i := range inputs {
		identity, err := s.resolveIdentity(tx, source, account, &inputs[i].IdentityInput)
		if err != nil {
			return nil, fmt.Errorf("recipient %d: %w", i, err)
		}

		recipients = append(recipients, resolvedRecipient{
			identity: identity,
			role:     inputs[i].Role,
		})
	}

	return recipients, nil
}

// deriveDirection uses the sender when the connector did not provide a
// direction. On a partial update neither field is present and the existing
// direction is kept.
func deriveDirection(in *MessageInput, senderIsSelf bool) model.Direction {
	if direction := model.Direction(in.Direction); direction != "" {
		return direction
	}

	if in.Sender == nil && in.Recipients == nil {
		return ""
	}

	if senderIsSelf {
		return model.DirectionOutbound
	}

	return model.DirectionInbound
}

func (s *Service) createMessage(tx *gorm.DB, in *MessageInput, resolved *messageContext) (Result, error) {
	kind := in.Kind
	if kind == "" {
		kind = string(model.MessageKindChat)
	}

	direction := resolved.direction
	if direction == "" {
		direction = model.DirectionInbound
	}

	var senderID *string

	if resolved.sender != nil {
		senderID = &resolved.sender.ID
	}

	message := model.Message{
		SourceID:   resolved.source.ID,
		AccountID:  resolved.account.ID,
		ChannelID:  resolved.channel.ID,
		ExternalID: in.ExternalID,
		ThreadID:   in.ThreadID,
		ParentID:   resolveParent(tx, resolved.account.ID, in.ParentExternalID),
		SenderID:   senderID,
		Direction:  string(direction),
		Kind:       kind,
		Subject:    in.Subject,
		Body:       in.Body,
		BodyFormat: in.BodyFormat,
		Preview:    in.Preview,
		Language:   in.Language,
		URL:        in.URL,
		SentAt:     resolved.sentAt,
		ReceivedAt: utcPtr(in.ReceivedAt),
		IngestedAt: s.now(),
		EditedAt:   utcPtr(in.EditedAt),

		DeletedInSourceAt: utcPtr(in.DeletedInSourceAt),
		ContentHash:       resolved.hash,
		Importance:        in.Importance,
		SphereID:          resolved.sphereID,
		Metadata:          model.JSON(in.Metadata),
		Raw:               model.JSON(in.Raw),
	}

	if err := tx.Omit(clause.Associations).Create(&message).Error; err != nil {
		return Result{}, fmt.Errorf("create message: %w", err)
	}

	if err := s.syncRelated(tx, &message, in, resolved.recipients); err != nil {
		return Result{}, err
	}

	return Result{ID: message.ID, Status: model.ResultCreated}, nil
}

func messageUpdates(in *MessageInput, resolved *messageContext, sphereID, parentID *string, editedAt time.Time) map[string]any {
	return map[string]any{
		"channel_id":   resolved.channel.ID,
		"thread_id":    in.ThreadID,
		"parent_id":    parentID,
		"subject":      in.Subject,
		"body":         in.Body,
		"body_format":  in.BodyFormat,
		"preview":      in.Preview,
		"language":     in.Language,
		"url":          in.URL,
		"sent_at":      resolved.sentAt,
		"received_at":  utcPtr(in.ReceivedAt),
		"edited_at":    editedAt,
		"content_hash": resolved.hash,
		"importance":   in.Importance,
		"sphere_id":    sphereID,
		"metadata":     model.JSON(in.Metadata),
		"raw":          model.JSON(in.Raw),
	}
}

func (s *Service) updateMessage(tx *gorm.DB, existing *model.Message, in *MessageInput, resolved *messageContext) (Result, error) {
	now := s.now()

	sphereID := resolved.sphereID
	if in.Sphere == "" && existing.SphereID != nil {
		sphereID = existing.SphereID
	}

	parentID := existing.ParentID

	if in.ParentExternalID != "" {
		if parent := resolveParent(tx, resolved.account.ID, in.ParentExternalID); parent != nil {
			parentID = parent
		}
	}

	editedAt := utcPtr(in.EditedAt)
	if editedAt == nil {
		editedAt = &now
	}

	updates := messageUpdates(in, resolved, sphereID, parentID, *editedAt)

	if in.Kind != "" {
		updates["kind"] = in.Kind
		existing.Kind = in.Kind
	}

	if resolved.direction != "" {
		updates["direction"] = string(resolved.direction)
		existing.Direction = string(resolved.direction)
	}

	if resolved.sender != nil {
		updates["sender_id"] = resolved.sender.ID
		existing.SenderID = &resolved.sender.ID
	}

	if err := tx.Model(existing).Updates(updates).Error; err != nil {
		return Result{}, fmt.Errorf("update message: %w", err)
	}

	existing.ChannelID = resolved.channel.ID
	existing.SphereID = sphereID

	if err := s.syncRelated(tx, existing, in, resolved.recipients); err != nil {
		return Result{}, err
	}

	return Result{ID: existing.ID, Status: model.ResultUpdated}, nil
}

func (s *Service) resolveSource(tx *gorm.DB, in *MessageInput) (*model.Source, *model.Account, error) {
	var source model.Source

	err := tx.Where("slug = ?", in.Source).First(&source).Error

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		source = model.Source{
			Slug:        in.Source,
			Name:        in.Source,
			Kind:        strings.TrimSpace(in.SourceKind),
			Enabled:     true,
			Description: "",
		}

		if err := tx.Omit(clause.Associations).Create(&source).Error; err != nil {
			return nil, nil, fmt.Errorf("create source: %w", err)
		}
	case err != nil:
		return nil, nil, fmt.Errorf("lookup source: %w", err)
	}

	var account model.Account

	err = tx.Where("source_id = ? AND slug = ?", source.ID, in.Account).First(&account).Error

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		account = model.Account{
			SourceID:         source.ID,
			Slug:             in.Account,
			Name:             in.Account,
			Enabled:          true,
			ExternalUserKind: DefaultIdentityKind(source.Slug),
		}

		if err := tx.Omit(clause.Associations).Create(&account).Error; err != nil {
			return nil, nil, fmt.Errorf("create account: %w", err)
		}
	case err != nil:
		return nil, nil, fmt.Errorf("lookup account: %w", err)
	}

	return &source, &account, nil
}

func (s *Service) resolveChannel(tx *gorm.DB, source *model.Source, account *model.Account, in *MessageInput) (*model.Channel, error) {
	var channel model.Channel

	err := tx.Where("account_id = ? AND external_id = ?", account.ID, in.Channel.ExternalID).
		First(&channel).Error

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		name := strings.TrimSpace(in.Channel.Name)
		if name == "" {
			name = in.Channel.ExternalID
		}

		channel = model.Channel{
			SourceID:   source.ID,
			AccountID:  account.ID,
			ExternalID: in.Channel.ExternalID,
			Name:       name,
			Kind:       in.Channel.Kind,
			Topic:      in.Channel.Topic,
		}

		if in.Channel.Sphere != "" {
			sphere, err := resolveSphere(tx, in.Channel.Sphere)
			if err != nil {
				return nil, err
			}

			channel.SphereID = &sphere.ID
		}

		if err := tx.Omit(clause.Associations).Create(&channel).Error; err != nil {
			return nil, fmt.Errorf("create channel: %w", err)
		}

		return &channel, nil
	case err != nil:
		return nil, fmt.Errorf("lookup channel: %w", err)
	}

	if err := s.updateChannel(tx, &channel, in); err != nil {
		return nil, err
	}

	return &channel, nil
}

func (s *Service) updateChannel(tx *gorm.DB, channel *model.Channel, in *MessageInput) error {
	updates := map[string]any{}

	if name := strings.TrimSpace(in.Channel.Name); name != "" && name != channel.Name {
		updates["name"] = name
	}

	if in.Channel.Topic != "" && in.Channel.Topic != channel.Topic {
		updates["topic"] = in.Channel.Topic
	}

	if in.Channel.Kind != "" && in.Channel.Kind != channel.Kind {
		updates["kind"] = in.Channel.Kind
	}

	if in.Channel.Sphere != "" {
		sphere, err := resolveSphere(tx, in.Channel.Sphere)
		if err != nil {
			return err
		}

		if channel.SphereID == nil || *channel.SphereID != sphere.ID {
			updates["sphere_id"] = sphere.ID
			channel.SphereID = &sphere.ID
		}
	}

	if len(updates) == 0 {
		return nil
	}

	if err := tx.Model(channel).Updates(updates).Error; err != nil {
		return fmt.Errorf("update channel: %w", err)
	}

	return nil
}

func (s *Service) resolveIdentity(tx *gorm.DB, source *model.Source, account *model.Account, in *IdentityInput) (*model.Identity, error) {
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = DefaultIdentityKind(source.Slug)
	}

	value := strings.TrimSpace(in.Value)
	normalized := NormalizeIdentity(kind, value)

	var identity model.Identity

	err := tx.Preload("Correspondent").
		Where("source_id = ? AND kind = ? AND normalized_value = ?", source.ID, kind, normalized).
		First(&identity).Error

	switch {
	case err == nil:
		return s.updateExistingIdentity(tx, &identity, in, account, kind, normalized)
	case errors.Is(err, gorm.ErrRecordNotFound):
		return s.createIdentity(tx, source, account, in, kind, value, normalized)
	default:
		return nil, fmt.Errorf("lookup identity: %w", err)
	}
}

// updateExistingIdentity fills in missing display names and marks the
// correspondent as self when the account has learned who "self" is.
func (s *Service) updateExistingIdentity(tx *gorm.DB, identity *model.Identity, in *IdentityInput, account *model.Account, kind, normalized string) (*model.Identity, error) {
	displayName := strings.TrimSpace(in.DisplayName)

	if identity.DisplayName == "" && displayName != "" {
		identity.DisplayName = displayName

		if err := tx.Model(identity).Update("display_name", displayName).Error; err != nil {
			return nil, fmt.Errorf("update identity: %w", err)
		}
	}

	if !identity.Correspondent.IsSelf && accountIsSelf(account, kind, normalized) {
		identity.Correspondent.IsSelf = true

		if err := tx.Model(&model.Correspondent{}).
			Where("id = ?", identity.CorrespondentID).
			Update("is_self", true).Error; err != nil {
			return nil, fmt.Errorf("update correspondent: %w", err)
		}
	}

	return identity, nil
}

func (s *Service) createIdentity(tx *gorm.DB, source *model.Source, account *model.Account, in *IdentityInput, kind, value, normalized string) (*model.Identity, error) {
	correspondent := model.Correspondent{
		DisplayName: strings.TrimSpace(in.DisplayName),
		IsSelf:      accountIsSelf(account, kind, normalized),
	}
	if correspondent.DisplayName == "" {
		correspondent.DisplayName = value
	}

	if err := tx.Omit(clause.Associations).Create(&correspondent).Error; err != nil {
		return nil, fmt.Errorf("create correspondent: %w", err)
	}

	identity := model.Identity{
		CorrespondentID: correspondent.ID,
		SourceID:        source.ID,
		Kind:            kind,
		Value:           value,
		NormalizedValue: normalized,
		DisplayName:     strings.TrimSpace(in.DisplayName),
	}

	if err := tx.Omit(clause.Associations).Create(&identity).Error; err != nil {
		return nil, fmt.Errorf("create identity: %w", err)
	}

	return &identity, nil
}

func (s *Service) syncRelated(tx *gorm.DB, message *model.Message, in *MessageInput, recipients []resolvedRecipient) error {
	if in.Recipients != nil {
		if err := syncRecipients(tx, message.ID, recipients); err != nil {
			return err
		}
	}

	if in.Attachments != nil {
		if err := s.syncAttachments(tx, message.ID, in.Attachments); err != nil {
			return err
		}
	}

	if in.Tags != nil {
		if err := syncTags(tx, message.ID, in.Tags); err != nil {
			return err
		}
	}

	return nil
}

func syncRecipients(tx *gorm.DB, messageID string, recipients []resolvedRecipient) error {
	if err := tx.Where("message_id = ?", messageID).Delete(&model.MessageRecipient{}).Error; err != nil {
		return fmt.Errorf("clear recipients: %w", err)
	}

	rows := make([]model.MessageRecipient, 0, len(recipients))

	for _, recipient := range recipients {
		rows = append(rows, model.MessageRecipient{
			MessageID:  messageID,
			IdentityID: recipient.identity.ID,
			Role:       recipient.role,
		})
	}

	if len(rows) > 0 {
		if err := tx.Omit(clause.Associations).Create(&rows).Error; err != nil {
			return fmt.Errorf("create recipients: %w", err)
		}
	}

	return nil
}

func (s *Service) syncAttachments(tx *gorm.DB, messageID string, attachments []AttachmentInput) error {
	if err := tx.Where("message_id = ?", messageID).Delete(&model.Attachment{}).Error; err != nil {
		return fmt.Errorf("clear attachments: %w", err)
	}

	rows := make([]model.Attachment, 0, len(attachments))

	for _, attachment := range attachments {
		storagePath := attachment.StoragePath

		if path, ok := s.blobPath(attachment.SHA256); ok {
			storagePath = path
		}

		rows = append(rows, model.Attachment{
			MessageID:   messageID,
			ExternalID:  attachment.ExternalID,
			Filename:    attachment.Filename,
			MimeType:    attachment.MimeType,
			Size:        attachment.Size,
			SHA256:      attachment.SHA256,
			StoragePath: storagePath,
			URL:         attachment.URL,
			Metadata:    model.JSON(attachment.Metadata),
		})
	}

	if len(rows) > 0 {
		if err := tx.Omit(clause.Associations).Create(&rows).Error; err != nil {
			return fmt.Errorf("create attachments: %w", err)
		}
	}

	return nil
}

// blobPath returns the local blob path for a checksum, when it was uploaded to
// this hub.
func (s *Service) blobPath(checksum string) (string, bool) {
	if s.blobs == nil || !s.blobs.Exists(checksum) {
		return "", false
	}

	path, err := s.blobs.Path(checksum)
	if err != nil {
		return "", false
	}

	return path, true
}

func syncTags(tx *gorm.DB, messageID string, names []string) error {
	if err := tx.Where("message_id = ?", messageID).Delete(&model.MessageTag{}).Error; err != nil {
		return fmt.Errorf("clear tags: %w", err)
	}

	seen := make(map[string]struct{}, len(names))

	for _, name := range names {
		slug := Slugify(name)
		if slug == "" {
			continue
		}

		if _, ok := seen[slug]; ok {
			continue
		}

		seen[slug] = struct{}{}

		var tag model.Tag

		err := tx.Where("slug = ?", slug).First(&tag).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			tag = model.Tag{Slug: slug, Name: strings.TrimSpace(name)}
			if err := tx.Omit(clause.Associations).Create(&tag).Error; err != nil {
				return fmt.Errorf("create tag: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("lookup tag: %w", err)
		}

		link := model.MessageTag{MessageID: messageID, TagID: tag.ID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Omit(clause.Associations).Create(&link).Error; err != nil {
			return fmt.Errorf("link tag: %w", err)
		}
	}

	return nil
}

func resolveSphere(tx *gorm.DB, slug string) (*model.Sphere, error) {
	var sphere model.Sphere

	err := tx.Where("slug = ?", slug).First(&sphere).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, Invalid("sphere", fmt.Sprintf("unknown sphere %q", slug))
	}

	if err != nil {
		return nil, fmt.Errorf("lookup sphere: %w", err)
	}

	return &sphere, nil
}

func resolveParent(tx *gorm.DB, accountID, externalID string) *string {
	if externalID == "" {
		return nil
	}

	var parent model.Message

	err := tx.Select("id").
		Where("account_id = ? AND external_id = ?", accountID, externalID).
		First(&parent).Error
	if err != nil {
		return nil
	}

	return &parent.ID
}

func accountIsSelf(account *model.Account, kind, normalized string) bool {
	if account.ExternalUser == "" {
		return false
	}

	externalKind := account.ExternalUserKind
	if externalKind == "" {
		externalKind = string(model.IdentityKindUsername)
	}

	return kind == externalKind &&
		NormalizeIdentity(kind, account.ExternalUser) == normalized
}

func contentHash(in *MessageInput, sender *model.Identity, sentAt time.Time) string {
	hasher := sha256.New()

	write := func(values ...string) {
		for _, value := range values {
			_, _ = hasher.Write([]byte(value))
			_, _ = hasher.Write([]byte{0})
		}
	}

	senderKey := ""
	if sender != nil {
		senderKey = sender.SourceID + ":" + sender.Kind + ":" + sender.NormalizedValue
	}

	attachmentKeys := make([]string, 0, len(in.Attachments))
	for _, attachment := range in.Attachments {
		attachmentKeys = append(attachmentKeys, fmt.Sprintf("%s:%s:%s:%d", attachment.Filename, attachment.MimeType, attachment.SHA256, attachment.Size))
	}

	sort.Strings(attachmentKeys)

	recipientKeys := make([]string, 0, len(in.Recipients))
	for _, recipient := range in.Recipients {
		recipientKeys = append(recipientKeys,
			recipient.Role+":"+NormalizeIdentity(recipient.Kind, recipient.Value))
	}

	sort.Strings(recipientKeys)

	tagKeys := make([]string, 0, len(in.Tags))
	for _, tag := range in.Tags {
		tagKeys = append(tagKeys, Slugify(tag))
	}

	sort.Strings(tagKeys)

	write(
		in.Subject,
		in.Body,
		in.Kind,
		in.BodyFormat,
		in.URL,
		in.Language,
		in.Preview,
		sentAt.Format(time.RFC3339Nano),
		senderKey,
		strings.Join(attachmentKeys, "|"),
		strings.Join(recipientKeys, "|"),
		strings.Join(tagKeys, "|"),
		canonicalJSON(in.Metadata),
	)

	return hex.EncodeToString(hasher.Sum(nil))
}

func canonicalJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}

	canonical, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}

	return string(canonical)
}

func utcPtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}

	utc := value.UTC()

	return &utc
}
