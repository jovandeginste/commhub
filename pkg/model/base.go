package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Base is embedded in every model. It provides a time-ordered UUIDv7 primary
// key and the bookkeeping timestamps.
type Base struct {
	ID        string    `gorm:"primaryKey;type:text" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BeforeCreate assigns a UUIDv7 identifier when none was supplied. UUIDv7 is
// time-ordered, so text primary keys sort chronologically.
func (b *Base) BeforeCreate(*gorm.DB) error {
	if b.ID != "" {
		return nil
	}

	id, err := uuid.NewV7()
	if err != nil {
		return err
	}

	b.ID = id.String()

	return nil
}
