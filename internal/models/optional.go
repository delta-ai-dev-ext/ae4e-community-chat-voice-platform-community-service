package models

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// OptionalString distinguishes an absent JSON property from an explicit null in
// PATCH bodies, so "clear this field" and "leave this field alone" remain
// distinguishable at the boundary.
type OptionalString struct {
	Set   bool
	Value *string
}

// UnmarshalJSON records that the property was present and keeps null as nil.
func (o *OptionalString) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Value = nil
		return nil
	}

	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("expected a string or null: %w", err)
	}
	o.Value = &value
	return nil
}

// MarshalJSON round-trips the optional value.
func (o OptionalString) MarshalJSON() ([]byte, error) { return json.Marshal(o.Value) }

// OptionalInt distinguishes an absent JSON property from an explicit null.
type OptionalInt struct {
	Set   bool
	Value *int
}

// UnmarshalJSON records that the property was present and keeps null as nil.
func (o *OptionalInt) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Value = nil
		return nil
	}

	var value int
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("expected an integer or null: %w", err)
	}
	o.Value = &value
	return nil
}

// MarshalJSON round-trips the optional value.
func (o OptionalInt) MarshalJSON() ([]byte, error) { return json.Marshal(o.Value) }
