// agent_max_tool_iterations_test.go — #904 RED: the system agent's
// create_agent / update_agent tools obey the same limit rules as the UI
// (D15: "no second way around them").
//
// Spec: docs/internal/specs/tool-iteration-limit-spec.md (Approved) — User
// Story 8 scenarios, Dataset "Per-agent bounds", Machine-Verifiable
// Constraints ("System-agent tools: the same two texts, returned as the
// tool's INVALID_INPUT error result"), API and Data grill G2 (literal nil on
// update_agent clears the own value). Test plan row 11.

package systools_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

const sysBoundMsg = "max_tool_iterations must be between 1 and 1000"

func sysAboveGlobalMsg(n, g int) string {
	return fmt.Sprintf("max_tool_iterations %d is above the global limit (%d); "+
		"lower it, or raise the global limit in Settings → Performance", n, g)
}

// newLimitDeps returns Deps whose live config carries the given global and
// one seeded agent "lim-agent" with the given own value (0 = none).
func newLimitDeps(t *testing.T, global, own int) *systools.Deps {
	t.Helper()
	deps, cfg := newTestDeps(t)
	cfg.Agents.Defaults.MaxToolIterations = global
	rec := &config.AgentConfig{ID: "lim-agent", Name: "Lim Agent", MaxToolIterations: own}
	if err := agentstore.New(deps.Home).Create("lim-agent", rec); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return deps
}

func storedLimit(t *testing.T, deps *systools.Deps, id string) int {
	t.Helper()
	a, err := agentstore.New(deps.Home).Get(id)
	if err != nil {
		t.Fatalf("read agent %s: %v", id, err)
	}
	return a.MaxToolIterations
}

// assertInvalidInput asserts the tool returned an INVALID_INPUT error whose
// message contains the exact spec text.
func assertInvalidInput(t *testing.T, body, wantMsg string) {
	t.Helper()
	m := parseError(t, body)
	e, _ := m["error"].(map[string]any)
	if e == nil {
		t.Fatalf("error object missing: %s", body)
	}
	if e["code"] != "INVALID_INPUT" {
		t.Errorf("error code = %v, want INVALID_INPUT; body: %s", e["code"], body)
	}
	if msg, _ := e["message"].(string); !strings.Contains(msg, wantMsg) {
		t.Errorf("error message = %q, want it to contain %q", msg, wantMsg)
	}
}

func runUpdate(t *testing.T, deps *systools.Deps, value any) (isErr bool, body string) {
	t.Helper()
	res := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":                  "lim-agent",
		"revision":            currentAgentRevision(t, deps, "lim-agent"),
		"max_tool_iterations": value,
	})
	return res.IsError, res.ForLLM
}

// Scenario "System agent cannot exceed the global" (US-8 AS-1).
func TestSysagentAgentTools_MaxToolIterations_UpdateAboveGlobalRefused(t *testing.T) {
	deps := newLimitDeps(t, 200, 0)
	isErr, body := runUpdate(t, deps, float64(300))
	if !isErr {
		t.Fatalf("update_agent with 300 over global 200 must fail; got success: %s", body)
	}
	assertInvalidInput(t, body, sysAboveGlobalMsg(300, 200))
	if got := storedLimit(t, deps, "lim-agent"); got != 0 {
		t.Errorf("stored own value = %d, want unchanged (none)", got)
	}
}

// Scenario "System agent creates with an own value" (US-8 AS-2).
func TestSysagentAgentTools_MaxToolIterations_CreateWithOwnValue(t *testing.T) {
	deps, cfg := newTestDeps(t)
	cfg.Agents.Defaults.MaxToolIterations = 200
	res := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":                "Lim Created",
		"description":         "d",
		"soul":                "s",
		"model":               "test/model",
		"max_tool_iterations": float64(40),
	})
	if res.IsError {
		t.Fatalf("create_agent with 40 (global 200) must succeed: %s", res.ForLLM)
	}
	if got := storedLimit(t, deps, "lim-created"); got != 40 {
		t.Errorf("created agent own value = %d, want 40", got)
	}
}

// create_agent above the global is refused with the same text (D15/FR-007).
func TestSysagentAgentTools_MaxToolIterations_CreateAboveGlobalRefused(t *testing.T) {
	deps, cfg := newTestDeps(t)
	cfg.Agents.Defaults.MaxToolIterations = 200
	res := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":                "Lim Too High",
		"description":         "d",
		"soul":                "s",
		"model":               "test/model",
		"max_tool_iterations": float64(500),
	})
	if !res.IsError {
		t.Fatalf("create_agent with 500 over global 200 must fail; got success: %s", res.ForLLM)
	}
	assertInvalidInput(t, res.ForLLM, sysAboveGlobalMsg(500, 200))
	if _, err := agentstore.New(deps.Home).Get("lim-too-high"); err == nil {
		t.Error("a refused create must leave no agent record")
	}
}

// Scenario "System agent clears an own value" (US-8 AS-3) — grill G2: a
// LITERAL nil arg (JSON null) clears the own value; no error.
func TestSysagentAgentTools_MaxToolIterations_UpdateNilClears(t *testing.T) {
	deps := newLimitDeps(t, 200, 40)
	isErr, body := runUpdate(t, deps, nil)
	if isErr {
		t.Fatalf("update_agent with max_tool_iterations: null must clear, not fail: %s", body)
	}
	if got := storedLimit(t, deps, "lim-agent"); got != 0 {
		t.Errorf("own value after null = %d, want cleared (0 = no own value)", got)
	}
}

// Omitted field leaves the own value unchanged (FR-008, map-level presence).
func TestSysagentAgentTools_MaxToolIterations_UpdateOmittedUnchanged(t *testing.T) {
	deps := newLimitDeps(t, 200, 40)
	res := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":       "lim-agent",
		"revision": currentAgentRevision(t, deps, "lim-agent"),
		"name":     "Renamed",
	})
	if res.IsError {
		t.Fatalf("unrelated update failed: %s", res.ForLLM)
	}
	if got := storedLimit(t, deps, "lim-agent"); got != 40 {
		t.Errorf("own value = %d, want 40 unchanged", got)
	}
}

// Scenario "System agent out of range" (US-8 AS-4) — Dataset "Per-agent
// bounds" rows 3–5 on BOTH tools (global 1000), plus rows 1–2 accepted.
func TestSysagentAgentTools_MaxToolIterations_BoundsDataset(t *testing.T) {
	cases := []struct {
		name   string
		value  float64
		wantOK bool
	}{
		{"row1_min_1", 1, true},
		{"row2_max_1000", 1000, true},
		{"row3_zero", 0, false},
		{"row4_negative", -3, false},
		{"row5_1001", 1001, false},
	}
	for _, tc := range cases {
		t.Run("update_"+tc.name, func(t *testing.T) {
			deps := newLimitDeps(t, 1000, 0)
			isErr, body := runUpdate(t, deps, tc.value)
			if tc.wantOK {
				if isErr {
					t.Fatalf("update_agent %v must be accepted: %s", tc.value, body)
				}
				if got := storedLimit(t, deps, "lim-agent"); got != int(tc.value) {
					t.Errorf("own value = %d, want %v", got, tc.value)
				}
				return
			}
			if !isErr {
				t.Fatalf("update_agent %v must be refused: %s", tc.value, body)
			}
			assertInvalidInput(t, body, sysBoundMsg)
			if got := storedLimit(t, deps, "lim-agent"); got != 0 {
				t.Errorf("stored own value = %d, want unchanged (none)", got)
			}
		})
		t.Run("create_"+tc.name, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			cfg.Agents.Defaults.MaxToolIterations = 1000
			res := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
				"name":                "Bound " + tc.name,
				"description":         "d",
				"soul":                "s",
				"model":               "test/model",
				"max_tool_iterations": tc.value,
			})
			if tc.wantOK {
				if res.IsError {
					t.Fatalf("create_agent %v must be accepted: %s", tc.value, res.ForLLM)
				}
				return
			}
			if !res.IsError {
				t.Fatalf("create_agent %v must be refused: %s", tc.value, res.ForLLM)
			}
			assertInvalidInput(t, res.ForLLM, sysBoundMsg)
		})
	}
}
