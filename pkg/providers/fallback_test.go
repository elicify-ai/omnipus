package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

func makeCandidate(provider, model string) FallbackCandidate {
	return FallbackCandidate{Provider: provider, Model: model}
}

func successRun(content string) func(ctx context.Context, provider, model string) (*LLMResponse, error) {
	return func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		return &LLMResponse{Content: content, FinishReason: "stop"}, nil
	}
}

func TestFallback_SingleCandidate_Success(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}
	result, err := fc.Execute(context.Background(), candidates, successRun("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Response.Content != "hello" {
		t.Errorf("content = %q, want hello", result.Response.Content)
	}
	if result.Provider != "openai" || result.Model != "gpt-4" {
		t.Errorf("provider/model = %s/%s, want openai/gpt-4", result.Provider, result.Model)
	}
}

func TestFallback_SecondCandidateSuccess(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude-opus"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		if attempt == 1 {
			return nil, errors.New("rate limit exceeded")
		}
		return &LLMResponse{Content: "from claude", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", result.Provider)
	}
	if result.Response.Content != "from claude" {
		t.Errorf("content = %q, want 'from claude'", result.Response.Content)
	}
	if len(result.Attempts) != 1 {
		t.Errorf("attempts = %d, want 1 (failed attempt recorded)", len(result.Attempts))
	}
}

func TestFallback_AllFail(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
		makeCandidate("groq", "llama"),
	}

	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		return nil, errors.New("rate limit exceeded")
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error when all candidates fail")
	}
	exhausted, ok := err.(*FallbackExhaustedError)
	if !ok {
		t.Fatalf("expected exactly *FallbackExhaustedError (Execute returns it unwrapped), got %T: %v", err, err)
	}
	if len(exhausted.Attempts) != 3 {
		t.Errorf("attempts = %d, want 3", len(exhausted.Attempts))
	}
}

func TestFallback_ContextCanceled(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	ctx, cancel := context.WithCancel(context.Background())
	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		if attempt == 1 {
			cancel() // cancel context
			return nil, context.Canceled
		}
		t.Error("should not reach second candidate after cancel")
		return nil, errors.New("test: unreachable candidate invoked after context cancel")
	}

	_, err := fc.Execute(ctx, candidates, run)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestFallback_NonRetriableError(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		return nil, errors.New("string should match pattern")
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error for non-retriable")
	}
	fe, ok := err.(*FailoverError)
	if !ok {
		t.Fatalf("expected exactly *FailoverError (Execute returns it unwrapped), got %T", err)
	}
	if fe.Reason != FailoverFormat {
		t.Errorf("reason = %q, want format", fe.Reason)
	}
	if attempt != 1 {
		t.Errorf("attempt = %d, want 1 (non-retriable should not try next)", attempt)
	}
}

func TestFallback_CooldownSkip(t *testing.T) {
	now := time.Now()
	ct, _ := newTestTracker(now)
	fc := NewFallbackChain(ct)

	// Put openai/gpt-4 in cooldown (using ModelKey now)
	ct.MarkFailure(ModelKey("openai", "gpt-4"), FailoverRateLimit)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		if provider == "openai" {
			t.Error("should not call openai (in cooldown)")
		}
		return &LLMResponse{Content: "claude response", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", result.Provider)
	}
	// Should have 1 skipped attempt
	skipped := 0
	for _, a := range result.Attempts {
		if a.Skipped {
			skipped++
		}
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
}

func TestFallback_AllInCooldown(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	// Put all models in cooldown (using ModelKey now)
	ct.MarkFailure(ModelKey("openai", "gpt-4"), FailoverRateLimit)
	ct.MarkFailure(ModelKey("anthropic", "claude"), FailoverBilling)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	_, err := fc.Execute(context.Background(), candidates,
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			t.Error("should not call any provider (all in cooldown)")
			return nil, errors.New("test: unreachable candidate invoked while all providers in cooldown")
		})

	if err == nil {
		t.Fatal("expected error when all in cooldown")
	}
	if _, ok := err.(*FallbackExhaustedError); !ok {
		t.Fatalf("expected exactly *FallbackExhaustedError (Execute returns it unwrapped), got %T", err)
	}
}

func TestFallback_NoCandidates(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	_, err := fc.Execute(context.Background(), nil, successRun("ok"))
	if err == nil {
		t.Error("expected error for empty candidates")
	}
}

func TestFallback_EmptyFallbacks(t *testing.T) {
	// Single primary, no fallbacks: should work like direct call
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}
	result, err := fc.Execute(context.Background(), candidates, successRun("ok"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Response.Content != "ok" {
		t.Error("expected success with single candidate")
	}
}

func TestFallback_UnclassifiedError(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		return nil, errors.New("completely unknown internal error")
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error for unclassified error")
	}
	if attempt != 1 {
		t.Errorf("attempt = %d, want 1 (should not fallback on unclassified)", attempt)
	}
}

func TestFallback_SuccessResetsCooldown(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}
	modelKey := ModelKey("openai", "gpt-4")

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		if attempt == 1 {
			ct.MarkFailure(modelKey, FailoverRateLimit) // simulate failure tracked elsewhere
		}
		return &LLMResponse{Content: "ok", FinishReason: "stop"}, nil
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ct.IsAvailable(modelKey) {
		t.Error("success should reset cooldown")
	}
}

// --- Image Fallback Tests ---

func TestImageFallback_Success(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4o")}
	result, err := fc.ExecuteImage(context.Background(), candidates, successRun("image result"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Response.Content != "image result" {
		t.Error("expected image result")
	}
}

func TestImageFallback_DimensionError(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4o"),
		makeCandidate("anthropic", "claude"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		return nil, errors.New("image dimensions exceed max 4096x4096")
	}

	_, err := fc.ExecuteImage(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error for image dimension error")
	}
	if attempt != 1 {
		t.Errorf("attempt = %d, want 1 (image dimension error should not retry)", attempt)
	}
}

func TestImageFallback_SizeError(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4o"),
		makeCandidate("anthropic", "claude"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		return nil, errors.New("image exceeds 20 mb")
	}

	_, err := fc.ExecuteImage(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error for image size error")
	}
	if attempt != 1 {
		t.Errorf("attempt = %d, want 1 (image size error should not retry)", attempt)
	}
}

func TestImageFallback_RetryOnOtherErrors(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4o"),
		makeCandidate("anthropic", "claude-sonnet"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		if attempt == 1 {
			return nil, errors.New("rate limit exceeded")
		}
		return &LLMResponse{Content: "image ok", FinishReason: "stop"}, nil
	}

	result, err := fc.ExecuteImage(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", result.Provider)
	}
}

func TestImageFallback_NoCandidates(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	_, err := fc.ExecuteImage(context.Background(), nil, successRun("ok"))
	if err == nil {
		t.Error("expected error for empty candidates")
	}
}

// --- ResolveCandidates Tests ---

func TestResolveCandidates_Simple(t *testing.T) {
	cfg := ModelConfig{
		Primary:   "gpt-4",
		Fallbacks: []string{"anthropic/claude-opus", "groq/llama-3"},
	}

	candidates := ResolveCandidates(cfg, "openai")
	if len(candidates) != 3 {
		t.Fatalf("candidates = %d, want 3", len(candidates))
	}

	if candidates[0].Provider != "openai" || candidates[0].Model != "gpt-4" {
		t.Errorf("candidate[0] = %s/%s, want openai/gpt-4", candidates[0].Provider, candidates[0].Model)
	}
	if candidates[1].Provider != "anthropic" || candidates[1].Model != "claude-opus" {
		t.Errorf("candidate[1] = %s/%s, want anthropic/claude-opus", candidates[1].Provider, candidates[1].Model)
	}
	if candidates[2].Provider != "groq" || candidates[2].Model != "llama-3" {
		t.Errorf("candidate[2] = %s/%s, want groq/llama-3", candidates[2].Provider, candidates[2].Model)
	}
}

func TestResolveCandidates_Deduplication(t *testing.T) {
	cfg := ModelConfig{
		Primary:   "openai/gpt-4",
		Fallbacks: []string{"openai/gpt-4", "anthropic/claude"},
	}

	candidates := ResolveCandidates(cfg, "default")
	if len(candidates) != 2 {
		t.Errorf("candidates = %d, want 2 (duplicate removed)", len(candidates))
	}
}

func TestResolveCandidates_EmptyFallbacks(t *testing.T) {
	cfg := ModelConfig{
		Primary:   "gpt-4",
		Fallbacks: nil,
	}

	candidates := ResolveCandidates(cfg, "openai")
	if len(candidates) != 1 {
		t.Errorf("candidates = %d, want 1", len(candidates))
	}
}

func TestResolveCandidates_EmptyPrimary(t *testing.T) {
	cfg := ModelConfig{
		Primary:   "",
		Fallbacks: []string{"anthropic/claude"},
	}

	candidates := ResolveCandidates(cfg, "openai")
	if len(candidates) != 1 {
		t.Errorf("candidates = %d, want 1", len(candidates))
	}
}

func TestResolveCandidatesWithLookup_AliasResolvesToNestedModel(t *testing.T) {
	cfg := ModelConfig{
		Primary:   "step-3.5-flash",
		Fallbacks: nil,
	}

	lookup := func(raw string) (ResolvedRef, bool) {
		if raw == "step-3.5-flash" {
			return ResolvedRef{Model: "openrouter/stepfun/step-3.5-flash:free", Provider: "openrouter"}, true
		}
		return ResolvedRef{}, false
	}

	candidates := ResolveCandidatesWithLookup(cfg, "", lookup)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if candidates[0].Provider != "openrouter" {
		t.Fatalf("provider = %q, want openrouter", candidates[0].Provider)
	}
	if candidates[0].Model != "stepfun/step-3.5-flash:free" {
		t.Fatalf("model = %q, want stepfun/step-3.5-flash:free", candidates[0].Model)
	}
}

func TestResolveCandidatesWithLookup_DeduplicateAfterLookup(t *testing.T) {
	cfg := ModelConfig{
		Primary:   "step-3.5-flash",
		Fallbacks: []string{"openrouter/stepfun/step-3.5-flash:free"},
	}

	lookup := func(raw string) (ResolvedRef, bool) {
		if raw == "step-3.5-flash" {
			return ResolvedRef{Model: "openrouter/stepfun/step-3.5-flash:free", Provider: "openrouter"}, true
		}
		return ResolvedRef{}, false
	}

	candidates := ResolveCandidatesWithLookup(cfg, "", lookup)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
}

func TestResolveCandidatesWithLookup_AliasWithoutProtocolUsesDefaultProvider(t *testing.T) {
	cfg := ModelConfig{
		Primary:   "glm-5",
		Fallbacks: nil,
	}

	// Lookup that returns the slug with NO Provider — the resolver chain
	// falls back to defaultProvider via ParseModelRef.
	lookup := func(raw string) (ResolvedRef, bool) {
		if raw == "glm-5" {
			return ResolvedRef{Model: "glm-5"}, true
		}
		return ResolvedRef{}, false
	}

	candidates := ResolveCandidatesWithLookup(cfg, "openai", lookup)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if candidates[0].Provider != "openai" {
		t.Fatalf("provider = %q, want openai", candidates[0].Provider)
	}
	if candidates[0].Model != "glm-5" {
		t.Fatalf("model = %q, want glm-5", candidates[0].Model)
	}
}

// TestFallback_SecondCandidate_GetsFreshBudget_AfterPrimaryTimesOut verifies the
// fix for issue #235: when the primary candidate times out and exhausts the parent
// context deadline, fallback candidates must receive a fresh per-candidate budget
// rather than inheriting the already-expired parent deadline.
//
// Pre-fix: the second candidate received the expired parent ctx, so
// time.Until(deadline) was ≤0 and the stub returned DeadlineExceeded in ~1ms,
// causing FallbackExhaustedError — both the remaining-positive and err-nil
// assertions fail (red).
//
// Post-fix: the second candidate gets its own fresh context detached from the
// expired parent deadline, so remaining is positive and the stub succeeds (green).
func TestFallback_SecondCandidate_GetsFreshBudget_AfterPrimaryTimesOut(t *testing.T) {
	ct := NewCooldownTracker()
	// 50ms per-candidate budget — enough for prov2 to run after prov1 times out.
	fc := NewFallbackChainWithTimeout(ct, 50*time.Millisecond)

	// Parent deadline of 30ms — the primary will consume it entirely.
	parentCtx, parentCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer parentCancel()

	candidates := []FallbackCandidate{
		{Provider: "prov1", Model: "modelA"},
		{Provider: "prov2", Model: "modelB"},
	}

	var fallbackRemaining time.Duration

	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		if model == "modelA" {
			// Block until the PARENT deadline is fully exhausted (not just this
			// candidate's split slice), so the fallback deterministically hits the
			// "exhausted parent → fresh per-candidate budget" path. Blocking on the
			// attempt ctx alone would only consume the parent's remaining/2 split,
			// leaving the fallback a shrinking parent-bounded budget that flakes
			// under parallel load.
			<-parentCtx.Done()
			return nil, ctx.Err()
		}
		// modelB (fallback): record how much budget is left in the attempt ctx.
		if d, ok := ctx.Deadline(); ok {
			fallbackRemaining = time.Until(d)
		}
		time.Sleep(5 * time.Millisecond) // simulate real work
		return &LLMResponse{Content: "ok-from-fallback", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(parentCtx, candidates, run)
	if err != nil {
		t.Fatalf("expected success from fallback candidate, got error: %v", err)
	}
	if result.Model != "modelB" {
		t.Errorf("expected result from modelB, got %q", result.Model)
	}
	if result.Response == nil || result.Response.Content != "ok-from-fallback" {
		t.Errorf("expected content ok-from-fallback, got %v", result.Response)
	}
	if fallbackRemaining <= 5*time.Millisecond {
		t.Errorf("fallback candidate should have received a positive budget (≥5ms), got %v", fallbackRemaining)
	}
}

// TestFallback_UserCancel_StillAbortsImmediately verifies that canceling the
// original ctx during the primary attempt still aborts the whole chain and does
// not advance to the second candidate — even with per-candidate budgeting active.
func TestFallback_UserCancel_StillAbortsImmediately(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChainWithTimeout(ct, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	candidates := []FallbackCandidate{
		{Provider: "prov1", Model: "modelA"},
		{Provider: "prov2", Model: "modelB"},
	}

	reached2 := false
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		if model == "modelA" {
			cancel() // user aborts
			return nil, context.Canceled
		}
		reached2 = true
		return &LLMResponse{Content: "should not reach here", FinishReason: "stop"}, nil
	}

	_, err := fc.Execute(ctx, candidates, run)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if reached2 {
		t.Error("chain must not advance to candidate 2 after user cancellation")
	}
}

// TestFallback_FairSplitFloor verifies that when many candidates compete for a
// small parent deadline, each candidate receives at least minCandidateBudget
// rather than a sub-second slice from naive division.
//
// Setup: 10 candidates, parent deadline of 2s (200ms per candidate via naive
// fair-split). minCandidateBudget is 5s, so every candidate must receive ≥5s.
// We inspect the deadline the first candidate receives; because the floor is
// applied before we even run the first attempt, the budget must be ≥5s.
func TestFallback_FairSplitFloor_EnforcesMinimumBudget(t *testing.T) {
	ct := NewCooldownTracker()
	// perCandidateTimeout = 30s (well above floor) — the floor governs here
	fc := NewFallbackChainWithTimeout(ct, 30*time.Second)

	// Parent deadline: 2s total / 10 candidates = 200ms naive share (below 5s floor).
	parentCtx, parentCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer parentCancel()

	const numCandidates = 10
	candidates := make([]FallbackCandidate, numCandidates)
	for i := range candidates {
		candidates[i] = FallbackCandidate{
			Provider: "prov",
			Model:    fmt.Sprintf("model-%d", i),
		}
	}

	var firstCandidateBudget time.Duration
	callCount := 0

	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		callCount++
		if callCount == 1 {
			// Record how much budget the first candidate got.
			if d, ok := ctx.Deadline(); ok {
				firstCandidateBudget = time.Until(d)
			}
			// Return a retriable error to advance to the next candidate.
			return nil, errors.New("rate limit exceeded")
		}
		// All other candidates succeed immediately.
		return &LLMResponse{Content: "ok", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(parentCtx, candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a successful result")
	}

	// The first candidate must have received at least minCandidateBudget (5s),
	// not the naive 200ms from 2s/10 candidates.
	if firstCandidateBudget < minCandidateBudget-100*time.Millisecond {
		t.Errorf("first candidate budget = %v, want >= minCandidateBudget (%v); "+
			"fair-split floor is not being applied", firstCandidateBudget, minCandidateBudget)
	}
}

func TestFallbackExhaustedError_Message(t *testing.T) {
	e := &FallbackExhaustedError{
		Attempts: []FallbackAttempt{
			{
				Provider: "openai",
				Model:    "gpt-4",
				Error:    errors.New("rate limited"),
				Reason:   FailoverRateLimit,
				Duration: 500 * time.Millisecond,
			},
			{Provider: "anthropic", Model: "claude", Skipped: true},
		},
	}
	msg := e.Error()
	if msg == "" {
		t.Error("expected non-empty error message")
	}
}

// TestResolveCandidatesWithLookup_UnknownBareSlug_Warns is the W2-25 contract:
// when the model_list lookup doesn't recognize a fallback slug AND the slug
// has no provider prefix to anchor on, addCandidate MUST emit a WARN so an
// operator auditing the config load can spot the typo before it bites at
// runtime (silent-failure-A #4). Without this, a typo'd model silently
// routes through defaultProvider and the agent loop surfaces the failure
// several stack frames later with no breadcrumb pointing to the bad config
// value.
//
// The test captures the WARN by routing logger output to a temp file
// (DisableConsole + EnableFileLogging) and asserting the message + fields
// appear in the file. This is the standard logger test pattern in this repo
// (see pkg/logger/logger_test.go).
func TestResolveCandidatesWithLookup_UnknownBareSlug_Warns(t *testing.T) {
	// Set up log capture to a temp file. DisableConsole silences stderr;
	// EnableFileLogging routes log output to the file so the assertion can
	// read it back.
	tmpDir := t.TempDir()
	logFile := tmpDir + "/providers-warn.log"

	prevLevel := logger.GetLevel()

	// DisableConsole disables stdout; EnableFileLogging routes to a file at
	// WARN-or-higher. SetLevel(WARN) ensures our WARN fires but INFO is
	// suppressed.
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		// Best-effort restore — we don't want to leak WARN level into
		// other tests in the same package.
		logger.SetLevel(prevLevel)
	})

	// A lookup that returns false for any unknown slug, and a config with
	// a typo'd bare slug as a fallback (no provider prefix, no model_list
	// match). Without the W2-25 fix, addCandidate silently accepts the
	// typo'd slug and the operator has no breadcrumb.
	lookup := func(raw string) (ResolvedRef, bool) {
		// Intentionally always miss — the test asserts the WARN fires
		// on a lookup miss for a bare slug.
		return ResolvedRef{}, false
	}
	cfg := ModelConfig{
		Primary:   "gpt-4",
		Fallbacks: []string{"claude-opus-typo"}, // bare slug, no prefix, no match
	}

	candidates := ResolveCandidatesWithLookup(cfg, "openai", lookup)

	// Behavior preservation: the typo'd slug still produces a candidate
	// (routed through defaultProvider=openai) so the chat runtime is not
	// broken. The fix is purely additive — the WARN.
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2 (primary + typo'd fallback)", len(candidates))
	}
	if candidates[1].Provider != "openai" || candidates[1].Model != "claude-opus-typo" {
		t.Errorf("candidates[1] = %s/%s, want openai/claude-opus-typo (behavior preservation)",
			candidates[1].Provider, candidates[1].Model)
	}

	// The WARN must have been written to the file. Read it back.
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	logged := string(data)
	if !strings.Contains(logged, "addCandidate") {
		t.Errorf("log file missing addCandidate warn entry; got:\n%s", logged)
	}
	if !strings.Contains(logged, "claude-opus-typo") {
		t.Errorf("log file missing the typo'd slug; got:\n%s", logged)
	}
	if !strings.Contains(logged, "openai") {
		t.Errorf("log file missing defaultProvider field; got:\n%s", logged)
	}
	if !strings.Contains(logged, `"level":"warn"`) {
		t.Errorf("log file missing warn level; got:\n%s", logged)
	}
}

// TestResolveCandidatesWithLookup_KnownBareSlug_NoWarn is the negative case:
// when the lookup DOES resolve the slug (or it has a provider prefix), the
// WARN MUST NOT fire. We don't want to spam the log on every legitimate
// fallback that happens to be a bare slug already covered by the resolver.
func TestResolveCandidatesWithLookup_KnownBareSlug_NoWarn(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := tmpDir + "/providers-nowarn.log"

	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	// Lookup that resolves the slug — no warn expected.
	lookup := func(raw string) (ResolvedRef, bool) {
		if raw == "claude-opus" {
			return ResolvedRef{Model: "anthropic/claude-opus", Provider: "anthropic"}, true
		}
		return ResolvedRef{}, false
	}
	cfg := ModelConfig{
		Primary:   "gpt-4",
		Fallbacks: []string{"claude-opus"},
	}

	candidates := ResolveCandidatesWithLookup(cfg, "openai", lookup)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(candidates))
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	if strings.Contains(string(data), "addCandidate") {
		t.Errorf("log file unexpectedly contains addCandidate warn entry for a resolved slug; got:\n%s", string(data))
	}
}
