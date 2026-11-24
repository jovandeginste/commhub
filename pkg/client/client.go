// Package client is a small HTTP client for the CommHub REST API. It is used by
// the CLI subcommands and can be reused by other tools.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jovandeginste/commhub/pkg/model"
)

// Client talks to a running CommHub.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// MessagePage is one page of messages returned by the query endpoint.
type MessagePage struct {
	Messages   []model.Message `json:"messages"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// SourceStats is the per-source breakdown of the hub statistics.
type SourceStats struct {
	Source   string `json:"source"`
	Messages int64  `json:"messages"`
}

// Stats is the response of GET /api/v1/stats.
type Stats struct {
	Messages       int64         `json:"messages"`
	Unread         int64         `json:"unread"`
	Sources        int64         `json:"sources"`
	Accounts       int64         `json:"accounts"`
	Channels       int64         `json:"channels"`
	Correspondents int64         `json:"correspondents"`
	Identities     int64         `json:"identities"`
	Tags           int64         `json:"tags"`
	BySource       []SourceStats `json:"by_source"`
	EarliestSentAt *time.Time    `json:"earliest_sent_at,omitempty"`
	LatestSentAt   *time.Time    `json:"latest_sent_at,omitempty"`
}

type cursorList struct {
	Cursors []model.Cursor `json:"cursors"`
}

// APIError is a non-2xx response from the API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("API returned %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	}

	return fmt.Sprintf("API returned %d: %s", e.StatusCode, e.Message)
}

// New returns a client for baseURL. token is optional when the API runs without
// authentication. A unix listen address ("unix:/run/.../commhub.sock") is
// accepted as well; the client then dials the socket.
func New(baseURL, token string) *Client {
	client := &Client{
		token: strings.TrimSpace(token),
		http:  &http.Client{Timeout: 30 * time.Second},
	}

	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")

	if path, ok := unixSocketPath(baseURL); ok {
		// The host in the URL is irrelevant: every request goes over the
		// socket.
		client.baseURL = "http://commhub"
		client.http.Transport = &http.Transport{ //nolint:exhaustruct_v5 // defaults are fine
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
		}
	} else {
		client.baseURL = baseURL
	}

	return client
}

// Messages queries messages. params are passed as-is to GET /api/v1/messages.
func (c *Client) Messages(ctx context.Context, params url.Values) (*MessagePage, error) {
	var page MessagePage

	if err := c.get(ctx, "/api/v1/messages", params, &page); err != nil {
		return nil, err
	}

	return &page, nil
}

// Message fetches one message by id.
func (c *Client) Message(ctx context.Context, id string) (*model.Message, error) {
	var message model.Message

	if err := c.get(ctx, "/api/v1/messages/"+url.PathEscape(id), nil, &message); err != nil {
		return nil, err
	}

	return &message, nil
}

// Stats fetches the hub statistics.
func (c *Client) Stats(ctx context.Context) (*Stats, error) {
	var stats Stats

	if err := c.get(ctx, "/api/v1/stats", nil, &stats); err != nil {
		return nil, err
	}

	return &stats, nil
}

// Cursors lists the connector cursors stored in the hub.
func (c *Client) Cursors(ctx context.Context) ([]model.Cursor, error) {
	var list cursorList

	if err := c.get(ctx, "/api/v1/cursors", nil, &list); err != nil {
		return nil, err
	}

	return list.Cursors, nil
}

// Cursor fetches one connector cursor.
func (c *Client) Cursor(ctx context.Context, key string) (*model.Cursor, error) {
	var cursor model.Cursor

	if err := c.get(ctx, "/api/v1/cursors/"+url.PathEscape(key), nil, &cursor); err != nil {
		return nil, err
	}

	return &cursor, nil
}

// SetCursor stores a connector cursor.
func (c *Client) SetCursor(ctx context.Context, key, value string) (*model.Cursor, error) {
	var cursor model.Cursor

	body := map[string]string{"value": value}

	if err := c.do(ctx, http.MethodPut, "/api/v1/cursors/"+url.PathEscape(key), nil, body, &cursor); err != nil {
		return nil, err
	}

	return &cursor, nil
}

func (c *Client) get(ctx context.Context, path string, params url.Values, result any) error {
	return c.do(ctx, http.MethodGet, path, params, nil, result)
}

func (c *Client) do(ctx context.Context, method, path string, params url.Values, body, result any) error {
	endpoint := c.baseURL + path
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	var payload io.Reader

	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}

		payload = bytes.NewReader(data)
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return err
	}

	request.Header.Set("Accept", "application/json")

	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, endpoint, err)
	}

	defer response.Body.Close()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return newAPIError(response.StatusCode, raw)
	}

	if result == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}

	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// unixSocketPath reports the socket path for a "unix:<path>" base URL.
func unixSocketPath(address string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(address), "unix:")
	if !ok {
		return "", false
	}

	rest = strings.TrimPrefix(rest, "//")
	if rest == "" {
		return "", false
	}

	return rest, true
}

func newAPIError(status int, body []byte) error {
	var payload struct {
		Error string `json:"error"`
	}

	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		return &APIError{StatusCode: status, Message: payload.Error}
	}

	return &APIError{StatusCode: status, Message: strings.TrimSpace(string(body))}
}
