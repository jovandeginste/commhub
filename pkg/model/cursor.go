package model

import "time"

// Cursor is a connector-owned position marker, e.g. "last synced post id" for
// one account. The hub stores it so connectors do not need their own state
// files and can move to another machine without re-syncing everything.
type Cursor struct {
	Key       string    `gorm:"primaryKey;type:text" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}
