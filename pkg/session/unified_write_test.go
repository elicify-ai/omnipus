// unified_write_test.go: tests for append and rewrite session transcripts and their sidecar files

package session

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from unified.go tests 2026-09-15 ---

// TestAppendTranscript_CacheTokens_AccumulatesInStats verifies that an assistant
// TranscriptEntry with CacheReadTokens and CacheWriteTokens populated correctly
// updates SessionStats.TokensCacheRead and TokensCacheWrite.
//
// BDD:
//
//	Given a fresh session,
//	When an assistant entry with Tokens=200, CacheReadTokens=50, CacheWriteTokens=20
//	  is appended,
//	Then Stats.TokensCacheRead==50 and Stats.TokensCacheWrite==20.
func TestAppendTranscript_CacheTokens_AccumulatesInStats(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	entry := TranscriptEntry{
		ID:               "cache-entry-001",
		Role:             "assistant",
		Content:          "Cached response",
		Tokens:           200,
		CacheReadTokens:  50,
		CacheWriteTokens: 20,
		Timestamp:        time.Now().UTC(),
	}

	require.NoError(t, store.AppendTranscript(meta.ID, entry))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 50, got.Stats.TokensCacheRead,
		"TokensCacheRead must equal the entry's CacheReadTokens")
	assert.Equal(t, 20, got.Stats.TokensCacheWrite,
		"TokensCacheWrite must equal the entry's CacheWriteTokens")
	assert.Equal(t, 200, got.Stats.TokensOut,
		"TokensOut must equal the full turn total (Tokens field)")
}

// TestAppendTranscript_ByModel_AccumulatesPerModel verifies that two assistant
// entries with different Model fields produce separate entries in ByModel.
//
// BDD:
//
//	Given two assistant entries using model "claude-sonnet-4-6" and "glm-5.2",
//	When both are appended,
//	Then Stats.ByModel["claude-sonnet-4-6"].Total==150 and
//	     Stats.ByModel["glm-5.2"].Total==80.
func TestAppendTranscript_ByModel_AccumulatesPerModel(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	entry1 := TranscriptEntry{
		ID:               "bymodel-001",
		Role:             "assistant",
		Content:          "Claude response",
		Model:            "claude-sonnet-4-6",
		Tokens:           150,
		CacheReadTokens:  30,
		CacheWriteTokens: 0,
		Timestamp:        time.Now().UTC(),
	}
	entry2 := TranscriptEntry{
		ID:               "bymodel-002",
		Role:             "assistant",
		Content:          "GLM response",
		Model:            "glm-5.2",
		Tokens:           80,
		CacheReadTokens:  0,
		CacheWriteTokens: 0,
		Timestamp:        time.Now().UTC(),
	}

	require.NoError(t, store.AppendTranscript(meta.ID, entry1))
	require.NoError(t, store.AppendTranscript(meta.ID, entry2))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	require.NotNil(t, got.Stats.ByModel, "ByModel must be non-nil after entries with Model set")
	require.Contains(t, got.Stats.ByModel, "claude-sonnet-4-6",
		"ByModel must contain an entry for claude-sonnet-4-6")
	require.Contains(t, got.Stats.ByModel, "glm-5.2",
		"ByModel must contain an entry for glm-5.2")

	claudeEntry := got.Stats.ByModel["claude-sonnet-4-6"]
	assert.Equal(t, 150, claudeEntry.Total,
		"claude-sonnet-4-6 total must equal Tokens from entry1")
	assert.Equal(t, 30, claudeEntry.CacheRead,
		"claude-sonnet-4-6 CacheRead must equal CacheReadTokens from entry1")

	glmEntry := got.Stats.ByModel["glm-5.2"]
	assert.Equal(t, 80, glmEntry.Total,
		"glm-5.2 total must equal Tokens from entry2")
}

// TestAppendTranscript_ExternalCLITurn_ZeroTokenContribution verifies the scope
// guard: an assistant entry produced by an external CLI sub-agent (which never
// calls AddTurnStats/AddTurnCacheStats) has all zero token fields, and appending
// it does NOT inflate SessionStats beyond zero for token-related fields.
//
// BDD:
//
//	Given an assistant TranscriptEntry with Tokens==0 and all cache fields==0
//	  (simulating a subagent_3p turn that produced text but no native LLM usage),
//	When the entry is appended,
//	Then Stats.TokensTotal==0, TokensCacheRead==0, TokensCacheWrite==0.
//
// Guards against: external CLI turns accidentally inflating token counts.
// Traces to: token-usage-tracking-2026-06.md §Wave1 item 3 (scope guard)
func TestAppendTranscript_ExternalCLITurn_ZeroTokenContribution(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	// An external CLI sub-agent always writes entries with Tokens=0
	// because AddTurnStats is never called for external-cli dispatch.
	extEntry := TranscriptEntry{
		ID:               "ext-cli-001",
		Role:             "assistant",
		Content:          "Result from external CLI agent",
		Model:            "claude-code", // model label from the external CLI
		Tokens:           0,             // zero — no native engine was used
		CacheReadTokens:  0,
		CacheWriteTokens: 0,
		Timestamp:        time.Now().UTC(),
	}

	require.NoError(t, store.AppendTranscript(meta.ID, extEntry))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 0, got.Stats.TokensTotal,
		"TokensTotal must be 0 — external CLI turn contributes no usage")
	assert.Equal(t, 0, got.Stats.TokensCacheRead,
		"TokensCacheRead must be 0 — external CLI turn contributes no cache")
	assert.Equal(t, 0, got.Stats.TokensCacheWrite,
		"TokensCacheWrite must be 0 — external CLI turn contributes no cache")
}

// TestAppendTranscript_TwoCacheAppendsSum verifies that two sequential assistant
// entries with cache tokens sum their cache counts (not replace/reset).
//
// BDD:
//
//	Given two assistant entries, both with CacheReadTokens,
//	When both are appended,
//	Then Stats.TokensCacheRead equals the sum of both entries' CacheReadTokens.
func TestAppendTranscript_TwoCacheAppendsSum(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	ts := time.Now().UTC()

	e1 := TranscriptEntry{
		ID:              "cache-sum-001",
		Role:            "assistant",
		Content:         "First cached",
		Tokens:          100,
		CacheReadTokens: 40,
		Timestamp:       ts,
	}
	e2 := TranscriptEntry{
		ID:              "cache-sum-002",
		Role:            "assistant",
		Content:         "Second cached",
		Tokens:          90,
		CacheReadTokens: 35,
		Timestamp:       ts,
	}

	require.NoError(t, store.AppendTranscript(meta.ID, e1))
	require.NoError(t, store.AppendTranscript(meta.ID, e2))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 75, got.Stats.TokensCacheRead,
		"TokensCacheRead must sum both entries (40+35)")
	assert.Equal(t, 190, got.Stats.TokensTotal,
		"TokensTotal must sum both entries (100+90)")
}

func TestSave_WithColonInKey(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	// Create a session with a key containing colon (typical channel session key).
	key := "telegram:123456"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")

	// Save should succeed even though the key contains ':'.
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save(%q) failed: %v", key, err)
	}

	// The file on disk should use hex-encoded name.
	expectedFile := filepath.Join(tmpDir, hex.EncodeToString([]byte(key))+".json")
	if _, err := os.Stat(expectedFile); errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected session file %s to exist", expectedFile)
	}

	// Load into a fresh manager and verify the session round-trips.
	sm2 := NewSessionManager(tmpDir)
	history := sm2.GetHistory(key)
	if len(history) != 1 {
		t.Fatalf("expected 1 message after reload, got %d", len(history))
	}
	if history[0].Content != "hello" {
		t.Errorf("expected message content %q, got %q", "hello", history[0].Content)
	}
}

func TestSave_RejectsPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	// Invalid raw keys that must be rejected before encoding.
	badKeys := []string{"", ".", ".."}
	for _, key := range badKeys {
		sm.GetOrCreate(key)
		if err := sm.Save(key); err == nil {
			t.Errorf("Save(%q) should have failed but didn't", key)
		}
	}

	// Keys containing path separators are hex-encoded (no subdirs created).
	sm.GetOrCreate("foo/bar")
	if err := sm.Save("foo/bar"); err != nil {
		t.Fatalf("Save(\"foo/bar\") after sanitize should succeed: %v", err)
	}
	expectedHex := hex.EncodeToString([]byte("foo/bar"))
	if _, err := os.Stat(filepath.Join(tmpDir, expectedHex+".json")); errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected %s.json in storage (hex-encoded from foo/bar)", expectedHex)
	}
}

// TestWriteMetaLocked_PendingAskDiffDispatch exercises writeMetaLocked's
// (unified.go) diff-based dispatcher directly — the path
// pkg/session/unified_api.go's CreateSessionWithID/AppendTranscriptStrict
// call, which SetMeta's own tests never reach. A meta whose ONLY change
// from the currently-persisted value is PendingAskJSON must write
// pending_ask.json and MUST NOT rewrite goal.json/loop.json.
func TestWriteMetaLocked_PendingAskDiffDispatch(t *testing.T) {
	store := newTestStore(t)
	created, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
	require.NoError(t, err)
	sid := created.ID

	h := store.lockSession(sid)
	current, err := store.readMetaLocked(sid)
	require.NoError(t, err)
	h.Unlock()

	mutated := current.Clone()
	mutated.PendingAskJSON = `{"questions":[{"id":"direct-dispatch"}]}`

	h = store.lockSession(sid)
	err = store.writeMetaLocked(sid, mutated)
	h.Unlock()
	require.NoError(t, err)

	pendingAskPath := filepath.Join(store.BaseDir(), sid, "pending_ask.json")
	_, statErr := os.Stat(pendingAskPath)
	require.NoError(t, statErr, "writeMetaLocked must dispatch a PendingAskJSON-only change to pending_ask.json")

	goalPath := filepath.Join(store.BaseDir(), sid, "goal.json")
	_, statErr = os.Stat(goalPath)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "writeMetaLocked must NOT create goal.json for a PendingAskJSON-only diff")

	onDisk, err := u5ReadPendingAskFile(filepath.Join(store.BaseDir(), sid))
	require.NoError(t, err)
	assert.Equal(t, mutated.PendingAskJSON, onDisk.PendingAskJSON)
}

// TestAppendTranscript_AssistantEntry_StatsNonZero asserts that appending a single
// assistant TranscriptEntry with Tokens=120 and Cost=0.0034 causes the session
// Stats.TokensOut, Stats.TokensTotal, and Stats.Cost to all be non-zero.
//
// BDD:
//
//	Given a fresh session,
//	When an assistant TranscriptEntry{Tokens:120, Cost:0.0034} is appended,
//	Then Stats.TokensOut==120, Stats.TokensTotal==120, Stats.Cost≈0.0034.
//
// Guards against: AppendTranscript silently discarding token/cost fields.
// Traces to: pkg/session/unified.go AppendTranscript stats accumulation — #411
func TestAppendTranscript_AssistantEntry_StatsNonZero(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// No PromptTokens/CompletionTokens set — this is the legacy no-split
	// shape (see accumulateEntryStats in entry_stats.go). It is NOT true in
	// general that an assistant entry leaves TokensIn at 0: an entry that
	// DOES carry the provider's input/output split (PromptTokens > 0)
	// contributes to TokensIn — see TestAppendTranscript_RecordsInputOutputSplit
	// in token_split_test.go for that case.
	entry := TranscriptEntry{
		ID:        "entry-001",
		Role:      "assistant",
		Content:   "Hello, how can I help?",
		Tokens:    120,
		Cost:      0.0034,
		Timestamp: time.Now().UTC(),
	}

	err = store.AppendTranscript(sessionID, entry)
	require.NoError(t, err, "AppendTranscript must succeed")

	got, err := store.GetMeta(sessionID)
	require.NoError(t, err, "GetMeta must succeed after AppendTranscript")

	assert.Equal(t, 120, got.Stats.TokensOut,
		"TokensOut must equal the assistant entry Tokens")
	assert.Equal(t, 0, got.Stats.TokensIn,
		"TokensIn stays 0 here because this entry carries no PromptTokens/CompletionTokens split "+
			"(the legacy no-split fallback), not because assistant entries never affect TokensIn")
	assert.Equal(t, 120, got.Stats.TokensTotal,
		"TokensTotal must equal the assistant entry Tokens")
	assert.InDelta(t, 0.0034, got.Stats.Cost, 1e-9,
		"Cost must equal the assistant entry Cost")
	assert.Equal(t, 1, got.Stats.MessageCount,
		"MessageCount must be 1 after one assistant entry")
}

// TestAppendTranscript_TwoAppends_StatsSumNotDouble asserts that appending two
// separate assistant entries correctly sums their stats — proving the accumulator
// adds rather than replaces or doubles.
//
// BDD:
//
//	Given a session with Stats.TokensOut==120 from a prior append,
//	When a second assistant entry with Tokens=80, Cost=0.0021 is appended,
//	Then Stats.TokensOut==200, Stats.TokensTotal==200, Stats.Cost≈0.0055.
//
// Guards against: stats reset or double-counting on each append.
// Traces to: pkg/session/unified.go AppendTranscript stats accumulation — #411
func TestAppendTranscript_TwoAppends_StatsSumNotDouble(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	entry1 := TranscriptEntry{
		ID:        "entry-001",
		Role:      "assistant",
		Content:   "First reply",
		Tokens:    120,
		Cost:      0.0034,
		Timestamp: time.Now().UTC(),
	}
	entry2 := TranscriptEntry{
		ID:        "entry-002",
		Role:      "assistant",
		Content:   "Second reply",
		Tokens:    80,
		Cost:      0.0021,
		Timestamp: time.Now().UTC(),
	}

	require.NoError(t, store.AppendTranscript(sessionID, entry1))
	require.NoError(t, store.AppendTranscript(sessionID, entry2))

	got, err := store.GetMeta(sessionID)
	require.NoError(t, err)

	assert.Equal(t, 200, got.Stats.TokensOut,
		"TokensOut must equal sum of both assistant entries (120+80)")
	assert.Equal(t, 200, got.Stats.TokensTotal,
		"TokensTotal must equal sum of both entries (120+80)")
	assert.InDelta(t, 0.0055, got.Stats.Cost, 1e-9,
		"Cost must equal sum of both entries (0.0034+0.0021)")
	assert.Equal(t, 2, got.Stats.MessageCount,
		"MessageCount must be 2 after two assistant entries")
}

// TestAppendTranscript_UserEntry_TokensIn asserts that a user-role entry increments
// TokensIn (not TokensOut), providing the differentiation test proving the role
// routing is not hardcoded.
//
// BDD:
//
//	Given a fresh session,
//	When a user entry with Tokens=50 is appended,
//	Then TokensIn==50, TokensOut==0.
//
// Guards against: role-based token routing silently broken.
// Traces to: pkg/session/unified.go AppendTranscript role → TokensIn/TokensOut routing
func TestAppendTranscript_UserEntry_TokensIn(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	entry := TranscriptEntry{
		ID:        "user-001",
		Role:      "user",
		Content:   "Hi there",
		Tokens:    50,
		Cost:      0.0,
		Timestamp: time.Now().UTC(),
	}

	require.NoError(t, store.AppendTranscript(meta.ID, entry))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 50, got.Stats.TokensIn,
		"TokensIn must equal user entry tokens")
	assert.Equal(t, 0, got.Stats.TokensOut,
		"TokensOut must remain 0 for a user entry")
	assert.Equal(t, 50, got.Stats.TokensTotal,
		"TokensTotal must include user entry tokens")
}

// TestAppendTranscriptStrict_RecordsInputOutputSplit exercises the PRODUCTION
// write path (AppendTranscriptStrict is what pkg/agent/turn.go calls), not the
// legacy PartitionStore path.
func TestAppendTranscriptStrict_RecordsInputOutputSplit(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)

	require.NoError(t, store.AppendTranscriptStrict(meta.ID,
		assistantEntryWithSplit("z-ai/glm-5.2", 800, 200, 1000)))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 800, got.Stats.TokensIn, "tokens_in must reflect the provider's prompt tokens")
	assert.Equal(t, 200, got.Stats.TokensOut, "tokens_out must be completion tokens, not the whole total")
	assert.Equal(t, 1000, got.Stats.TokensTotal)

	mt, ok := got.Stats.ByModel["z-ai/glm-5.2"]
	require.True(t, ok, "per-model breakdown must exist")
	assert.Equal(t, 800, mt.In, "per-model in was structurally always 0 before this fix")
	assert.Equal(t, 200, mt.Out, "per-model out was structurally always 0 before this fix")
	assert.Equal(t, 1000, mt.Total)
}

// The reconciliation invariant. Without it a future change could repopulate
// tokens_out with the turn total again and the numbers would silently
// double-count input.
//
// This test previously used entries with ZERO cache tokens, which made it
// vacuous: it would have passed even if cache read/write were still (as the
// stale doc comments once claimed) a SUBSET of tokens_out rather than an
// ADDITIVE component of tokens_total, since 0 is a subset of anything. Every
// entry here carries non-zero CacheReadTokens/CacheWriteTokens, and Tokens
// (the provider-reported total) is set to prompt + completion + cache_read +
// cache_write — the real additive relation (see
// pkg/providers/protocoltypes/types.go's UsageInfo doc) — so this only
// passes if the code actually sums all four components into TokensTotal.
func TestAppendTranscriptStrict_InOutReconcilesWithTotal(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)

	const prompt, completion, cacheRead, cacheWrite = 700, 300, 150, 50
	const total = prompt + completion + cacheRead + cacheWrite // 1200

	for i := 0; i < 3; i++ {
		entry := TranscriptEntry{
			ID:               fmt.Sprintf("e-cache-%d", i),
			Role:             "assistant",
			Content:          "hi",
			Timestamp:        time.Now().UTC(),
			Model:            "m1",
			Tokens:           total,
			PromptTokens:     prompt,
			CompletionTokens: completion,
			CacheReadTokens:  cacheRead,
			CacheWriteTokens: cacheWrite,
		}
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, entry))
	}

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 3*cacheRead, got.Stats.TokensCacheRead, "cache_read must accumulate, not stay 0")
	assert.Equal(t, 3*cacheWrite, got.Stats.TokensCacheWrite, "cache_write must accumulate, not stay 0")

	sum := got.Stats.TokensIn + got.Stats.TokensOut + got.Stats.TokensCacheRead + got.Stats.TokensCacheWrite
	assert.Equal(t, got.Stats.TokensTotal, sum,
		"in + out + cache must reconcile with total (in=%d out=%d cache_read=%d cache_write=%d total=%d)",
		got.Stats.TokensIn, got.Stats.TokensOut, got.Stats.TokensCacheRead, got.Stats.TokensCacheWrite, got.Stats.TokensTotal)
}

// Multi-model sessions must attribute to the right model. The session that
// exposed this bug used two models, and the split has to survive that.
func TestAppendTranscriptStrict_SplitIsPerModel(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)

	require.NoError(t, store.AppendTranscriptStrict(meta.ID, assistantEntryWithSplit("model-a", 100, 10, 110)))
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, assistantEntryWithSplit("model-b", 500, 50, 550)))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 100, got.Stats.ByModel["model-a"].In)
	assert.Equal(t, 10, got.Stats.ByModel["model-a"].Out)
	assert.Equal(t, 500, got.Stats.ByModel["model-b"].In)
	assert.Equal(t, 50, got.Stats.ByModel["model-b"].Out)
	assert.Equal(t, 600, got.Stats.TokensIn)
	assert.Equal(t, 60, got.Stats.TokensOut)
}

// Back-compat: transcripts written before the split existed carry only a total.
// Those must keep aggregating exactly as they did, or historical sessions would
// suddenly report 0 output tokens.
func TestAppendTranscriptStrict_LegacyEntryWithoutSplitKeepsOldBehaviour(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)

	legacy := TranscriptEntry{
		ID:        "legacy-1",
		Role:      "assistant",
		Content:   "hi",
		Timestamp: time.Now().UTC(),
		Model:     "old-model",
		Tokens:    1234, // total only; no PromptTokens/CompletionTokens
	}
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, legacy))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 1234, got.Stats.TokensOut, "a legacy entry must still book its total as output")
	assert.Equal(t, 0, got.Stats.TokensIn, "an unrecorded split must stay 0 rather than be fabricated")
	assert.Equal(t, 1234, got.Stats.ByModel["old-model"].Total)
}

// TestAppendTranscriptStrict_HasSplitBoundary pins the hasSplit boundary
// (accumulateEntryStats in entry_stats.go: hasSplit := prompt>0 ||
// completion>0) for the two asymmetric cases where exactly one half of the
// split is genuinely zero rather than unrecorded.
//
// Decision (documented here and in accumulateEntryStats' doc comment): a
// genuinely-zero component is booked AS ZERO, not treated as "missing" and
// backfilled from the turn total. This is intentional, not a token-loss
// regression versus the pre-split behaviour: production callers always
// populate PromptTokens, CompletionTokens, CacheReadTokens, and
// CacheWriteTokens together from the SAME provider UsageInfo response
// (pkg/providers/protocoltypes/types.go), whose TotalTokens is documented to
// equal PromptTokens + CompletionTokens + CacheReadTokens + CacheWriteTokens.
// So when completion really is 0 (e.g. a turn that sent a large prompt and
// stopped before producing any completion tokens), the rest of Tokens is
// necessarily accounted for by cache — never silently dropped — and falling
// back to booking the whole total into TokensOut whenever ONE split
// component reads zero would reintroduce the over-counting bug this
// convention exists to fix, just gated on a boundary condition instead of
// unconditionally.
func TestAppendTranscriptStrict_HasSplitBoundary(t *testing.T) {
	cases := []struct {
		name             string
		prompt           int
		completion       int
		total            int
		wantTokensIn     int
		wantTokensOut    int
		wantHasSplitDocs string
	}{
		{
			name:             "prompt only, completion genuinely zero",
			prompt:           5000,
			completion:       0,
			total:            5200, // remainder (200) is unaccounted cache in this synthetic case
			wantTokensIn:     5000,
			wantTokensOut:    0,
			wantHasSplitDocs: "hasSplit is true (prompt>0); completion books as the real 0, not total",
		},
		{
			name:             "completion only, prompt genuinely zero",
			prompt:           0,
			completion:       300,
			total:            1000, // remainder (700) is unaccounted cache in this synthetic case
			wantTokensIn:     0,
			wantTokensOut:    300,
			wantHasSplitDocs: "hasSplit is true (completion>0); prompt books as the real 0, not total",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newUnifiedStoreForTest(t)
			meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
			require.NoError(t, err)

			entry := TranscriptEntry{
				ID:               "e-boundary",
				Role:             "assistant",
				Content:          "hi",
				Timestamp:        time.Now().UTC(),
				Model:            "m1",
				Tokens:           tc.total,
				PromptTokens:     tc.prompt,
				CompletionTokens: tc.completion,
			}
			require.NoError(t, store.AppendTranscriptStrict(meta.ID, entry))

			got, err := store.GetMeta(meta.ID)
			require.NoError(t, err)

			assert.Equal(t, tc.wantTokensIn, got.Stats.TokensIn, tc.wantHasSplitDocs)
			assert.Equal(t, tc.wantTokensOut, got.Stats.TokensOut, tc.wantHasSplitDocs)
			assert.Equal(t, tc.total, got.Stats.TokensTotal, "TokensTotal always books the provider-reported total verbatim")
		})
	}
}

// TestAppendTranscript_RecordsInputOutputSplit exercises the NON-strict
// UnifiedStore.AppendTranscript entry point (unified.go). Every other test
// in this file goes through AppendTranscriptStrict (what pkg/agent/turn.go
// actually calls in production); this test proves the extracted
// accumulateEntryStats helper (entry_stats.go) is wired identically at its
// second call site.
func TestAppendTranscript_RecordsInputOutputSplit(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)

	require.NoError(t, store.AppendTranscript(meta.ID,
		assistantEntryWithSplit("z-ai/glm-5.2", 800, 200, 1000)))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 800, got.Stats.TokensIn)
	assert.Equal(t, 200, got.Stats.TokensOut)
	assert.Equal(t, 1000, got.Stats.TokensTotal)

	mt, ok := got.Stats.ByModel["z-ai/glm-5.2"]
	require.True(t, ok, "per-model breakdown must exist")
	assert.Equal(t, 800, mt.In)
	assert.Equal(t, 200, mt.Out)
	assert.Equal(t, 1000, mt.Total)
}

// ---------------------------------------------------------------------
// Test #1: TestAppendTranscriptStrict_UnknownSession_ErrorsAndCreatesNothing
// ---------------------------------------------------------------------

// TestAppendTranscriptStrict_UnknownSession_ErrorsAndCreatesNothing is test
// #1 (BDD-01, dataset row 2): a UUID with no meta.json must return a
// non-nil error and create NOTHING on disk.
//
// Red/green evidence (required by this unit's task): this test's assertions
// were DELIBERATELY the exact assertions that FAILED against the
// PRE-ADR-057-U5 lenient UnifiedStore.AppendTranscript for the same input —
// see TestAppendTranscript_StrictAfterFR002_MatchesAppendTranscriptStrict
// below (// ADR-057-U5-inverted), which originally ran the identical setup
// against AppendTranscript and asserted the OPPOSITE outcome (nil error,
// directory created) as a RED pin, and is now INVERTED to assert the FR-002
// end state once U5 (Wave C) made AppendTranscript itself strict.
func TestAppendTranscriptStrict_UnknownSession_ErrorsAndCreatesNothing(t *testing.T) {
	store := u2NewTestStore(t)
	const unknownID = "unknown-session-adr057-1"

	err := store.AppendTranscriptStrict(unknownID, TranscriptEntry{Role: "user", Content: "hello"})
	require.Error(t, err, "AppendTranscriptStrict against an unknown session MUST return a non-nil error")

	sessionDir := filepath.Join(store.BaseDir(), unknownID)
	_, statErr := os.Stat(sessionDir)
	require.Truef(t, errors.Is(statErr, os.ErrNotExist), "AppendTranscriptStrict MUST create no directory for an unknown session — os.Stat(%q) returned err=%v", sessionDir, statErr)
}

// TestAppendTranscript_StrictAfterFR002_MatchesAppendTranscriptStrict is the
// GREEN half of this unit's required red/green pair, INVERTED from
// TestAppendTranscript_LenientBehavior_DemonstratesTheDefect
// (// ADR-057-U5-inverted) once ADR-057 U5 (Wave C, FR-002/W3a) deleted
// AppendTranscript's lenient slog.Warn+return-nil branch — `[grill2 C2-3]`:
// AC-1's frozen text is a property of AppendTranscript ITSELF, not of a
// strict sibling, so leaving the plain method lenient while only
// AppendTranscriptStrict was strict would satisfy AC-1 only for callers that
// had switched to the sibling. The original version of this test (preserved
// verbatim in git history at commit acfd0e5a) asserted the OPPOSITE outcome
// — nil error, orphan directory created — as a deliberate RED pin proving
// the pre-fix defect was real, not vacuous. Verified red before this
// inversion: `go test -run TestAppendTranscript_LenientBehavior` failed with
// "the LENIENT AppendTranscript returns nil for an unknown session (the
// defect)" once FR-002 landed, which is exactly the signal that the old
// assertions now describe a defect that no longer exists — the correct
// response is inversion (FR-072's principle), not silent deletion.
//
// This test now pins FR-002's actual end state: AppendTranscript and
// AppendTranscriptStrict are "a name, not a second behavior" (FR-002) — both
// must refuse an unknown session identically.
func TestAppendTranscript_StrictAfterFR002_MatchesAppendTranscriptStrict(t *testing.T) {
	store := u2NewTestStore(t)
	const unknownID = "unknown-session-adr057-1-lenient"

	err := store.AppendTranscript(unknownID, TranscriptEntry{Role: "user", Content: "hello"})
	require.Error(t, err, "AppendTranscript itself must now be strict (FR-002) — no lenient sibling survives")

	sessionDir := filepath.Join(store.BaseDir(), unknownID)
	_, statErr := os.Stat(sessionDir)
	require.Truef(t, errors.Is(statErr, os.ErrNotExist), "AppendTranscript must create NO directory for an unknown session (os.Stat(%q) err=%v) — "+
		"FR-002 deleted the orphan-create branch this test used to pin as a defect", sessionDir, statErr)

	transcriptPath := filepath.Join(sessionDir, "transcript.jsonl")
	_, statErr2 := os.Stat(transcriptPath)
	require.True(t, errors.Is(statErr2, os.ErrNotExist), "no transcript.jsonl may be written for an unknown session")

	// FR-002's own text: AppendTranscriptStrict is "a name, not a second
	// behavior" — both entry points must now agree on this exact input.
	const secondUnknownID = "unknown-session-adr057-1-lenient-strict-parity"
	strictErr := store.AppendTranscriptStrict(secondUnknownID, TranscriptEntry{Role: "user", Content: "hello"})
	require.Error(t, strictErr)
	assert.Equal(t, err != nil, strictErr != nil, "AppendTranscript and AppendTranscriptStrict must agree on an unknown session")
}

// ---------------------------------------------------------------------
// Test #2: TestAppendTranscriptStrict_KnownSession_AppendsExactlyOneLine
// ---------------------------------------------------------------------

// TestAppendTranscriptStrict_KnownSession_AppendsExactlyOneLine is test #2
// (BDD-02, dataset row 3): a real, existing session gets exactly one more
// line, and nil is returned.
func TestAppendTranscriptStrict_KnownSession_AppendsExactlyOneLine(t *testing.T) {
	store := u2NewTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	before, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	beforeLen := len(before)

	err = store.AppendTranscriptStrict(meta.ID, TranscriptEntry{Role: "user", Content: "hi"})
	require.NoError(t, err)

	after, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, beforeLen+1, len(after), "AppendTranscriptStrict must append EXACTLY one line")
	assert.Equal(t, "hi", after[len(after)-1].Content)
}

// TestAppendTranscriptStrict_StatsThrottled is the 14-reviewer fix-wave
// finding #4 regression guard: AppendTranscriptStrict's stats bookkeeping
// must go through the SAME FR-061 in-memory-only throttle AppendTranscript's
// own hot path already uses (u6MarkStatsDirtyLocked), not a synchronous
// writeMetaLocked call. Proven exactly like TestStatsThrottle_NoFileWriteWithinInterval
// (unified_stats_flush_adr057_test.go) proves it for the non-strict sibling:
// immediately after the call, the transcript line is durably on disk (never
// throttled — FR-062) but stats.json is NOT, while the cache (GetMeta) is
// already current; a forced flush then persists it.
func TestAppendTranscriptStrict_StatsThrottled(t *testing.T) {
	store := u2NewTestStore(t)
	store.SetStatsFlushInterval(time.Hour) // isolate from the periodic ticker

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	require.NoError(t, store.AppendTranscriptStrict(sessionID, TranscriptEntry{Role: "user", Content: "hi", Tokens: 5}))

	// The transcript line itself is never throttled (FR-062) — real bytes on
	// disk immediately.
	transcript, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, transcript, 1)

	// GREEN 1: stats.json must NOT exist yet — the delta is in-memory only.
	statsPath := filepath.Join(store.BaseDir(), sessionID, "stats.json")
	_, statErr := os.Stat(statsPath)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "stats.json must not exist immediately after AppendTranscriptStrict — the FR-061 throttle must apply here too")

	// GREEN 2: the cache is already current (GetMeta never touches disk on a
	// cache hit) — a caller reading back through the store sees the update
	// immediately despite the disk write being deferred.
	cached, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, 5, cached.Stats.TokensIn)

	// GREEN 3: a forced flush persists the deferred delta — the throttle
	// defers, it never drops.
	require.NoError(t, store.FlushSessionStats(sessionID))
	data, err := os.ReadFile(statsPath)
	require.NoError(t, err)
	var onDisk struct {
		TokensIn int `json:"tokens_in"`
	}
	require.NoError(t, json.Unmarshal(data, &onDisk))
	assert.Equal(t, 5, onDisk.TokensIn)
}

// ---------------------------------------------------------------------
// Dataset: AppendTranscriptStrict session-id resolution (spec §"Test
// Datasets", all 7 boundary rows)
// ---------------------------------------------------------------------

// TestAppendTranscriptStrict_SessionIDResolutionDataset exercises every row
// of the spec's "AppendTranscriptStrict session-id resolution" dataset.
// Assertions are on the OBSERVABLE contract BDD-01/BDD-02 actually state —
// non-nil error and no directory created for every negative row, nil and a
// line-count delta of exactly 1 for the happy row — rather than on which
// internal branch (validateSessionID vs. the readMetaLocked existence
// check) produced a given negative result.
//
// VERIFIED DISCREPANCY (evidence-based, not assumed): the spec's dataset row
// 6 (".hidden") claims the rejection comes from validateSessionID
// ("pre-existing... Leading-dot reject"). Reading pkg/session/unified.go's
// validateSessionID (owned by the U4->U5->U6 chain, not this unit) shows it
// rejects only id=="", a path separator, a literal "..", id=="." and
// id==".context" — it does NOT special-case an arbitrary leading dot, so
// validateSessionID(".hidden") returns nil. AppendTranscriptStrict(".hidden",
// ...) still returns a non-nil error and still creates no directory — via
// the readMetaLocked "session does not exist" branch instead — so the
// EXTERNALLY OBSERVABLE contract this unit owns (FR-001/BDD-01) holds
// regardless. This is flagged here rather than "fixed" because
// validateSessionID is chain-owned (ownership Rule 2 — a unit that needs a
// change in a file it does not own must request it from the owner, never
// edit it itself); the row's error-SOURCE claim, not its error-PRESENCE
// claim, is what does not match the verified tree.
func TestAppendTranscriptStrict_SessionIDResolutionDataset(t *testing.T) {
	type row struct {
		name      string
		id        string
		wantError bool
	}
	rows := []row{
		{name: "row1_empty", id: "", wantError: true},
		{name: "row2_missing_entity", id: "dataset-row2-fresh-uuid", wantError: true},
		{name: "row5_path_traversal", id: "../escape", wantError: true},
		{name: "row6_leading_dot", id: ".hidden", wantError: true}, // see discrepancy note above
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			store := u2NewTestStore(t)
			err := store.AppendTranscriptStrict(r.id, TranscriptEntry{Role: "user", Content: "x"})
			if r.wantError {
				require.Error(t, err, "id %q must be refused", r.id)
			} else {
				require.NoError(t, err)
			}
			// None of these ids may ever gain a directory (validateSessionID's
			// own precondition for "/" ids like "../escape" makes computing a
			// meaningful join unsafe/misleading, so only check paths that stay
			// within baseDir).
			if r.id != "" && r.id != "../escape" {
				sessionDir := filepath.Join(store.BaseDir(), r.id)
				_, statErr := os.Stat(sessionDir)
				assert.Truef(t, errors.Is(statErr, os.ErrNotExist), "id %q must create no directory", r.id)
			}
		})
	}

	t.Run("row3_existing_session_happy_path", func(t *testing.T) {
		store := u2NewTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, TranscriptEntry{Role: "user", Content: "x"}))
	})

	t.Run("row4_directory_with_no_meta_json", func(t *testing.T) {
		store := u2NewTestStore(t)
		const corruptID = "dataset-row4-corrupt"
		sessionDir := filepath.Join(store.BaseDir(), corruptID)
		require.NoError(t, os.MkdirAll(sessionDir, 0o700))
		// Deliberately no meta.json written.

		err := store.AppendTranscriptStrict(corruptID, TranscriptEntry{Role: "user", Content: "x"})
		require.Error(t, err, "an existing directory with no meta.json must still be refused (the D11 asymmetry)")

		transcriptPath := filepath.Join(sessionDir, "transcript.jsonl")
		_, statErr := os.Stat(transcriptPath)
		assert.True(t, errors.Is(statErr, os.ErrNotExist), "no transcript.jsonl may be written into the corrupt directory")
	})

	t.Run("row7_deleted_between_resolve_and_append", func(t *testing.T) {
		store := u2NewTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.DeleteSession(meta.ID))

		err = store.AppendTranscriptStrict(meta.ID, TranscriptEntry{Role: "user", Content: "x"})
		require.Error(t, err, "a deleted session id must not be silently re-created by a subsequent strict append")

		_, statErr := os.Stat(filepath.Join(store.BaseDir(), meta.ID))
		assert.True(t, errors.Is(statErr, os.ErrNotExist), "AppendTranscriptStrict must not resurrect a deleted session's directory")
	})
}

// TestSetMeta_WriteFailureDoesNotCorruptCache is the MB-1 regression guard.
//
// Root cause under test: readMetaLocked previously returned the LIVE cache
// entry pointer on a hit, so SetMeta/SwitchAgent/AppendTranscript mutated the
// cached object IN PLACE before calling writeMetaLocked. If writeMetaLocked's
// disk write then failed, it returned early WITHOUT re-storing a clone — but
// the in-place mutation had already corrupted the cached object, so
// GetMeta/ListSessions went on reporting the unpersisted, attempted value
// while meta.json on disk still held the old one.
//
// This test injects a deterministic writeMetaLocked failure via the
// writeFileAtomicFn package-level test seam rather than a chmod-based trick:
// CI runs as root, which bypasses permission enforcement via
// CAP_DAC_OVERRIDE (see removeAllFn's doc comment for the identical
// rationale), so a chmod'd read-only directory would provide zero coverage
// of this behavior in CI.
//
// BDD: Given a session with a persisted title, When SetMeta is called with a
// new title AND the disk write fails, Then SetMeta returns the injected
// error AND both GetMeta and ListSessions still return the OLD title
// (matching what is still on disk) — never the attempted-but-unpersisted
// new title.
//
// Traces to: pkg/session/unified.go readMetaLocked, writeMetaLocked, SetMeta.
func TestSetMeta_WriteFailureDoesNotCorruptCache(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	const originalTitle = "Original Title"
	newTitle := originalTitle
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{Title: &newTitle}))

	// Inject a deterministic write failure for the duration of this test.
	injectedErr := errors.New("injected write failure")
	origWriteFileAtomicFn := writeFileAtomicFn
	t.Cleanup(func() { writeFileAtomicFn = origWriteFileAtomicFn })
	writeFileAtomicFn = func(path string, data []byte, perm os.FileMode) error {
		return injectedErr
	}

	attemptedTitle := "Attempted-But-Unpersisted Title"
	err = store.SetMeta(sessionID, MetaPatch{Title: &attemptedTitle})
	require.Error(t, err, "SetMeta must propagate the injected write failure")
	assert.ErrorIs(t, err, injectedErr)

	// Restore the real write function before reading back — GetMeta/
	// ListSessions never write, but this keeps the test seam's blast radius
	// minimal and matches the removeAllFn precedent's t.Cleanup discipline.
	writeFileAtomicFn = origWriteFileAtomicFn

	got, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, originalTitle, got.Title,
		"GetMeta must still return the OLD value after a FAILED SetMeta — cache must not diverge from disk")

	metas, err := store.ListSessions()
	require.NoError(t, err)
	found := findMeta(metas, sessionID)
	require.NotNil(t, found)
	assert.Equal(t, originalTitle, found.Title,
		"ListSessions must still return the OLD value after a FAILED SetMeta — cache must not diverge from disk")

	// Confirm disk itself matches — the cache and disk must agree, not just
	// both happen to be wrong in the same way.
	diskMeta, err := readUnifiedMeta(filepath.Join(store.baseDir, sessionID))
	require.NoError(t, err)
	assert.Equal(t, originalTitle, diskMeta.Title, "disk meta.json must be unchanged by the failed write")
}

// TestUpdateToolCallProjections_BatchRewriteAndRevert covers the transcript
// half of ADR-066 FR-022 (T066-12): the D5 emptying pass marks several tool
// calls `emptied` in ONE rewrite, each record's `result` becomes the
// projected content (the recall mark), the previous state comes back so an
// aborted turn can put the transcript back, and an id that matches nothing
// is skipped without failing the batch.
func TestUpdateToolCallProjections_BatchRewriteAndRevert(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sid := meta.ID

	require.NoError(t, store.AppendTranscript(sid, TranscriptEntry{
		ID: "e1", Type: EntryTypeToolCall, AgentID: "jim",
		ToolCalls: []ToolCall{
			{ID: "c1", Tool: "bash", Status: "success", Result: map[string]any{"text": "full one"}},
		},
	}))
	require.NoError(t, store.AppendTranscript(sid, TranscriptEntry{
		ID: "e2", Type: EntryTypeToolCall, AgentID: "jim",
		ToolCalls: []ToolCall{
			{ID: "c2", Tool: "read_file", Status: "error", Error: "boom"}, // Result nil: reason lives in Error
			{ID: "c3", Tool: "read_file", Status: "success", Result: map[string]any{"text": "full three"}},
		},
	}))

	prev, err := store.UpdateToolCallProjections(sid, []ToolCallProjectionUpdate{
		{ToolCallID: "c1", ContentState: "emptied", Result: map[string]any{"text": "[mark c1]"}},
		{ToolCallID: "c2", ContentState: "emptied", Result: map[string]any{"text": "[mark c2]"}},
		{ToolCallID: "missing", ContentState: "emptied", Result: map[string]any{"text": "never"}},
	})
	require.NoError(t, err)
	require.Len(t, prev, 2, "one previous-state row per record that was found; the unknown id is skipped")
	prevByID := map[ToolCallID]ToolCallProjectionUpdate{}
	for _, p := range prev {
		prevByID[p.ToolCallID] = p
	}
	assert.Equal(t, "", prevByID["c1"].ContentState)
	assert.Equal(t, "full one", prevByID["c1"].Result["text"])
	assert.Nil(t, prevByID["c2"].Result, "a failed call's previous Result is nil — must round-trip as nil")

	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "emptied", entries[0].ToolCalls[0].ContentState)
	assert.Equal(t, "[mark c1]", entries[0].ToolCalls[0].Result["text"])
	assert.Equal(t, "success", entries[0].ToolCalls[0].Status, "status untouched")
	assert.Equal(t, "emptied", entries[1].ToolCalls[0].ContentState)
	assert.Equal(t, "[mark c2]", entries[1].ToolCalls[0].Result["text"])
	assert.Equal(t, "boom", entries[1].ToolCalls[0].Error, "error text untouched")
	assert.Equal(t, "", entries[1].ToolCalls[1].ContentState, "sibling on the same entry untouched")
	assert.Equal(t, "full three", entries[1].ToolCalls[1].Result["text"])

	// Revert by feeding the previous rows back (restoreSession's path).
	_, err = store.UpdateToolCallProjections(sid, prev)
	require.NoError(t, err)
	entries, err = store.ReadTranscript(sid)
	require.NoError(t, err)
	assert.Equal(t, "", entries[0].ToolCalls[0].ContentState)
	assert.Equal(t, "full one", entries[0].ToolCalls[0].Result["text"])
	assert.Equal(t, "", entries[1].ToolCalls[0].ContentState)
	assert.Nil(t, entries[1].ToolCalls[0].Result, "nil Result CLEARS the field on this method")

	// Empty batch is a no-op, not an error.
	prev, err = store.UpdateToolCallProjections(sid, nil)
	require.NoError(t, err)
	assert.Nil(t, prev)
}

// --- SwitchAgent tests ---

// TestSwitchAgent_UpdatesActiveAgentID verifies that SwitchAgent updates the
// ActiveAgentID field and adds the new agent to AgentIDs.
//
// BDD: Given a session created with "agent-a",
// When SwitchAgent is called with "agent-b",
// Then ActiveAgentID == "agent-b" and AgentIDs contains "agent-b".
//
// Traces to: pkg/session/unified.go SwitchAgent
func TestSwitchAgent_UpdatesActiveAgentID(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	sessionID := meta.ID

	err = store.SwitchAgent(sessionID, "agent-b")
	require.NoError(t, err, "SwitchAgent must succeed")

	updated, err := store.GetMeta(sessionID)
	require.NoError(t, err)

	assert.Equal(t, "agent-b", updated.ActiveAgentID, "ActiveAgentID must be updated to agent-b")

	found := false
	for _, id := range updated.AgentIDs {
		if id == "agent-b" {
			found = true
			break
		}
	}
	assert.True(t, found, "AgentIDs must contain the new agent-b")
}

// TestSwitchAgent_SameAgent_ReturnsErrAlreadyActive verifies that switching to
// the already-active agent returns ErrAlreadyActive (idempotent guard).
//
// BDD: Given a session where ActiveAgentID == "agent-a",
// When SwitchAgent("agent-a") is called,
// Then ErrAlreadyActive is returned.
//
// Traces to: pkg/session/unified.go SwitchAgent — ErrAlreadyActive guard
func TestSwitchAgent_SameAgent_ReturnsErrAlreadyActive(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)

	err = store.SwitchAgent(meta.ID, "agent-a")

	assert.ErrorIs(t, err, ErrAlreadyActive,
		"switching to the already-active agent must return ErrAlreadyActive")
}

// TestSwitchAgent_NonExistentSession_ReturnsError verifies that SwitchAgent
// returns an error when the session does not exist.
//
// BDD: Given no session with ID "nonexistent-session",
// When SwitchAgent is called,
// Then an error is returned.
//
// Traces to: pkg/session/unified.go SwitchAgent — readMetaLocked error path
func TestSwitchAgent_NonExistentSession_ReturnsError(t *testing.T) {
	store := newTestStore(t)

	err := store.SwitchAgent("nonexistent-session-id-xyz", "agent-b")

	assert.Error(t, err, "SwitchAgent on a nonexistent session must return an error")
	// Must NOT be ErrAlreadyActive — this is a different error class.
	assert.NotErrorIs(t, err, ErrAlreadyActive,
		"error for nonexistent session must not be ErrAlreadyActive")
}

// TestSwitchAgent_AgentIDs_NoDuplicates verifies that switching back and forth
// between agents does not create duplicate entries in AgentIDs.
//
// BDD: Given a session with AgentIDs ["agent-a"],
// When SwitchAgent("agent-b") then SwitchAgent("agent-a") are called,
// Then AgentIDs contains exactly ["agent-a", "agent-b"] (no duplicates).
//
// Traces to: pkg/session/unified.go SwitchAgent — deduplication guard
func TestSwitchAgent_AgentIDs_NoDuplicates(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	sessionID := meta.ID

	// Switch a → b
	require.NoError(t, store.SwitchAgent(sessionID, "agent-b"))
	// Switch b → a (agent-a already in AgentIDs)
	require.NoError(t, store.SwitchAgent(sessionID, "agent-a"))

	updated, err := store.GetMeta(sessionID)
	require.NoError(t, err)

	// Count occurrences of each agent ID.
	counts := make(map[string]int)
	for _, id := range updated.AgentIDs {
		counts[id]++
	}
	if counts["agent-a"] != 1 {
		t.Errorf("agent-a appears %d times in AgentIDs, want exactly 1", counts["agent-a"])
	}
	if counts["agent-b"] != 1 {
		t.Errorf("agent-b appears %d times in AgentIDs, want exactly 1", counts["agent-b"])
	}
}

// --- MarkLastEntryTruncated tests ---

// TestMarkLastEntryTruncated_FlagsLastAssistantEntry verifies the core invariant of FR-14:
// after calling MarkLastEntryTruncated, the last assistant transcript entry has
// Truncated==true while all other fields are preserved unchanged.
//
// BDD: Given a session with one assistant transcript entry with a known turnID,
// When MarkLastEntryTruncated is called with that session's ID and turnID,
// Then ReadTranscript returns the entry with Truncated==true and all other fields intact.
//
// Traces to: pkg/session/unified.go MarkLastEntryTruncated (FR-14, H2)
func TestMarkLastEntryTruncated_FlagsLastAssistantEntry(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// Append an assistant entry with a known turn ID.
	entry := TranscriptEntry{
		ID:      "entry-001",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "Hello from the assistant",
		AgentID: "test-agent",
		TurnID:  "turn-001",
	}
	require.NoError(t, store.AppendTranscript(sessionID, entry))

	// Call MarkLastEntryTruncated with the turn ID.
	require.NoError(t, store.MarkLastEntryTruncated(sessionID, "turn-001", "cancelled"))

	// Read back and assert Truncated==true and other fields preserved.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1, "must have exactly one entry")

	got := entries[0]
	assert.True(t, got.Truncated, "Truncated must be true after MarkLastEntryTruncated")
	assert.Equal(t, "entry-001", got.ID, "ID must be preserved")
	assert.Equal(t, EntryTypeMessage, got.Type, "Type must be preserved")
	assert.Equal(t, "assistant", got.Role, "Role must be preserved")
	assert.Equal(t, "Hello from the assistant", got.Content, "Content must be preserved")
	assert.Equal(t, "test-agent", got.AgentID, "AgentID must be preserved")
}

// TestMarkLastEntryTruncated_TrailingNewlineSurvivesSubsequentAppend is the
// MarkLastEntryTruncated counterpart to
// TestUpdateToolCallStatus_TrailingNewlineSurvivesSubsequentAppend: this
// rewrite path shares the exact same "no trailing newline on last line" bug
// pattern and must be verified independently, since a fix to one function
// does not guarantee the sibling function (which duplicates the same
// rebuild-the-file logic) was fixed too.
//
// Negative-test discipline: this test was confirmed to FAIL against the
// pre-fix MarkLastEntryTruncated before the fix was applied.
func TestMarkLastEntryTruncated_TrailingNewlineSurvivesSubsequentAppend(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "entry-001",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "Partial response before cancel",
		AgentID: "test-agent",
		TurnID:  "turn-001",
	}))

	require.NoError(t, store.MarkLastEntryTruncated(sessionID, "turn-001", "cancelled"))

	transcriptPath := filepath.Join(store.baseDir, sessionID, "transcript.jsonl")
	raw, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	require.True(t, len(raw) > 0 && raw[len(raw)-1] == '\n',
		"MarkLastEntryTruncated's rewritten transcript file must end with a trailing "+
			"newline; got %q", raw)

	// A follow-up turn appends a new assistant entry after the cancel.
	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "entry-002",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "New turn after the cancel",
		AgentID: "test-agent",
		TurnID:  "turn-002",
	}))

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 2,
		"both the truncated entry AND the subsequently appended assistant entry must "+
			"survive — neither may be silently dropped by a newline-corrupted line")
	assert.True(t, entries[0].Truncated)
	assert.Equal(t, "New turn after the cancel", entries[1].Content)
}

// TestMarkLastEntryTruncated_NoAssistantEntryIsNoOp verifies that calling
// MarkLastEntryTruncated on a session with no assistant entries (only user
// entries or an empty transcript) is a no-op — nil error, no file mutation.
//
// BDD: Given a session with only user transcript entries,
// When MarkLastEntryTruncated is called,
// Then nil is returned and entries are unchanged (Truncated remains false).
//
// Traces to: pkg/session/unified.go MarkLastEntryTruncated — no-assistant-entry path (FR-14)
func TestMarkLastEntryTruncated_NoAssistantEntryIsNoOp(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// Append a user entry (no assistant entries).
	userEntry := TranscriptEntry{
		ID:      "user-001",
		Type:    EntryTypeMessage,
		Role:    "user",
		Content: "A user message",
		AgentID: "test-agent",
	}
	require.NoError(t, store.AppendTranscript(sessionID, userEntry))

	// MarkLastEntryTruncated must return nil.
	require.NoError(t, store.MarkLastEntryTruncated(sessionID, "", "cancelled"))

	// Entries must be unchanged.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.False(t, entries[0].Truncated, "user entry must not have Truncated set")

	// Also verify the empty-transcript case (fresh session with no appended entries).
	metaEmpty, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	require.NoError(t, store.MarkLastEntryTruncated(metaEmpty.ID, "", "cancelled"),
		"MarkLastEntryTruncated on empty transcript must be a no-op")
}

// TestMarkLastEntryTruncated_DoesNotTouchContextStore verifies the FR-14a invariant:
// MarkLastEntryTruncated only mutates transcript.jsonl; context.jsonl is never
// touched. This is T9's key invariant from the cancel spec.
//
// BDD: Given a session whose context.jsonl contains an assistant message,
// When MarkLastEntryTruncated is called,
// Then transcript.jsonl's last assistant entry has Truncated==true,
// AND context.jsonl is byte-for-byte identical to before the call.
//
// Traces to: pkg/session/unified.go MarkLastEntryTruncated (FR-14a / T9)
func TestMarkLastEntryTruncated_DoesNotTouchContextStore(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// Write an assistant entry to transcript.jsonl via AppendTranscript.
	transcriptEntry := TranscriptEntry{
		ID:      "transcript-001",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "assistant partial content",
		AgentID: "test-agent",
	}
	require.NoError(t, store.AppendTranscript(sessionID, transcriptEntry))

	// Write a message to context.jsonl via the SessionStore interface.
	// AddMessage appends role/content to context.jsonl through the JSONL backend.
	store.AddMessage(sessionID, "assistant", "context store assistant content")

	// Snapshot context.jsonl before the call.
	contextPath := filepath.Join(store.BaseDir(), ".context", sessionID+".jsonl")
	contextBefore, readErr := os.ReadFile(contextPath)
	require.NoError(t, readErr, "context.jsonl must exist after AddMessage")

	// Call MarkLastEntryTruncated (empty turnID = backward-compat path).
	require.NoError(t, store.MarkLastEntryTruncated(sessionID, "", "cancelled"))

	// Assert transcript.jsonl has Truncated==true on the assistant entry.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, entries[0].Truncated, "transcript.jsonl assistant entry must have Truncated==true")

	// Assert context.jsonl is byte-for-byte unchanged.
	contextAfter, readErr := os.ReadFile(contextPath)
	require.NoError(t, readErr, "context.jsonl must still be readable after MarkLastEntryTruncated")
	assert.Equal(t, string(contextBefore), string(contextAfter),
		"context.jsonl must not be mutated by MarkLastEntryTruncated (FR-14a / T9)")
}

// TestMarkLastEntryTruncated_DoesNotMutatePreviousTurnEntry verifies the H2 invariant:
// MarkLastEntryTruncated with a specific turnID must only flag entries belonging
// to that turn and must NOT touch assistant entries from other turns.
//
// BDD: Given a session with two assistant entries with different turnIDs (T1 and T2),
// When MarkLastEntryTruncated is called with turnID="T1",
// Then only the T1 entry has Truncated==true; the T2 entry is unchanged.
//
// Traces to: pkg/session/unified.go MarkLastEntryTruncated (H2 / turn-scoped truncation)
func TestMarkLastEntryTruncated_DoesNotMutatePreviousTurnEntry(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sid := meta.ID

	// Write assistant entry for turn T1.
	require.NoError(t, store.AppendTranscript(sid, TranscriptEntry{
		ID:      "asst-T1",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "Response from turn T1",
		AgentID: "test-agent",
		TurnID:  "T1",
	}))
	// Write assistant entry for turn T2 (the "current" turn at cancel time).
	require.NoError(t, store.AppendTranscript(sid, TranscriptEntry{
		ID:      "asst-T2",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "Partial response from turn T2",
		AgentID: "test-agent",
		TurnID:  "T2",
	}))

	// Cancel arrives for T1 only (e.g., a delayed cancel for a previous turn).
	require.NoError(t, store.MarkLastEntryTruncated(sid, "T1", "cancelled"))

	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Find by TurnID.
	var t1, t2 *TranscriptEntry
	for i := range entries {
		switch entries[i].TurnID {
		case "T1":
			t1 = &entries[i]
		case "T2":
			t2 = &entries[i]
		}
	}
	require.NotNil(t, t1, "T1 entry must exist")
	require.NotNil(t, t2, "T2 entry must exist")
	assert.True(t, t1.Truncated, "T1 entry must be marked truncated")
	assert.False(t, t2.Truncated, "T2 entry must NOT be marked truncated by a T1 cancel")
}

// TestMarkLastEntryTruncated_PersistsReason verifies ADR-087 D2: the reason
// argument is written to disk as truncation_reason on the rewritten entry.
//
// BDD: Given an assistant entry for turn "turn-001",
// When MarkLastEntryTruncated(sessionID, "turn-001", "max_output_tokens") is called,
// Then the rewritten JSONL line contains "truncation_reason":"max_output_tokens"
// AND ReadTranscript's TruncationReason field reflects the same value.
//
// Traces to: pkg/session/unified.go MarkLastEntryTruncated (ADR-087 D2)
func TestMarkLastEntryTruncated_PersistsReason(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "entry-001",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "Truncated by the output cap",
		AgentID: "test-agent",
		TurnID:  "turn-001",
	}))

	require.NoError(t, store.MarkLastEntryTruncated(sessionID, "turn-001", "max_output_tokens"))

	transcriptPath := filepath.Join(store.baseDir, sessionID, "transcript.jsonl")
	raw, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"truncation_reason":"max_output_tokens"`,
		"rewritten JSONL line must carry the persisted reason")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, entries[0].Truncated)
	assert.Equal(t, "max_output_tokens", entries[0].TruncationReason)
}

// TestMarkLastEntryTruncated_RejectsUnknownReason verifies MarkLastEntryTruncated
// fails loud on a reason outside the ADR-087 D2 enum, and never writes it —
// the on-disk entry is left byte-identical to before the rejected call.
//
// BDD: Given an assistant entry for turn "turn-001",
// When MarkLastEntryTruncated(sessionID, "turn-001", "bogus") is called,
// Then an error is returned AND the transcript file is unchanged.
//
// Traces to: pkg/session/unified.go MarkLastEntryTruncated (ADR-087 D2)
func TestMarkLastEntryTruncated_RejectsUnknownReason(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "entry-001",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "Untouched content",
		AgentID: "test-agent",
		TurnID:  "turn-001",
	}))

	transcriptPath := filepath.Join(store.baseDir, sessionID, "transcript.jsonl")
	before, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)

	err = store.MarkLastEntryTruncated(sessionID, "turn-001", "bogus")
	require.Error(t, err, "an unrecognized reason must be rejected")

	after, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"a rejected reason must leave the transcript file byte-identical")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.False(t, entries[0].Truncated, "entry must not be flagged truncated on a rejected reason")
	assert.Empty(t, entries[0].TruncationReason)
}

// --- UpdateToolCallStatus tests (Wave 3 fix 5b) ---

// TestUpdateToolCallStatus_RewritesMatchingToolCall verifies the core invariant
// of fix 5b: after calling UpdateToolCallStatus with a ToolCall.ID that exists in
// the transcript, ReadTranscript returns that ToolCall with the new Status and
// DurationMS while every other field (Tool, Parameters, Result, ...) is preserved.
//
// This simulates the ASYNC delegation scenario: the spawning "delegate" tool call
// is first persisted with a placeholder ack (Status="success", DurationMS=0, per
// tools.AsyncResult), then corrected once the real sub-turn finishes.
//
// BDD: Given a transcript entry carrying a ToolCall with ID "c1" and a placeholder
//
//	Status/DurationMS,
//	When UpdateToolCallStatus is called for "c1" with the real status/duration,
//	Then ReadTranscript returns "c1" with the updated Status/DurationMS and all
//	other fields unchanged.
//
// Traces to: pkg/session/unified.go UpdateToolCallStatus (Wave 3 fix 5b)
func TestUpdateToolCallStatus_RewritesMatchingToolCall(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// Simulate the async delegation "ack" record loop.go's standard tool-completion
	// path writes immediately after DelegateTool.executeAsync returns AsyncResult.
	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "c1",
		Type:    EntryTypeToolCall,
		AgentID: "jim",
		ToolCalls: []ToolCall{
			{
				ID:         "c1",
				Tool:       "delegate",
				Status:     "success",
				DurationMS: 0,
				Parameters: map[string]any{"task": "audit go files"},
			},
		},
	}))

	// The sub-turn actually finishes later with a real status/duration.
	found, updateErr := store.UpdateToolCallStatus(sessionID, "c1", "success", 4210)
	require.NoError(t, updateErr)
	assert.True(t, found, "the matching tool-call entry must be found")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1, "must have exactly one entry")

	require.Len(t, entries[0].ToolCalls, 1)
	got := entries[0].ToolCalls[0]
	assert.Equal(t, ToolCallID("c1"), got.ID, "ID must be preserved")
	assert.Equal(t, "delegate", got.Tool, "Tool must be preserved")
	assert.Equal(t, "success", got.Status, "Status must be updated to the real terminal status")
	assert.EqualValues(t, 4210, got.DurationMS, "DurationMS must be updated to the real wall-clock duration")
	assert.Equal(t, "audit go files", got.Parameters["task"], "Parameters must be preserved")
}

// TestUpdateToolCallStatus_FlipsToError verifies UpdateToolCallStatus can move a
// ToolCall from a placeholder "success" to a real "error" status — the mirror
// case of the success-preserved test, proving the function does not hardcode a
// bias toward either terminal value.
func TestUpdateToolCallStatus_FlipsToError(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "c2",
		Type:    EntryTypeToolCall,
		AgentID: "jim",
		ToolCalls: []ToolCall{
			{ID: "c2", Tool: "delegate", Status: "success", DurationMS: 0},
		},
	}))

	found, updateErr := store.UpdateToolCallStatus(sessionID, "c2", "error", 987)
	require.NoError(t, updateErr)
	assert.True(t, found, "the matching tool-call entry must be found")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Len(t, entries[0].ToolCalls, 1)
	got := entries[0].ToolCalls[0]
	assert.Equal(t, "error", got.Status)
	assert.EqualValues(t, 987, got.DurationMS)
}

// TestUpdateToolCallStatus_NoMatchIsNoOp verifies that calling UpdateToolCallStatus
// with a ToolCall.ID that does not exist in the transcript is a no-op: nil error,
// and every existing entry is byte-for-byte unchanged.
//
// This is the expected outcome for SYNCHRONOUS delegation (DelegateTool.
// executeSync): spawnSubTurn blocks until the child finishes, so at the moment
// EventKindSubTurnEnd fires the spawning tool call's own record has not been
// appended to the transcript yet.
//
// Traces to: pkg/session/unified.go UpdateToolCallStatus doc comment (Wave 3 fix 5b)
func TestUpdateToolCallStatus_NoMatchIsNoOp(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "c3",
		Type:    EntryTypeToolCall,
		AgentID: "jim",
		ToolCalls: []ToolCall{
			{ID: "c3", Tool: "read_file", Status: "success", DurationMS: 12},
		},
	}))

	found, err := store.UpdateToolCallStatus(sessionID, "does-not-exist", "success", 999)
	require.NoError(t, err, "no matching ToolCall.ID must be a no-op, not an error")
	assert.False(t, found, "no matching ToolCall.ID must report found=false")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Len(t, entries[0].ToolCalls, 1)
	got := entries[0].ToolCalls[0]
	assert.Equal(t, "success", got.Status, "unrelated entry must be unchanged")
	assert.EqualValues(t, 12, got.DurationMS, "unrelated entry must be unchanged")
}

// TestUpdateToolCallStatus_EmptyTranscriptIsNoOp verifies that calling
// UpdateToolCallStatus on a session with no transcript file yet is a no-op
// (nil error), mirroring MarkLastEntryTruncated's os.IsNotExist handling.
func TestUpdateToolCallStatus_EmptyTranscriptIsNoOp(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	found, err := store.UpdateToolCallStatus(sessionID, "c1", "error", 100)
	assert.NoError(t, err, "no transcript file yet must be a no-op, not an error")
	assert.False(t, found, "no transcript file yet must report found=false")
}

// TestUpdateToolCallStatus_TrailingNewlineSurvivesSubsequentAppend is the
// dedicated regression test for the real-world data-loss bug found by live
// verification: UpdateToolCallStatus's read-mutate-rewrite previously
// omitted the trailing newline on the rewritten file's last line. The VERY
// NEXT AppendTranscript call (exactly the sequence Wave 3 fix 5b's
// UpdateToolCallStatus call, immediately followed by fix 5d's
// AsyncNotifier-delivered result, produces in production) then concatenated
// its record directly onto that line, producing invalid JSON that
// ReadTranscript's line parser could not parse — silently dropping BOTH the
// rewritten entry AND the newly appended one.
//
// Unlike TestUpdateToolCallStatus_RewritesMatchingToolCall (which reads
// immediately after the rewrite and therefore could never observe this bug),
// this test performs the full rewrite-THEN-append-THEN-read sequence.
//
// Negative-test discipline: this test was confirmed to FAIL against the
// pre-fix UpdateToolCallStatus (which built the rewritten file without a
// trailing newline on the last line) before the fix was applied — see the
// delivery report for the byte-level repro output.
func TestUpdateToolCallStatus_TrailingNewlineSurvivesSubsequentAppend(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "jim")
	require.NoError(t, err)
	sessionID := meta.ID

	// 1. The spawning delegate tool call's placeholder ack.
	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "c1",
		Type:    EntryTypeToolCall,
		AgentID: "jim",
		ToolCalls: []ToolCall{
			{ID: "c1", Tool: "delegate", Status: "success", DurationMS: 0},
		},
	}))

	// 2. The sub-turn's real terminal status/duration corrects the placeholder
	// (fix 5b) — this is the rewrite that must terminate with a newline.
	found, updateErr := store.UpdateToolCallStatus(sessionID, "c1", "success", 4210)
	require.NoError(t, updateErr)
	require.True(t, found)

	// Assert the raw file bytes on disk actually end in a newline — the
	// precise mechanism, not just the end-to-end symptom.
	transcriptPath := filepath.Join(store.baseDir, sessionID, "transcript.jsonl")
	raw, err := os.ReadFile(transcriptPath)
	require.NoError(t, err)
	require.True(t, len(raw) > 0 && raw[len(raw)-1] == '\n',
		"UpdateToolCallStatus's rewritten transcript file must end with a trailing "+
			"newline; got %q", raw)

	// 3. The delegate's async result lands as a new assistant entry (fix 5d) —
	// this is the append that, pre-fix, would corrupt onto the rewritten line.
	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "c2",
		Role:    "assistant",
		AgentID: "ray",
		Content: "Ray's delegated narration.",
	}))

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 2,
		"both the corrected tool-call entry AND the subsequently appended assistant "+
			"entry must survive — neither may be silently dropped by a newline-corrupted line")
	assert.Equal(t, "success", entries[0].ToolCalls[0].Status)
	assert.EqualValues(t, 4210, entries[0].ToolCalls[0].DurationMS)
	assert.Equal(t, "Ray's delegated narration.", entries[1].Content)
	assert.Equal(t, "ray", entries[1].AgentID)
}

// --- G2: External CLI sub-turn scope guard ---

// TestAppendTranscript_G2_ExternalCLITurn_ZeroTokens verifies that an assistant entry
// written for an external CLI sub-turn (Tokens=0, all cache fields=0) contributes
// exactly zero to all SessionStats token fields.
//
// This is a simulation test: the real production gate (asserting that external-cli
// dispatch never calls AddTurnStats) cannot be written as a narrow unit test without
// linking pkg/agent (OOM risk). This test instead verifies the behavioral invariant
// from the session side: a zero-token entry MUST NOT inflate any stats field.
//
// The production scope guard is: external CLI dispatch (pkg/agent/external_dispatch.go)
// calls AppendTranscript with Tokens=0 and NEVER calls AddTurnStats/AddTurnCacheStats.
// This test verifies the session-side consequence of that contract.
//
// NOTE: A full production-gate test asserting that external_dispatch.go does NOT call
// AddTurnStats is deferred — it would require linking pkg/agent which risks OOM in CI.
// See token-usage-tracking-2026-06.md §G2 deferral note.
//
// Traces to: token-usage-tracking-2026-06.md §Wave1 item 3 (scope guard G2)
func TestAppendTranscript_G2_ExternalCLITurn_ZeroTokens(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "native-agent")
	require.NoError(t, err)

	// External CLI sub-turn: produced text, but no native engine was used.
	// Tokens=0 because AddTurnStats is never called for this path.
	extEntry := TranscriptEntry{
		ID:               "ext-g2-001",
		Role:             "assistant",
		Content:          "Result from external claude-code CLI",
		Model:            "claude-code",
		Tokens:           0,
		CacheReadTokens:  0,
		CacheWriteTokens: 0,
		Timestamp:        time.Now().UTC(),
	}
	require.NoError(t, store.AppendTranscript(meta.ID, extEntry))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 0, got.Stats.TokensTotal,
		"G2: external CLI turn must contribute zero to TokensTotal")
	assert.Equal(t, 0, got.Stats.TokensOut,
		"G2: external CLI turn must contribute zero to TokensOut")
	assert.Equal(t, 0, got.Stats.TokensCacheRead,
		"G2: external CLI turn must contribute zero to TokensCacheRead")
	assert.Equal(t, 0, got.Stats.TokensCacheWrite,
		"G2: external CLI turn must contribute zero to TokensCacheWrite")

	// Even if ByModel is populated (zero-valued entry), the Total must be 0.
	if got.Stats.ByModel != nil {
		if mt, ok := got.Stats.ByModel["claude-code"]; ok {
			assert.Equal(t, 0, mt.Total,
				"G2: claude-code ByModel entry must have Total=0 for zero-token external turn")
		}
	}
}
