package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jovandeginste/commhub/pkg/client"
)

func TestMessagesSendsQueryAndToken(t *testing.T) {
	var (
		gotQuery url.Values
		gotPath  string
		gotAuth  string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"messages": [{"id": "m1", "external_id": "e1", "body": "hello"}],
			"next_cursor": "next"
		}`))
	}))
	defer server.Close()

	page, err := client.New(server.URL+"/", "secret").Messages(context.Background(), url.Values{
		"source": {"mail"},
		"tag":    {"urgent", "done"},
	})
	require.NoError(t, err)

	assert.Equal(t, "/api/v1/messages", gotPath)
	assert.Equal(t, "Bearer secret", gotAuth)
	assert.Equal(t, []string{"mail"}, gotQuery["source"])
	assert.Equal(t, []string{"urgent", "done"}, gotQuery["tag"])

	require.Len(t, page.Messages, 1)
	assert.Equal(t, "m1", page.Messages[0].ID)
	assert.Equal(t, "hello", page.Messages[0].Body)
	assert.Equal(t, "next", page.NextCursor)
}

func TestMessageFetchesOneMessage(t *testing.T) {
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": "m1", "body": "hello"}`))
	}))
	defer server.Close()

	message, err := client.New(server.URL, "").Message(context.Background(), "m1")
	require.NoError(t, err)

	assert.Equal(t, "/api/v1/messages/m1", gotPath)
	assert.Equal(t, "m1", message.ID)
	assert.Equal(t, "hello", message.Body)
}

func TestAPIErrorIsReturned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error": "invalid tag_mode \"bogus\""}`))
	}))
	defer server.Close()

	_, err := client.New(server.URL, "").Messages(context.Background(), url.Values{
		"tag_mode": {"bogus"},
	})
	require.Error(t, err)

	var apiErr *client.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, `invalid tag_mode "bogus"`, apiErr.Message)
}

func TestNonJSONErrorFallsBackToBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream broke"))
	}))
	defer server.Close()

	_, err := client.New(server.URL, "").Message(context.Background(), "m1")
	require.Error(t, err)

	var apiErr *client.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.Equal(t, "upstream broke", apiErr.Message)
}
