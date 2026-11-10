package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Dispatch #2: terminal billing failure and ADR-087's one truncated-argument
// repair each retain the failed call once. Recovery does not debit it again.
// The turn/recovery/transcript are real; only the external transport is scripted.
func TestCachingFixBilledTerminalFailureOnce(t *testing.T) {
	usage := &providers.UsageInfo{PromptTokens: 40, CompletionTokens: 10, TotalTokens: 50}
	for _, tc := range []struct {
		name       string
		failure    error
		wantCalls  int
		wantError  bool
		wantTokens []int
	}{
		{"stream_billing", &common.StreamProviderError{Code: "402", Message: "insufficient credit", Usage: usage}, 1, true, []int{50}},
		{"truncated_tool_arguments", &common.ToolArgumentsError{Cause: common.ErrToolArgumentsUndecodable, ToolName: "fixture", FinishReason: "length", Truncated: true, Usage: usage}, 2, false, []int{50, 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			al, home := schedTestLoop(t)
			provider := testutil.NewScenario().WithError(tc.failure).WithText("ACCOUNTED").WithUsage(2, 3)
			registerAgent(t, al, home, "mia", provider)
			meta, err := al.GetSessionStore().NewScheduledSession("mia")
			if err != nil {
				t.Fatal(err)
			}
			reply, err := al.ProcessScheduled(context.Background(), "mia", meta.ID, "go", "scheduled", meta.ID)
			if (err != nil) != tc.wantError || provider.CallCount() != tc.wantCalls {
				t.Fatalf("wrong recovery outcome: calls=%d err=%v, want%d/error%t", provider.CallCount(), err, tc.wantCalls, tc.wantError)
			}
			if !tc.wantError && reply != "ACCOUNTED" {
				t.Fatalf("repair reply=%q want ACCOUNTED", reply)
			}
			entries, readErr := al.GetSessionStore().ReadTranscript(meta.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			var billed []int
			total := 0
			for _, entry := range entries {
				if entry.Role == "assistant" {
					total += entry.Tokens
					if entry.Tokens > 0 {
						wantInput, wantOutput := 40, 10
						if len(billed) > 0 {
							wantInput, wantOutput = 2, 3
						}
						if entry.PromptTokens != wantInput || entry.CompletionTokens != wantOutput || entry.CacheReadTokens != 0 || entry.CacheWriteTokens != 0 {
							t.Errorf("billed split=%d/%d/%d/%d, want %d/%d/0/0", entry.PromptTokens, entry.CompletionTokens, entry.CacheReadTokens, entry.CacheWriteTokens, wantInput, wantOutput)
						}
						billed = append(billed, entry.Tokens)
					}
				}
			}
			if len(billed) != len(tc.wantTokens) {
				t.Fatalf("billed calls=%v want%v", billed, tc.wantTokens)
			}
			wantTotal := 0
			for i, want := range tc.wantTokens {
				wantTotal += want
				if billed[i] != want {
					t.Errorf("attempt%d=%d tokens want%d", i, billed[i], want)
				}
			}
			if total != wantTotal {
				t.Fatalf("total=%d want%d", total, wantTotal)
			}
			stored, readErr := al.GetSessionStore().GetMeta(meta.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if stored.Stats.TokensTotal != wantTotal {
				t.Fatalf("session total=%d want%d", stored.Stats.TokensTotal, wantTotal)
			}
		})
	}
}
