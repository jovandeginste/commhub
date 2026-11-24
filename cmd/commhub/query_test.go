package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testQueryCommand(t *testing.T) (*cobra.Command, *queryOptions) {
	t.Helper()

	opts := &queryOptions{}
	cmd := &cobra.Command{Use: "query"}
	addQueryFlags(cmd, opts)

	return cmd, opts
}

func TestQueryValuesDefaults(t *testing.T) {
	cmd, opts := testQueryCommand(t)

	values, err := queryValues(cmd, opts)
	require.NoError(t, err)

	assert.Equal(t, "20", values.Get("limit"))
	assert.Empty(t, values.Get("is_read"))
	assert.Empty(t, values.Get("is_archived"))
	assert.Empty(t, values.Get("importance_min"))
	assert.Empty(t, values["tag"])
}

func TestQueryValuesMapsFilters(t *testing.T) {
	cmd, opts := testQueryCommand(t)

	for flag, value := range map[string]string{
		"source":              "mail",
		"account":             "private",
		"sender":              "alice@example.org",
		"sphere":              "personal",
		"tag-mode":            "all",
		"direction":           "inbound",
		"kind":                "email",
		"language":            "en",
		"since":               "2026-01-01",
		"until":               "2026-12-31",
		"subject":             "invoice",
		"limit":               "10",
		"sort":                "importance",
		"order":               "asc",
		"importance-min":      "0",
		"importance-max":      "5",
		"attachment-filename": "pdf",
	} {
		require.NoError(t, cmd.Flags().Set(flag, value), flag)
	}

	for _, tag := range []string{"urgent", "done"} {
		require.NoError(t, cmd.Flags().Set("tag", tag))
	}

	require.NoError(t, cmd.Flags().Set("unread", "true"))
	require.NoError(t, cmd.Flags().Set("archived", "false"))
	require.NoError(t, cmd.Flags().Set("has-attachments", "true"))
	require.NoError(t, cmd.Flags().Set("param", "deleted=true"))
	require.NoError(t, cmd.Flags().Set("param", "channel_name=Inbox"))
	require.NoError(t, cmd.Flags().Set("param", "limit=7"))

	values, err := queryValues(cmd, opts)
	require.NoError(t, err)

	assert.Equal(t, "mail", values.Get("source"))
	assert.Equal(t, "private", values.Get("account"))
	assert.Equal(t, "alice@example.org", values.Get("sender"))
	assert.Equal(t, "personal", values.Get("sphere"))
	assert.Equal(t, "all", values.Get("tag_mode"))
	assert.Equal(t, "inbound", values.Get("direction"))
	assert.Equal(t, "email", values.Get("kind"))
	assert.Equal(t, "en", values.Get("language"))
	assert.Equal(t, "2026-01-01", values.Get("since"))
	assert.Equal(t, "2026-12-31", values.Get("until"))
	assert.Equal(t, "invoice", values.Get("subject"))
	assert.Equal(t, "7", values.Get("limit"), "--param overrides a scalar flag")
	assert.Equal(t, "importance", values.Get("sort"))
	assert.Equal(t, "asc", values.Get("order"))
	assert.Equal(t, "0", values.Get("importance_min"))
	assert.Equal(t, "5", values.Get("importance_max"))
	assert.Equal(t, "pdf", values.Get("attachment_filename"))
	assert.Equal(t, []string{"urgent", "done"}, values["tag"])
	assert.Equal(t, "false", values.Get("is_read"), "--unread means is_read=false")
	assert.Equal(t, "false", values.Get("is_archived"))
	assert.Equal(t, "true", values.Get("has_attachments"))
	assert.Equal(t, "true", values.Get("deleted"))
	assert.Equal(t, "Inbox", values.Get("channel_name"))
}

func TestQueryValuesInvertsUnread(t *testing.T) {
	cmd, opts := testQueryCommand(t)
	require.NoError(t, cmd.Flags().Set("unread", "false"))

	values, err := queryValues(cmd, opts)
	require.NoError(t, err)

	assert.Equal(t, "true", values.Get("is_read"))
}

func TestQueryValuesRejectsMalformedParam(t *testing.T) {
	cmd, opts := testQueryCommand(t)
	require.NoError(t, cmd.Flags().Set("param", "not-a-pair"))

	_, err := queryValues(cmd, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key=value")
}
