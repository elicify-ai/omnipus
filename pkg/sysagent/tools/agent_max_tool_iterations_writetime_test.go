// agent_max_tool_iterations_writetime_test.go — #904 gate ROUND 2 gap: the
// system agent's create_agent / update_agent re-read the global limit at
// WRITE time, not only at call start (D10/D15).
//
// What the code promises (and therefore what is asserted):
//   - pkg/sysagent/tools/agent.go::persistAndJoin: "re-check the agent's own
//     limit against a FRESH read of the global immediately before the write"
//   - pkg/sysagent/tools/agent_update_adr090.go: "read the global in force
//     INSIDE the closure, under the agent store's per-entity lock"
//   - the documented residual window (a lowering that commits AFTER that
//     fresh read) ends capped-and-flagged by the resolver (D1) — that half is
//     the resolver's job and is covered by the resolver dataset tests.
// So a global lowered BETWEEN the call-start check and the write must refuse
// the save with the D10 text (spec "Machine-Verifiable Constraints": the
// system-agent tools return the same text as INVALID_INPUT), and nothing may
// be written.
//
// Seam: Deps.GetCfg is the only way these tools read the global. It answers
// the call-start global (300) until it is called from inside the write phase
// (persistAndJoin for create; the agent store's MutateState for update), and
// the lowered global (100) from then on — exactly "the admin lowered the
// global while the tool call was in flight". Mutants killed: dropping the
// persistAndJoin re-check; hoisting update's defaults read out of the
// MutateState closure.

package systools_test

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// inWritePhase reports whether the current call stack is inside a frame whose
// function name contains marker.
func inWritePhase(marker string) bool {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, marker) {
			return true
		}
		if !more {
			return false
		}
	}
}

// lowerDuringWrite rewires deps.GetCfg: callStart before the write phase,
// lowered once any call comes from inside it. It returns a counter of calls
// made from inside the write phase, so the test can prove the seam fired.
func lowerDuringWrite(deps *systools.Deps, marker string, callStart, lowered int) *int {
	var mu sync.Mutex
	inside := new(int)
	early := config.DefaultConfig()
	early.Agents.Defaults.MaxToolIterations = callStart
	late := config.DefaultConfig()
	late.Agents.Defaults.MaxToolIterations = lowered
	deps.GetCfg = func() *config.Config {
		if inWritePhase(marker) {
			mu.Lock()
			*inside++
			mu.Unlock()
			return late
		}
		return early
	}
	return inside
}

func TestSysagentCreateAgent_GlobalLoweredBeforeWrite_Refused(t *testing.T) {
	deps, _ := newTestDeps(t)
	inside := lowerDuringWrite(deps, "persistAndJoin", 300, 100)
	res := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":                "Lim Race",
		"description":         "d",
		"soul":                "s",
		"model":               "test/model",
		"max_tool_iterations": float64(200), // valid under the call-start global 300
	})
	if *inside == 0 {
		t.Fatalf("the global was never re-read inside persistAndJoin (D10 write-time check missing); result: isErr=%v %s",
			res.IsError, res.ForLLM)
	}
	if !res.IsError {
		t.Fatalf("create_agent with 200 after the global was lowered to 100 before the write must be refused (D10); got success: %s", res.ForLLM)
	}
	assertInvalidInput(t, res.ForLLM, sysAboveGlobalMsg(200, 100))
	if _, err := agentstore.New(deps.Home).Get("lim-race"); err == nil {
		t.Error("a refused create must leave no agent record")
	}
}

func TestSysagentUpdateAgent_GlobalLoweredBeforeWrite_Refused(t *testing.T) {
	deps := newLimitDeps(t, 300, 0)
	inside := lowerDuringWrite(deps, "agentstore.(*Store).MutateState", 300, 100)
	isErr, body := runUpdate(t, deps, float64(200)) // valid under the call-start global 300
	if *inside == 0 {
		t.Fatalf("the global was never read inside the store write (D10 write-time check missing); result: isErr=%v %s", isErr, body)
	}
	if !isErr {
		t.Fatalf("update_agent with 200 after the global was lowered to 100 before the write must be refused (D10); got success: %s", body)
	}
	assertInvalidInput(t, body, sysAboveGlobalMsg(200, 100))
	if got := storedLimit(t, deps, "lim-agent"); got != 0 {
		t.Errorf("stored own value = %d, want unchanged (none)", got)
	}
}

// Control: with no lowering in flight, the same value is accepted — proves
// the refusal above comes from the write-time read, not from the seam.
func TestSysagentCreateUpdate_NoLoweringInFlight_Accepted(t *testing.T) {
	deps, _ := newTestDeps(t)
	lowerDuringWrite(deps, "persistAndJoin", 300, 300)
	res := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Lim Calm", "description": "d", "soul": "s", "model": "test/model",
		"max_tool_iterations": float64(200),
	})
	if res.IsError {
		t.Fatalf("create_agent 200 under an unchanged global 300 must succeed: %s", res.ForLLM)
	}
	if got := storedLimit(t, deps, "lim-calm"); got != 200 {
		t.Errorf("created own value = %d, want 200", got)
	}

	udeps := newLimitDeps(t, 300, 0)
	lowerDuringWrite(udeps, "agentstore.(*Store).MutateState", 300, 300)
	if isErr, body := runUpdate(t, udeps, float64(200)); isErr {
		t.Fatalf("update_agent 200 under an unchanged global 300 must succeed: %s", body)
	}
	if got := storedLimit(t, udeps, "lim-agent"); got != 200 {
		t.Errorf("updated own value = %d, want 200", got)
	}
}
