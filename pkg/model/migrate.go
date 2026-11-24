package model

import "gorm.io/gorm"

// AllModels returns every persisted model in dependency order.
func AllModels() []any {
	return []any{
		&Sphere{},
		&Source{},
		&Account{},
		&Channel{},
		&Correspondent{},
		&Identity{},
		&Tag{},
		&Message{},
		&Attachment{},
		&MessageRecipient{},
		&MessageTag{},
		&IngestBatch{},
		&IngestError{},
		&Cursor{},
	}
}

// Migrate creates or updates the schema.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(AllModels()...)
}
