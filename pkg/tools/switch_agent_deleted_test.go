// switch_agent_deleted_test.go — deletion guard for session-core DEL-07 and
// DEL-21: the switch_agent tool, its handover path (HandoffEvent, the routing
// pin map, the agent_switched emitter), UnifiedStore.SwitchAgent, and the
// legacy tool-policy key remap (MigrateLegacyToolPolicyKeys) are gone and must
// not be reintroduced by a merge from an older branch.

package tools

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// retiredSwitchAgentTokens are the live-code spellings of the deleted surface.
// Comments are exempt (retirement notes may name them); only code lines count.
var retiredSwitchAgentTokens = []string{
	`"switch_agent"`,
	"SwitchAgentTool",
	"SwitchAgentDefaultTarget",
	"ExcludedSwitchAgent",
	"HandoffEvent",
	"HandoffSessionStore",
	"MigrateLegacyToolPolicyKeys",
	"migrateLegacyToolPolicyMap",
	"legacyToolPolicyKeyMigrations",
	"GetLastSwitchToDefault",
	"GetSessionActiveAgent",
	"sessionActiveAgent",
	"lastSwitchToDefault",
	".SwitchAgent(",
	"hubEmitAgentSwitched",
	"WsFrameTypeAgentSwitched",
	"AgentSwitchedFrame",
}

// retiredTokenOnLine reports the first retired token found on a code line, or
// "" when the line is blank, a comment, or clean.
func retiredTokenOnLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") {
		return ""
	}
	for _, tok := range retiredSwitchAgentTokens {
		if strings.Contains(line, tok) {
			return tok
		}
	}
	return ""
}

func TestSwitchAgentDeleted_NoProductionReferences(t *testing.T) {
	// Instrument check: the matcher must be able to see the failure it guards.
	require.Equal(t, `"switch_agent"`, retiredTokenOnLine(`	tools.Register("switch_agent")`),
		"instrument check: the matcher must flag a live switch_agent literal")
	require.Equal(t, "", retiredTokenOnLine(`	// "switch_agent" was deleted`),
		"instrument check: a retirement comment must not be flagged")

	roots := []string{"../../pkg", "../../cmd"}
	scanned := 0
	sawKnownSymbol := false
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "generated" || d.Name() == "spa" || d.Name() == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			scanned++
			for i, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "NewMessageTool") {
					sawKnownSymbol = true
				}
				if tok := retiredTokenOnLine(line); tok != "" {
					t.Errorf("%s:%d reintroduces retired switch_agent surface %s: %s",
						path, i+1, tok, strings.TrimSpace(line))
				}
			}
			return nil
		})
		require.NoError(t, err)
	}
	require.Greater(t, scanned, 500, "scanner must walk the real source tree")
	require.True(t, sawKnownSymbol, "instrument check: scanner must see a symbol known to exist (NewMessageTool)")
}

func TestSwitchAgentDeleted_EmbeddedSkillsDoNotReference(t *testing.T) {
	root := "../skills/embedded"
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++
		assert.NotContains(t, string(data), "switch_agent", "%s names the deleted switch_agent tool", path)
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, scanned, 10, "scanner must walk the embedded skills")
	_, err = os.Stat(filepath.Join(root, "handoff"))
	assert.True(t, os.IsNotExist(err), "the handoff skill existed only to call switch_agent and must stay deleted")
}

func TestSwitchAgentDeleted_NotInCatalogOrPolicies(t *testing.T) {
	// Instrument check: the catalog does expose names, so absence is meaningful.
	names := map[string]bool{}
	for _, tl := range GeneralBuiltinMetadata() {
		names[tl.Name()] = true
	}
	require.True(t, names["send_message"], "instrument check: catalog must list send_message")
	assert.False(t, names["switch_agent"], "switch_agent must not be in the general builtin catalog")

	policies := config.DefaultConfig().Sandbox.ToolPolicies
	require.Contains(t, policies, "send_message", "instrument check: shipped ceiling must list send_message")
	for _, retired := range []string{"switch_agent", "hand_off", "return_to_default"} {
		assert.NotContains(t, policies, retired, "shipped ceiling must not carry retired key %q", retired)
	}
}

func TestSwitchAgentDeleted_StoreMethodGone(t *testing.T) {
	typ := reflect.TypeOf(&session.UnifiedStore{})
	_, ok := typ.MethodByName("SetMeta")
	require.True(t, ok, "instrument check: reflection must see SetMeta")
	_, has := typ.MethodByName("SwitchAgent")
	assert.False(t, has, "UnifiedStore.SwitchAgent (the handover writer) must stay deleted (DEL-07)")
}
