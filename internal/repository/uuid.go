package repository

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// pgUUID adapts google/uuid values to pgx's UUID codec. pgx only encodes types
// implementing pgtype.UUIDValuer and only decodes into pgtype.UUIDScanner, so
// every identifier parameter and scan target in this package goes through this
// type instead of casting identifiers to text in SQL.
type pgUUID struct {
	uuid.UUID
}

// id marks a UUID as a query parameter.
func id(value uuid.UUID) pgUUID { return pgUUID{UUID: value} }

// nullableID maps an optional UUID to a query parameter, preserving SQL NULL.
func nullableID(value *uuid.UUID) pgUUID {
	if value == nil {
		return pgUUID{}
	}
	return pgUUID{UUID: *value}
}

// UUIDValue implements pgtype.UUIDValuer; the zero value encodes as SQL NULL.
func (p pgUUID) UUIDValue() (pgtype.UUID, error) {
	if p.UUID == uuid.Nil {
		return pgtype.UUID{}, nil
	}
	return pgtype.UUID{Bytes: [16]byte(p.UUID), Valid: true}, nil
}

// ScanUUID implements pgtype.UUIDScanner.
func (p *pgUUID) ScanUUID(value pgtype.UUID) error {
	if !value.Valid {
		p.UUID = uuid.Nil
		return nil
	}
	p.UUID = uuid.UUID(value.Bytes)
	return nil
}
