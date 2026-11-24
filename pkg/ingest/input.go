// Package ingest implements the write path of the hub: resolve natural keys
// into rows and upsert messages idempotently.
package ingest

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jovandeginste/commhub/pkg/model"
)

// MaxBatchSize is the hard limit for one batch or stream chunk.
const MaxBatchSize = 1000

// ValidationError marks a problem with connector input. The API maps it to
// HTTP 400.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}

	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// Invalid returns a validation error for a field.
func Invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

// ChannelInput describes (and can create/update) the channel a message belongs
// to. Only ExternalID is required.
type ChannelInput struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Topic      string `json:"topic,omitempty"`
	Sphere     string `json:"sphere,omitempty"`
}

// IdentityInput refers to a correspondent by one of their handles.
type IdentityInput struct {
	Kind        string `json:"kind,omitempty"`
	Value       string `json:"value"`
	DisplayName string `json:"display_name,omitempty"`
}

// RecipientInput is an identity with a role in the message.
type RecipientInput struct {
	IdentityInput
	Role string `json:"role,omitempty"`
}

// AttachmentInput describes a file without carrying its bytes.
type AttachmentInput struct {
	ExternalID  string          `json:"external_id,omitempty"`
	Filename    string          `json:"filename"`
	MimeType    string          `json:"mime_type,omitempty"`
	Size        int64           `json:"size,omitempty"`
	SHA256      string          `json:"sha256,omitempty"`
	StoragePath string          `json:"storage_path,omitempty"`
	URL         string          `json:"url,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// MessageInput is the payload every connector sends, regardless of ingestion
// mode. Natural keys are used for everything the hub might not know yet.
type MessageInput struct {
	Source     string `json:"source"`
	SourceKind string `json:"source_kind,omitempty"`
	Account    string `json:"account,omitempty"`

	Channel          ChannelInput `json:"channel"`
	ExternalID       string       `json:"external_id"`
	ThreadID         string       `json:"thread_id,omitempty"`
	ParentExternalID string       `json:"parent_external_id,omitempty"`

	Sender     *IdentityInput   `json:"sender,omitempty"`
	Recipients []RecipientInput `json:"recipients,omitempty"`

	Direction  string `json:"direction,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Subject    string `json:"subject,omitempty"`
	Body       string `json:"body,omitempty"`
	BodyFormat string `json:"body_format,omitempty"`
	Preview    string `json:"preview,omitempty"`
	Language   string `json:"language,omitempty"`
	URL        string `json:"url,omitempty"`

	SentAt     *time.Time `json:"sent_at,omitempty"`
	ReceivedAt *time.Time `json:"received_at,omitempty"`
	EditedAt   *time.Time `json:"edited_at,omitempty"`

	Importance  int               `json:"importance,omitempty"`
	Sphere      string            `json:"sphere,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Attachments []AttachmentInput `json:"attachments,omitempty"`

	Metadata json.RawMessage `json:"metadata,omitempty"`
	Raw      json.RawMessage `json:"raw,omitempty"`
}

// Validate normalizes the input and checks required fields and enum values.
func (in *MessageInput) Validate() error {
	if err := in.validateTarget(); err != nil {
		return err
	}

	if err := in.validateContent(); err != nil {
		return err
	}

	if err := in.validateSender(); err != nil {
		return err
	}

	if err := in.validateRecipients(); err != nil {
		return err
	}

	if err := in.validateAttachments(); err != nil {
		return err
	}

	in.Sphere = Slugify(in.Sphere)
	in.Channel.Sphere = Slugify(in.Channel.Sphere)

	return nil
}

// validateTarget normalizes the source, account and channel a message belongs
// to. Both keys are required because they scope the idempotency of a message.
func (in *MessageInput) validateTarget() error {
	in.Source = Slugify(in.Source)
	if in.Source == "" {
		return Invalid("source", "required")
	}

	in.Account = Slugify(in.Account)
	if in.Account == "" {
		in.Account = "default"
	}

	in.Channel.ExternalID = strings.TrimSpace(in.Channel.ExternalID)
	if in.Channel.ExternalID == "" {
		return Invalid("channel.external_id", "required")
	}

	in.Channel.Kind = strings.ToLower(strings.TrimSpace(in.Channel.Kind))

	in.ExternalID = strings.TrimSpace(in.ExternalID)
	if in.ExternalID == "" {
		return Invalid("external_id", "required")
	}

	return nil
}

func (in *MessageInput) validateContent() error {
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))

	in.BodyFormat = strings.ToLower(strings.TrimSpace(in.BodyFormat))
	if in.BodyFormat == "" {
		in.BodyFormat = string(model.BodyFormatPlain)
	}

	switch model.BodyFormat(in.BodyFormat) {
	case model.BodyFormatPlain, model.BodyFormatMarkdown, model.BodyFormatHTML:
	default:
		return Invalid("body_format", fmt.Sprintf("unknown format %q", in.BodyFormat))
	}

	in.Direction = strings.ToLower(strings.TrimSpace(in.Direction))
	switch model.Direction(in.Direction) {
	case "", model.DirectionInbound, model.DirectionOutbound, model.DirectionInternal:
	default:
		return Invalid("direction", fmt.Sprintf("unknown direction %q", in.Direction))
	}

	return nil
}

func (in *MessageInput) validateSender() error {
	if in.Sender == nil {
		return nil
	}

	in.Sender.Kind = strings.ToLower(strings.TrimSpace(in.Sender.Kind))
	in.Sender.Value = strings.TrimSpace(in.Sender.Value)

	if in.Sender.Value == "" {
		return Invalid("sender.value", "required")
	}

	return nil
}

func (in *MessageInput) validateRecipients() error {
	seen := make(map[string]struct{}, len(in.Recipients))

	for i := range in.Recipients {
		recipient := &in.Recipients[i]
		recipient.Kind = strings.ToLower(strings.TrimSpace(recipient.Kind))
		recipient.Value = strings.TrimSpace(recipient.Value)
		recipient.Role = strings.ToLower(strings.TrimSpace(recipient.Role))

		if recipient.Value == "" {
			return Invalid(fmt.Sprintf("recipients[%d].value", i), "required")
		}

		if recipient.Role == "" {
			recipient.Role = string(model.RecipientRoleTo)
		}

		switch model.RecipientRole(recipient.Role) {
		case model.RecipientRoleTo, model.RecipientRoleCC, model.RecipientRoleBCC,
			model.RecipientRoleMention, model.RecipientRoleWatcher:
		default:
			return Invalid(fmt.Sprintf("recipients[%d].role", i), fmt.Sprintf("unknown role %q", recipient.Role))
		}

		keyKind := recipient.Kind
		if keyKind == "" {
			// The real kind is derived from the source later; both the email
			// and username fallbacks normalize case-insensitively.
			keyKind = string(model.IdentityKindUsername)
		}

		key := keyKind + "\x00" + NormalizeIdentity(keyKind, recipient.Value) + "\x00" + recipient.Role
		if _, ok := seen[key]; ok {
			return Invalid(fmt.Sprintf("recipients[%d]", i), "duplicate recipient")
		}

		seen[key] = struct{}{}
	}

	return nil
}

func (in *MessageInput) validateAttachments() error {
	for i := range in.Attachments {
		in.Attachments[i].Filename = strings.TrimSpace(in.Attachments[i].Filename)
		if in.Attachments[i].Filename == "" {
			return Invalid(fmt.Sprintf("attachments[%d].filename", i), "required")
		}
	}

	return nil
}

// DefaultIdentityKind returns the kind of handle a source usually provides.
func DefaultIdentityKind(sourceSlug string) string {
	switch sourceSlug {
	case "mail", "email", "imap", "smtp":
		return string(model.IdentityKindEmail)
	case "phone", "sms", "whatsapp", "signal":
		return string(model.IdentityKindPhone)
	default:
		return string(model.IdentityKindUsername)
	}
}

// NormalizeIdentity returns the comparison key for a handle. Emails and
// usernames compare case-insensitively; phone numbers keep only + and digits;
// opaque handles are only trimmed.
func NormalizeIdentity(kind, value string) string {
	value = strings.TrimSpace(value)

	switch model.IdentityKind(strings.ToLower(strings.TrimSpace(kind))) {
	case model.IdentityKindEmail, model.IdentityKindUsername,
		model.IdentityKindUserID, model.IdentityKindMemberID:
		return strings.ToLower(value)
	case model.IdentityKindPhone:
		var b strings.Builder

		for i, r := range value {
			if r == '+' && i == 0 {
				b.WriteRune(r)

				continue
			}

			if unicode.IsDigit(r) {
				b.WriteRune(r)
			}
		}

		return b.String()
	case model.IdentityKindHandle:
		return value
	default:
		return value
	}
}

// Slugify makes a stable, lowercase key from free text.
func Slugify(value string) string {
	var b strings.Builder

	lastDash := false

	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteRune('-')
			lastDash = true
		}
	}

	return strings.Trim(b.String(), "-")
}
