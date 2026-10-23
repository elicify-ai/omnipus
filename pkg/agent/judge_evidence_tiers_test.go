// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_evidence_tiers_test.go covers ADR-084 revision 9's §Q, D14 tier
// system at the unit level: FR-105/FR-106's tier-1 tool-call rendering
// (formatTierOneToolCall, renderTranscriptEntriesForWindow's 32 KiB
// drop-oldest bound), and FR-108's contradiction veto
// (applyBehaviorContradictionVeto). mkToolCall/mkEntry/baseTime are
// package-level fixtures from behavior_scan_test.go; intPtr is from
// delegation_enforce_test.go — all in this SAME package, reused here
// rather than redefined.

package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- FR-105/FR-106: formatTierOneToolCall -----------------------------------

func TestFormatTierOneToolCall_RendersParametersStatusAndError(t *testing.T) {
	tc := session.ToolCall{
		Tool: "send_email", Status: "success",
		Parameters: map[string]any{"to": "supplier@example.com"},
	}
	got := formatTierOneToolCall(tc, nil)
	if !strings.Contains(got, "send_email") || !strings.Contains(got, "success") {
		t.Fatalf("missing tool/status: %q", got)
	}
	if !strings.Contains(got, "supplier@example.com") {
		t.Errorf("JUDGE-FR-105: parameters must be rendered, got %q", got)
	}
}

func TestFormatTierOneToolCall_RendersError(t *testing.T) {
	tc := session.ToolCall{Tool: "bash", Status: "error", Error: "exit code 1: command not found"}
	got := formatTierOneToolCall(tc, nil)
	if !strings.Contains(got, "exit code 1") {
		t.Errorf("JUDGE-FR-105: error must be rendered, got %q", got)
	}
}

// TestFormatTierOneToolCall_RendersResultPayloadWhenPresent — FR-106 as
// amended 2026-10-23 (ADR-084 §13, MAJ-004, issue #1431): a tool call's
// result payload that still exists on the record (ContentState empty) MUST
// be rendered to the Judge through this same seam — without it, a decisive
// value living only in a worker's tool result is unreachable, and the
// amended empty-evidence claim path promises evidence it cannot see. This
// test inverts TestFormatTierOneToolCall_NeverRendersResult (deleted per
// §13 "Tests and specs that must change"): the D5-projection safety the old
// test guarded is now carried by the ContentState marker (emptied/capped
// render the explicit projection marker — retained below), not by never
// reading Result.
func TestFormatTierOneToolCall_RendersResultPayloadWhenPresent(t *testing.T) {
	tc := session.ToolCall{
		Tool: "read_file", Status: "success",
		Result: map[string]any{"content": "report.md lists three supplier quotes: Acme, Borel, Cide"},
	}
	got := formatTierOneToolCall(tc, nil)
	if !strings.Contains(got, "Acme, Borel, Cide") {
		t.Fatalf("JUDGE-FR-106 as amended (§13 MAJ-004): a result payload that exists on the record must be rendered, got %q", got)
	}
	if strings.Contains(got, "projected away") {
		t.Fatalf("a present result (ContentState empty) is not a D5 projection — the projection marker must not appear, got %q", got)
	}
}

// TestFormatTierOneToolCall_EmptiedResultRendersMarkerNotAbsence proves
// FR-106's core failure mode is closed: a call whose Result was projected
// away by ADR-066 D5 (ContentState "emptied"/"capped") must render an
// EXPLICIT marker, never silent absence — silent absence would read as
// "this tool returned nothing", which could wrongly CONTRADICT (and veto,
// FR-108) a true claim.
func TestFormatTierOneToolCall_EmptiedResultRendersMarkerNotAbsence(t *testing.T) {
	for _, state := range []string{"emptied", "capped"} {
		tc := session.ToolCall{Tool: "read_file", Status: "success", ContentState: state}
		got := formatTierOneToolCall(tc, nil)
		if !strings.Contains(got, "projected away") {
			t.Errorf("ContentState=%q: want an explicit projection marker, got %q", state, got)
		}
	}
}

func TestFormatTierOneToolCall_RedactsSensitiveParametersAndErrors(t *testing.T) {
	redact := func(s string) string { return strings.ReplaceAll(s, "sk-live-secret", "[REDACTED]") }
	tc := session.ToolCall{
		Tool: "http_request", Status: "error",
		Parameters: map[string]any{"authorization": "Bearer sk-live-secret"},
		Error:      "auth failed with token sk-live-secret",
	}
	got := formatTierOneToolCall(tc, redact)
	if strings.Contains(got, "sk-live-secret") {
		t.Fatalf("JUDGE-FR-105: sensitive values must be redacted before reaching the Judge, got %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected the redaction marker to survive, got %q", got)
	}
}

func TestFormatTierOneToolCall_ParametersTruncatedAt512RunesWithMarker(t *testing.T) {
	tc := session.ToolCall{
		Tool: "write_file", Status: "success",
		Parameters: map[string]any{"content": strings.Repeat("a", 2000)},
	}
	got := formatTierOneToolCall(tc, nil)
	if !strings.Contains(got, tierOneTruncationMarker) {
		t.Fatalf("JUDGE-FR-105: an over-512-rune parameter rendering must carry the truncation marker, got len=%d", len(got))
	}
	// The rendered params segment itself must not run away unbounded.
	if idx := strings.Index(got, "params="); idx >= 0 && len(got)-idx > 700 {
		t.Errorf("rendered params segment too long (%d bytes after 'params='), truncation not applied", len(got)-idx)
	}
}

// TestFormatTierOneToolCall_ResultIsRedactedAndBounded — FR-106 as amended
// 2026-10-23 (ADR-084 §13, MAJ-004): the result payload travels the SAME
// redaction function and the SAME rune truncation already applied to
// parameters and error — never raw, never unbounded.
func TestFormatTierOneToolCall_ResultIsRedactedAndBounded(t *testing.T) {
	redact := func(s string) string { return strings.ReplaceAll(s, "sk-live-secret", "[REDACTED]") }
	tc := session.ToolCall{
		Tool: "http_request", Status: "success",
		Result: map[string]any{"body": "token sk-live-secret accepted; " + strings.Repeat("a", 2000)},
	}
	got := formatTierOneToolCall(tc, redact)
	if strings.Contains(got, "sk-live-secret") {
		t.Fatalf("§13 MAJ-004: the rendered result payload must pass the same redaction as parameters/error, got %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("§13 MAJ-004: expected the redaction marker to survive into the rendered result, got %q", got)
	}
	if !strings.Contains(got, tierOneTruncationMarker) {
		t.Fatalf("§13 MAJ-004: an over-512-rune result payload must carry the same truncation marker, got len=%d", len(got))
	}
	if idx := strings.Index(got, "result="); idx >= 0 && len(got)-idx > 700 {
		t.Errorf("rendered result segment too long (%d bytes after 'result='), truncation not applied", len(got)-idx)
	}
}

// TestFormatTierOneToolCall_NoResultRendersAbsence — FR-106 as amended
// 2026-10-23 (ADR-084 §13, MAJ-004): a call that produced no result says so
// — never silence, which would read as an inconclusive projection, and never
// a payload that does not exist.
func TestFormatTierOneToolCall_NoResultRendersAbsence(t *testing.T) {
	tc := session.ToolCall{Tool: "bash", Status: "success"}
	got := formatTierOneToolCall(tc, nil)
	lower := strings.ToLower(got)
	if !strings.Contains(lower, "no result") {
		t.Fatalf("§13 MAJ-004: a call with no result must render an explicit absence, got %q", got)
	}
	if strings.Contains(got, "projected away") {
		t.Fatalf("absence of a result is not a D5 projection — the projection marker must not appear, got %q", got)
	}
}

// TestTierOneFeed_RendersDecisiveToolResultOnly — FR-106 as amended
// 2026-10-23 (ADR-084 §13, MAJ-004), the amended empty-evidence path's
// sufficiency requirement: a value that exists ONLY in a worker tool result
// — not in the narration, the claim text or the criteria — is visible in the
// tier-one evidence feed the Judge receives (renderTranscriptEntriesForWindow).
func TestTierOneFeed_RendersDecisiveToolResultOnly(t *testing.T) {
	const decisive = "supplier Cide accepted clause 7.2 verbatim"
	entries := []session.TranscriptEntry{
		{ID: "narration-1", Content: "I have written the report and checked every clause.", Timestamp: baseTime.Add(time.Second)},
		{ID: "call-1", Timestamp: baseTime.Add(2 * time.Second), ToolCalls: []session.ToolCall{{
			Tool: "read_file", Status: "success",
			Parameters: map[string]any{"path": "report.md"},
			Result:     map[string]any{"content": decisive},
		}}},
	}
	msgs := renderTranscriptEntriesForWindow(entries)
	joined := make([]string, 0, len(msgs))
	for _, m := range msgs {
		joined = append(joined, m.Content)
	}
	feed := strings.Join(joined, "\n")
	if !strings.Contains(feed, decisive) {
		t.Fatalf("§13 MAJ-004: a decisive value living ONLY in a worker tool result must be visible in the rendered tier-one feed, got:\n%s", feed)
	}
}

// --- FR-105: renderTranscriptEntriesForWindow's 32 KiB tier-1 block cap ----

func TestRenderTranscriptEntriesForWindow_DropsOldestToolCallsOver32KiB(t *testing.T) {
	var entries []session.TranscriptEntry
	// Each call's rendered content is truncated to 512 runes PER CALL
	// (FR-105) before the whole-block bound is ever applied — so it takes
	// many such calls, not a few large ones, to exceed the 32 KiB
	// whole-tier-1-block bound. 100 truncated-and-rendered calls comfortably
	// clears it.
	big := strings.Repeat("x", 3000)
	const callCount = 100
	for i := 0; i < callCount; i++ {
		entries = append(entries, mkEntry(baseTime.Add(time.Duration(i)*time.Second), session.ToolCall{
			Tool: "bash", Status: "success", Parameters: map[string]any{"command": big},
		}))
	}
	msgs := renderTranscriptEntriesForWindow(entries)
	if len(msgs) >= callCount {
		t.Fatalf("JUDGE-FR-105: expected some tool-call summaries dropped over the 32 KiB cap, got %d messages for %d calls", len(msgs), callCount)
	}
	if !strings.Contains(msgs[0].Content, "dropped") {
		t.Errorf("expected a leading drop-count marker message, got first message %q", msgs[0].Content)
	}
}

func TestRenderTranscriptEntriesForWindow_UnderBudget_NoDropMarker(t *testing.T) {
	entries := []session.TranscriptEntry{
		mkEntry(baseTime, session.ToolCall{Tool: "web_search", Status: "success"}),
	}
	msgs := renderTranscriptEntriesForWindow(entries)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1 (no drop marker under budget): %+v", len(msgs), msgs)
	}
}

// --- FR-108: applyBehaviorContradictionVeto ---------------------------------

func behaviorCriterionWithMinZero(tool string) task.AcceptanceCriterion {
	zero := 0
	return task.AcceptanceCriterion{
		ID: "c-behavior", Kind: task.KindBehavior,
		Behavior: &task.CriterionBehavior{Tool: tool, MinCount: &zero},
	}
}

func TestApplyBehaviorContradictionVeto_AllCallsErrored_VetoesOptionalCall(t *testing.T) {
	c := behaviorCriterionWithMinZero("send_email")
	// rung 2 already resolved Met:true — Observed==0 successful calls
	// satisfies MinCount==0, exactly the "optional call" case FR-108 case 2
	// exists for.
	v := task.CriterionVerdict{CriterionID: c.ID, Met: true, Reason: "0 successful calls, min_count=0"}
	entries := []session.TranscriptEntry{
		mkEntry(baseTime, session.ToolCall{Tool: "send_email", Status: "error", Error: "smtp timeout"}),
		mkEntry(baseTime.Add(time.Second), session.ToolCall{Tool: "send_email", Status: "error", Error: "smtp timeout"}),
	}
	got := applyBehaviorContradictionVeto(entries, c, v)
	if got.Met {
		t.Fatal("JUDGE-FR-108 case 2: every recorded call errored — Met must be vetoed to false")
	}
	if !strings.Contains(got.Reason, "smtp timeout") {
		t.Errorf("veto reason should quote the error, got %q", got.Reason)
	}
}

func TestApplyBehaviorContradictionVeto_ToolNeverCalled_NoVeto(t *testing.T) {
	c := behaviorCriterionWithMinZero("send_email")
	v := task.CriterionVerdict{CriterionID: c.ID, Met: true, Reason: "never called, min_count=0"}
	got := applyBehaviorContradictionVeto(nil, c, v)
	if !got.Met {
		t.Error("D14: a tool never called at all is informative-only, not decisive — must not veto")
	}
}

func TestApplyBehaviorContradictionVeto_AtLeastOneSuccess_NoVeto(t *testing.T) {
	c := behaviorCriterionWithMinZero("send_email")
	v := task.CriterionVerdict{CriterionID: c.ID, Met: true, Reason: "1 successful call"}
	entries := []session.TranscriptEntry{
		mkEntry(baseTime, session.ToolCall{Tool: "send_email", Status: "error"}),
		mkEntry(baseTime.Add(time.Second), session.ToolCall{Tool: "send_email", Status: "success"}),
	}
	got := applyBehaviorContradictionVeto(entries, c, v)
	if !got.Met {
		t.Error("a mix including a success must not be vetoed — only ALL-errored is decisive (FR-108)")
	}
}

func TestApplyBehaviorContradictionVeto_AlreadyUnmet_NoOp(t *testing.T) {
	c := behaviorCriterionWithMinZero("send_email")
	v := task.CriterionVerdict{CriterionID: c.ID, Met: false, Reason: "already unmet"}
	entries := []session.TranscriptEntry{
		mkEntry(baseTime, session.ToolCall{Tool: "send_email", Status: "error"}),
	}
	got := applyBehaviorContradictionVeto(entries, c, v)
	if got.Reason != "already unmet" {
		t.Errorf("an already-unmet verdict must pass through unchanged, got reason %q", got.Reason)
	}
}

func TestApplyBehaviorContradictionVeto_NonBehaviorCriterion_NoOp(t *testing.T) {
	c := task.AcceptanceCriterion{ID: "c-prose", Kind: task.KindProse}
	v := task.CriterionVerdict{CriterionID: c.ID, Met: true, Reason: "judge said so"}
	entries := []session.TranscriptEntry{
		mkEntry(baseTime, session.ToolCall{Tool: "anything", Status: "error"}),
	}
	got := applyBehaviorContradictionVeto(entries, c, v)
	if !got.Met {
		t.Error("FR-108 applies only to a criterion that declares a KindBehavior payload")
	}
}
