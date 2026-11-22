//go:build goolm && stdjson

package systools_test

// Order 48 — TestSetConfig_RefusesRetiredManifestKeys (S-44; ST-Q4 B; C-22
// row 3). Oracle: skills-tools-redesign-spec.md — C-22 row 3: "`set_config`
// and `PUT /config` refuse `tools.manifest.compressed`,
// `tools.manifest.preview_all_lazy` and `tools.mcp.servers.<id>.deferred`
// with 'This setting was replaced by levels in Skills & Tools.'; the
// Settings entries for `tools.manifest.compressed` (`performanceOnlyConfigKeys`,
// the FR-100 guard list) are removed in the same change, so the key is never
// unguarded." S-44 names the same refusal for `set_config` with
// `tools.manifest.compressed: true`.
//
// RED today: this tree is the C-22 "between the two landings" state — the
// Settings base's block list explicitly KEEPS `tools.manifest.compressed`
// settable (config_blocklist_test.go lists it under the allowed keys), so
// set_config accepts all three retired keys and no refusal with the spec's
// text exists.
import (
	"context"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"

	"github.com/stretchr/testify/require"
)

func TestSetConfig_RefusesRetiredManifestKeys(t *testing.T) {
	deps, _ := newTestDepsWithRealSave(t)
	t.Setenv("OMNIPUS_HOME", deps.Home)
	setTool := systools.NewConfigSetTool(deps)

	const wantReason = "This setting was replaced by levels in Skills & Tools."

	for _, tc := range []struct {
		key   string
		value any
	}{
		{"tools.manifest.compressed", true},
		{"tools.manifest.preview_all_lazy", true},
		{"tools.mcp.servers.github.deferred", true},
	} {
		t.Run(tc.key, func(t *testing.T) {
			res := setTool.Execute(context.Background(), map[string]any{"key": tc.key, "value": tc.value})
			require.True(t, res.IsError,
				"C-22 row 3: the retired key %s must be refused, got success: %s", tc.key, res.ForLLM)
			require.Contains(t, res.ForLLM, wantReason,
				"C-22 row 3: the refusal for %s carries the spec's text, got: %s", tc.key, res.ForLLM)
		})
	}

	// Control (S-44: only the retired keys leave): an ordinary setting under
	// tools.* that this package does not retire stays settable.
	res := setTool.Execute(context.Background(), map[string]any{
		"key": "tools.read_file.max_read_file_size", "value": 4096,
	})
	require.False(t, res.IsError,
		"S-44: a non-retired tools.* key must stay settable, got: %s", res.ForLLM)
}
