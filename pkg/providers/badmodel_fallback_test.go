// badmodel_fallback_test.go — founder decision 2026-10-24 (Q70, train 3):
// a bad_model failure (the provider says the model was not found or not
// offered) must fall back to the agent's configured backup model, the same
// as a rate limit, instead of stopping. Oracles are the four Q70 rules,
// read before any implementation existed:
//
//	rule 1: with a backup, a bad_model primary moves to the next candidate;
//	        the answer comes from the backup (notice wording pinned in
//	        pkg/agent/badmodel_fallback_note_test.go).
//	rule 2: with NO backup, or when every candidate fails bad_model, the
//	        outcome is unchanged — terminal bad_model; the surfaced
//	        provider/model facts stay recoverable for the terminal message
//	        (message wording pinned agent-side by the routing_1340 terminal
//	        tests, which stay untouched).
//	rule 3: a bad_model primary followed by retriable outcomes on later
//	        candidates keeps today's chain semantics.
//	rule 4: context overflow and format errors stay terminal (pinned by the
//	        unchanged rows of TestRouting1340_Classification and the §7.4
//	        tests; not re-pinned here).
//
// The transport is a scripted fake at the process edge; chain, cooldown and
// classifier are real. bad_model never retries in place (only rate limits
// do), so every case is deterministic with no sleeps and no test seams.
package providers

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// badModelBody is the structured verdict the classifier reads as bad_model
// (the same proven shape TestRouting1340_CodexStructuredErrors uses).
const badModelBody = `{"error":{"code":"model_not_found","message":"the model does not exist"}}`

// Q70 rule 1: a bad_model primary must move to the next candidate and the
// answer must come from the backup.
func TestBadModelFallback_BackupAnswersWhenPrimaryModelMissing(t *testing.T) {
	var attempted []string
	chain := NewFallbackChain(NewCooldownTracker())
	result, err := chain.Execute(context.Background(), []FallbackCandidate{
		{Provider: "openrouter", Model: "bad"},
		{Provider: "other", Model: "good"},
	}, func(_ context.Context, provider, model string) (*LLMResponse, error) {
		attempted = append(attempted, provider+"/"+model)
		if provider == "openrouter" {
			return nil, &common.ProviderError{Status: 400, Body: badModelBody}
		}
		return &LLMResponse{Content: "answered by " + model, FinishReason: "stop"}, nil
	})
	if err != nil || result == nil {
		t.Fatalf("bad_model must move to the next candidate (Q70 rule 1): result=%+v err=%v", result, err)
	}
	if result.Provider != "other" || result.Model != "good" || result.Response == nil || result.Response.Content != "answered by good" {
		t.Fatalf("the answer must come from the backup: result=%+v", result)
	}
	if !reflect.DeepEqual(attempted, []string{"openrouter/bad", "other/good"}) {
		t.Fatalf("chain call order = %v, want both candidates in configured order", attempted)
	}
	if len(result.Attempts) != 1 || result.Attempts[0].Reason != FailoverBadModel ||
		result.Attempts[0].Provider != "openrouter" || result.Attempts[0].Model != "bad" {
		t.Fatalf("the bad_model hop must be recorded with its identity: %+v", result.Attempts)
	}
}

// Q70 rule 2: when every candidate fails bad_model the chain stays terminal
// bad_model — every candidate is tried, and the surfaced FailoverError keeps
// provider/model facts an attempted candidate actually produced, so the
// terminal message can name the pair and the setting to fix.
func TestBadModelFallback_AllCandidatesBadModel_StaysTerminalBadModel(t *testing.T) {
	var attempted []string
	chain := NewFallbackChain(NewCooldownTracker())
	result, err := chain.Execute(context.Background(), []FallbackCandidate{
		{Provider: "openrouter", Model: "bad"},
		{Provider: "other", Model: "worse"},
	}, func(_ context.Context, provider, model string) (*LLMResponse, error) {
		attempted = append(attempted, provider+"/"+model)
		return nil, &common.ProviderError{Status: 400, Body: badModelBody}
	})
	var fe *FailoverError
	if result != nil || !errors.As(err, &fe) || fe.Reason != FailoverBadModel {
		t.Fatalf("all-bad_model must stay terminal bad_model (Q70 rule 2): result=%+v err=%v", result, err)
	}
	if !reflect.DeepEqual(attempted, []string{"openrouter/bad", "other/worse"}) {
		t.Fatalf("every candidate must be tried before the chain gives up: %v", attempted)
	}
	if fe.Status != 400 {
		t.Fatalf("surfaced status = %d, want the provider's 400", fe.Status)
	}
	if pair := fe.Provider + "/" + fe.Model; pair != "openrouter/bad" && pair != "other/worse" {
		t.Fatalf("surfaced provider/model %q is not a candidate the chain attempted", pair)
	}
}

// Q70 rule 2 control (passes on pre-change code too): with NO backup the
// single candidate stays terminal bad_model with its identity preserved.
func TestBadModelFallback_SingleCandidate_TerminalClassificationSurvives(t *testing.T) {
	var attempted []string
	chain := NewFallbackChain(NewCooldownTracker())
	result, err := chain.Execute(context.Background(), []FallbackCandidate{
		{Provider: "openrouter", Model: "bad"},
	}, func(_ context.Context, provider, model string) (*LLMResponse, error) {
		attempted = append(attempted, provider+"/"+model)
		return nil, &common.ProviderError{Status: 400, Body: badModelBody}
	})
	var fe *FailoverError
	if result != nil || !errors.As(err, &fe) || fe.Reason != FailoverBadModel {
		t.Fatalf("no-backup bad_model must stay terminal bad_model (Q70 rule 2): result=%+v err=%v", result, err)
	}
	if fe.Provider != "openrouter" || fe.Model != "bad" || fe.Status != 400 {
		t.Fatalf("terminal facts lost: %+v", fe)
	}
	if !reflect.DeepEqual(attempted, []string{"openrouter/bad"}) {
		t.Fatalf("single candidate must be tried exactly once: %v", attempted)
	}
}

// Q70 rule 3: a bad_model primary followed by retriable outcomes on later
// candidates keeps today's chain semantics — a 429 over the retry ceiling
// skips without marking (A-4), billing moves on unwaited (C-7/D2), and the
// next candidate answers.
func TestBadModelFallback_BadModelPrimaryThenRetriableBackup(t *testing.T) {
	cases := []struct {
		name        string
		backupModel string
		backupErr   *common.ProviderError
		wantSkip    bool // does the retriable backup end in an A-4 cooldown skip?
	}{
		{
			name:        "backup_rate_limited_over_ceiling_skips_then_next_answers",
			backupModel: "limited",
			// Retry-After far over the §7.4 auto-retry ceiling: the chain
			// skips this candidate's retries and moves on (no sleep).
			backupErr: &common.ProviderError{Status: 429, Body: "rate limit exceeded", RetryAfterSeconds: 3600},
			wantSkip:  true,
		},
		{
			name:        "backup_billing_moves_on_then_next_answers",
			backupModel: "unpaid",
			backupErr:   &common.ProviderError{Status: 402, Body: `{"error":{"message":"credit balance is too low"}}`},
			wantSkip:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var attempted []string
			chain := NewFallbackChain(NewCooldownTracker())
			result, err := chain.Execute(context.Background(), []FallbackCandidate{
				{Provider: "openrouter", Model: "bad"},
				{Provider: "prov", Model: tc.backupModel},
				{Provider: "prov", Model: "good"},
			}, func(_ context.Context, provider, model string) (*LLMResponse, error) {
				attempted = append(attempted, provider+"/"+model)
				if provider == "openrouter" {
					return nil, &common.ProviderError{Status: 400, Body: badModelBody}
				}
				if model == tc.backupModel {
					return nil, tc.backupErr
				}
				return &LLMResponse{Content: "answered by " + model, FinishReason: "stop"}, nil
			})
			if err != nil || result == nil || result.Response == nil || result.Response.Content != "answered by good" {
				t.Fatalf("the chain must continue past the bad_model hop to the answering candidate: result=%+v err=%v", result, err)
			}
			if !reflect.DeepEqual(attempted, []string{"openrouter/bad", "prov/" + tc.backupModel, "prov/good"}) {
				t.Fatalf("chain call order = %v, want all three candidates in order", attempted)
			}
			if len(result.Attempts) != 2 || result.Attempts[0].Reason != FailoverBadModel || result.Attempts[0].Model != "bad" {
				t.Fatalf("the bad_model hop must be recorded first: %+v", result.Attempts)
			}
			if result.Attempts[1].Model != tc.backupModel || result.Attempts[1].Skipped != tc.wantSkip {
				t.Fatalf("retriable backup hop = %+v, want model %q skipped=%t (today's chain semantics)", result.Attempts[1], tc.backupModel, tc.wantSkip)
			}
		})
	}
}
