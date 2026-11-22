//go:build goolm && stdjson

package agent

// Order 36 — TestFullSkills_SentinelAndBudget (S-42, S-89, S-90; Dataset D9;
// C-14). Oracle: skills-tools-redesign-spec.md D9 rows 1, 2, 7, 8, 9, 10 —
// "sentinel in first request" for a Full Allow verified skill (row 1 / S-42);
// sentinel absent but listed with its description for Full Ask not approved
// (row 2 / S-89); the C-14 budget with fixed token estimators (rows 7–10).
// Every expected value below is transcribed from the spec, never observed.
//
// Harness: the spec's own "Integration Go (scripted provider, request
// capture)". The package's scripted-provider pattern (scriptedToolProvider,
// eventbus_test.go) is extended test-locally with a RECORDING provider —
// the same process-edge double, no fake server (per the dispatch brief).
// The real agent loop builds and sends the first request.
//
// RED today: the loop injects no Full-skill instructions into the request
// (the C-14 consent/budget machinery is missing), so the sentinel leg fails;
// the S-89 legs (sentinel absent, skill listed) assert what the spec pins
// and must hold once GREEN adds the injection. The budget comparator does
// not exist — its seam is below, and GREEN wires it to the C-14 comparator
// fed by the loop's token estimators.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"

	"github.com/stretchr/testify/require"
)

// capturingProvider scripts one "done" answer and records every request —
// messages and the per-turn tool surface (definition names).
type capturingProvider struct {
	calls     int
	messages  []json.RawMessage
	toolNames [][]string
}

func (m *capturingProvider) Chat(ctx context.Context, msgs []providers.Message, toolDefs []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	m.calls++
	if raw, err := json.Marshal(msgs); err == nil {
		m.messages = append(m.messages, raw)
	}
	names := make([]string, 0, len(toolDefs))
	for _, td := range toolDefs {
		names = append(names, td.Function.Name)
	}
	m.toolNames = append(m.toolNames, names)
	return &providers.LLMResponse{Content: "done"}, nil
}

func (m *capturingProvider) GetDefaultModel() string { return "sentinel-test-model" }

// fullSkillSentinelLoop boots the real loop with a recording provider, a
// granted notes-helper skill whose instructions carry the sentinel, and runs
// one turn. It returns the provider for the request assertions.
func fullSkillSentinelLoop(t *testing.T, session string) *capturingProvider {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmp,
				DefaultModel:      config.DefaultModel{Model: "sentinel-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 2,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmp, Skills: []string{"notes-helper"}}},
		},
	}
	dir := filepath.Join(tmp, "skills", "notes-helper")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	body := "---\nname: notes-helper\ndescription: Notes helper.\n---\n\nZEBRA-NOTES-3 hidden marker.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600))

	provider := &capturingProvider{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	agent := al.registry.primaryChatAgentForTest()
	require.NotNil(t, agent, "expected the chat agent")
	_, err := al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey: session, Channel: "cli", ChatID: "direct",
		UserMessage: "hello", DefaultResponse: defaultResponse, SendResponse: false,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, provider.calls, 1, "the loop must have sent one request")
	return provider
}

func TestFullSkills_SentinelAndBudget(t *testing.T) {
	t.Run("D9#1 S-42: a Full Allow verified skill's sentinel is in the first request", func(t *testing.T) {
		provider := fullSkillSentinelLoop(t, "session-full-1")
		require.Contains(t, string(provider.messages[0]), "ZEBRA-NOTES-3",
			"D9#1: the Full skill's instructions (sentinel) are injected into the first request")
	})

	t.Run("D9#2 S-89: an unapproved Full skill is listed with its description, never injected", func(t *testing.T) {
		// The skill has no stored policy and no approval — the Ask-unapproved
		// row. The sentinel must stay absent while the menu lists the skill.
		provider := fullSkillSentinelLoop(t, "session-full-2")
		first := string(provider.messages[0])
		require.NotContains(t, first, "ZEBRA-NOTES-3", "D9#2: no instructions leak without approval")
		require.Contains(t, first, "notes-helper", "D9#2: the skill is listed with its description")
	})

	// D9 rows 7-10: the C-14 budget comparator with FIXED estimators
	// (context, maxTokens, pinned core, Full skills charged once, tool
	// definitions, current message). B = context − maxTokens −
	// ceil(context/20) − (pinnedCore + FullSkills); send when
	// toolDefs + message ≤ B.
	budget := func(t *testing.T, contextTokens, maxTokens, pinnedCore, fullSkills, toolDefs, message int, wantSend bool, row string) {
		t.Helper()
		if skillBudgetSendOf == nil {
			t.Fatal("BLOCKED: the C-14 Full-skill budget comparator is not implemented — the loop must stop sending when the Full skills no longer fit (D9 rows 7-10, S-90, ST-Q1 A)")
		}
		require.Equal(t, wantSend, skillBudgetSendOf(contextTokens, maxTokens, pinnedCore, fullSkills, toolDefs, message), row)
	}
	t.Run("D9#7: budget B=1600 with toolDefs 1400 + message 200 sends", func(t *testing.T) {
		budget(t, 8000, 1000, 2000, 3000, 1400, 200, true, "row 7: request sent")
	})
	t.Run("D9#8: one token over (Full skills 3001) does not send", func(t *testing.T) {
		budget(t, 8000, 1000, 2000, 3001, 1400, 200, false, "row 8: request not sent")
	})
	t.Run("D9#9: a model switch to a smaller context stops sending", func(t *testing.T) {
		budget(t, 6500, 1000, 2000, 3000, 1400, 200, false, "row 9: smaller model, not sent")
	})
	t.Run("D9#10: a Full skill growing past the limit stops sending", func(t *testing.T) {
		budget(t, 8000, 1000, 2000, 3200, 1400, 200, false, "row 10: grown skill, not sent")
	})
}

// skillBudgetSendOf is the test-local seam for the C-14 budget comparator.
// GREEN wires it to the production comparator the loop feeds with its
// estimators; nil keeps the RED run loud (BLOCKED), never a skip.
var skillBudgetSendOf func(contextTokens, maxTokens, pinnedCoreTokens, fullSkillTokens, toolDefTokens, messageTokens int) bool
