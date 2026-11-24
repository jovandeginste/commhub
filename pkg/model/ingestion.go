package model

import "time"

// IngestBatch records one ingestion call. It makes connector behaviour
// inspectable: how much came in, what failed and why.
type IngestBatch struct {
	Base

	Mode   string `gorm:"type:text;not null;default:batch" json:"mode"`
	Status string `gorm:"type:text;not null;default:running;index" json:"status"`

	Total     int `gorm:"not null;default:0" json:"total"`
	Created   int `gorm:"not null;default:0" json:"created"`
	Updated   int `gorm:"not null;default:0" json:"updated"`
	Unchanged int `gorm:"not null;default:0" json:"unchanged"`
	Failed    int `gorm:"not null;default:0" json:"failed"`

	StartedAt  time.Time  `gorm:"not null;index" json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `gorm:"type:text" json:"error,omitempty"`

	Errors []IngestError `gorm:"foreignKey:BatchID;constraint:OnDelete:CASCADE" json:"errors,omitempty"`
}

// IngestError is one failed item inside a batch.
type IngestError struct {
	Base

	BatchID    string `gorm:"type:text;not null;index" json:"batch_id"`
	Position   int    `gorm:"not null;default:0" json:"position"`
	ExternalID string `gorm:"type:text" json:"external_id,omitempty"`
	Error      string `gorm:"type:text;not null" json:"error"`
	Payload    JSON   `gorm:"type:json" json:"payload,omitempty"`

	Batch IngestBatch `gorm:"foreignKey:BatchID;constraint:OnDelete:CASCADE" json:"-"`
}
