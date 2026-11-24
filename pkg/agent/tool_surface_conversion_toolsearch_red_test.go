//go:build goolm && stdjson

package agent

// Order 56 — TestToolSurface_ConversionToolSearch (S-41, S-44; Dataset D7
// row 16; ST-06's capture rule). Oracle: skills-tools-redesign-spec.md —
//   D7 row 16 (the switch-off folder): ToolSearch is locked full and is
//        OFFERED in the normal request after the conversion, ABSENT before.
//        Before, the old request stripped the infrastructure tools
//        (pkg/agent/tool_manifest.go::stripInfraToolDefs; ToolSearch is in
//        pkg/tools/manifest.go::infraManifestToolNames) so ToolSearch was
//        not offered; after the conversion it is offered. This is the
//        SC-003/DOC-014 named change (C-6, ST-Q4 B) and it changes no
//        permission.
//   ST-06: the comparison captures the goal-narrowed first request when there
//        is one AND the next normal request. A goal-narrowed first request
//        bypasses the normal tool surface (loop_run_turn.go
//        ::prepareToolSurface; the narrowed pair is exactly the ADR-088 D3
//        pair), so the conversion must not change it.
//
// Harness: the package's capturing provider (order 36's capturingProvider,
// extended test-locally there) for the normal request, and the real goal
// forcing evaluation (goal_first_move_test.go's helpers) for the narrowed
// capture. The conversion itself (C-19 step 3) does not exist —
// runToolLoadingConversion below is that seam; GREEN wires it to the real
// one-time conversion over the literal pre-upgrade home. Nil keeps the RED
// run loud (BLOCKED), never a skip.
//
// RED today: the "absent before" leg passes (it pins the pre-conversion half,
// today's strip); the "offered after the conversion" leg is BLOCKED at the
// conversion seam — today nothing converts, and the normal request strips
// ToolSearch whatever a config says.
import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"

	"github.com/stretchr/testify/require"
)

// conversionHome writes the literal pre-upgrade home D7 row 16 reads: the
// loading switch OFF (`tools.manifest.compressed` false/absent), no levels.
func conversionHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"),
		[]byte(`{"tools":{"manifest":{"compressed":false}}}`), 0o600))
	return home
}

func TestToolSurface_ConversionToolSearch(t *testing.T) {
	t.Run("D7#16 absent before: the uncompressed normal request does not offer ToolSearch", func(t *testing.T) {
		// Positive control pinning the pre-conversion half of row 16.
		tmp := conversionHome(t)
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home:              tmp,
					DefaultModel:      config.DefaultModel{Model: "conversion-test-model"},
					MaxTokens:         4096,
					MaxToolIterations: 2,
				},
				List: []config.AgentConfig{{ID: "mia", Home: tmp}},
			},
		}
		provider := &capturingProvider{}
		al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
		agentInst := al.registry.primaryChatAgentForTest()
		require.NotNil(t, agentInst)
		_, err := al.runAgentLoop(context.Background(), agentInst, processOptions{
			SessionKey: "session-conversion-1", Channel: "cli", ChatID: "direct",
			UserMessage: "hello", DefaultResponse: defaultResponse, SendResponse: false,
		})
		require.NoError(t, err)
		require.GreaterOrEqual(t, provider.calls, 1, "the loop must have sent one request")
		require.NotContains(t, strings.Join(provider.toolNames[0], ","),
			"ToolSearch",
			"D7#16: before the conversion ToolSearch is absent from the normal request (the old infra strip)")
	})

	t.Run("D7#16 offered after: the conversion locks ToolSearch full and the normal request offers it", func(t *testing.T) {
		nativebinding.Require(t, "pkg/config", runToolLoadingConversion,
			"C-19/D7#16: actual startup tool-loading conversion; actual model request remains the observer")
		home := conversionHome(t)
		t.Setenv(config.EnvHome, home)
		runToolLoadingConversion(t, home)

		cfg := &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home:              home,
					DefaultModel:      config.DefaultModel{Model: "conversion-test-model"},
					MaxTokens:         4096,
					MaxToolIterations: 2,
				},
				List: []config.AgentConfig{{ID: "mia", Home: home}},
			},
		}
		provider := &capturingProvider{}
		al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
		agentInst := al.registry.primaryChatAgentForTest()
		require.NotNil(t, agentInst)
		_, err := al.runAgentLoop(context.Background(), agentInst, processOptions{
			SessionKey: "session-conversion-2", Channel: "cli", ChatID: "direct",
			UserMessage: "hello", DefaultResponse: defaultResponse, SendResponse: false,
		})
		require.NoError(t, err)
		last := provider.toolNames[len(provider.toolNames)-1]
		require.Contains(t, strings.Join(last, ","),
			"ToolSearch",
			"D7#16: after the conversion ToolSearch is locked full and OFFERED in the normal request")

		// Locked: the converted level is full and locked (C-6 locked levels
		// accept only their locked value). The converted config carries it.
		raw, readErr := os.ReadFile(filepath.Join(home, "config.json"))
		require.NoError(t, readErr)
		require.Contains(t, string(raw), "ToolSearch",
			"C-6: the conversion writes ToolSearch's level into the config")
	})

	t.Run("ST-06: the goal-narrowed first request is captured and stays the pair", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &capturingProvider{}, nil)
		agentInst, ok := al.GetRegistry().GetAgent("native-agent")
		require.True(t, ok, "the goal harness registers native-agent")
		allowGoalToolsPolicy(agentInst)
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store, sid, "goal-conversion", "build a tetris game")

		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store, TranscriptSessionID: sid, Channel: "webchat",
		}}
		d := al.evaluateGoalForcing(ts, 1, agentInst.Tools.GetAll())
		require.True(t, d.layer1, "ST-06: the active goal narrows the first request")
		names := make([]string, 0, len(d.narrowed))
		for _, tl := range d.narrowed {
			names = append(names, tl.Name())
		}
		require.NotContains(t, strings.Join(names, ","), "ToolSearch",
			"ST-06: the goal-narrowed first request is exactly the pair — the conversion does not change it")
	})
}

// runToolLoadingConversion runs the one-time C-19 step-3 tool-loading
// conversion over the given OMNIPUS_HOME (reading the old
// tools.manifest.compressed / preview_all_lazy / mcp discovery keys and each
// server's deferred, writing the D7 starting levels with ToolSearch locked
// full). GREEN wires it to the production conversion entry point; nil keeps
// the RED run loud (BLOCKED), never a skip.
var runToolLoadingConversion func(t testing.TB, home string)
