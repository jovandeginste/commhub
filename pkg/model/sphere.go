package model

// Sphere is a part of life a message belongs to: work, personal, hockey,
// school, family, ... It is a classification dimension, not an access control
// boundary.
type Sphere struct {
	Base

	Slug        string `gorm:"type:text;not null;uniqueIndex" json:"slug"`
	Name        string `gorm:"type:text;not null" json:"name"`
	Description string `gorm:"type:text" json:"description,omitempty"`
	Color       string `gorm:"type:text" json:"color,omitempty"`
	SortOrder   int    `gorm:"not null;default:0" json:"sort_order"`
}
