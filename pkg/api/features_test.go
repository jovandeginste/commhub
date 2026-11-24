package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jovandeginste/commhub/pkg/api"
	"github.com/jovandeginste/commhub/pkg/blobs"
	"github.com/jovandeginste/commhub/pkg/client"
	"github.com/jovandeginste/commhub/pkg/ingest"
	"github.com/jovandeginste/commhub/pkg/model"
	"github.com/jovandeginste/commhub/pkg/store"
)

func newBlobEnv(t *testing.T) (*testEnv, *blobs.Store) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := store.Open(":memory:", logger)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(db))
	require.NoError(t, store.Seed(db))

	blobStore := blobs.New(t.TempDir())

	server := api.New(db, db, ingest.New(db, logger), api.Config{
		BatchSize: 10,
		Blobs:     blobStore,
	}, logger)

	return &testEnv{server: server, db: db}, blobStore
}

func TestDeleteMessage(t *testing.T) {
	env := newTestEnv(t, nil)

	response := env.do(t, http.MethodPost, "/api/v1/messages",
		payload("post-1", "Hello", "2026-11-24T09:00:00Z"), "")
	require.Equal(t, http.StatusCreated, response.Code)

	var created ingest.Result
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))

	response = env.do(t, http.MethodDelete, "/api/v1/messages/"+created.ID, nil, "")
	require.Equal(t, http.StatusNoContent, response.Code)

	response = env.do(t, http.MethodGet, "/api/v1/messages/"+created.ID, nil, "")
	assert.Equal(t, http.StatusNotFound, response.Code)

	response = env.do(t, http.MethodDelete, "/api/v1/messages/"+created.ID, nil, "")
	assert.Equal(t, http.StatusNotFound, response.Code)

	response = env.do(t, http.MethodGet, "/api/v1/messages", nil, "")
	require.Equal(t, http.StatusOK, response.Code)

	var list struct {
		Messages []model.Message `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	assert.Empty(t, list.Messages)

	// The delete cascades to recipients, attachments and tag links.
	for _, entry := range []struct {
		name  string
		model any
	}{
		{"messages", &model.Message{}},
		{"message_recipients", &model.MessageRecipient{}},
		{"message_tags", &model.MessageTag{}},
	} {
		var count int64

		require.NoError(t, env.db.Model(entry.model).Count(&count).Error)
		assert.Zero(t, count, entry.name)
	}
}

func TestIngestDeletedInSource(t *testing.T) {
	env := newTestEnv(t, nil)

	response := env.do(t, http.MethodPost, "/api/v1/messages",
		payload("post-1", "Hello", "2026-11-24T09:00:00Z"), "")
	require.Equal(t, http.StatusCreated, response.Code)

	var created ingest.Result
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))

	deleted := payload("post-1", "Hello", "2026-11-24T09:00:00Z")
	deleted["deleted_in_source_at"] = "2026-11-24T10:00:00Z"

	response = env.do(t, http.MethodPost, "/api/v1/messages", deleted, "")
	require.Equal(t, http.StatusOK, response.Code)

	var updated ingest.Result
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &updated))
	assert.Equal(t, model.ResultUpdated, updated.Status)

	response = env.do(t, http.MethodGet, "/api/v1/messages/"+created.ID, nil, "")
	require.Equal(t, http.StatusOK, response.Code)

	var message model.Message
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &message))
	require.NotNil(t, message.DeletedInSourceAt)
	assert.Equal(t, time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC), message.DeletedInSourceAt.UTC())

	// A deletion for a message we never saw is a no-op.
	deleted["external_id"] = "unknown"
	response = env.do(t, http.MethodPost, "/api/v1/messages", deleted, "")
	require.Equal(t, http.StatusOK, response.Code)

	var missing ingest.Result
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &missing))
	assert.Equal(t, model.ResultUnchanged, missing.Status)
}

func TestAttachmentUploadAndServe(t *testing.T) {
	env, blobStore := newBlobEnv(t)

	content := "attachment body"
	sum := sha256.Sum256([]byte(content))
	checksum := hex.EncodeToString(sum[:])

	request := httptest.NewRequest(http.MethodPut, "/api/v1/attachments/"+checksum,
		strings.NewReader(content))
	recorder := httptest.NewRecorder()
	env.server.Handler().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	// Uploading the same blob again is idempotent.
	request = httptest.NewRequest(http.MethodPut, "/api/v1/attachments/"+checksum,
		strings.NewReader(content))
	recorder = httptest.NewRecorder()
	env.server.Handler().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)

	assert.True(t, blobStore.Exists(checksum))

	request = httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+checksum, nil)
	recorder = httptest.NewRecorder()
	env.server.Handler().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, content, recorder.Body.String())
	assert.Equal(t, `"`+checksum+`"`, recorder.Header().Get("ETag"))
	assert.Contains(t, recorder.Header().Get("Content-Type"), "text/plain")

	request = httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+strings.Repeat("0", 64), nil)
	recorder = httptest.NewRecorder()
	env.server.Handler().ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestAttachmentUploadRejectsMismatch(t *testing.T) {
	env, _ := newBlobEnv(t)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/attachments/"+strings.Repeat("a", 64),
		strings.NewReader("does not match"))
	recorder := httptest.NewRecorder()
	env.server.Handler().ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestIngestLinksUploadedAttachment(t *testing.T) {
	env, blobStore := newBlobEnv(t)

	content := "linked blob"
	sum := sha256.Sum256([]byte(content))
	checksum := hex.EncodeToString(sum[:])

	path, err := blobStore.Put(checksum, strings.NewReader(content))
	require.NoError(t, err)

	message := payload("post-1", "With attachment", "2026-11-24T09:00:00Z")
	message["attachments"] = []map[string]any{{
		"filename": "note.txt",
		"sha256":   checksum,
		"size":     len(content),
	}}

	response := env.do(t, http.MethodPost, "/api/v1/messages", message, "")
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

	var created ingest.Result
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))

	response = env.do(t, http.MethodGet, "/api/v1/messages/"+created.ID, nil, "")
	require.Equal(t, http.StatusOK, response.Code)

	var stored model.Message
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &stored))
	require.Len(t, stored.Attachments, 1)
	assert.Equal(t, path, stored.Attachments[0].StoragePath)
	assert.Equal(t, "/api/v1/attachments/"+checksum, stored.Attachments[0].URL)
}

func TestTokenScopes(t *testing.T) {
	env := newTestEnv(t, []string{"read:read-token", "write:write-token"})

	assert.Equal(t, http.StatusUnauthorized, env.do(t, http.MethodGet, "/api/v1/messages", nil, "").Code)
	assert.Equal(t, http.StatusUnauthorized,
		env.do(t, http.MethodGet, "/api/v1/messages", nil, "wrong").Code)

	assert.Equal(t, http.StatusOK,
		env.do(t, http.MethodGet, "/api/v1/messages", nil, "read-token").Code)
	assert.Equal(t, http.StatusForbidden,
		env.do(t, http.MethodPost, "/api/v1/messages",
			payload("post-1", "Hello", "2026-11-24T09:00:00Z"), "read-token").Code)

	assert.Equal(t, http.StatusForbidden,
		env.do(t, http.MethodGet, "/api/v1/messages", nil, "write-token").Code)
	assert.Equal(t, http.StatusCreated,
		env.do(t, http.MethodPost, "/api/v1/messages",
			payload("post-1", "Hello", "2026-11-24T09:00:00Z"), "write-token").Code)
}

func TestCursorAPI(t *testing.T) {
	env := newTestEnv(t, nil)

	response := env.do(t, http.MethodGet, "/api/v1/cursors", nil, "")
	require.Equal(t, http.StatusOK, response.Code)

	response = env.do(t, http.MethodPut, "/api/v1/cursors/teams%3Awork", map[string]string{"value": "123"}, "")
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

	var cursor model.Cursor
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &cursor))
	assert.Equal(t, "123", cursor.Value)

	response = env.do(t, http.MethodPut, "/api/v1/cursors/teams%3Awork", map[string]string{"value": "456"}, "")
	require.Equal(t, http.StatusOK, response.Code)

	response = env.do(t, http.MethodGet, "/api/v1/cursors/teams%3Awork", nil, "")
	require.Equal(t, http.StatusOK, response.Code)

	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &cursor))
	assert.Equal(t, "456", cursor.Value)

	response = env.do(t, http.MethodGet, "/api/v1/cursors", nil, "")
	require.Equal(t, http.StatusOK, response.Code)

	var list struct {
		Cursors []model.Cursor `json:"cursors"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	require.Len(t, list.Cursors, 1)
	assert.Equal(t, "teams:work", list.Cursors[0].Key)

	response = env.do(t, http.MethodDelete, "/api/v1/cursors/teams%3Awork", nil, "")
	require.Equal(t, http.StatusNoContent, response.Code)

	response = env.do(t, http.MethodGet, "/api/v1/cursors/teams%3Awork", nil, "")
	assert.Equal(t, http.StatusNotFound, response.Code)
}

func TestUnixSocketListen(t *testing.T) {
	env := newTestEnv(t, nil)

	socket := filepath.Join(t.TempDir(), "commhub.sock")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)

	go func() { done <- env.server.StartContext(ctx, "unix:"+socket) }()

	require.Eventually(t, func() bool {
		_, err := os.Stat(socket)

		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	apiClient := client.New("unix:"+socket, "")

	page, err := apiClient.Messages(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, page.Messages)

	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}
