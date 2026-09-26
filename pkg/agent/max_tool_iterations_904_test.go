// max_tool_iterations_904_test.go — #904 RED: the runtime side of the tool
// iteration limit. The per-agent value may only LOWER the global (effective
// = min(global, own)); the running instance, the loop's stop point, the
// tool-limit message and the external-CLI turn cap all follow that one rule.
//
// Spec: docs/internal/specs/tool-iteration-limit-spec.md (Approved) — Dataset
// "Resolver", Dataset "Saved global", Machine-Verifiable Constraints
// (tool-limit message), FR-002/FR-004/FR-015, SC-002. Expected values come
// from the spec, never from running the code.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/routing"
)

// specToolLimitMessage is the exact text from the spec's Machine-Verifiable
// Constraints ("Tool-limit message").
const specToolLimitMessage = "I've reached this agent's limit of tool steps for one turn without a final response. " +
	"An admin can raise the limit in Settings → Performance (\"Max tool calls per turn\"), " +
	"and each agent's own lower limit is on its profile's Advanced tab."

// TestNewAgentInstance_EffectiveLimit_ResolverDataset — test plan row 1
// (runtime leg of SC-001): the instance a turn runs with carries the
// effective limit from Dataset "Resolver", plus the D13 in-memory
// correction of an out-of-range saved global (Dataset "Saved global").
func TestNewAgentInstance_EffectiveLimit_ResolverDataset(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		name        string
		global, own int
		want        int
	}{
		{"resolver_row1_G200_none", 200, 0, 200},
		{"resolver_row2_G200_O50", 200, 50, 50},
		{"resolver_row3_G200_O200", 200, 200, 200},
		{"resolver_row4_G200_O201_capped", 200, 201, 200},
		{"resolver_row5_G200_O500_capped", 200, 500, 200},
		{"resolver_row6_G600_O500", 600, 500, 500},
		{"resolver_row7_G1_none", 1, 0, 1},
		{"resolver_row8_G1000_O1000", 1000, 1000, 1000},
		{"resolver_row9_G200_O5000_capped", 200, 5000, 200},
		{"resolver_row10_G200_O0", 200, 0, 200},
		{"resolver_row11_G200_Oneg5", 200, -5, 200},
		{"saved_row3_G0_none", 0, 0, 200},
		{"saved_row4_Gneg4_none", -4, 0, 200},
		{"saved_row6_G1001_none", 1001, 0, 1000},
		{"saved_row7_G5000_none", 5000, 0, 1000},
		{"saved_row7_G5000_O500", 5000, 500, 500},
		{"saved_row7_G5000_O2000_capped", 5000, 2000, 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Home = filepath.Join(home, "ws")
			cfg.Agents.Defaults.MaxToolIterations = tc.global
			agentCfg := &config.AgentConfig{ID: "iter-904", Name: "Iter904", MaxToolIterations: tc.own}
			ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
			if ag == nil {
				t.Fatal("NewAgentInstance returned nil")
			}
			if ag.MaxIterations != tc.want {
				t.Fatalf("MaxIterations = %d, want %d (global %d, own %d; effective = min(global, own), own <= 0 = none, global corrected per D13)",
					ag.MaxIterations, tc.want, tc.global, tc.own)
			}
		})
	}
}

// TestLoop_StopsAtEffectiveLimit — test plan row 12, scenario "Operator
// lowers one agent" (loop leg) inverted to the ceiling case: global 2, own
// value 5 → the turn stops after exactly 2 tool rounds (own cannot raise).
// History of a turn that stops at limit N: user + N×(assistant, tool) +
// final assistant = 2N+2 entries (N=1 → 4, cf. TestAgentLoop_ToolLimitUsesDedicatedFallback).
func TestLoop_StopsAtEffectiveLimit(t *testing.T) {
	outer := t.TempDir()
	tmpDir := filepath.Join(outer, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 2,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir, MaxToolIterations: 5}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &toolLimitOnlyProvider{})
	al.RegisterTool(&toolLimitTestTool{})

	if _, err := al.ProcessDirectWithChannel(context.Background(), "hello", "tool-limit-904", "test", "chat1"); err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
	def := al.registry.GetDefaultAgent()
	if def == nil {
		t.Fatal("no default agent")
	}
	if def.MaxIterations != 2 {
		t.Fatalf("instance MaxIterations = %d, want 2 (global 2 caps own 5)", def.MaxIterations)
	}
	route := al.registry.ResolveRoute(routing.RouteInput{
		Channel: "test",
		Peer:    &routing.RoutePeer{Kind: string(bus.PeerDirect), ID: "cron"},
	})
	history := def.Sessions.GetHistory(route.SessionKey)
	if len(history) != 6 {
		t.Fatalf("history len = %d, want 6 (limit 2 → user + 2×(assistant,tool) + final)", len(history))
	}
}

// TestToolLimitResponse_Text — FR-015 / US-9: the final message equals the
// spec text exactly and never mentions config.json.
func TestToolLimitResponse_Text(t *testing.T) {
	if toolLimitResponse != specToolLimitMessage {
		t.Fatalf("toolLimitResponse =\n  %q\nwant\n  %q", toolLimitResponse, specToolLimitMessage)
	}
	if strings.Contains(toolLimitResponse, "config.json") {
		t.Fatalf("toolLimitResponse must not mention config.json: %q", toolLimitResponse)
	}
}

// TestNoHiddenLiteral_Guard — test plan row 4, FR-004 / SC-002: no hidden
// fallback for the limit survives in the runtime. It scans the production
// sources the spec names (external_dispatch.go's DefaultExternalMaxTurns,
// instance.go's literal 200 rung) across pkg/.
//
// Instrument check: the same walker must find a symbol that IS present
// (toolLimitResponse) — an empty result from a broken walk is not evidence.
func TestNoHiddenLiteral_Guard(t *testing.T) {
	root := filepath.Join("..") // pkg/
	type hit struct{ file, pattern string }
	banned := []string{"DefaultExternalMaxTurns", "maxIter = 200"}
	var hits []hit
	sawKnown := false
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "generated" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		src := string(raw)
		if strings.Contains(src, "toolLimitResponse") {
			sawKnown = true
		}
		for _, b := range banned {
			if strings.Contains(src, b) {
				hits = append(hits, hit{path, b})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if !sawKnown {
		t.Fatal("instrument check failed: the walker did not find toolLimitResponse in pkg/ — the scan is broken, not clean")
	}
	for _, h := range hits {
		t.Errorf("hidden limit fallback %q still present in %s (FR-004: only the one shipped-default constant may exist)", h.pattern, h.file)
	}
}
