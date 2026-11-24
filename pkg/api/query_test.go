package api_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jovandeginste/commhub/pkg/ingest"
	"github.com/jovandeginste/commhub/pkg/model"
)

func (e *testEnv) ingestMessages(t *testing.T, payloads ...map[string]any) map[string]string {
	t.Helper()

	batch := make([]map[string]any, 0, len(payloads))
	batch = append(batch, payloads...)

	response := e.do(t, http.MethodPost, "/api/v1/messages/batch", batch, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var result ingest.BatchResult
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Zero(t, result.Failed)

	ids := make(map[string]string, len(result.Results))
	for _, item := range result.Results {
		ids[item.ExternalID] = item.ID
	}

	return ids
}

func (e *testEnv) queryExternalIDs(t *testing.T, query string) []string {
	t.Helper()

	response := e.do(t, http.MethodGet, "/api/v1/messages?"+query, nil, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var list struct {
		Messages []model.Message `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))

	ids := make([]string, 0, len(list.Messages))
	for _, message := range list.Messages {
		ids = append(ids, message.ExternalID)
	}

	sort.Strings(ids)

	return ids
}

func queryTestMessages() []map[string]any {
	first := payload("m1", "first message", "2026-11-24T09:00:00Z")
	first["importance"] = 5
	first["language"] = "en"
	first["received_at"] = "2026-06-01T00:00:00Z"
	first["attachments"] = []map[string]any{{
		"filename":  "report.png",
		"mime_type": "image/png",
		"sha256":    "aaa",
	}}

	second := payload("m2", "second message", "2026-11-24T10:00:00Z")
	second["sender"] = map[string]any{"kind": "username", "value": "alice"}
	second["subject"] = "Second"
	second["importance"] = 1
	second["tags"] = []string{"urgent", "done"}

	third := payload("m3", "mail body", "2026-11-24T08:00:00Z")
	third["source"] = "mail"
	third["source_kind"] = "email"
	third["account"] = "private"
	third["channel"] = map[string]any{"external_id": "INBOX", "name": "Inbox", "kind": "folder"}
	third["sender"] = map[string]any{"kind": "email", "value": "Alice@Example.org", "display_name": "Alice"}
	third["subject"] = "Invoice"
	third["kind"] = "email"
	third["body_format"] = "plain"
	third["sphere"] = "personal"
	third["tags"] = []string{"invoice"}
	third["importance"] = 3
	third["thread_id"] = "thread-3"
	third["attachments"] = []map[string]any{{
		"filename":  "invoice.pdf",
		"mime_type": "application/pdf",
		"sha256":    "bbb",
	}}

	fourth := payload("m4", "notification", "2026-11-24T11:00:00Z")
	fourth["source"] = "teams"
	fourth["source_kind"] = "chat"
	fourth["channel"] = map[string]any{"external_id": "chan-9", "name": "Team A", "kind": "channel"}
	fourth["sender"] = map[string]any{"kind": "username", "value": "bob"}
	fourth["kind"] = "notification"
	fourth["body_format"] = "plain"
	fourth["sphere"] = ""
	fourth["thread_id"] = "thread-4"
	fourth["importance"] = 2
	fourth["tags"] = []string{}
	fourth["recipients"] = []map[string]any{}

	return []map[string]any{first, second, third, fourth}
}

func TestMessageQueryFilters(t *testing.T) {
	env := newTestEnv(t, nil)

	ids := env.ingestMessages(t, queryTestMessages()...)

	// Make state and deletion differ between messages.
	for _, patch := range []struct {
		externalID string
		body       map[string]any
	}{
		{"m2", map[string]any{"is_read": true, "is_starred": true}},
		{"m3", map[string]any{"is_read": true, "is_archived": true}},
	} {
		response := env.do(t, http.MethodPatch, "/api/v1/messages/"+ids[patch.externalID], patch.body, "")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}

	require.NoError(t, env.db.Model(&model.Message{}).
		Where("external_id = ?", "m4").
		Update("deleted_at", time.Now().UTC()).Error)

	var alice model.Correspondent
	require.NoError(t, env.db.Where("display_name = ?", "alice").First(&alice).Error)

	// "me" exists once per source (a Mattermost username and a mail
	// address), so pick the Mattermost one explicitly.
	var meIdentity model.Identity
	require.NoError(t, env.db.Joins("JOIN sources ON sources.id = identities.source_id").
		Where("identities.value = ? AND sources.slug = ?", "me", "mattermost").
		First(&meIdentity).Error)

	var me model.Correspondent
	require.NoError(t, env.db.First(&me, "id = ?", meIdentity.CorrespondentID).Error)

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"external id", "external_id=m2", []string{"m2"}},
		{"internal id", "id=" + ids["m2"], []string{"m2"}},
		{"thread", "thread_id=thread-4", []string{"m4"}},
		{"source slug", "source=mail", []string{"m3"}},
		{"source kind", "source_kind=chat", []string{"m1", "m2", "m4"}},
		{"account", "account=private", []string{"m3"}},
		{"channel external id", "channel=chan-9", []string{"m4"}},
		{"channel name", "channel_name=Team", []string{"m4"}},
		{"channel kind", "channel_kind=folder", []string{"m3"}},
		{"sender username", "sender=alice", []string{"m2"}},
		{"sender email", "sender=alice@example.org", []string{"m3"}},
		{"sender kind", "sender=ALICE@EXAMPLE.ORG&sender_kind=email", []string{"m3"}},
		{"sender correspondent", "sender_correspondent=" + alice.ID, []string{"m2"}},
		{"any correspondent", "correspondent_id=" + alice.ID, []string{"m2"}},
		{"recipient handle", "recipient=me&recipient_role=mention", []string{"m1", "m2", "m3"}},
		{"recipient id", "recipient_id=" + meIdentity.ID, []string{"m1", "m2"}},
		{"recipient correspondent", "recipient_correspondent=" + me.ID, []string{"m1", "m2"}},
		{"sphere", "sphere=personal", []string{"m3"}},
		{"no sphere", "no_sphere=true", []string{"m4"}},
		{"tag any", "tag=urgent", []string{"m1", "m2"}},
		{"tag all", "tag=urgent&tag=done&tag_mode=all", []string{"m2"}},
		{"tag list", "tag=urgent,done&tag_mode=any", []string{"m1", "m2"}},
		{"kind", "kind=notification", []string{"m4"}},
		{"body format", "body_format=plain", []string{"m3", "m4"}},
		{"language", "language=en", []string{"m1"}},
		{"subject", "subject=invoice", []string{"m3"}},
		{"body", "body=second", []string{"m2"}},
		{"free text", "q=mail+body", []string{"m3"}},
		{"importance min", "importance_min=3", []string{"m1", "m3"}},
		{"importance max", "importance_max=1", []string{"m2"}},
		{"read", "is_read=true", []string{"m2", "m3"}},
		{"unread", "is_read=false", []string{"m1", "m4"}},
		{"unread alias", "unread=true", []string{"m1", "m4"}},
		{"starred", "is_starred=true", []string{"m2"}},
		{"archived", "is_archived=true", []string{"m3"}},
		{"attachments", "has_attachments=true", []string{"m1", "m3"}},
		{"no attachments", "has_attachments=false", []string{"m2", "m4"}},
		{"attachment mime", "attachment_mime=image/", []string{"m1"}},
		{"attachment filename", "attachment_filename=invoice", []string{"m3"}},
		{"deleted", "deleted=true", []string{"m4"}},
		{"not deleted", "deleted=false", []string{"m1", "m2", "m3"}},
		{"received since", "received_since=2026-06-01", []string{"m1"}},
		{"since", "since=2026-11-24T10:00:00Z", []string{"m2", "m4"}},
		{"until", "until=2026-11-24T09:00:00Z", []string{"m1", "m3"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, env.queryExternalIDs(t, test.query))
		})
	}
}

func TestMessageQuerySorting(t *testing.T) {
	env := newTestEnv(t, nil)
	env.ingestMessages(t, queryTestMessages()...)

	response := env.do(t, http.MethodGet,
		"/api/v1/messages?sort=importance&order=asc&limit=2", nil, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var firstPage struct {
		Messages   []model.Message `json:"messages"`
		NextCursor string          `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &firstPage))
	require.Len(t, firstPage.Messages, 2)
	assert.Equal(t, "m2", firstPage.Messages[0].ExternalID)
	assert.Equal(t, "m4", firstPage.Messages[1].ExternalID)
	require.NotEmpty(t, firstPage.NextCursor)

	response = env.do(t, http.MethodGet,
		"/api/v1/messages?sort=importance&order=asc&limit=2&cursor="+firstPage.NextCursor, nil, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var secondPage struct {
		Messages   []model.Message `json:"messages"`
		NextCursor string          `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &secondPage))
	require.Len(t, secondPage.Messages, 2)
	assert.Equal(t, "m3", secondPage.Messages[0].ExternalID)
	assert.Equal(t, "m1", secondPage.Messages[1].ExternalID)
	assert.Empty(t, secondPage.NextCursor)

	// The cursor belongs to its sort order.
	response = env.do(t, http.MethodGet,
		"/api/v1/messages?sort=sent_at&cursor="+firstPage.NextCursor, nil, "")
	assert.Equal(t, http.StatusBadRequest, response.Code)

	// Default order is newest first.
	response = env.do(t, http.MethodGet, "/api/v1/messages?sort=importance", nil, "")
	require.Equal(t, http.StatusOK, response.Code)

	var defaultOrder struct {
		Messages []model.Message `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &defaultOrder))
	require.Len(t, defaultOrder.Messages, 4)
	assert.Equal(t, "m1", defaultOrder.Messages[0].ExternalID)
}

func TestMessageQueryValidation(t *testing.T) {
	env := newTestEnv(t, nil)

	for _, query := range []string{
		"sort=bogus",
		"order=sideways",
		"is_read=maybe",
		"is_archived=maybe",
		"has_attachments=maybe",
		"deleted=maybe",
		"importance_min=high",
		"importance_max=low",
		"since=yesterday",
		"until=2026-13-01",
		"tag=urgent&tag_mode=some",
		"cursor=not-a-cursor",
	} {
		t.Run(query, func(t *testing.T) {
			response := env.do(t, http.MethodGet, "/api/v1/messages?"+query, nil, "")
			assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		})
	}
}
