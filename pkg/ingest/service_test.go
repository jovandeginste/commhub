package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jovandeginste/commhub/pkg/ingest"
	"github.com/jovandeginste/commhub/pkg/model"
	"github.com/jovandeginste/commhub/pkg/store"
)

func newTestService(t *testing.T) (*ingest.Service, *gorm.DB) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := store.Open(":memory:", logger)
	require.NoError(t, err)

	require.NoError(t, store.Migrate(db))
	require.NoError(t, store.Seed(db))

	return ingest.New(db, logger), db
}

func baseMessage() ingest.MessageInput {
	sentAt := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

	return ingest.MessageInput{
		Source:     "mattermost",
		SourceKind: "chat",
		Account:    "work",
		Channel: ingest.ChannelInput{
			ExternalID: "channel-42",
			Name:       "general",
			Kind:       string(model.ChannelKindChannel),
			Sphere:     "work",
		},
		ExternalID: "post-1001",
		Sender: &ingest.IdentityInput{
			Kind:        string(model.IdentityKindUsername),
			Value:       "jdoe",
			DisplayName: "John Doe",
		},
		Recipients: []ingest.RecipientInput{{
			ingest.IdentityInput{
				Kind:  string(model.IdentityKindUsername),
				Value: "me",
			},
			string(model.RecipientRoleMention),
		}},
		Body:       "Hello @me",
		BodyFormat: string(model.BodyFormatMarkdown),
		SentAt:     &sentAt,
		Sphere:     "work",
		Tags:       []string{"Urgent", "follow up"},
		Metadata:   []byte(`{"reactions":2}`),
		Raw:        []byte(`{"post_id":"post-1001"}`),
	}
}

func TestIngestIsIdempotent(t *testing.T) {
	service, db := newTestService(t)
	ctx := context.Background()

	input := baseMessage()

	result, err := service.Ingest(ctx, &input)
	require.NoError(t, err)
	assert.Equal(t, model.ResultCreated, result.Status)

	var message model.Message
	require.NoError(t, db.Preload("Tags").First(&message).Error)

	assert.Equal(t, "post-1001", message.ExternalID)
	assert.Equal(t, string(model.MessageKindChat), message.Kind)
	assert.Equal(t, string(model.DirectionInbound), message.Direction)
	require.NotNil(t, message.SphereID)
	assert.Len(t, message.Tags, 2)

	var correspondent model.Correspondent
	require.NoError(t, db.Where("display_name = ?", "John Doe").First(&correspondent).Error)
	assert.False(t, correspondent.IsSelf)

	var identityCount int64
	require.NoError(t, db.Model(&model.Identity{}).Count(&identityCount).Error)
	assert.EqualValues(t, 2, identityCount, "sender and recipient")

	// Replaying the same message must not create a second row.
	replay := baseMessage()

	result, err = service.Ingest(ctx, &replay)
	require.NoError(t, err)
	assert.Equal(t, model.ResultUnchanged, result.Status)

	var messageCount int64
	require.NoError(t, db.Model(&model.Message{}).Count(&messageCount).Error)
	assert.EqualValues(t, 1, messageCount)

	// Local state must survive a replay.
	require.NoError(t, db.Model(&model.Message{}).Where("id = ?", message.ID).
		Update("is_read", true).Error)

	result, err = service.Ingest(ctx, &replay)
	require.NoError(t, err)
	assert.Equal(t, model.ResultUnchanged, result.Status)

	require.NoError(t, db.First(&message, "id = ?", message.ID).Error)
	assert.True(t, message.IsRead)

	// A real content change updates the message and replaces tags.
	changed := baseMessage()
	changed.Body = "Hello again"
	changed.Tags = []string{"follow up"}

	result, err = service.Ingest(ctx, &changed)
	require.NoError(t, err)
	assert.Equal(t, model.ResultUpdated, result.Status)

	require.NoError(t, db.Preload("Tags").First(&message, "id = ?", message.ID).Error)
	assert.Equal(t, "Hello again", message.Body)
	assert.True(t, message.IsRead)
	assert.True(t, message.EditedAt != nil)
	assert.Len(t, message.Tags, 1)
}

func TestIngestBatchContinuesAfterFailure(t *testing.T) {
	service, db := newTestService(t)
	ctx := context.Background()

	first := baseMessage()

	broken := baseMessage()
	broken.ExternalID = "post-1002"
	broken.Sphere = "does-not-exist"

	third := baseMessage()
	third.ExternalID = "post-1003"

	result, err := service.IngestBatch(ctx,
		[]ingest.MessageInput{first, broken, third}, model.IngestModeBatch)
	require.NoError(t, err)

	assert.Equal(t, 3, result.Total)
	assert.Equal(t, 2, result.Created)
	assert.Equal(t, 1, result.Failed)
	assert.Len(t, result.Results, 3)
	assert.Empty(t, result.Results[0].Error)
	assert.Contains(t, result.Results[1].Error, "unknown sphere")
	assert.Empty(t, result.Results[2].Error)

	var batch model.IngestBatch
	require.NoError(t, db.Preload("Errors").First(&batch, "id = ?", result.BatchID).Error)
	assert.Equal(t, string(model.IngestStatusPartial), batch.Status)
	assert.Equal(t, 1, batch.Failed)
	assert.Len(t, batch.Errors, 1)
	assert.Equal(t, "post-1002", batch.Errors[0].ExternalID)

	var messageCount int64
	require.NoError(t, db.Model(&model.Message{}).Count(&messageCount).Error)
	assert.EqualValues(t, 2, messageCount)
}

func TestIngestStreamInChunks(t *testing.T) {
	service, db := newTestService(t)
	ctx := context.Background()

	first := baseMessage()
	second := baseMessage()
	second.ExternalID = "post-1002"

	var stream bytes.Buffer
	for _, input := range []ingest.MessageInput{first, second} {
		data, err := json.Marshal(input)
		require.NoError(t, err)
		stream.Write(data)
		stream.WriteByte('\n')
	}

	result, err := service.IngestStream(ctx, &stream, 1)
	require.NoError(t, err)

	assert.Equal(t, 2, result.Total)
	assert.Equal(t, 2, result.Created)
	assert.Len(t, result.Results, 2)
	assert.Equal(t, 1, result.Results[1].Index)

	var batches int64
	require.NoError(t, db.Model(&model.IngestBatch{}).
		Where("mode = ?", string(model.IngestModeStream)).
		Count(&batches).Error)
	assert.EqualValues(t, 2, batches)

	var messageCount int64
	require.NoError(t, db.Model(&model.Message{}).Count(&messageCount).Error)
	assert.EqualValues(t, 2, messageCount)
}

func TestIngestStreamRejectsMalformedLine(t *testing.T) {
	service, _ := newTestService(t)

	valid := baseMessage()
	data, err := json.Marshal(valid)
	require.NoError(t, err)

	stream := bytes.NewBuffer(data)
	stream.WriteString("\n{not json}\n")

	_, err = service.IngestStream(context.Background(), stream, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode NDJSON")
}

func TestIngestDetectsOwnMessages(t *testing.T) {
	service, db := newTestService(t)

	source := model.Source{Slug: "mattermost", Name: "Mattermost", Kind: "chat", Enabled: true}
	require.NoError(t, db.Create(&source).Error)

	account := model.Account{
		SourceID:         source.ID,
		Slug:             "work",
		Name:             "Work",
		ExternalUser:     "me",
		ExternalUserKind: string(model.IdentityKindUsername),
		Enabled:          true,
	}
	require.NoError(t, db.Create(&account).Error)

	outbound := baseMessage()
	outbound.ExternalID = "post-out"
	outbound.Sender = &ingest.IdentityInput{
		Kind:  string(model.IdentityKindUsername),
		Value: "ME",
	}

	result, err := service.Ingest(context.Background(), &outbound)
	require.NoError(t, err)

	var message model.Message
	require.NoError(t, db.First(&message, "id = ?", result.ID).Error)
	assert.Equal(t, string(model.DirectionOutbound), message.Direction)

	var correspondent model.Correspondent
	require.NoError(t, db.Where("is_self = ?", true).First(&correspondent).Error)
	assert.Equal(t, "ME", correspondent.DisplayName)

	inbound := baseMessage()
	inbound.ExternalID = "post-in"
	inbound.Sender = &ingest.IdentityInput{
		Kind:  string(model.IdentityKindUsername),
		Value: "jdoe",
	}

	result, err = service.Ingest(context.Background(), &inbound)
	require.NoError(t, err)

	var inboundMessage model.Message
	require.NoError(t, db.First(&inboundMessage, "id = ?", result.ID).Error)
	assert.Equal(t, string(model.DirectionInbound), inboundMessage.Direction)
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ingest.MessageInput)
		field  string
	}{
		{"missing source", func(in *ingest.MessageInput) { in.Source = "" }, "source"},
		{"missing channel", func(in *ingest.MessageInput) { in.Channel.ExternalID = "" }, "channel.external_id"},
		{"missing external id", func(in *ingest.MessageInput) { in.ExternalID = "" }, "external_id"},
		{"bad format", func(in *ingest.MessageInput) { in.BodyFormat = "rtf" }, "body_format"},
		{"bad direction", func(in *ingest.MessageInput) { in.Direction = "sideways" }, "direction"},
		{"bad role", func(in *ingest.MessageInput) { in.Recipients[0].Role = "boss" }, "recipients[0].role"},
		{"duplicate recipient", func(in *ingest.MessageInput) {
			in.Recipients = append(in.Recipients, in.Recipients[0])
		}, "recipients[1]"},
		{"empty attachment name", func(in *ingest.MessageInput) {
			in.Attachments = []ingest.AttachmentInput{{Filename: " "}}
		}, "attachments[0].filename"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := baseMessage()
			test.mutate(&input)

			err := input.Validate()
			require.Error(t, err)

			var validation *ingest.ValidationError
			require.ErrorAs(t, err, &validation)
			assert.Equal(t, test.field, validation.Field)
		})
	}
}

func TestNormalizeIdentity(t *testing.T) {
	assert.Equal(t, "jo@example.org", ingest.NormalizeIdentity("email", " Jo@Example.ORG "))
	assert.Equal(t, "+3212345678", ingest.NormalizeIdentity("phone", "+32 (12) 34-56-78"))
	assert.Equal(t, "jdoe", ingest.NormalizeIdentity("username", "JDoe"))
	assert.Equal(t, "CaseSensitive", ingest.NormalizeIdentity("handle", " CaseSensitive "))
}

func TestSlugify(t *testing.T) {
	assert.Equal(t, "school-x", ingest.Slugify("School X"))
	assert.Equal(t, "follow-up", ingest.Slugify("  Follow  up  "))
	assert.Equal(t, "", ingest.Slugify("!!!"))

	// Slugify and NormalizeIdentity are used as natural keys, so they must be
	// stable across runs.
	assert.Equal(t, ingest.Slugify("School X"), ingest.Slugify("school x"))
}
