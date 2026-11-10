// T11-B §4 Agents / FR-11. Schemas AND the real argument-validation seam
// must refuse path/preset writes. Existing update/set_config protection is
// labelled a regression pin, never claimed as observed RED.
package systools

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/stretchr/testify/require"
)

func t11bPathOrPresetKeys(t *testing.T, params map[string]any) []string {
	t.Helper()
	props, ok := params["properties"].(map[string]any)
	require.True(t, ok, "instrument: the real tool schema must expose properties")
	require.Contains(t, props, "name", "instrument: inspect a known real property before trusting no forbidden fields")
	var found []string
	for key := range props {
		low := strings.ToLower(key)
		if low == "cli_path" || strings.Contains(low, "preset") {
			found = append(found, key)
		}
	}
	sort.Strings(found)
	return found
}

func TestAgentCreateTool_NoPathOrPresetParameters(t *testing.T) {
	tool := &AgentCreateTool{}
	require.Empty(t, t11bPathOrPresetKeys(t, tool.Parameters()), "FR-11: create_agent must not advertise path/preset writes; a person owns Settings")
}

// Regression pin: absence from the schema alone cannot prove Execute refuses.
func TestAgentUpdateTool_NoPathOrPresetParameters(t *testing.T) {
	tool := &AgentUpdateTool{}
	require.Empty(t, t11bPathOrPresetKeys(t, tool.Parameters()), "FR-11: update_agent must not advertise path/preset writes")
}

func TestAgentTools_RejectPathOrPresetArguments(t *testing.T) {
	// The real create/update Execute paths both call this validator. Probe
	// that seam without requiring unrelated model credentials or persistence.
	for _, tool := range []struct {
		name    string
		allowed map[string]struct{}
	}{
		{"create_agent", agentCreateArgNames},
		{"update_agent", agentUpdateArgNames},
	} {
		for _, field := range []string{"cli_path", "preset_args"} {
			t.Run(tool.name+"/"+field, func(t *testing.T) {
				// Passing control: an allowed property must clear validation.
				require.NoError(t, rejectUnknownAgentFields(map[string]any{"name": "Codex worker"}, tool.allowed))
				err := rejectUnknownAgentFields(map[string]any{field: "/agent/attempt"}, tool.allowed)
				require.Error(t, err, "FR-11: %s must refuse %s, not silently discard or accept it", tool.name, field)
				var fe *agentmutation.FieldError
				require.True(t, errors.As(err, &fe), "a forbidden write needs a field-attributed input error; got %T", err)
				require.Same(t, fe, err, "retain the original top-level FieldError assertion; a wrapper must not broaden this test")
				require.Equal(t, agentmutation.InvalidInput, fe.Code)
				require.Equal(t, []string{field}, fe.Fields)
				require.Contains(t, fe.Error(), field)
			})
		}
	}
}

// Regression pin: sandbox is already write-blocked; assert the refusal is
// specifically a write-policy refusal rather than an unrelated unknown key.
func TestSetConfig_BlocksThirdPartyAgentSettingsKeys(t *testing.T) {
	for _, key := range []string{
		"sandbox.third_party_agents",
		"sandbox.third_party_agents.clis.codex",
		"sandbox.third_party_agents.clis.codex.path",
		"sandbox.third_party_agents.clis.codex.preset_args",
	} {
		t.Run(key, func(t *testing.T) {
			err := validateConfigKey(nil, key)
			require.Error(t, err, "FR-11: the preset store is person-only")
			require.Contains(t, err.Error(), "not writable via this tool")
			require.Contains(t, err.Error(), key)
		})
	}
}
