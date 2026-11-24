package model

// String enums are used instead of SQL enums on purpose: sources evolve and an
// unknown value from a new connector must not require a migration. The constants
// below are the values the hub understands today.

// ChannelKind describes what a channel represents on its source.
type ChannelKind string

const (
	ChannelKindChannel ChannelKind = "channel"
	ChannelKindDirect  ChannelKind = "direct"
	ChannelKindGroup   ChannelKind = "group"
	ChannelKindThread  ChannelKind = "thread"
	ChannelKindFolder  ChannelKind = "folder"
	ChannelKindFeed    ChannelKind = "feed"
	ChannelKindCourse  ChannelKind = "course"
	ChannelKindSession ChannelKind = "session"
	ChannelKindOther   ChannelKind = "other"
)

// IdentityKind describes what an identity value is.
type IdentityKind string

const (
	IdentityKindEmail    IdentityKind = "email"
	IdentityKindUsername IdentityKind = "username"
	IdentityKindUserID   IdentityKind = "user_id"
	IdentityKindMemberID IdentityKind = "member_id"
	IdentityKindPhone    IdentityKind = "phone"
	IdentityKindHandle   IdentityKind = "handle"
)

// Direction describes the flow of a message relative to the hub user.
type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
	DirectionInternal Direction = "internal"
)

// MessageKind is the coarse nature of a message.
type MessageKind string

const (
	MessageKindChat         MessageKind = "chat"
	MessageKindEmail        MessageKind = "email"
	MessageKindPost         MessageKind = "post"
	MessageKindComment      MessageKind = "comment"
	MessageKindNotification MessageKind = "notification"
	MessageKindEvent        MessageKind = "event"
	MessageKindSystem       MessageKind = "system"
)

// BodyFormat tells renderers how to interpret the body.
type BodyFormat string

const (
	BodyFormatPlain    BodyFormat = "plain"
	BodyFormatMarkdown BodyFormat = "markdown"
	BodyFormatHTML     BodyFormat = "html"
)

// RecipientRole is the role of an identity in a message.
type RecipientRole string

const (
	RecipientRoleTo      RecipientRole = "to"
	RecipientRoleCC      RecipientRole = "cc"
	RecipientRoleBCC     RecipientRole = "bcc"
	RecipientRoleMention RecipientRole = "mention"
	RecipientRoleWatcher RecipientRole = "watcher"
)

// IngestMode is how a message entered the hub.
type IngestMode string

const (
	IngestModeSingle IngestMode = "single"
	IngestModeBatch  IngestMode = "batch"
	IngestModeStream IngestMode = "stream"
)

// IngestStatus is the outcome of an ingestion call.
type IngestStatus string

const (
	IngestStatusRunning   IngestStatus = "running"
	IngestStatusCompleted IngestStatus = "completed"
	IngestStatusPartial   IngestStatus = "partial"
	IngestStatusFailed    IngestStatus = "failed"
)

// ResultStatus is the outcome for a single ingested message.
type ResultStatus string

const (
	ResultCreated   ResultStatus = "created"
	ResultUpdated   ResultStatus = "updated"
	ResultUnchanged ResultStatus = "unchanged"
)
