// Package agent — circuit-breaker integration test (FIX C4).
//
// This test verifies that the loop.go wiring (`contentForLLM += cbGuidance`)
// actually delivers the breaker guidance into the tool-result message the model
// sees. A regression that drops the `contentForLLM += cbGuidance` line, or
// places it after the message is built, would leave the pure recordToolOutcome
// unit tests green while silently breaking the LLM-visible path.
//
// Seam used: ScenarioProvider.LastMessages() captures the full message slice
// passed to the MOST RECENT Chat() call. After the threshold of consecutive
// provisioning-tool failures the next Chat() call (where the model decides what
// to do next) receives all prior messages — including the tool-result message
// that carries the breaker guidance. We assert that at least one role="tool"
// message in that slice contains shellBreakerGuidance.
//
// Level: loop-level integration through ProcessDirect → runAgentLoop → runTurn
// → tool dispatch → recordToolOutcome → contentForLLM injection.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// alwaysErrorExecStub is a test-only tool that masquerades as the "exec" tool
// (provisioning-prone) and always returns IsError=true. This simulates a
// sandbox that denies every exec attempt.
type alwaysErrorExecStub struct {
	tools.BaseTool
}

func (s *alwaysErrorExecStub) Name() string        { return "exec" }
func (s *alwaysErrorExecStub) Description() string { return "exec stub — always errors" }
func (s *alwaysErrorExecStub) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cmd": map[string]any{"type": "string"},
		},
	}
}
func (s *alwaysErrorExecStub) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (s *alwaysErrorExecStub) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return &tools.ToolResult{
		ForLLM:  "sandbox denied: exec is not permitted",
		IsError: true,
	}
}

// TestBreakerIntegration_GuidanceReachesModel drives runTurn end-to-end with
// consecutive provisioning-tool failures and asserts that the model receives
// the shellBreakerGuidance text in a tool-result message after the threshold.
//
// The scenario:
//
//	Step 1: LLM calls exec (→ error 1)
//	Step 2: LLM calls exec (→ error 2)
//	Step 3: LLM calls exec (→ error 3, breaker fires)
//	Step 4: LLM returns plain text (receives messages including the guided result)
//
// Assertion: the messages seen by the LLM at step 4 contain at least one
// role="tool" message whose content includes shellBreakerGuidance.
func TestBreakerIntegration_GuidanceReachesModel(t *testing.T) {
	// -----------------------------------------------------------------------
	// Arrange: minimal workspace, AgentLoop with scripted provider.
	// -----------------------------------------------------------------------
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}

	// Four scripted steps: three consecutive exec calls then a plain text reply.
	provider := testutil.NewScenario().
		WithToolCall("exec", `{"cmd":"apt install curl"}`).
		WithToolCall("exec", `{"cmd":"apt install wget"}`).
		WithToolCall("exec", `{"cmd":"snap install go"}`).
		WithText("I cannot install software in this sandbox.")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspaceDir,
				ModelName:         "scripted-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	// Register the exec stub so the loop can dispatch the tool calls.
	al.RegisterTool(&alwaysErrorExecStub{})

	// -----------------------------------------------------------------------
	// Act: drive runTurn through ProcessDirect.
	// -----------------------------------------------------------------------
	ctx := context.Background()
	_, err := al.ProcessDirect(ctx, "please run apt install curl", "test-breaker-integration")
	if err != nil {
		t.Fatalf("ProcessDirect returned unexpected error: %v", err)
	}

	// -----------------------------------------------------------------------
	// Assert: ScenarioProvider must have consumed all 4 steps.
	// -----------------------------------------------------------------------
	if provider.CallCount() != 4 {
		t.Fatalf("expected 4 LLM calls (3 tool steps + 1 recovery), got %d", provider.CallCount())
	}

	// -----------------------------------------------------------------------
	// Assert: the messages delivered to the 4th Chat() call must contain at
	// least one role="tool" message that includes shellBreakerGuidance.
	//
	// LastMessages() returns the slice passed to the most recent (4th) Chat.
	// That slice includes all prior messages including the three tool results.
	// The 3rd tool result is the one where cbGuidance was appended.
	// -----------------------------------------------------------------------
	msgs := provider.LastMessages()
	if len(msgs) == 0 {
		t.Fatal("LastMessages() returned empty — provider was not called or messages not captured")
	}

	var foundGuidance bool
	for _, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, shellBreakerGuidance) {
			foundGuidance = true
			break
		}
	}

	if !foundGuidance {
		// Build a summary of tool messages for diagnosis.
		var toolContents []string
		for _, m := range msgs {
			if m.Role == "tool" {
				toolContents = append(toolContents, m.Content)
			}
		}
		t.Errorf(
			"breaker guidance was NOT delivered to model:\n"+
				"  want: role=tool message containing %q\n"+
				"  tool message contents seen by model: %v\n"+
				"  (regression: contentForLLM += cbGuidance line in loop.go may be missing or misplaced)",
			shellBreakerGuidance,
			toolContents,
		)
	}
}
