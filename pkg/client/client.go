// Package client is a small HTTP client for the CommHub REST API. It is used by
// the CLI subcommands and can be reused by other tools.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
// authentication.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
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

func (c *Client) get(ctx context.Context, path string, params url.Values, result any) error {
	endpoint := c.baseURL + path
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}

	request.Header.Set("Accept", "application/json")

	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request %s: %w", endpoint, err)
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return newAPIError(response.StatusCode, body)
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
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
