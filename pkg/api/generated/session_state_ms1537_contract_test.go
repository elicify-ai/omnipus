package generated

// Oracle: helper-model-notices-spec.md Contract Changes SessionStateFrame / FR-008.
// This is a test file, not a generated implementation artifact.
import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestContract_SessionStateFrame_EmittedAtMs(t *testing.T) {
	for _, copyName := range []string{"component", "asyncapi-inline"} {
		t.Run(copyName, func(t *testing.T) {
			path := filepath.Join(contractsDir(), "components", "schemas", "SessionStateFrame.yaml")
			if copyName == "asyncapi-inline" {
				path = filepath.Join(contractsDir(), "asyncapi.yaml")
			}
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			var schema map[string]any
			require.NoError(t, yaml.Unmarshal(raw, &schema))
			if copyName == "asyncapi-inline" {
				components, ok := schema["components"].(map[string]any)
				require.True(t, ok, "Q1 parity asyncapi-inline: components must be a mapping")
				schemas, ok := components["schemas"].(map[string]any)
				require.True(t, ok, "Q1 parity asyncapi-inline: components.schemas must be a mapping")
				sessionState, ok := schemas["SessionStateFrame"].(map[string]any)
				require.True(t, ok, "Q1 parity asyncapi-inline: components.schemas.SessionStateFrame must be a mapping")
				schema = sessionState
			}
			properties, ok := schema["properties"].(map[string]any)
			require.True(t, ok, "Q1 parity %s: properties must be a mapping", copyName)
			property, exists := properties["emitted_at_ms"].(map[string]any)
			assert.True(t, exists, "Q1 parity %s: emitted_at_ms property must exist", copyName)
			assert.Equal(t, "integer", property["type"], "Q1 parity %s: emitted_at_ms is integer", copyName)
			assert.Equal(t, "int64", property["format"], "Q1 parity %s: emitted_at_ms is int64", copyName)
			assert.Equal(t, 0, property["minimum"], "Q1 parity %s: emitted_at_ms minimum is zero", copyName)
			assert.NotContains(t, schema["required"], "emitted_at_ms", "Q1 parity %s: old gateways may omit the additive field", copyName)
			validate := validateAgainstComponentSchema
			if copyName == "asyncapi-inline" {
				validate = validateAgainstAsyncAPISchema
			}
			// Both real schema validators must see the valid field, not just
			// reject an unknown property in the negative controls.
			fixture := map[string]any{"type": "session_state", "user_id": "q1-reader", "pending_approvals": []any{}, "emitted_at": "2026-11-15T12:00:00Z"}
			assert.NoError(t, validate(t, "SessionStateFrame", fixture), "Q1 parity %s: absent emitted_at_ms remains valid", copyName)
			for _, value := range []int64{0, 1, 1794744000900} {
				// Keep even shape-validation fixtures consistent with the same-instant rule.
				fixture["emitted_at"] = time.UnixMilli(value).UTC().Format(time.RFC3339)
				fixture["emitted_at_ms"] = value
				assert.NoError(t, validate(t, "SessionStateFrame", fixture), "Q1 parity %s: valid integer emitted_at_ms=%d is accepted", copyName, value)
			}
			for _, value := range []any{"1794744000900", -1, 0.5, nil} {
				fixture["emitted_at_ms"] = value
				assert.Error(t, validate(t, "SessionStateFrame", fixture), "Q1 parity %s: invalid emitted_at_ms=%#v is rejected", copyName, value)
			}
		})
	}
	t.Run("generated-SPA-copy", func(t *testing.T) {
		path := filepath.Join(filepath.Dir(contractsDir()), "src", "lib", "api", "generated", "ws-schemas.ts")
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		// Existence guard only; the companion SPA contract test executes the
		// actual generated Zod validator on valid/invalid frames.
		declaration := regexp.MustCompile(`(?s)export const SessionStateFrame\s*=.*?\.strict\(\);`).Find(raw)
		require.NotEmpty(t, declaration, "Q1 parity instrument: known generated SessionStateFrame declaration was found")
		assert.Regexp(t, `\bemitted_at_ms\s*:`, string(declaration), "Q1 parity generated SPA: SessionStateFrame must contain emitted_at_ms")
	})
}
