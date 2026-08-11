// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// denialTableFixture is every reason ClassifyDenial must classify as KNOWN,
// paired with its expected Permanent value. ADR-058 spec §4.1 specified ten
// rows; a post-epic-review pass found two more real, driveable reasons the
// spec table missed — the headless auto-deny literal
// (agent.autoDenyHeadlessReason) and "session canceled" (distinct from the
// single-word "cancel" row, reachable via
// pkg/agent/cancel.go::AgentLoop.RequestCancel ->
// pkg/gateway/approvals.go::cancelAllPendingForSessions) — bringing the
// table to twelve rows. Declared once so every test below iterates the same
// canonical set rather than re-typing the enumeration and risking drift
// between tests.
var denialTableFixture = []struct {
	reason    string
	permanent bool
}{
	{"user", true},
	{"timeout", true},
	{"saturated", false},
	{"cancel", true},
	{"restart", true},
	{"batch_short_circuit", true},
	{"internal_error", true},
	{"no_approver_configured", true},
	{"policy_denied", true},
	{"", true},
	{autoDenyHeadlessReason, true},
	{"session canceled", true},
}

// TestClassifyDenial_TableHasExactlyTwelveRows guards the table's own size
// directly — a table-driven test that only checks the rows it already
// expects would not notice an extra or missing row. Originally asserted ten
// rows (ADR-058 spec §4.1); a post-epic-review pass added two real,
// driveable reasons the spec table missed (see denialTableFixture's doc),
// bringing the count to twelve.
func TestClassifyDenial_TableHasExactlyTwelveRows(t *testing.T) {
	if got, want := len(denialTable), 12; got != want {
		t.Fatalf("denialTable has %d rows, want exactly %d", got, want)
	}
	if got, want := len(denialTableFixture), 12; got != want {
		t.Fatalf("test fixture has %d rows, want exactly %d — keep denialTableFixture in sync with denialTable", got, want)
	}
}

// TestClassifyDenial_TableDriven is BDD-02: every one of the ten table rows
// classifies as known, exactly one ("saturated") is permanent=false, and
// "approved" (not a denial — spec §2.2) classifies as known=false.
//
// Stub-resistance (spec §8.2 AC-01): a classifier returning {Permanent:true}
// for every reason would pass a test that only checks each row's Permanent
// value one at a time against an all-true expectation — this test instead
// counts how many rows come back permanent=false across the WHOLE table and
// requires that count to be exactly one, which an always-true stub fails
// outright.
func TestClassifyDenial_TableDriven(t *testing.T) {
	permanentFalseCount := 0
	for _, tc := range denialTableFixture {
		t.Run("reason="+tc.reason, func(t *testing.T) {
			cls, known := ClassifyDenial(tc.reason)
			if !known {
				t.Fatalf("ClassifyDenial(%q): known=false, want known=true (row is in the ten-row table)", tc.reason)
			}
			if cls.Permanent != tc.permanent {
				t.Fatalf("ClassifyDenial(%q): Permanent=%v, want %v", tc.reason, cls.Permanent, tc.permanent)
			}
			if cls.ModelMessage == "" {
				t.Fatalf("ClassifyDenial(%q): ModelMessage is empty", tc.reason)
			}
			if cls.TranscriptText == "" {
				t.Fatalf("ClassifyDenial(%q): TranscriptText is empty", tc.reason)
			}
		})
		if !tc.permanent {
			permanentFalseCount++
		}
	}
	if permanentFalseCount != 1 {
		t.Fatalf("expected exactly ONE retryable (permanent=false) reason in the table, found %d", permanentFalseCount)
	}

	// "approved" carries Approved:true in pkg/gateway/approvals.go — it is
	// not a denial at all (spec §2.2). A classifier asserting it "known"
	// would be asserting a lie (revision-1's X2 mistake).
	if _, known := ClassifyDenial("approved"); known {
		t.Fatalf(`ClassifyDenial("approved"): known=true, want known=false — "approved" is not a denial`)
	}
}

// TestClassifyDenial_UnknownReasonIsFailSafe is BDD-02's probe case and
// FR-058-03: a reason with no table row returns known=false, Permanent=true
// (fail safe — treated as terminal, never silently retryable), and its
// ModelMessage and TranscriptText are byte-identical, since there is no
// established "what a human sees" vs "what the model sees" distinction for
// a reason nobody has classified.
func TestClassifyDenial_UnknownReasonIsFailSafe(t *testing.T) {
	const probe = "__unclassified_probe__"
	cls, known := ClassifyDenial(probe)
	if known {
		t.Fatalf("ClassifyDenial(%q): known=true, want known=false", probe)
	}
	if !cls.Permanent {
		t.Fatalf("ClassifyDenial(%q): Permanent=false, want true (unknown reasons fail safe to terminal)", probe)
	}
	if cls.ModelMessage != cls.TranscriptText {
		t.Fatalf("ClassifyDenial(%q): ModelMessage (%q) != TranscriptText (%q), want byte-identical for an unknown reason",
			probe, cls.ModelMessage, cls.TranscriptText)
	}
	if !strings.Contains(cls.ModelMessage, probe) {
		t.Fatalf("ClassifyDenial(%q): message %q does not name the unrecognised reason", probe, cls.ModelMessage)
	}
}

// TestDenialUserMarker_OnlyReasonUserContainsIt is FR-058-06's positive half:
// the lower-cased substring "user denied" must appear in exactly the "user"
// row's ModelMessage, and nowhere else — not in any other known row's
// ModelMessage or TranscriptText, and not in the unknown-reason fallback.
func TestDenialUserMarker_OnlyReasonUserContainsIt(t *testing.T) {
	for _, tc := range denialTableFixture {
		cls, _ := ClassifyDenial(tc.reason)
		modelHas := strings.Contains(strings.ToLower(cls.ModelMessage), denialUserMarker)
		transcriptHas := strings.Contains(strings.ToLower(cls.TranscriptText), denialUserMarker)
		if tc.reason == "user" {
			if !modelHas {
				t.Fatalf(`reason "user": ModelMessage %q does not contain marker %q`, cls.ModelMessage, denialUserMarker)
			}
			continue
		}
		if modelHas {
			t.Fatalf("reason %q: ModelMessage %q unexpectedly contains marker %q", tc.reason, cls.ModelMessage, denialUserMarker)
		}
		if transcriptHas {
			t.Fatalf("reason %q: TranscriptText %q unexpectedly contains marker %q", tc.reason, cls.TranscriptText, denialUserMarker)
		}
	}

	// The unknown-reason fallback must not contain it either.
	cls, _ := ClassifyDenial("__unclassified_probe__")
	if strings.Contains(strings.ToLower(cls.ModelMessage), denialUserMarker) {
		t.Fatalf("unknown-reason message unexpectedly contains marker %q: %q", denialUserMarker, cls.ModelMessage)
	}

	if !strings.Contains(strings.ToLower(DenialMessageUser), denialUserMarker) {
		t.Fatalf("DenialMessageUser %q does not contain its own marker %q", DenialMessageUser, denialUserMarker)
	}
}

// TestDenialPayloadJSON_ShapeAndFields exercises denialPayloadJSON
// (FR-058-05) directly: the emitted string must be valid JSON carrying
// exactly the five documented fields with the classified values, for both a
// permanent and a retryable reason.
func TestDenialPayloadJSON_ShapeAndFields(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		reason string
	}{
		{"permanent", "bash", "timeout"},
		{"retryable", "web_fetch", "saturated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cls, known := ClassifyDenial(tc.reason)
			if !known {
				t.Fatalf("test setup: reason %q must be a known row", tc.reason)
			}
			raw := denialPayloadJSON(tc.tool, tc.reason, cls)

			var decoded map[string]any
			if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
				t.Fatalf("denialPayloadJSON produced invalid JSON: %v\nraw: %s", err, raw)
			}
			if got := decoded["error"]; got != "permission_denied" {
				t.Fatalf(`decoded["error"] = %v, want "permission_denied"`, got)
			}
			if got := decoded["message"]; got != cls.ModelMessage {
				t.Fatalf("decoded[\"message\"] = %v, want %q", got, cls.ModelMessage)
			}
			if got := decoded["tool"]; got != tc.tool {
				t.Fatalf("decoded[\"tool\"] = %v, want %q", got, tc.tool)
			}
			if got := decoded["reason"]; got != tc.reason {
				t.Fatalf("decoded[\"reason\"] = %v, want %q", got, tc.reason)
			}
			if got := decoded["permanent"]; got != cls.Permanent {
				t.Fatalf("decoded[\"permanent\"] = %v, want %v", got, cls.Permanent)
			}
		})
	}
}

// TestDenialPayloadJSON_ControlCharacterToolNameIsValidJSON is the direct
// regression for issue #618: denialPayloadJSON used to build this string
// with fmt.Sprintf's %q verb, which is Go-string quoting, not JSON quoting.
// %q emits \xNN for a control byte outside \n\t\r, and \x is not a legal
// JSON escape, so json.Unmarshal used to fail here with "invalid character
// 'x' in string escape code". tool is reachable with adversarial content
// because it takes MCP-declared tool names, which are not statically
// enumerable (see denialPayloadJSON's doc comment).
func TestDenialPayloadJSON_ControlCharacterToolNameIsValidJSON(t *testing.T) {
	cls, known := ClassifyDenial("policy_denied")
	require.True(t, known, "test setup: policy_denied must be a known row")

	adversarialTools := []string{
		"mcp_tool_\x01bad",
		"mcp_tool_\x7f",
		"mcp_tool_\xff", // invalid UTF-8
		"mcp_tool_\"quoted\"",
		"mcp_tool_\\backslash",
	}
	for _, tool := range adversarialTools {
		raw := denialPayloadJSON(tool, "policy_denied", cls)
		var decoded map[string]any
		err := json.Unmarshal([]byte(raw), &decoded)
		require.NoErrorf(t, err, "denialPayloadJSON produced invalid JSON for adversarial tool name %q: %s", tool, raw)
		require.Equal(t, "permission_denied", decoded["error"])
	}
}

// TestToolDenialAbortReason_PinnedFormat asserts the exact abort-reason
// format pinned by spec §3.8/§9 — every downstream consumer (W4's
// abortTurn call, W5's assertions on Task.Result) depends on this literal
// format string never drifting.
func TestToolDenialAbortReason_PinnedFormat(t *testing.T) {
	got := toolDenialAbortReason("bash", "timeout", "mia", turnDenialBudget)
	want := `blocked: tool "bash" denied (timeout) for agent "mia" — the turn reached its limit of 10 tool denials`
	if got != want {
		t.Fatalf("toolDenialAbortReason mismatch:\n got:  %s\n want: %s", got, want)
	}
	for _, substr := range []string{"bash", "timeout", "mia", "10"} {
		if !strings.Contains(got, substr) {
			t.Fatalf("abort reason %q does not name %q", got, substr)
		}
	}
}

// --- Ledger tests (FR-058-09/10/11/12) ---

// newLedgerTestTurnState builds a real, freshly-constructed turnState via
// newTurnState — the same constructor runTurn uses for every real turn — so
// the ledger tests below exercise the actual per-turn lifecycle rather than
// a bare turnDenialLedger{} literal that could never prove per-turn
// isolation.
func newLedgerTestTurnState(t *testing.T, turnID string) *turnState {
	t.Helper()
	agent := &AgentInstance{ID: "mia"}
	ts := newTurnState(agent, processOptions{SessionKey: "sess-" + turnID}, turnEventScope{turnID: turnID})
	if ts == nil {
		t.Fatalf("newTurnState returned nil")
	}
	return ts
}

// TestTurnDenialLedger_FreshTurnStateIsEmpty is BDD-10's precondition: a
// brand new turnState has denied nothing — used is 0 and the quarantine map
// has no entries for any tool.
func TestTurnDenialLedger_FreshTurnStateIsEmpty(t *testing.T) {
	ts := newLedgerTestTurnState(t, "t-fresh")
	if ts.denialLedger.used != 0 {
		t.Fatalf("fresh turnState.denialLedger.used = %d, want 0", ts.denialLedger.used)
	}
	if len(ts.denialLedger.quarantined) != 0 {
		t.Fatalf("fresh turnState.denialLedger.quarantined has %d entries, want 0", len(ts.denialLedger.quarantined))
	}
	if _, _, ok := ts.quarantinedDenialFor("bash"); ok {
		t.Fatalf("quarantinedDenialFor(bash) on a fresh turn returned ok=true, want false")
	}
}

// TestRecordToolDenial_PermanentQuarantinesOnFirstOccurrenceOnly is
// FR-058-10: a permanent denial quarantines its tool on the FIRST
// occurrence, and a second denial for the SAME tool does not overwrite the
// cached entry — the first permanent denial's payload/reason is what every
// replay serves for the rest of the turn.
func TestRecordToolDenial_PermanentQuarantinesOnFirstOccurrenceOnly(t *testing.T) {
	ts := newLedgerTestTurnState(t, "t-quarantine")

	used, exhausted := ts.recordToolDenial("bash", "timeout", true, "payload-1")
	require.Equal(t, 1, used)
	require.False(t, exhausted)

	payload, reason, ok := ts.quarantinedDenialFor("bash")
	require.True(t, ok, "bash must be quarantined after its first permanent denial")
	require.Equal(t, "payload-1", payload)
	require.Equal(t, "timeout", reason)

	// A second denial for the SAME tool must not overwrite the cached entry.
	used, exhausted = ts.recordToolDenial("bash", "cancel", true, "payload-2")
	require.Equal(t, 2, used, "aggregate budget still increments on a second real denial")
	require.False(t, exhausted)

	payload, reason, ok = ts.quarantinedDenialFor("bash")
	require.True(t, ok)
	require.Equal(t, "payload-1", payload, "quarantine payload must not be overwritten by a later denial")
	require.Equal(t, "timeout", reason, "quarantine reason must not be overwritten by a later denial")

	// An unrelated tool is unaffected — quarantine is keyed by tool alone,
	// not globally.
	if _, _, ok := ts.quarantinedDenialFor("web_fetch"); ok {
		t.Fatalf("web_fetch must not be quarantined by bash's denial")
	}
}

// TestBindingRule4_SaturatedDoesNotQuarantineAndPermitsRetry is the
// mandatory positive lower bound (ADR-058 §3.5 / spec Binding Rule 4).
// Without this test, an implementation that quarantined on EVERY reason —
// or classified everything permanent — would satisfy every other test in
// this file. It asserts, at the ledger's own level:
//  1. ClassifyDenial("saturated") is the one retryable reason.
//  2. Recording a saturated denial (Permanent=false, exactly as
//     ClassifyDenial reports it) does NOT create a quarantine entry.
//  3. The tool remains un-quarantined afterward, and a later real permanent
//     denial for a DIFFERENT tool still works normally — proving the
//     saturated call left no stray quarantine state anywhere.
//
// The full end-to-end proof that a retry after the queue drains actually
// EXECUTES belongs to pkg/gateway/tool_denial_saturated_retry_test.go
// (spec §2.5's import-direction constraint: pkg/agent cannot construct the
// real PolicyApprover that proof needs). This test proves the necessary
// ledger-level precondition for that: nothing here would block the retry.
func TestBindingRule4_SaturatedDoesNotQuarantineAndPermitsRetry(t *testing.T) {
	cls, known := ClassifyDenial("saturated")
	require.True(t, known)
	require.False(t, cls.Permanent, `"saturated" must be the one retryable reason`)

	ts := newLedgerTestTurnState(t, "t-saturated")

	used, exhausted := ts.recordToolDenial(
		"web_fetch", "saturated", cls.Permanent, denialPayloadJSON("web_fetch", "saturated", cls))
	require.Equal(t, 1, used)
	require.False(t, exhausted)

	if _, _, ok := ts.quarantinedDenialFor("web_fetch"); ok {
		t.Fatalf("web_fetch was quarantined after a SATURATED denial — saturated must never quarantine (Binding Rule 4)")
	}

	// A second, real permanent denial for a DIFFERENT tool must still work
	// normally, proving the saturated call left no stray state behind.
	permCls, _ := ClassifyDenial("timeout")
	used, exhausted = ts.recordToolDenial("bash", "timeout", permCls.Permanent, "bash-payload")
	require.Equal(t, 2, used)
	require.False(t, exhausted)
	if _, _, ok := ts.quarantinedDenialFor("bash"); !ok {
		t.Fatalf("bash must be quarantined after its own permanent denial")
	}
	if _, _, ok := ts.quarantinedDenialFor("web_fetch"); ok {
		t.Fatalf("web_fetch must still not be quarantined — it was never permanently denied")
	}
}

// TestRecordToolDenial_BudgetFiresAt10NotAt9 is FR-058-12/13's arithmetic:
// the aggregate budget covers a HETEROGENEOUS storm (BDD-06) — ten DISTINCT
// tools, each denied exactly once — and exhausts starting exactly at the
// 10th denial, never at the 9th. A per-(tool,reason) implementation would
// never exhaust here at all (each pair is denied only once), so this also
// guards against that key-shape mistake (spec X7/N4).
func TestRecordToolDenial_BudgetFiresAt10NotAt9(t *testing.T) {
	ts := newLedgerTestTurnState(t, "t-budget")

	for i := 1; i <= turnDenialBudget+2; i++ {
		tool := fmt.Sprintf("tool-%d", i)
		used, exhausted := ts.recordToolDenial(tool, "timeout", true, "payload")
		if used != i {
			t.Fatalf("denial %d: used = %d, want %d", i, used, i)
		}
		wantExhausted := i >= turnDenialBudget
		if exhausted != wantExhausted {
			t.Fatalf("denial %d (used=%d, budget=%d): exhausted = %v, want %v", i, used, turnDenialBudget, exhausted, wantExhausted)
		}
	}

	if ts.denialLedger.used != turnDenialBudget+2 {
		t.Fatalf("final ledger.used = %d, want %d", ts.denialLedger.used, turnDenialBudget+2)
	}
	if got, want := len(ts.denialLedger.quarantined), turnDenialBudget+2; got != want {
		t.Fatalf("expected all %d distinct tools quarantined, got %d entries", want, got)
	}
}

// TestRecordQuarantineReplay_ConsumesBudgetWithoutTouchingQuarantine is
// FR-058-12's "counting quarantine replays is deliberate" clause and
// BDD-05's arithmetic: call 1 opens the real denial (recordToolDenial);
// calls 2..10 are cache replays (recordQuarantineReplay) that still consume
// the aggregate budget but must never mutate the cached quarantine entry.
func TestRecordQuarantineReplay_ConsumesBudgetWithoutTouchingQuarantine(t *testing.T) {
	ts := newLedgerTestTurnState(t, "t-replay")

	used, exhausted := ts.recordToolDenial("bash", "timeout", true, "original-payload")
	require.Equal(t, 1, used)
	require.False(t, exhausted)

	for i := 2; i <= turnDenialBudget; i++ {
		used, exhausted = ts.recordQuarantineReplay("bash")
		if used != i {
			t.Fatalf("replay %d: used = %d, want %d", i, used, i)
		}
		wantExhausted := i >= turnDenialBudget
		if exhausted != wantExhausted {
			t.Fatalf("replay %d: exhausted = %v, want %v", i, exhausted, wantExhausted)
		}
	}

	payload, reason, ok := ts.quarantinedDenialFor("bash")
	require.True(t, ok)
	require.Equal(t, "original-payload", payload, "replays must never mutate the cached quarantine payload")
	require.Equal(t, "timeout", reason)

	if got := len(ts.denialLedger.quarantined); got != 1 {
		t.Fatalf("expected exactly ONE quarantined tool (bash) after %d replays of it, got %d entries", turnDenialBudget-1, got)
	}
}

// TestTurnDenialLedger_PerTurnIsolation is BDD-10: counters and quarantine
// are per-turn only. A turn that denied one tool 9 times must not affect a
// second, independently-constructed turnState — whether that second
// turnState represents a new turn in the same session or an unrelated,
// concurrently-running session on the same AgentLoop. newTurnState gives
// every turn its own turnDenialLedger value with no shared backing store, so
// this test constructs three real turnStates and proves none of their
// ledgers is visible from another.
func TestTurnDenialLedger_PerTurnIsolation(t *testing.T) {
	ts1 := newLedgerTestTurnState(t, "t-iso-1")
	for i := 0; i < turnDenialBudget-1; i++ { // 9 denials — one short of the budget
		used, exhausted := ts1.recordToolDenial("bash", "timeout", true, "p")
		if exhausted {
			t.Fatalf("ts1 exhausted after only %d denials (budget=%d)", used, turnDenialBudget)
		}
	}
	if ts1.denialLedger.used != turnDenialBudget-1 {
		t.Fatalf("ts1.denialLedger.used = %d, want %d", ts1.denialLedger.used, turnDenialBudget-1)
	}

	// A brand-new turn starts completely fresh.
	ts2 := newLedgerTestTurnState(t, "t-iso-2")
	if ts2.denialLedger.used != 0 {
		t.Fatalf("ts2 (new turn) starts with used = %d, want 0 — ts1's count leaked", ts2.denialLedger.used)
	}
	if _, _, ok := ts2.quarantinedDenialFor("bash"); ok {
		t.Fatalf("ts2 (new turn) already has bash quarantined — ts1's quarantine leaked")
	}
	// 9 further denials on the fresh turn must not abort it (none is the
	// 10th of ITS OWN turn).
	for i := 0; i < turnDenialBudget-1; i++ {
		_, exhausted := ts2.recordToolDenial("run_task", "timeout", true, "p")
		if exhausted {
			t.Fatalf("ts2 exhausted after only %d denials of its own", i+1)
		}
	}

	// An unrelated, concurrently-running "session" is just a third
	// turnState — its ledger is unaffected by either of the above.
	ts3 := newLedgerTestTurnState(t, "t-iso-3")
	if ts3.denialLedger.used != 0 || len(ts3.denialLedger.quarantined) != 0 {
		t.Fatalf("ts3 (unrelated session) is not empty: used=%d quarantined=%d", ts3.denialLedger.used, len(ts3.denialLedger.quarantined))
	}
}

// TestTurnDenialLedger_ConcurrentMutatorsRace proves the ledger's mutators
// actually take ts.mu.Lock() and not RLock() (spec N3 — ts.mu is a
// sync.RWMutex). If either recordToolDenial or recordQuarantineReplay used
// RLock() instead of Lock(), concurrent goroutines would both hold the
// "read" lock while mutating the same map/int — a genuine data race that
// `go test -race` catches even though RWMutex's own bookkeeping would not
// panic. Run with -race (see this package's mandated test invocation).
func TestTurnDenialLedger_ConcurrentMutatorsRace(t *testing.T) {
	ts := newLedgerTestTurnState(t, "t-race")

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			tool := fmt.Sprintf("race-tool-%d", i)
			ts.recordToolDenial(tool, "timeout", true, "payload")
		}()
		go func() {
			defer wg.Done()
			// Concurrent reads racing the mutation above — the race
			// detector's job is to prove this never races with the
			// concurrent Lock()-held mutation, not to assert a particular
			// interleaving outcome.
			tool := fmt.Sprintf("race-tool-%d", i)
			ts.quarantinedDenialFor(tool)
		}()
	}
	wg.Wait()

	if ts.denialLedger.used != goroutines {
		t.Fatalf("after %d concurrent denials, ledger.used = %d, want %d", goroutines, ts.denialLedger.used, goroutines)
	}
	if got := len(ts.denialLedger.quarantined); got != goroutines {
		t.Fatalf("expected %d distinct tools quarantined, got %d", goroutines, got)
	}
}
