package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jovandeginste/commhub/pkg/client"
	"github.com/jovandeginste/commhub/pkg/config"
	"github.com/jovandeginste/commhub/pkg/model"
)

type queryOptions struct {
	apiURL string
	token  string

	json   bool
	params []string

	limit  int
	cursor string
	sort   string
	order  string

	source        string
	account       string
	channel       string
	channelID     string
	correspondent string
	sender        string
	recipient     string
	sphere        string
	tags          []string
	tagMode       string
	direction     string
	kind          string
	bodyFormat    string
	language      string

	since   string
	until   string
	subject string
	body    string
	text    string

	unread         bool
	starred        bool
	archived       bool
	deleted        bool
	hasAttachments bool

	attachmentMime     string
	attachmentFilename string
	importanceMin      int
	importanceMax      int
}

func newQueryCmd(configPath *string) *cobra.Command {
	opts := &queryOptions{}

	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query messages through the REST API",
		Long: "Query messages through the running hub. Any API filter can be passed " +
			"with --param key=value.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runQuery(cmd, *configPath, opts)
		},
	}

	addQueryFlags(cmd, opts)

	return cmd
}

func addQueryFlags(cmd *cobra.Command, opts *queryOptions) {
	cmd.Flags().StringVar(&opts.apiURL, "url", "", "API base URL (default from config)")
	cmd.Flags().StringVar(&opts.token, "token", "", "API bearer token (default from config)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "print the raw JSON response")
	cmd.Flags().StringArrayVar(&opts.params, "param", nil, "extra API parameter key=value (repeatable)")

	cmd.Flags().IntVar(&opts.limit, "limit", 20, "maximum number of messages")
	cmd.Flags().StringVar(&opts.cursor, "cursor", "", "continue after this cursor")
	cmd.Flags().StringVar(&opts.sort, "sort", "", "sort by sent_at, ingested_at, created_at or importance")
	cmd.Flags().StringVar(&opts.order, "order", "", "sort order: asc or desc")

	cmd.Flags().StringVar(&opts.source, "source", "", "source slug")
	cmd.Flags().StringVar(&opts.account, "account", "", "account slug")
	cmd.Flags().StringVar(&opts.channel, "channel", "", "channel external id")
	cmd.Flags().StringVar(&opts.channelID, "channel-id", "", "internal channel id")
	cmd.Flags().StringVar(&opts.correspondent, "correspondent", "", "correspondent id, as sender or recipient")
	cmd.Flags().StringVar(&opts.sender, "sender", "", "sender handle (email, username, ...)")
	cmd.Flags().StringVar(&opts.recipient, "recipient", "", "recipient handle")
	cmd.Flags().StringVar(&opts.sphere, "sphere", "", "sphere slug")
	cmd.Flags().StringArrayVar(&opts.tags, "tag", nil, "tag slug (repeatable, comma separated)")
	cmd.Flags().StringVar(&opts.tagMode, "tag-mode", "", "tag matching: any or all")
	cmd.Flags().StringVar(&opts.direction, "direction", "", "inbound, outbound or internal")
	cmd.Flags().StringVar(&opts.kind, "kind", "", "message kind")
	cmd.Flags().StringVar(&opts.bodyFormat, "body-format", "", "plain, markdown or html")
	cmd.Flags().StringVar(&opts.language, "language", "", "language code")

	cmd.Flags().StringVar(&opts.since, "since", "", "sent at or after (RFC 3339 or YYYY-MM-DD)")
	cmd.Flags().StringVar(&opts.until, "until", "", "sent at or before")
	cmd.Flags().StringVar(&opts.subject, "subject", "", "subject substring")
	cmd.Flags().StringVar(&opts.body, "body", "", "body substring")
	cmd.Flags().StringVar(&opts.text, "text", "", "search subject, body, preview and url")

	cmd.Flags().BoolVar(&opts.unread, "unread", false, "filter on read state (--unread=false means read)")
	cmd.Flags().BoolVar(&opts.starred, "starred", false, "filter on starred state")
	cmd.Flags().BoolVar(&opts.archived, "archived", false, "filter on archived state")
	cmd.Flags().BoolVar(&opts.deleted, "deleted", false, "filter on deleted-in-source state")
	cmd.Flags().BoolVar(&opts.hasAttachments, "has-attachments", false, "filter on attachment presence")

	cmd.Flags().StringVar(&opts.attachmentMime, "attachment-mime", "", "attachment MIME substring")
	cmd.Flags().StringVar(&opts.attachmentFilename, "attachment-filename", "", "attachment filename substring")
	cmd.Flags().IntVar(&opts.importanceMin, "importance-min", 0, "minimum importance")
	cmd.Flags().IntVar(&opts.importanceMax, "importance-max", 0, "maximum importance")
}

func newGetCmd(configPath *string) *cobra.Command {
	var apiURL, token string

	cmd := &cobra.Command{
		Use:   "get <message-id>",
		Short: "Fetch one message through the REST API",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}

			message, err := client.New(resolveAPIURL(cfg, apiURL), resolveToken(cfg, token)).
				Message(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			return writeJSON(cmd.OutOrStdout(), message)
		},
	}

	cmd.Flags().StringVar(&apiURL, "url", "", "API base URL (default from config)")
	cmd.Flags().StringVar(&token, "token", "", "API bearer token (default from config)")

	return cmd
}

func runQuery(cmd *cobra.Command, configPath string, opts *queryOptions) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	values, err := queryValues(cmd, opts)
	if err != nil {
		return err
	}

	page, err := client.New(resolveAPIURL(cfg, opts.apiURL), resolveToken(cfg, opts.token)).
		Messages(cmd.Context(), values)
	if err != nil {
		return err
	}

	if opts.json {
		return writeJSON(cmd.OutOrStdout(), page)
	}

	if len(page.Messages) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "no messages")
	}

	if err := printMessageTable(cmd.OutOrStdout(), page.Messages); err != nil {
		return err
	}

	if page.NextCursor != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "next cursor: %s\n", page.NextCursor)
	}

	return nil
}

func queryValues(cmd *cobra.Command, opts *queryOptions) (url.Values, error) {
	values := url.Values{}

	addStringFilters(values, opts)
	addTagFilters(values, opts.tags)

	values.Set("limit", strconv.Itoa(opts.limit))

	addBoolFilters(cmd, values, opts)
	addImportanceFilters(cmd, values, opts)

	return addExtraParams(values, opts.params)
}

func addStringFilters(values url.Values, opts *queryOptions) {
	setString(values, "source", opts.source)
	setString(values, "account", opts.account)
	setString(values, "channel", opts.channel)
	setString(values, "channel_id", opts.channelID)
	setString(values, "correspondent_id", opts.correspondent)
	setString(values, "sender", opts.sender)
	setString(values, "recipient", opts.recipient)
	setString(values, "sphere", opts.sphere)
	setString(values, "tag_mode", opts.tagMode)
	setString(values, "direction", opts.direction)
	setString(values, "kind", opts.kind)
	setString(values, "body_format", opts.bodyFormat)
	setString(values, "language", opts.language)
	setString(values, "since", opts.since)
	setString(values, "until", opts.until)
	setString(values, "subject", opts.subject)
	setString(values, "body", opts.body)
	setString(values, "q", opts.text)
	setString(values, "attachment_mime", opts.attachmentMime)
	setString(values, "attachment_filename", opts.attachmentFilename)
	setString(values, "cursor", opts.cursor)
	setString(values, "sort", opts.sort)
	setString(values, "order", opts.order)
}

func addTagFilters(values url.Values, tags []string) {
	for _, tag := range tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			values.Add("tag", tag)
		}
	}
}

func addBoolFilters(cmd *cobra.Command, values url.Values, opts *queryOptions) {
	if cmd.Flags().Changed("unread") {
		values.Set("is_read", strconv.FormatBool(!opts.unread))
	}

	setBoolFlag(cmd, values, "starred", "is_starred", opts.starred)
	setBoolFlag(cmd, values, "archived", "is_archived", opts.archived)
	setBoolFlag(cmd, values, "deleted", "deleted", opts.deleted)
	setBoolFlag(cmd, values, "has-attachments", "has_attachments", opts.hasAttachments)
}

func addImportanceFilters(cmd *cobra.Command, values url.Values, opts *queryOptions) {
	if cmd.Flags().Changed("importance-min") {
		values.Set("importance_min", strconv.Itoa(opts.importanceMin))
	}

	if cmd.Flags().Changed("importance-max") {
		values.Set("importance_max", strconv.Itoa(opts.importanceMax))
	}
}

func addExtraParams(values url.Values, params []string) (url.Values, error) {
	for _, raw := range params {
		key, value, ok := strings.Cut(raw, "=")

		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid --param %q, expected key=value", raw)
		}

		// Tags are the only repeatable filter; scalars may override flags.
		if key == "tag" || key == "tags" {
			values.Add(key, value)
		} else {
			values.Set(key, value)
		}
	}

	return values, nil
}

func setString(values url.Values, name, value string) {
	if value = strings.TrimSpace(value); value != "" {
		values.Set(name, value)
	}
}

func setBoolFlag(cmd *cobra.Command, values url.Values, flag, param string, value bool) {
	if cmd.Flags().Changed(flag) {
		values.Set(param, strconv.FormatBool(value))
	}
}

func resolveAPIURL(cfg *config.Config, flagValue string) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}

	return cfg.APIBaseURL()
}

func resolveToken(cfg *config.Config, flagValue string) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}

	return cfg.ClientToken()
}

func printMessageTable(w io.Writer, messages []model.Message) error {
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	if _, err := fmt.Fprintln(table, "ID\tSENT\tSOURCE\tCHANNEL\tFROM\tSUMMARY\tTAGS\tFLAGS"); err != nil {
		return err
	}

	for _, message := range messages {
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			message.ID,
			message.SentAt.Local().Format("2006-01-02 15:04"),
			sourceLabel(message),
			channelLabel(message),
			senderLabel(message),
			messageSummary(message),
			tagLabel(message),
			flagLabel(message),
		); err != nil {
			return err
		}
	}

	return table.Flush()
}

func sourceLabel(message model.Message) string {
	if message.Source == nil || message.Source.Slug == "" {
		return "-"
	}

	return message.Source.Slug
}

func channelLabel(message model.Message) string {
	if message.Channel == nil {
		return "-"
	}

	if message.Channel.Name != "" {
		return message.Channel.Name
	}

	if message.Channel.ExternalID != "" {
		return message.Channel.ExternalID
	}

	return "-"
}

func senderLabel(message model.Message) string {
	if message.Sender == nil {
		return "-"
	}

	if message.Sender.DisplayName != "" {
		return message.Sender.DisplayName
	}

	if message.Sender.Value != "" {
		return message.Sender.Value
	}

	return "-"
}

func messageSummary(message model.Message) string {
	subject := strings.TrimSpace(message.Subject)
	body := strings.Join(strings.Fields(message.Body), " ")

	var summary string

	switch {
	case subject != "" && body != "":
		summary = subject + " — " + body
	case subject != "":
		summary = subject
	case body != "":
		summary = body
	default:
		summary = strings.TrimSpace(message.Preview)
	}

	if summary == "" {
		return "-"
	}

	return truncate(summary, 70)
}

func tagLabel(message model.Message) string {
	if len(message.Tags) == 0 {
		return "-"
	}

	slugs := make([]string, 0, len(message.Tags))
	for _, tag := range message.Tags {
		slugs = append(slugs, tag.Slug)
	}

	return strings.Join(slugs, ",")
}

func flagLabel(message model.Message) string {
	flags := make([]string, 0, 4)

	if !message.IsRead {
		flags = append(flags, "unread")
	}

	if message.IsStarred {
		flags = append(flags, "starred")
	}

	if message.IsArchived {
		flags = append(flags, "archived")
	}

	if message.DeletedInSourceAt != nil {
		flags = append(flags, "deleted")
	}

	if len(flags) == 0 {
		return "-"
	}

	return strings.Join(flags, ",")
}

func truncate(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}

	return string(runes[:maximum-1]) + "…"
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	return encoder.Encode(value)
}
