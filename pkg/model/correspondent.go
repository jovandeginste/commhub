package model

// Correspondent is a person or bot, independent of the platform(s) they use.
// The platform handles are stored as Identities.
type Correspondent struct {
	Base

	DisplayName string  `gorm:"type:text;not null" json:"display_name"`
	SortName    string  `gorm:"type:text" json:"sort_name,omitempty"`
	SphereID    *string `gorm:"type:text;index" json:"sphere_id,omitempty"`
	IsSelf      bool    `gorm:"not null;default:false" json:"is_self"`
	Notes       string  `gorm:"type:text" json:"notes,omitempty"`
	AvatarURL   string  `gorm:"type:text" json:"avatar_url,omitempty"`
	Metadata    JSON    `gorm:"type:json" json:"metadata,omitempty"`

	Sphere     *Sphere    `gorm:"foreignKey:SphereID;constraint:OnDelete:SET NULL" json:"sphere,omitempty"`
	Identities []Identity `gorm:"foreignKey:CorrespondentID;constraint:OnDelete:CASCADE" json:"identities,omitempty"`
}

// Identity is a handle that identifies a correspondent on one source: an email
// address, a username, a member id, a phone number. Identities are unique per
// source, because usernames and ids are only unique within a platform.
type Identity struct {
	Base

	CorrespondentID string `gorm:"type:text;not null;index" json:"correspondent_id"`
	SourceID        string `gorm:"type:text;not null;uniqueIndex:idx_identities_source_kind_value" json:"source_id"`
	Kind            string `gorm:"type:text;not null;uniqueIndex:idx_identities_source_kind_value" json:"kind"`
	Value           string `gorm:"type:text;not null" json:"value"`

	// NormalizedValue is the comparison key: lowercased for emails and
	// usernames, digits-only for phone numbers.
	NormalizedValue string `gorm:"type:text;not null;uniqueIndex:idx_identities_source_kind_value" json:"-"`

	DisplayName string `gorm:"type:text" json:"display_name,omitempty"`
	Verified    bool   `gorm:"not null;default:false" json:"verified"`
	Metadata    JSON   `gorm:"type:json" json:"metadata,omitempty"`

	Correspondent Correspondent `gorm:"foreignKey:CorrespondentID;constraint:OnDelete:CASCADE" json:"-"`
	Source        Source        `gorm:"foreignKey:SourceID;constraint:OnDelete:CASCADE" json:"-"`
}
