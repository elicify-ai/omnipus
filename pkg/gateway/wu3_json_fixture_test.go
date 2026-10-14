package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func wu3Object(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "expected JSON object, got %T", value)
	return object
}

func wu3Array(t *testing.T, value any) []any {
	t.Helper()
	array, ok := value.([]any)
	require.True(t, ok, "expected JSON array, got %T", value)
	return array
}
