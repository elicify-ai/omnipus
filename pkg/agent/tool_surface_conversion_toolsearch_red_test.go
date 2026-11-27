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
// capture. The conversion itself (C-19 step 3) is the one-time
// skill-permissions upgrade, pkg/skillperm.Run(home, Options{}), which the
// gateway runs at boot BEFORE it loads the config
// (pkg/gateway/gateway.go::loadConfigAndProvider) — toolLoadingConversionRunOf
// below is the seam to it. The "offered after" leg therefore follows the
// production boot order: the upgrade rewrites the literal pre-upgrade home,
// config.LoadConfig reads the converted file, and the loop is built from that
// loaded config. A config built in memory would leave Sandbox.ToolVisibility
// nil, the turn would read the pre-conversion tiers, and the leg could never
// observe the conversion. A nil seam keeps a tree without the upgrade loud
// (BLOCKED), never a skip.
//
// RED today: the "absent before" leg passes (it pins the pre-conversion half,
// today's strip); the "offered after the conversion" leg is BLOCKED at the
// conversion seam — today nothing converts, and the normal request strips
// ToolSearch whatever a config says.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"

	"github.com/stretchr/testify/require"
)

// conversionHome writes the literal pre-upgrade home D7 row 16 reads: the
// loading switch OFF (`tools.manifest.compressed` false/absent), no levels. The
// file carries the schema version every installed config has, because the
// "offered after" leg loads it with config.LoadConfig exactly as boot does.
func conversionHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"),
		[]byte(`{"version":1,"tools":{"manifest":{"compressed":false}}}`), 0o600))
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
		home := conversionHome(t)
		t.Setenv(config.EnvHome, home)
		// Production boot order: the one-time upgrade rewrites config.json, then the
		// config is loaded from it, then the loop is built from the loaded config.
		runToolLoadingConversion(t, home)
		cfg, loadErr := config.LoadConfig(filepath.Join(home, "config.json"))
		require.NoError(t, loadErr, "the converted config.json must load")
		cfg.Agents.Defaults.Home = home
		cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "conversion-test-model"}
		cfg.Agents.Defaults.MaxTokens = 4096
		cfg.Agents.Defaults.MaxToolIterations = 2
		cfg.Agents.List = []config.AgentConfig{{ID: "mia", Home: home}}
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
		// accept only their locked value). The converted config carries it, under
		// the levels' own key (spec Storage: sandbox.tool_visibility.<key>). The
		// earlier check only looked for the text "ToolSearch" anywhere in the file,
		// which sandbox.tool_policies.ToolSearch also satisfies, so it could not
		// tell a conversion that wrote the level from one that did not.
		raw, readErr := os.ReadFile(filepath.Join(home, "config.json"))
		require.NoError(t, readErr)
		var stored map[string]any
		require.NoError(t, json.Unmarshal(raw, &stored), "the converted config.json is valid JSON")
		sandboxSection, _ := stored["sandbox"].(map[string]any)
		levels, _ := sandboxSection["tool_visibility"].(map[string]any)
		require.Equal(t, "full", levels["ToolSearch"],
			"D7#16/C-6: the conversion stores ToolSearch locked at full under sandbox.tool_visibility")
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

// toolLoadingConversionRunOf is the seam to the one-time C-19 upgrade that
// holds the step-3 tool-loading conversion (reading the old
// tools.manifest.compressed / preview_all_lazy / mcp discovery keys and each
// server's deferred, writing the D7 starting levels with ToolSearch locked
// full): pkg/skillperm.Run(home string, options skillperm.Options) error,
// the call the gateway makes at boot with zero options.
//
// Part L: the earlier seam took a testing.TB, which no production function can
// have (Go assigns only identical function types), so it could never be
// bound. The entry's options type cannot be named here — this package's tests
// must still compile on a tree that has no pkg/skillperm — so the seam is
// typed any (the same shape as pkg/gateway's skillUpgradeRunOf) and
// runToolLoadingConversion checks the signature it needs.
// tool_surface_conversion_bindings_test.go assigns skillperm.Run;
// tests/helpers/nativebinding refuses a closure or test code, so a test-side
// stand-in cannot satisfy it. nil keeps the run loud (BLOCKED), never a skip.
var toolLoadingConversionRunOf any

// runToolLoadingConversion runs the real one-time upgrade once over home,
// exactly as the gateway boot does (zero options), and fails the test with
// the upgrade's own error if it returns one.
func runToolLoadingConversion(t *testing.T, home string) {
	t.Helper()
	nativebinding.Require(t, "pkg/skillperm", toolLoadingConversionRunOf,
		"C-19 step 3/D7#16: the one-time upgrade entry that converts the tool-loading switches (the binding is skillperm.Run); the actual model request remains the observer")
	fn := reflect.ValueOf(toolLoadingConversionRunOf)
	ft := fn.Type()
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	if ft.NumIn() != 2 || ft.In(0).Kind() != reflect.String || ft.In(1).Kind() != reflect.Struct ||
		ft.NumOut() != 1 || ft.Out(0) != errorType {
		t.Fatalf("BLOCKED: the bound conversion entry has the signature %s, want func(home string, options <struct>) error — required by C-19 step 3", ft)
	}
	out := fn.Call([]reflect.Value{reflect.ValueOf(home).Convert(ft.In(0)), reflect.New(ft.In(1)).Elem()})
	if out[0].IsNil() {
		return
	}
	upgradeErr, isError := out[0].Interface().(error)
	require.True(t, isError, "the conversion entry's one result is an error")
	require.NoError(t, upgradeErr, "C-19: the one-time upgrade over the pre-upgrade home must finish")
}
