package tools

// Oracle: r17 SR-1 (CK-11). The approval display copy masks nested
// env_overrides containers even when the top-level value is malformed (the
// path where the record copy refuses and the display replaces it whole).
import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovalDisplayArguments_MalformedTopLevelStillMasksNestedUnknown(t *testing.T) {
	buf := make([]byte, 12)
	_, err := rand.Read(buf)
	require.NoError(t, err)
	top, nested := "t8-top-"+hex.EncodeToString(buf), "t8-nested-"+hex.EncodeToString(buf)
	args := map[string]any{
		"env_overrides": top,
		"zz_probe":      []any{map[string]any{"env_overrides": map[string]any{"TOKEN": nested}}},
		"zz_null":       map[string]any{"env_overrides": nil},
	}
	_, refuseErr := RedactEnvOverrides(args)
	require.Error(t, refuseErr, "instrument: the record copy still refuses a non-object top-level value")

	shown := ApprovalDisplayArguments(args)
	encoded, err := json.Marshal(shown)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), top)
	assert.NotContains(t, string(encoded), nested)
	assert.Equal(t, map[string]any{
		"env_overrides": RedactedValue,
		"zz_probe":      []any{map[string]any{"env_overrides": map[string]any{"TOKEN": RedactedValue}}},
		"zz_null":       map[string]any{"env_overrides": nil},
	}, shown)
	assert.Equal(t, top, args["env_overrides"], "the pending entry keeps its own arguments")
}
