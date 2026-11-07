package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadinessFailsOnceShutdownBegins proves the drain gate: as soon as shutdown
// starts every readiness probe fails, so no new traffic is routed to a dying
// instance. The dependency checks are never reached while draining, which is what
// keeps this test free of PostgreSQL and Kafka.
func TestReadinessFailsOnceShutdownBegins(t *testing.T) {
	t.Parallel()

	state := &readiness{}
	require.NoError(t, state.Ready(context.Background()))

	state.beginShutdown()

	err := state.Ready(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "draining")
}
