package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jovandeginste/commhub/pkg/api"
	"github.com/jovandeginste/commhub/pkg/ingest"
	"github.com/jovandeginste/commhub/pkg/model"
	"github.com/jovandeginste/commhub/pkg/store"
)

type testEnv struct {
	server *api.Server
	db     *gorm.DB
}

func newTestEnv(t *testing.T, tokens []string) *testEnv {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := store.Open(":memory:", logger)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(db))
	require.NoError(t, store.Seed(db))

	server := api.New(db, ingest.New(db, logger), api.Config{
		Tokens:    tokens,
		BatchSize: 10,
	}, logger)

	return &testEnv{server: server, db: db}
}

func (e *testEnv) do(t *testing.T, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader

	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}

	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")

	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	recorder := httptest.NewRecorder()
	e.server.Handler().ServeHTTP(recorder, request)

	return recorder
}

func payload(externalID, body, sentAt string) map[string]any {
	return map[string]any{
		"source":      "mattermost",
		"source_kind": "chat",
		"account":     "work",
		"channel": map[string]any{
			"external_id": "channel-42",
			"name":        "general",
			"kind":        "channel",
			"sphere":      "work",
		},
		"external_id": externalID,
		"thread_id":   "thread-1",
		"sender": map[string]any{
			"kind":         "username",
			"value":        "jdoe",
			"display_name": "John Doe",
		},
		"recipients": []map[string]any{
			{"kind": "username", "value": "me", "role": "mention"},
		},
		"kind":        "chat",
		"body":        body,
		"body_format": "markdown",
		"sent_at":     sentAt,
		"sphere":      "work",
		"tags":        []string{"Urgent", "follow up"},
		"metadata":    map[string]any{"reactions": 2},
		"raw":         map[string]any{"post_id": externalID},
	}
}

func TestIngestReadAndPatch(t *testing.T) {
	env := newTestEnv(t, nil)

	response := env.do(t, http.MethodPost, "/api/v1/messages",
		payload("post-1", "Hello", "2026-11-24T09:00:00Z"), "")
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

	var created ingest.Result
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
	assert.Equal(t, model.ResultCreated, created.Status)
	require.NotEmpty(t, created.ID)

	response = env.do(t, http.MethodPost, "/api/v1/messages",
		payload("post-1", "Hello", "2026-11-24T09:00:00Z"), "")
	require.Equal(t, http.StatusOK, response.Code)

	var replay ingest.Result
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &replay))
	assert.Equal(t, model.ResultUnchanged, replay.Status)
	assert.Equal(t, created.ID, replay.ID)

	response = env.do(t, http.MethodGet, "/api/v1/messages", nil, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var list struct {
		Messages   []model.Message `json:"messages"`
		NextCursor string          `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	require.Len(t, list.Messages, 1)
	assert.Empty(t, list.NextCursor)
	assert.Equal(t, "general", list.Messages[0].Channel.Name)
	require.NotNil(t, list.Messages[0].Source)

	response = env.do(t, http.MethodGet, "/api/v1/messages/"+created.ID, nil, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var message model.Message
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &message))
	assert.Equal(t, "Hello", message.Body)
	require.NotNil(t, message.Sender)
	assert.Equal(t, "John Doe", message.Sender.DisplayName)
	require.Len(t, message.Recipients, 1)
	require.NotNil(t, message.Recipients[0].Identity)

	response = env.do(t, http.MethodPatch, "/api/v1/messages/"+created.ID, map[string]any{
		"is_read": true,
		"sphere":  "personal",
		"tags":    []string{"archived"},
	}, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &message))
	assert.True(t, message.IsRead)
	require.NotNil(t, message.Sphere)
	assert.Equal(t, "personal", message.Sphere.Slug)
	require.Len(t, message.Tags, 1)
	assert.Equal(t, "archived", message.Tags[0].Slug)

	response = env.do(t, http.MethodGet, "/api/v1/stats", nil, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var stats struct {
		Messages int64 `json:"messages"`
		Unread   int64 `json:"unread"`
		BySource []struct {
			Source   string `json:"source"`
			Messages int64  `json:"messages"`
		} `json:"by_source"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &stats))
	assert.EqualValues(t, 1, stats.Messages)
	assert.EqualValues(t, 0, stats.Unread)
	require.Len(t, stats.BySource, 1)
	assert.Equal(t, "mattermost", stats.BySource[0].Source)

	response = env.do(t, http.MethodGet, "/api/v1/sources", nil, "")
	require.Equal(t, http.StatusOK, response.Code)

	var sources struct {
		Sources []model.Source `json:"sources"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &sources))
	require.Len(t, sources.Sources, 1)
	require.Len(t, sources.Sources[0].Accounts, 1)
	assert.Equal(t, "work", sources.Sources[0].Accounts[0].Slug)
}

func TestAuthentication(t *testing.T) {
	env := newTestEnv(t, []string{"secret"})

	response := env.do(t, http.MethodGet, "/api/v1/messages", nil, "")
	assert.Equal(t, http.StatusUnauthorized, response.Code)

	response = env.do(t, http.MethodGet, "/api/v1/messages", nil, "wrong")
	assert.Equal(t, http.StatusUnauthorized, response.Code)

	response = env.do(t, http.MethodGet, "/api/v1/messages", nil, "secret")
	assert.Equal(t, http.StatusOK, response.Code)

	response = env.do(t, http.MethodGet, "/healthz", nil, "")
	assert.Equal(t, http.StatusOK, response.Code, "health is public")
}

func TestBatchAndCursorPagination(t *testing.T) {
	env := newTestEnv(t, nil)

	batch := []map[string]any{
		payload("post-1", "one", "2026-11-24T09:00:00Z"),
		payload("post-2", "two", "2026-11-24T10:00:00Z"),
		payload("post-3", "three", "2026-11-24T11:00:00Z"),
	}

	response := env.do(t, http.MethodPost, "/api/v1/messages/batch", batch, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var batchResult ingest.BatchResult
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &batchResult))
	assert.Equal(t, 3, batchResult.Created)
	assert.Equal(t, 0, batchResult.Failed)
	assert.Len(t, batchResult.Results, 3)

	response = env.do(t, http.MethodGet, "/api/v1/messages?limit=2", nil, "")
	require.Equal(t, http.StatusOK, response.Code)

	var firstPage struct {
		Messages   []model.Message `json:"messages"`
		NextCursor string          `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &firstPage))
	require.Len(t, firstPage.Messages, 2)
	assert.Equal(t, "post-3", firstPage.Messages[0].ExternalID)
	assert.Equal(t, "post-2", firstPage.Messages[1].ExternalID)
	require.NotEmpty(t, firstPage.NextCursor)

	response = env.do(t, http.MethodGet,
		"/api/v1/messages?limit=2&cursor="+firstPage.NextCursor, nil, "")
	require.Equal(t, http.StatusOK, response.Code)

	var secondPage struct {
		Messages   []model.Message `json:"messages"`
		NextCursor string          `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &secondPage))
	require.Len(t, secondPage.Messages, 1)
	assert.Equal(t, "post-1", secondPage.Messages[0].ExternalID)
	assert.Empty(t, secondPage.NextCursor)

	// Filters use natural keys.
	response = env.do(t, http.MethodGet, "/api/v1/messages?source=mattermost&unread=true", nil, "")
	require.Equal(t, http.StatusOK, response.Code)

	var filtered struct {
		Messages []model.Message `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &filtered))
	assert.Len(t, filtered.Messages, 3)
}

func TestValidationReturnsBadRequest(t *testing.T) {
	env := newTestEnv(t, nil)

	invalid := payload("", "no id", "2026-11-24T09:00:00Z")

	response := env.do(t, http.MethodPost, "/api/v1/messages", invalid, "")
	require.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "external_id")

	response = env.do(t, http.MethodGet, "/api/v1/messages/unknown", nil, "")
	assert.Equal(t, http.StatusNotFound, response.Code)

	response = env.do(t, http.MethodGet, "/api/v1/messages?limit=zero", nil, "")
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestCreateSphereAndCorrespondent(t *testing.T) {
	env := newTestEnv(t, nil)

	response := env.do(t, http.MethodPost, "/api/v1/spheres", map[string]any{
		"name": "School X",
	}, "")
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

	var sphere model.Sphere
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &sphere))
	assert.Equal(t, "school-x", sphere.Slug)

	// The source does not exist yet; correspondents need a known source.
	response = env.do(t, http.MethodPost, "/api/v1/correspondents", map[string]any{
		"display_name": "Alice",
		"sphere":       "school-x",
		"identities": []map[string]any{{
			"source": "unknown",
			"value":  "alice@example.org",
		}},
	}, "")
	assert.Equal(t, http.StatusBadRequest, response.Code)

	source := model.Source{Slug: "mail", Name: "Mail", Kind: "email", Enabled: true}
	require.NoError(t, env.db.Create(&source).Error)

	response = env.do(t, http.MethodPost, "/api/v1/correspondents", map[string]any{
		"display_name": "Alice",
		"sphere":       "school-x",
		"identities": []map[string]any{{
			"source": "mail",
			"kind":   "email",
			"value":  "Alice@Example.org",
		}},
	}, "")
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

	var correspondent model.Correspondent
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &correspondent))
	assert.Equal(t, "Alice", correspondent.DisplayName)
	require.Len(t, correspondent.Identities, 1)

	var identity model.Identity
	require.NoError(t, env.db.Where("correspondent_id = ?", correspondent.ID).
		First(&identity).Error)
	assert.Equal(t, "alice@example.org", identity.NormalizedValue)

	// Duplicate handles are a conflict, not a second correspondent.
	response = env.do(t, http.MethodPost, "/api/v1/correspondents", map[string]any{
		"display_name": "Alice Again",
		"identities": []map[string]any{{
			"source": "mail",
			"kind":   "email",
			"value":  "alice@example.org",
		}},
	}, "")
	assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())

	// A handle from another platform can be merged into the same person.
	mattermost := model.Source{Slug: "mattermost", Name: "Mattermost", Kind: "chat", Enabled: true}
	require.NoError(t, env.db.Create(&mattermost).Error)

	response = env.do(t, http.MethodPost,
		"/api/v1/correspondents/"+correspondent.ID+"/identities", map[string]any{
			"source": "mattermost",
			"kind":   "username",
			"value":  "alice",
		}, "")
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &correspondent))
	assert.Len(t, correspondent.Identities, 2)
}

func TestIngestStreamEndpoint(t *testing.T) {
	env := newTestEnv(t, nil)

	var stream bytes.Buffer

	for _, externalID := range []string{"post-1", "post-2"} {
		data, err := json.Marshal(payload(externalID, "hi", "2026-11-24T09:00:00Z"))
		require.NoError(t, err)
		stream.Write(data)
		stream.WriteByte('\n')
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/messages/stream", &stream)
	request.Header.Set("Content-Type", "application/x-ndjson")

	recorder := httptest.NewRecorder()
	env.server.Handler().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var result ingest.BatchResult
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	assert.Equal(t, 2, result.Total)
	assert.Equal(t, 2, result.Created)
}
