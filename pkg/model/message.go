package model

import "time"

// Message is one normalized message, post or mail. The platform payload is kept
// in Raw; the columns above it are what the hub queries and displays.
type Message struct {
	Base

	SourceID  string `gorm:"type:text;not null;index:idx_messages_source_sent,priority:1" json:"source_id"`
	AccountID string `gorm:"type:text;not null;uniqueIndex:idx_messages_account_external;index" json:"account_id"`
	ChannelID string `gorm:"type:text;not null;index:idx_messages_channel_sent,priority:1" json:"channel_id"`

	// ExternalID is the platform identifier (mail Message-ID, chat post id,
	// ...). It is unique within an account.
	ExternalID string `gorm:"type:text;not null;uniqueIndex:idx_messages_account_external" json:"external_id"`

	// ThreadID is the source's conversation key. ParentID is the resolved
	// reply-to message inside the hub.
	ThreadID string  `gorm:"type:text;index" json:"thread_id,omitempty"`
	ParentID *string `gorm:"type:text;index" json:"parent_id,omitempty"`
	SenderID *string `gorm:"type:text;index" json:"sender_id,omitempty"`

	Direction string `gorm:"type:text;not null;default:inbound;index" json:"direction"`
	Kind      string `gorm:"type:text;not null;default:chat;index" json:"kind"`

	Subject    string `gorm:"type:text" json:"subject,omitempty"`
	Body       string `gorm:"type:text" json:"body,omitempty"`
	BodyFormat string `gorm:"type:text;not null;default:plain" json:"body_format"`
	Preview    string `gorm:"type:text" json:"preview,omitempty"`
	Language   string `gorm:"type:text" json:"language,omitempty"`
	URL        string `gorm:"type:text" json:"url,omitempty"`

	SentAt     time.Time  `gorm:"not null;index:idx_messages_channel_sent,priority:2;index:idx_messages_source_sent,priority:2;index:idx_messages_sent_at" json:"sent_at"`
	ReceivedAt *time.Time `json:"received_at,omitempty"`
	IngestedAt time.Time  `gorm:"not null" json:"ingested_at"`
	EditedAt   *time.Time `json:"edited_at,omitempty"`

	// DeletedInSourceAt marks a message that was deleted on the source. It is
	// deliberately not a GORM soft-delete: that would break the unique
	// (account_id, external_id) key when a message is re-ingested.
	DeletedInSourceAt *time.Time `gorm:"column:deleted_at" json:"deleted_at,omitempty"`

	// ContentHash covers the normalized content. It is what makes ingestion
	// idempotent.
	ContentHash string `gorm:"type:text;not null;index" json:"content_hash"`

	Importance int  `gorm:"not null;default:0" json:"importance"`
	IsRead     bool `gorm:"not null;default:false;index" json:"is_read"`
	IsStarred  bool `gorm:"not null;default:false" json:"is_starred"`
	IsArchived bool `gorm:"not null;default:false" json:"is_archived"`

	SphereID *string `gorm:"type:text;index" json:"sphere_id,omitempty"`

	// Metadata holds stable, source-specific extras. Raw holds the original
	// payload and is never used for filtering.
	Metadata JSON `gorm:"type:json" json:"metadata,omitempty"`
	Raw      JSON `gorm:"type:json" json:"raw,omitempty"`

	Source      *Source            `gorm:"foreignKey:SourceID;constraint:OnDelete:CASCADE" json:"source,omitempty"`
	Account     *Account           `gorm:"foreignKey:AccountID;constraint:OnDelete:CASCADE" json:"account,omitempty"`
	Channel     *Channel           `gorm:"foreignKey:ChannelID;constraint:OnDelete:CASCADE" json:"channel,omitempty"`
	Sender      *Identity          `gorm:"foreignKey:SenderID;constraint:OnDelete:SET NULL" json:"sender,omitempty"`
	Sphere      *Sphere            `gorm:"foreignKey:SphereID;constraint:OnDelete:SET NULL" json:"sphere,omitempty"`
	Recipients  []MessageRecipient `gorm:"foreignKey:MessageID;constraint:OnDelete:CASCADE" json:"recipients,omitempty"`
	Attachments []Attachment       `gorm:"foreignKey:MessageID;constraint:OnDelete:CASCADE" json:"attachments,omitempty"`
	Tags        []Tag              `gorm:"many2many:message_tags" json:"tags,omitempty"`
}

// Attachment is a file that belongs to a message. File bytes are not stored in
// the hub; a connector may provide a local path, a URL, or both.
type Attachment struct {
	Base

	MessageID   string `gorm:"type:text;not null;index" json:"message_id"`
	ExternalID  string `gorm:"type:text" json:"external_id,omitempty"`
	Filename    string `gorm:"type:text" json:"filename"`
	MimeType    string `gorm:"type:text" json:"mime_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	SHA256      string `gorm:"type:text;index" json:"sha256,omitempty"`
	StoragePath string `gorm:"type:text" json:"storage_path,omitempty"`
	URL         string `gorm:"type:text" json:"url,omitempty"`
	Metadata    JSON   `gorm:"type:json" json:"metadata,omitempty"`

	Message Message `gorm:"foreignKey:MessageID;constraint:OnDelete:CASCADE" json:"-"`
}

// MessageRecipient links a message to an identity with a role. The sender is
// stored on the message itself, not here.
type MessageRecipient struct {
	MessageID  string `gorm:"type:text;primaryKey" json:"message_id"`
	IdentityID string `gorm:"type:text;primaryKey;index" json:"identity_id"`
	Role       string `gorm:"type:text;primaryKey" json:"role"`

	Message  Message   `gorm:"foreignKey:MessageID;constraint:OnDelete:CASCADE" json:"-"`
	Identity *Identity `gorm:"foreignKey:IdentityID;constraint:OnDelete:CASCADE" json:"identity,omitempty"`
}

// Tag is a free-form label that can be attached to any message.
type Tag struct {
	Base

	Slug  string `gorm:"type:text;not null;uniqueIndex" json:"slug"`
	Name  string `gorm:"type:text;not null" json:"name"`
	Color string `gorm:"type:text" json:"color,omitempty"`
}

// MessageTag is the many-to-many join between messages and tags.
type MessageTag struct {
	MessageID string `gorm:"type:text;primaryKey" json:"message_id"`
	TagID     string `gorm:"type:text;primaryKey;index" json:"tag_id"`

	Message Message `gorm:"foreignKey:MessageID;constraint:OnDelete:CASCADE" json:"-"`
	Tag     Tag     `gorm:"foreignKey:TagID;constraint:OnDelete:CASCADE" json:"-"`
}
