package systools_test

// Oracle: r17 SR-1/SR-3 (CK-11). The real agent-writer record step
// (tools.RecordArguments on the actual create_agent/update_agent tools) must
// mask an environment-shaped container nested under an UNKNOWN argument, in
// objects and arrays and in any letter case, while unknown key names,
// ordinary unknown values and the structure stay visible (17.3). Every
// secret is fresh per run, so no fixed masked fixture can satisfy the test.
import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func freshNestedEnvSecret(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 12)
	_, err := rand.Read(buf)
	require.NoError(t, err, "instrument: fresh canary generation")
	return "t8-nested-canary-" + hex.EncodeToString(buf)
}

func nestedEnvArgs(top, object, array, caseVariant string) map[string]any {
	return map[string]any{
		"id":            "worker",
		"revision":      "fixture-revision",
		"env_overrides": map[string]any{"TOKEN": top},
		"cli_args":      "--model x --verbose",
		"zz_plain":      "must remain visible",
		"zz_probe":      map[string]any{"env_overrides": map[string]any{"TOKEN": object}, "note": "kept"},
		"zz_list": []any{
			"plain item",
			map[string]any{"deeper": map[string]any{"ENV_OVERRIDES": map[string]any{"API_KEY": array}}},
		},
		"zz_case": map[string]any{"Env_Overrides": caseVariant},
	}
}

func TestT8RecordCopy_NestedUnknownEnvOverridesMaskedInObjectsAndArrays(t *testing.T) {
	writers := []tools.Tool{systools.NewAgentCreateTool(nil), systools.NewAgentUpdateTool(nil)}
	for _, writer := range writers {
		t.Run(writer.Name(), func(t *testing.T) {
			top, object, array, caseVariant := freshNestedEnvSecret(t), freshNestedEnvSecret(t), freshNestedEnvSecret(t), freshNestedEnvSecret(t)
			args := nestedEnvArgs(top, object, array, caseVariant)
			before, err := json.Marshal(args)
			require.NoError(t, err)

			recorded, err := tools.RecordArguments(writer, args)
			require.NoError(t, err, "a nested environment container is masked, not refused")
			encoded, err := json.Marshal(recorded)
			require.NoError(t, err)
			for _, secret := range []string{top, object, array, caseVariant} {
				assert.NotContains(t, string(encoded), secret, "no environment value may reach the record copy")
			}

			want := map[string]any{
				"id":            "worker",
				"revision":      "fixture-revision",
				"env_overrides": map[string]any{"TOKEN": tools.RedactedValue},
				"cli_args":      "--model x --verbose",
				"zz_plain":      "must remain visible",
				"zz_probe":      map[string]any{"env_overrides": map[string]any{"TOKEN": tools.RedactedValue}, "note": "kept"},
				"zz_list": []any{
					"plain item",
					map[string]any{"deeper": map[string]any{"ENV_OVERRIDES": map[string]any{"API_KEY": tools.RedactedValue}}},
				},
				"zz_case": map[string]any{"Env_Overrides": tools.RedactedValue},
			}
			assert.Equal(t, want, recorded, "unknown names, ordinary values and structure stay visible (17.3)")

			after, err := json.Marshal(args)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "the execution copy keeps every original value")
		})
	}
}
