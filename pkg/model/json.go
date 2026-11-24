package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSON is a nullable JSON column. Unlike datatypes.JSON it has no external
// dependency and it validates on write, so a broken connector payload cannot
// poison the database.
type JSON json.RawMessage

// Value implements driver.Valuer.
func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		//nolint:nilnil // a NULL column is not an error.
		return nil, nil
	}

	if !json.Valid(j) {
		return nil, fmt.Errorf("invalid JSON value")
	}

	return string(j), nil
}

// Scan implements sql.Scanner.
func (j *JSON) Scan(src any) error {
	if src == nil {
		*j = nil

		return nil
	}

	var data []byte

	switch v := src.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("cannot scan %T into JSON", src)
	}

	if len(data) == 0 {
		*j = nil

		return nil
	}

	if !json.Valid(data) {
		return fmt.Errorf("invalid JSON in database")
	}

	*j = append((*j)[:0], data...)

	return nil
}

// MarshalJSON implements json.Marshaler.
func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}

	return j, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (j *JSON) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*j = nil

		return nil
	}

	if !json.Valid(data) {
		return fmt.Errorf("invalid JSON value")
	}

	*j = append((*j)[:0], data...)

	return nil
}

// GormDataType tells GORM to use a JSON column.
func (JSON) GormDataType() string { return "json" }

// IsZero reports whether the value is empty, so GORM can omit it.
func (j JSON) IsZero() bool { return len(j) == 0 }

// ToJSON marshals an arbitrary value into a JSON column.
func ToJSON(v any) (JSON, error) {
	if v == nil {
		return nil, nil
	}

	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	return JSON(data), nil
}
