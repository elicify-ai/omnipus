// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Tests for the v0.2 #155 item 6 memory-write rate limiter. Covers:
//
//   - Within-budget calls succeed.
//   - Just-over-budget call is rejected with the rate_limited error
//     fingerprint AND emits a memory.rate_limited audit entry.
//   - After Retry-After elapses, the next call succeeds (clean recovery —
//     parallels gateway/auth_rate_limit_recovery_test.go which validates
//     the apiRateLimiter sliding-window primitive).
//   - Two different agents share the per-caller bucket but have
//     INDEPENDENT per-agent buckets.
//
// The tests drive the limiter directly (MemoryRateLimiter.Allow) AND end-to-end
// through RememberTool.Execute so both layers are covered.

package tools_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// Direct limiter tests (small, fast, deterministic).
// ---------------------------------------------------------------------------

// TestMemoryRateLimiter_WithinBudget_Allowed verifies that the first
// PerAgentLimit calls all return Allowed=true.
func TestMemoryRateLimiter_WithinBudget_Allowed(t *testing.T) {
	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  3,
		PerCallerLimit: 100,
		Window:         time.Minute,
	})

	for i := 0; i < 3; i++ {
		decision := limiter.Allow("agent-A", "rest:user-1")
		require.True(t, decision.Allowed, "request %d/3 must be allowed", i+1)
		require.Empty(t, decision.Scope, "allowed decisions carry no scope")
	}
}

// TestMemoryRateLimiter_OverBudget_DeniedWithScope verifies that the call
// just past the limit is rejected with Scope="agent" and a positive
// RetryAfter.
func TestMemoryRateLimiter_OverBudget_DeniedWithScope(t *testing.T) {
	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  3,
		PerCallerLimit: 100,
		Window:         time.Minute,
	})

	for i := 0; i < 3; i++ {
		require.True(t, limiter.Allow("agent-A", "rest:user-1").Allowed)
	}

	denied := limiter.Allow("agent-A", "rest:user-1")
	assert.False(t, denied.Allowed, "fourth call must be rejected")
	assert.Equal(t, "agent", denied.Scope, "agent bucket trips first")
	assert.Greater(t, denied.RetryAfter, time.Duration(0),
		"RetryAfter must be positive when bucket is full")
	// HTTP Retry-After is integer seconds; the documented invariant is >=1s.
	assert.GreaterOrEqual(t, denied.RetryAfter, time.Second,
		"RetryAfter must round up to >= 1 second so a Retry-After header makes sense")
}

// TestMemoryRateLimiter_CallerBucketTrips verifies that the per-caller
// bucket trips when the per-agent limit is much larger. Scope must be
// "caller" so SIEM rules can distinguish the two paths.
func TestMemoryRateLimiter_CallerBucketTrips(t *testing.T) {
	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  100,
		PerCallerLimit: 2,
		Window:         time.Minute,
	})

	require.True(t, limiter.Allow("agent-A", "rest:user-1").Allowed)
	require.True(t, limiter.Allow("agent-A", "rest:user-1").Allowed)
	denied := limiter.Allow("agent-A", "rest:user-1")
	assert.False(t, denied.Allowed)
	assert.Equal(t, "caller", denied.Scope, "caller bucket should trip first")
}

// TestMemoryRateLimiter_NilLimiter_AllowsEverything verifies the
// nil-limiter convention (used by tests and by deployments that opt out
// of rate limiting). A nil receiver is allowed to call Allow and the
// decision is always Allowed=true.
func TestMemoryRateLimiter_NilLimiter_AllowsEverything(t *testing.T) {
	var limiter *tools.MemoryRateLimiter
	for i := 0; i < 100; i++ {
		decision := limiter.Allow("agent-A", "rest:user-1")
		require.True(t, decision.Allowed, "nil limiter must allow every call")
	}
}

// TestMemoryRateLimiter_TwoAgents_IndependentBuckets verifies that two
// different agentIDs do NOT share their per-agent buckets — agent-B can
// still write even when agent-A is exhausted. They DO share the
// per-caller bucket (when callerID is the same).
//
// This is the core invariant of the dual-bucket design: a single
// compromised agent cannot drain the global memory-write budget for the
// rest of the system.
func TestMemoryRateLimiter_TwoAgents_IndependentBuckets(t *testing.T) {
	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  2,
		PerCallerLimit: 100,
		Window:         time.Minute,
	})

	// Exhaust agent-A's bucket via one caller.
	require.True(t, limiter.Allow("agent-A", "rest:user-1").Allowed)
	require.True(t, limiter.Allow("agent-A", "rest:user-1").Allowed)
	denied := limiter.Allow("agent-A", "rest:user-1")
	require.False(t, denied.Allowed, "agent-A bucket must be exhausted")
	require.Equal(t, "agent", denied.Scope)

	// agent-B from the SAME caller still has full budget.
	for i := 0; i < 2; i++ {
		ok := limiter.Allow("agent-B", "rest:user-1")
		assert.True(t, ok.Allowed,
			"agent-B request %d/2 must succeed even though agent-A is exhausted", i+1)
	}

	// Both agents have written 2 + 2 = 4 entries against the per-caller
	// bucket (limit 100), so the per-caller bucket still has plenty of
	// room. agent-B's third call would trip its own per-agent bucket
	// (because limit=2), so it gets denied with scope="agent" — but
	// agent-A is the one we want to assert remains denied.
	stillDenied := limiter.Allow("agent-A", "rest:user-1")
	assert.False(t, stillDenied.Allowed, "agent-A must still be exhausted")
	assert.Equal(t, "agent", stillDenied.Scope)
}

// TestMemoryRateLimiter_TwoAgents_ShareCallerBucket verifies that two
// agents under the SAME caller identity DO contend for the same
// per-caller bucket — this is the system-wide ceiling that prevents one
// originating client from running so many agents that the per-caller
// limit becomes meaningless.
//
// Setup: per-caller=3 (small), per-agent=100 (large). Three calls from
// any combination of agents under "rest:user-1" exhausts the caller
// bucket; the fourth must be rejected with scope="caller".
func TestMemoryRateLimiter_TwoAgents_ShareCallerBucket(t *testing.T) {
	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  100,
		PerCallerLimit: 3,
		Window:         time.Minute,
	})

	require.True(t, limiter.Allow("agent-A", "rest:user-1").Allowed)
	require.True(t, limiter.Allow("agent-B", "rest:user-1").Allowed)
	require.True(t, limiter.Allow("agent-A", "rest:user-1").Allowed)

	// All three slots in the per-caller bucket are now consumed. No matter
	// which agent we use next, "rest:user-1" is throttled.
	denied := limiter.Allow("agent-C", "rest:user-1")
	assert.False(t, denied.Allowed)
	assert.Equal(t, "caller", denied.Scope,
		"per-caller bucket should trip — three agents contending under same caller")

	// A different caller identity is unaffected.
	otherCaller := limiter.Allow("agent-A", "rest:user-2")
	assert.True(t, otherCaller.Allowed,
		"different caller has its own bucket and is not affected")
}

// TestMemoryRateLimiter_RecoveryAfterWindow verifies that after the
// sliding window expires, the bucket recovers and the next call is
// allowed again. Mirrors gateway/auth_rate_limit_recovery_test.go's
// pattern for the auth limiter.
func TestMemoryRateLimiter_RecoveryAfterWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping recovery test in short mode — requires real time.Sleep")
	}
	const (
		limit  = 2
		window = 200 * time.Millisecond
	)
	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  limit,
		PerCallerLimit: 100,
		Window:         window,
	})

	// Exhaust the agent bucket.
	require.True(t, limiter.Allow("agent-A", "rest:u").Allowed)
	require.True(t, limiter.Allow("agent-A", "rest:u").Allowed)
	denied := limiter.Allow("agent-A", "rest:u")
	require.False(t, denied.Allowed, "third call within window must be denied")
	require.Greater(t, denied.RetryAfter, time.Duration(0))

	// Sleep past the window so all timestamps evict.
	time.Sleep(window + 50*time.Millisecond)

	// Recovery: the bucket is empty again, the next call must succeed.
	recovered := limiter.Allow("agent-A", "rest:u")
	assert.True(t, recovered.Allowed,
		"bucket must recover after window expires (Allow must return Allowed=true)")
}

// ---------------------------------------------------------------------------
// End-to-end RememberTool tests (rate-limit gate + audit emission).
// ---------------------------------------------------------------------------

// withCallerCtx returns a context with both agent ID and channel/chatID
// set so the RememberTool can resolve a callerIdentity. The agent ID also
// flows into the per-agent rate-limit bucket.
func withCallerCtx(agentID, channel, chatID string) context.Context {
	ctx := context.Background()
	ctx = tools.WithAgentID(ctx, agentID)
	ctx = tools.WithToolContext(ctx, channel, chatID)
	return ctx
}

// newAuditLoggerForTest builds an audit.Logger pointing at tmpDir/system
// so the tests can assert audit entries are emitted on rate-limit deny.
func newAuditLoggerForTest(t *testing.T, tmpDir string) *audit.Logger {
	t.Helper()
	auditDir := filepath.Join(tmpDir, "system")
	require.NoError(t, os.MkdirAll(auditDir, 0o700))
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:               auditDir,
		RetentionDays:     1,
		AuditLogRequested: true,
	})
	require.NoError(t, err)
	return logger
}

// TestRememberTool_WithinBudget_Succeeds proves the happy path. The
// rate-limit gate is configured with a generous budget so it never fires;
// every Execute call is expected to write a MEMORY.md entry.
func TestRememberTool_WithinBudget_Succeeds(t *testing.T) {
	tmpDir := t.TempDir()
	store := newSimpleMemStore(tmpDir)
	tool := tools.NewRememberTool(store, nil)

	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  10,
		PerCallerLimit: 100,
		Window:         time.Minute,
	})
	tool.SetMemoryRateLimiter(limiter)

	ctx := withCallerCtx("agent-A", "rest", "user-1")
	for i := 0; i < 5; i++ {
		result := tool.Execute(ctx, map[string]any{
			"content":  "fact " + string(rune('A'+i)),
			"category": "reference",
		})
		require.NotNil(t, result)
		assert.False(t, result.IsError, "call %d/5 must succeed: %s", i+1, result.ForLLM)
	}
}

// TestRememberTool_OverBudget_DeniedWithRateLimitedError proves that the
// call past the per-agent limit is rejected with IsError=true and a
// machine-parseable error_kind="rate_limited" fingerprint, AND that the
// rejection is recorded as an audit entry with Decision="deny" and
// Event="memory.rate_limited" and a non-empty PolicyRule.
func TestRememberTool_OverBudget_DeniedWithRateLimitedError(t *testing.T) {
	tmpDir := t.TempDir()
	store := newSimpleMemStore(tmpDir)
	auditLogger := newAuditLoggerForTest(t, tmpDir)
	t.Cleanup(func() { _ = auditLogger.Close() })

	tool := tools.NewRememberTool(store, auditLogger)
	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  2,
		PerCallerLimit: 100,
		Window:         time.Minute,
	})
	tool.SetMemoryRateLimiter(limiter)

	ctx := withCallerCtx("agent-A", "rest", "user-1")

	// First two calls succeed and exhaust agent-A's budget.
	for i := 0; i < 2; i++ {
		r := tool.Execute(ctx, map[string]any{
			"content":  "fact",
			"category": "reference",
		})
		require.False(t, r.IsError, "preflight call %d/2 must succeed: %s", i+1, r.ForLLM)
	}

	// Third call must be rejected.
	denied := tool.Execute(ctx, map[string]any{
		"content":  "third fact",
		"category": "reference",
	})
	require.True(t, denied.IsError, "third call must return IsError=true")
	require.Contains(t, denied.ForLLM, `error_kind="rate_limited"`,
		"error message must include the machine-parseable error_kind")
	require.Contains(t, denied.ForLLM, "retry_after_seconds=",
		"error message must include retry_after_seconds for the LLM to plan its retry")
	// Scope should be the per-agent scope since it tripped first.
	require.Contains(t, denied.ForLLM, "agent scope",
		"error message must name the bucket that tripped (agent)")

	// Verify the audit entry was emitted with the correct shape.
	require.NoError(t, auditLogger.Close())
	auditFile := filepath.Join(tmpDir, "system", "audit.jsonl")
	entries := readAuditEntriesMemRL(t, auditFile)

	var rateLimitedEntries []map[string]any
	for _, e := range entries {
		if e["event"] == "memory.rate_limited" {
			rateLimitedEntries = append(rateLimitedEntries, e)
		}
	}
	require.Len(t, rateLimitedEntries, 1, "exactly one memory.rate_limited audit entry expected")

	entry := rateLimitedEntries[0]
	assert.Equal(t, "deny", entry["decision"], "rate-limited entry must be a deny")
	assert.Equal(t, "agent-A", entry["agent_id"])
	assert.Equal(t, "remember", entry["tool"])
	assert.Contains(t, entry["policy_rule"], "memory_rate_limit:",
		"policy_rule must identify the bucket that tripped")
	details, ok := entry["details"].(map[string]any)
	require.True(t, ok, "details field must be present")
	assert.Equal(t, "agent", details["scope"])
	assert.NotZero(t, details["retry_after_seconds"], "retry_after_seconds must be present and non-zero")
	assert.NotEmpty(t, details["content_sha256"], "content sha must be logged for forensics")
}

// TestRememberTool_RecoveryAfterRetryAfter proves that after the
// sliding window expires, a previously-throttled agent can write again.
// Models the "retry-after-elapses" leg of the lifecycle the gateway
// auth_rate_limit_recovery_test enforces, but at the tool boundary.
func TestRememberTool_RecoveryAfterRetryAfter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping recovery test in short mode — requires real time.Sleep")
	}

	tmpDir := t.TempDir()
	store := newSimpleMemStore(tmpDir)
	tool := tools.NewRememberTool(store, nil)

	const window = 200 * time.Millisecond
	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  2,
		PerCallerLimit: 100,
		Window:         window,
	})
	tool.SetMemoryRateLimiter(limiter)

	ctx := withCallerCtx("agent-A", "rest", "user-1")

	// Exhaust the bucket.
	for i := 0; i < 2; i++ {
		require.False(t,
			tool.Execute(ctx, map[string]any{"content": "x", "category": "reference"}).IsError,
			"setup call %d/2 must succeed", i+1)
	}
	require.True(t,
		tool.Execute(ctx, map[string]any{"content": "x", "category": "reference"}).IsError,
		"setup proof: bucket exhausted, third call rejected")

	// Sleep past the window so all timestamps evict.
	time.Sleep(window + 50*time.Millisecond)

	// Recovery: the next call must succeed.
	recovered := tool.Execute(ctx, map[string]any{"content": "after recovery", "category": "reference"})
	require.False(t, recovered.IsError,
		"call after window expiry must succeed (clean recovery): %s", recovered.ForLLM)
}

// TestRememberTool_TwoAgents_ShareCallerIndependentAgent proves the spec
// invariant: two different agents share the per-caller bucket but have
// independent per-agent buckets. Setup mirrors the direct-limiter test
// of the same name but goes end-to-end through Execute.
func TestRememberTool_TwoAgents_ShareCallerIndependentAgent(t *testing.T) {
	tmpDir := t.TempDir()
	store := newSimpleMemStore(tmpDir)
	tool := tools.NewRememberTool(store, nil)

	// Configure: per-agent=2 (small) and per-caller=10 (large) so the per-
	// caller bucket has room for both agents to make multiple calls but
	// each agent independently saturates at 2.
	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  2,
		PerCallerLimit: 10,
		Window:         time.Minute,
	})
	tool.SetMemoryRateLimiter(limiter)

	ctxA := withCallerCtx("agent-A", "rest", "user-1")
	ctxB := withCallerCtx("agent-B", "rest", "user-1") // SAME caller as A

	// agent-A exhausts its per-agent bucket.
	for i := 0; i < 2; i++ {
		require.False(t,
			tool.Execute(ctxA, map[string]any{"content": "a", "category": "reference"}).IsError,
			"agent-A setup call %d must succeed", i+1)
	}
	denied := tool.Execute(ctxA, map[string]any{"content": "a", "category": "reference"})
	require.True(t, denied.IsError, "agent-A third call must be denied (per-agent bucket full)")
	require.Contains(t, denied.ForLLM, "agent scope")

	// agent-B (SAME caller) must still be able to write. This is the
	// independence check — agent-A's exhaustion does NOT bleed into
	// agent-B's per-agent bucket. The per-caller bucket has used 2
	// slots so far (out of 10) so caller capacity is also fine.
	for i := 0; i < 2; i++ {
		r := tool.Execute(ctxB, map[string]any{"content": "b", "category": "reference"})
		assert.False(t, r.IsError,
			"agent-B call %d/2 under same caller must succeed despite agent-A exhaustion: %s",
			i+1, r.ForLLM)
	}
}

// readAuditEntriesMemRL decodes audit.jsonl line-by-line into a slice of
// maps for assertion access. Returns an empty slice if the file does
// not exist (some tests gate on the audit logger being set).
func readAuditEntriesMemRL(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	defer f.Close()

	var out []map[string]any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry),
			"audit line must be valid JSON: %s", line)
		out = append(out, entry)
	}
	require.NoError(t, scanner.Err())
	return out
}
