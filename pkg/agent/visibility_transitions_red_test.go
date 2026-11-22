//go:build goolm && stdjson

package agent

// Order 37 — TestVisibilityTransitions (S-91, S-92; Dataset D11, all six
// rows; C-15). Oracle: skills-tools-redesign-spec.md D11 —
//   1 loaded tool, level → hidden: stays in that chat; not in new chats (S-91)
//   2 read_file full → hidden: not in new chats (S-92; its ToolSearch half is
//     order 56's capture surface)
//   3 hidden → full: definition from next turn (S-38)
//   4 permission → deny: gone from next turn, also in that chat (S-40)
//   5 deny → allow: listed per level from next turn (S-40)
//   6 skill level → name_only: next turn's menu shows name only (S-43)
//
// Harness: the real loop with the recording provider (order 36's harness).
// Rows 4 and 5 seed the REAL persisted policy store (config.json
// sandbox.tool_policies) — no seam, they assert what the next turn resolves.
// Rows 1, 2, 3 and 6 need the visibility storage (C-6/C-15:
// sandbox.tool_visibility / sandbox.skill_visibility), which does not exist
// yet — seedToolVisibility below is that missing storage WRITE; GREEN wires
// it to the real store and makes the per-turn surface and menu consult it.
import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"

	"github.com/stretchr/testify/require"
)

// seedToolVisibility is the test-local seam for the C-6/C-15 visibility
// storage writes. key is the tool or skill name; level is the persisted
// visibility level. GREEN wires it to the real persisted store the per-turn
// surface and the skill menu read; nil keeps the RED run loud (BLOCKED),
// never a skip.
var seedToolVisibility func(t testing.TB, home, key, level string)

// visibilityLoop boots the real loop whose ceiling carries basePolicies (the
// in-memory global ceiling the compositor resolves against), runs one turn,
// and returns the tool names of the request.
func visibilityLoop(t *testing.T, session string, basePolicies map[string]string) []string {
	t.Helper()
	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "skills"), 0o700))

	provider := &capturingProvider{}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmp,
				DefaultModel:      config.DefaultModel{Model: "transitions-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 1,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmp}},
		},
	}
	if basePolicies != nil {
		cfg.Sandbox.ToolPolicies = basePolicies
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	agent := al.registry.primaryChatAgentForTest()
	require.NotNil(t, agent)
	_, err := al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey: session, Channel: "cli", ChatID: "direct",
		UserMessage: "hello", DefaultResponse: defaultResponse, SendResponse: false,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, provider.calls, 1, "the loop must have sent one request")
	if len(provider.toolNames) == 0 {
		return nil
	}
	return provider.toolNames[len(provider.toolNames)-1]
}

func requireVisibilitySeam(t *testing.T, what string) {
	t.Helper()
	if seedToolVisibility == nil {
		t.Fatal("BLOCKED: the visibility storage (C-6/C-15 sandbox.tool_visibility / sandbox.skill_visibility) is not implemented — " + what)
	}
}

func TestVisibilityTransitions(t *testing.T) {
	t.Run("D11#4 S-40: a denied tool is not in the turn's surface", func(t *testing.T) {
		names := visibilityLoop(t, "session-vis-4", map[string]string{"read_file": "deny"})
		require.NotContains(t, names, "read_file", "D11#4: the denied tool is gone from the next turn's request")
	})

	t.Run("D11#5 S-40: deny → allow lists the tool per its level from the next turn", func(t *testing.T) {
		names := visibilityLoop(t, "session-vis-5", map[string]string{"read_file": "allow"})
		require.Contains(t, names, "read_file", "D11#5: the re-allowed tool is listed again")
	})

	t.Run("D11#2 S-92: a built-in demoted to hidden is not in new chats' requests", func(t *testing.T) {
		requireVisibilitySeam(t, "required by D11 row 2 and S-92 (the ToolSearch half is order 56's capture surface)")
	})

	t.Run("D11#1 S-91: a loaded tool demoted to hidden stays in that chat, not in new chats", func(t *testing.T) {
		requireVisibilitySeam(t, "required by D11 row 1 and S-91")
	})

	t.Run("D11#3 S-38: hidden → full restores the definition from the next turn", func(t *testing.T) {
		requireVisibilitySeam(t, "required by D11 row 3 and S-38")
	})

	t.Run("D11#6 S-43: a skill demoted to name_only shows name only in the next turn's menu", func(t *testing.T) {
		requireVisibilitySeam(t, "required by D11 row 6 and S-43 (the menu line itself is order 11's surface)")
	})
}
