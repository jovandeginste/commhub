package model

import "time"

// Source is a platform or protocol the hub ingests from, for example
// "mattermost", "teams", "mail", "opencode" or "smartschool". The slug is the
// natural key connectors use.
type Source struct {
	Base

	Slug        string `gorm:"type:text;not null;uniqueIndex" json:"slug"`
	Name        string `gorm:"type:text;not null" json:"name"`
	Kind        string `gorm:"type:text;not null;default:''" json:"kind"`
	Description string `gorm:"type:text" json:"description,omitempty"`
	Enabled     bool   `gorm:"not null;default:true" json:"enabled"`
	Config      JSON   `gorm:"type:json" json:"config,omitempty"`

	Accounts []Account `gorm:"foreignKey:SourceID;constraint:OnDelete:CASCADE" json:"accounts,omitempty"`
}

// Account is one configured connection or identity on a Source: a specific mail
// account, your user on a particular Mattermost server, and so on. Channels and
// messages always belong to an account.
type Account struct {
	Base

	SourceID string `gorm:"type:text;not null;uniqueIndex:idx_accounts_source_slug;index" json:"source_id"`
	Slug     string `gorm:"type:text;not null;uniqueIndex:idx_accounts_source_slug" json:"slug"`
	Name     string `gorm:"type:text;not null" json:"name"`

	// ExternalUser is your own handle on this account. It is used to detect
	// outbound messages and to mark the corresponding correspondent as self.
	ExternalUser     string `gorm:"type:text" json:"external_user,omitempty"`
	ExternalUserKind string `gorm:"type:text;not null;default:username" json:"external_user_kind"`

	// SecretRef names a secret in the environment or keyring. Secrets are
	// never stored in the hub and never serialized.
	SecretRef string `gorm:"type:text" json:"-"`

	Config       JSON       `gorm:"type:json" json:"config,omitempty"`
	Enabled      bool       `gorm:"not null;default:true" json:"enabled"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`

	Source Source `gorm:"foreignKey:SourceID;constraint:OnDelete:CASCADE" json:"-"`
}
