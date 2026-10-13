//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func l1WU2RealTurnLoop(t *testing.T, id string, provider providers.LLMProvider) (*AgentLoop, *AgentInstance, *config.Config) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.RestrictToWorkspace = true
	roleTools := coreagent.NewCustomAgentToolsCfg()
	roleTools.Builtin.Policies["write_file"] = config.ToolPolicyAllow
	cfg.Agents.List = []config.AgentConfig{{ID: id, Name: id, Type: config.AgentTypeCore, Tools: roleTools}}
	for wsID, data := range map[string]string{
		"default":  `{"id":"default","name":"Default","is_default":true,"status":"active","core_team":["omnipus"]}`,
		"sales":    `{"id":"sales","name":"Sales","status":"active","core_team":["mia"]}`,
		"research": `{"id":"research","name":"Research","status":"active","core_team":[]}`,
	} {
		path := filepath.Join(home, "workspaces", wsID+".json")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces", wsID, "work"), 0o700))
	}
	// Do not use mustNewAgentLoop: it silently adds fixture memberships.
	al, err := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })
	inst, present := al.GetRegistry().GetAgent(id)
	require.True(t, present, "the explicit fixture identity must be live")
	return al, inst, cfg
}

// FR-006 / BDD-1.8 + amendment §2 A8: real write_file dispatch, no policy or
// filesystem mock. Only the paid model edge is scripted. It must not grant
// cross-workspace writes to an ordinary colleague.
func TestL1WU2CrossWorkspaceSetupAuthority_RealTurnOmnipusOnly(t *testing.T) {
	for _, row := range []struct {
		id, target string
		allowed    bool
	}{
		{"mia", "sales", true}, // same-workspace green positive control
		{"omnipus", "research", true},
		{"mia", "research", false},
	} {
		t.Run(row.id+"_"+row.target, func(t *testing.T) {
			provider := testutil.NewScenario()
			al, inst, _ := l1WU2RealTurnLoop(t, row.id, provider)
			home := config.OmnipusHomeDir()
			target := filepath.Join(home, "workspaces", row.target, "work", "setup-proof.txt")
			args, err := json.Marshal(map[string]any{"path": target, "content": "setup-file-from-real-turn", "overwrite": true})
			require.NoError(t, err)
			provider.WithToolCall("write_file", string(args)).WithText("turn complete")
			anchor := "sales"
			if row.id == "omnipus" {
				anchor = "default"
			}
			answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
				SessionKey: "l1-wu2-setup-" + row.id, Channel: "webchat", ChatID: "operator",
				WorkspaceID: anchor, UserInitiated: true, UserMessage: "Write the setup file in the named workspace.",
			})
			require.NoError(t, err, "the turn itself must run; a routing refusal is not evidence about file authority")
			assert.Equal(t, "turn complete", answer)
			result := toolResultText(t, provider, "write_file-0")
			if row.allowed {
				data, err := os.ReadFile(target)
				require.NoError(t, err, "FR-006: allowed real write_file dispatch must create the setup file; result=%s", result)
				assert.Equal(t, "setup-file-from-real-turn", string(data))
			} else {
				_, err := os.Stat(target)
				assert.ErrorIs(t, err, os.ErrNotExist, "Mia must not gain Omnipus's cross-workspace authority")
				assert.Contains(t, result, "permission_denied", "refusal must come from the real file boundary")
			}
			if row.id == "omnipus" {
				resolved, err := resolveTurnWorkDirOrRefuse(context.Background(), row.id, inst.Home, anchor)
				require.NoError(t, err)
				assert.Equal(t, inst.Home, resolved, "OSA-9.1: setup target must not reroot the Omnipus anchor")
			}
		})
	}
}

// FR-015: the MODE NOTE is dynamic context, not a source-text substring.
// Static workflow instructions mentioning God Mode cannot satisfy this test.
func TestL1WU2GodModeNote_OneNewLineReachesRealTurnAndDisappearsWhenOff(t *testing.T) {
	provider := testutil.NewScenario().WithText("normal").WithText("god").WithText("normal again")
	al, inst, cfg := l1WU2RealTurnLoop(t, "omnipus", provider)
	withGodModeAvailable(t, true)
	modeLines := func(messages []providers.Message) map[string]int {
		out := map[string]int{}
		for _, msg := range messages {
			for _, line := range strings.Split(msg.Content, "\n") {
				if strings.Contains(strings.ToLower(line), "god mode") {
					out[strings.TrimSpace(line)]++
				}
			}
		}
		return out
	}
	counts := []map[string]int{}
	for index, enabled := range []bool{false, true, false} {
		cfg.Sandbox.GodMode = enabled
		before := len(provider.AllRequests())
		_, err := al.runAgentLoop(context.Background(), inst, processOptions{
			SessionKey:  []string{"mode-normal", "mode-god", "mode-normal-again"}[index],
			WorkspaceID: "default", Channel: "webchat", ChatID: "operator", UserInitiated: true,
			UserMessage: "Describe your current mode.",
		})
		require.NoError(t, err)
		requests := provider.AllRequests()
		require.Greater(t, len(requests), before, "positive control: each turn actually reached the provider edge")
		counts = append(counts, modeLines(requests[before]))
	}
	added := 0
	for line, count := range counts[1] {
		if delta := count - counts[0][line]; delta > 0 {
			added += delta
		}
	}
	assert.Equal(t, 1, added, "FR-015: God Mode must add exactly one mode-note line to each Omnipus turn")
	assert.Equal(t, counts[0], counts[2], "turning God Mode off must remove the dynamic mode note")
}
