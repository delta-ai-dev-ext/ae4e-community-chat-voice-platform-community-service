package repository

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPGUUIDRoundTripsThroughPGXCodec proves the adapter actually plugs into
// pgx's UUID codec in both directions, which is the only thing standing between
// a query parameter and the uuid column it is compared against. It plans the
// codec through a real pgtype.Map, so no database is needed.
func TestPGUUIDRoundTripsThroughPGXCodec(t *testing.T) {
	t.Parallel()

	original := uuid.MustParse("8f14e45f-ceea-4a67-9f4a-1b2c3d4e5f60")
	typeMap := pgtype.NewMap()

	encoded, err := typeMap.Encode(pgtype.UUIDOID, pgtype.BinaryFormatCode, id(original), nil)
	require.NoError(t, err)
	require.Len(t, encoded, 16)

	var scanned pgUUID
	require.NoError(t, typeMap.Scan(pgtype.UUIDOID, pgtype.BinaryFormatCode, encoded, &scanned))
	assert.Equal(t, original, scanned.UUID)
}

func TestPGUUIDEncodesNilAsSQLNull(t *testing.T) {
	t.Parallel()

	typeMap := pgtype.NewMap()

	encoded, err := typeMap.Encode(pgtype.UUIDOID, pgtype.BinaryFormatCode, pgUUID{}, nil)
	require.NoError(t, err)
	assert.Nil(t, encoded)

	var scanned pgUUID
	require.NoError(t, typeMap.Scan(pgtype.UUIDOID, pgtype.BinaryFormatCode, nil, &scanned))
	assert.Equal(t, uuid.Nil, scanned.UUID)
}

func TestPGUUIDSupportsTextFormat(t *testing.T) {
	t.Parallel()

	original := uuid.MustParse("3f2504e0-4f89-41d3-9a0c-0305e82c3301")
	typeMap := pgtype.NewMap()

	encoded, err := typeMap.Encode(pgtype.UUIDOID, pgtype.TextFormatCode, id(original), nil)
	require.NoError(t, err)
	assert.Equal(t, original.String(), string(encoded))

	var scanned pgUUID
	require.NoError(t, typeMap.Scan(pgtype.UUIDOID, pgtype.TextFormatCode, encoded, &scanned))
	assert.Equal(t, original, scanned.UUID)
}

func TestNullableIDPreservesAbsence(t *testing.T) {
	t.Parallel()

	assert.Equal(t, pgUUID{}, nullableID(nil))

	value := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	assert.Equal(t, value, nullableID(&value).UUID)
}
