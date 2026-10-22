package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// This is a documentation-contract guard, NOT a source-text proxy for runtime
// correctness. Actual persisted Stop/end and consumer/admission behaviour are
// tested separately. Issue 1419 expressly permits this documented separation.
func TestCrashRecovery_1419_MetadataMeaningIsDocumentedInPublicContract(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(file), "..", "..", "contracts", "components", "schemas", "Session.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, yaml.Unmarshal(data, &schema))
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	metadata, ok := properties["status"].(map[string]any)
	require.True(t, ok)
	life, ok := properties["lifecycle_state"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"active", "archived", "failed", "interrupted"}, metadata["enum"], "coarse status must not be mistaken for the canonical running/stopped vocabulary")
	description, ok := metadata["description"].(string)
	require.True(t, ok)
	words := strings.Join(strings.Fields(description), " ")
	assert.Contains(t, words, "Coarse chat-transcript-metadata status")
	assert.Contains(t, words, "`active` covers")
	assert.Contains(t, words, "or stopped")
	assert.Contains(t, words, "`lifecycle_state` for the exact distinction")
	canonical, ok := life["description"].(string)
	require.True(t, ok)
	assert.Contains(t, canonical, "authoritative")
	assert.NotEqual(t, metadata["enum"], life["enum"], "separate documented fields are not interchangeable run-state enums")
}
