package model

// Channel is a container of messages on an account: a Mattermost channel, a
// Teams channel, a mail folder, an OpenCode session, a Smartschool course.
type Channel struct {
	Base

	SourceID   string `gorm:"type:text;not null;index" json:"source_id"`
	AccountID  string `gorm:"type:text;not null;uniqueIndex:idx_channels_account_external;index" json:"account_id"`
	ExternalID string `gorm:"type:text;not null;uniqueIndex:idx_channels_account_external" json:"external_id"`
	Name       string `gorm:"type:text" json:"name"`
	Topic      string `gorm:"type:text" json:"topic,omitempty"`
	Kind       string `gorm:"type:text;not null;default:channel" json:"kind"`

	// ParentID builds a tree for sub-channels, threads and nested folders.
	ParentID *string `gorm:"type:text;index" json:"parent_id,omitempty"`

	// SphereID is the default sphere for messages in this channel.
	SphereID *string `gorm:"type:text;index" json:"sphere_id,omitempty"`

	Metadata JSON `gorm:"type:json" json:"metadata,omitempty"`

	Source  Source  `gorm:"foreignKey:SourceID;constraint:OnDelete:CASCADE" json:"-"`
	Account Account `gorm:"foreignKey:AccountID;constraint:OnDelete:CASCADE" json:"-"`
	Sphere  *Sphere `gorm:"foreignKey:SphereID;constraint:OnDelete:SET NULL" json:"sphere,omitempty"`
}
