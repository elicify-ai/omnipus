// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

// handover_clamp_test.go — the field-overflow defect and its clamp.
//
// The defect: several Plan.HandoverText writers in pkg/agent build their note
// by embedding a provider-controlled, unbounded string verbatim (the judge's
// own failure reason, one reason per unmet criterion, the previous handover
// re-wrapped in new boilerplate). The store REJECTED anything over the bound,
// so a verbose provider error made the write fail deterministically, every
// time, for that plan — and the engine's bounded retry then ended a plan whose
// members had all SUCCEEDED at failed(supervision_unavailable).
//
// Oracle: the bound is 8000 characters (spec Part A §A / Wave 2-B, as carried
// by maxPlanHandoverRunes) and the note must survive the write naming what
// failed. Every expectation below is written against that statement of intent,
// not read off the implementation — the literal 8000 is asserted directly so a
// silent change to the constant fails here rather than sliding through.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// theBound is the documented Plan.HandoverText bound, stated independently of
// the constant the implementation uses so a drift in either is visible.
const theBound = 8000

// stallNotePrefix mirrors pkg/agent/plan_engine.go's stallHandoverNotePrefix.
// pkg/agent imports pkg/plan, so it cannot be imported back here; the literal
// is reproduced so these tests exercise the REAL note shape.
const stallNotePrefix = "[stalled] "

// verboseProviderError stands in for a real judge failure body: a provider
// error that is simply long. 40k characters is not an adversarial number —
// an HTML error page or a JSON blob with a stack reaches it easily.
func verboseProviderError() string {
	return strings.Repeat("upstream provider returned 502 bad gateway; retrying host pool member; ", 560)
}

// judgeUnavailableNote reproduces surfaceJudgeUnavailableStall's note shape
// (pkg/agent/plan_engine.go): a fixed lead that NAMES the failure, the
// provider's own reason embedded verbatim in the middle, and a fixed tail.
func judgeUnavailableNote(judgeReason string) string {
	return stallNotePrefix +
		"The plan judge could not be reached on 3 consecutive attempts, so this plan's Definition of " +
		"Done cannot be adjudicated right now. Every member has finished, but without a judge " +
		"verdict the plan cannot be declared done or unmet. Last judge failure: " + judgeReason +
		". No judge round was consumed by these attempts. A correction, or Stop, is needed — " +
		"retrying on its own has already been tried 3 times."
}

func TestMaxPlanHandoverRunes_IsTheDocumentedBound(t *testing.T) {
	if maxPlanHandoverRunes != theBound {
		t.Fatalf("maxPlanHandoverRunes = %d, want the documented bound %d; if the bound genuinely "+
			"moved, move this test's oracle deliberately", maxPlanHandoverRunes, theBound)
	}
}

// TestUpdate_OverlongJudgeErrorHandoverIsWrittenNotRejected is THE defect
// test. Before the clamp it fails at the Update call with
// "handover_text must be 8000 characters or fewer".
func TestUpdate_OverlongJudgeErrorHandoverIsWrittenNotRejected(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Ship the migration", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	note := judgeUnavailableNote(verboseProviderError())
	if utf8.RuneCountInString(note) <= theBound {
		t.Fatalf("test setup is not exercising the defect: note is %d characters, "+
			"which is within the %d bound", utf8.RuneCountInString(note), theBound)
	}

	updated, err := s.Update(p.ID, Patch{HandoverText: &note})
	if err != nil {
		t.Fatalf("Update with an over-long judge-error handover must SUCCEED, not be rejected: %v", err)
	}

	// The write must have survived to disk, not merely to the returned value.
	got, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.HandoverText == "" {
		t.Fatal("the persisted handover is empty — the diagnostic was destroyed, not clamped")
	}
	if n := utf8.RuneCountInString(got.HandoverText); n > theBound {
		t.Fatalf("persisted handover is %d characters, want at most %d", n, theBound)
	}

	// The surviving text must still NAME the failure.
	for _, want := range []string{
		stallNotePrefix,
		"The plan judge could not be reached on 3 consecutive attempts",
		"Last judge failure:",
		"upstream provider returned 502 bad gateway",
	} {
		if !strings.Contains(got.HandoverText, want) {
			t.Errorf("persisted handover does not name the failure: missing %q\ngot: %q",
				want, got.HandoverText)
		}
	}

	// And it must SAY it was cut, pointing at where the full text survives —
	// a reader must be able to tell "the judge said no more" apart from "the
	// judge said more and we dropped it".
	if !strings.Contains(got.HandoverText, "handover clamped:") {
		t.Errorf("clamped handover carries no truncation marker: %q", got.HandoverText)
	}
	if !strings.Contains(got.HandoverText, "plan: handover text clamped") {
		t.Errorf("the marker does not say where the full text survives: %q", got.HandoverText)
	}

	// What Update RETURNS must match what it wrote — a caller that keeps the
	// returned plan must not be holding a value disk disagrees with.
	if updated.HandoverText != got.HandoverText {
		t.Errorf("returned handover != persisted handover\nreturned: %q\npersisted: %q",
			updated.HandoverText, got.HandoverText)
	}
}

// TestStoreWrite_ClampsHandoverForAWriterThatSkipsNormalize is the
// cannot-be-bypassed test. It constructs a plan by hand and calls the
// unexported write() DIRECTLY — exactly the shape of a future in-package
// writer that forgets the clamp helper, or of a manual read-modify-write done
// under Store.Lock. normalize() never runs here, so only the last-mile clamp
// in write() can save it.
func TestStoreWrite_ClampsHandoverForAWriterThatSkipsNormalize(t *testing.T) {
	s := newStore(t)
	p := &Plan{
		ID:           "01JFORGETFULWRITER00000000",
		Title:        "Bypass",
		WorkspaceID:  "ws-1",
		OwnerAgentID: "agent-a",
		State:        StateRunning,
		HandoverText: judgeUnavailableNote(verboseProviderError()),
		CreatedAt:    "2026-09-13T00:00:00Z",
		UpdatedAt:    "2026-09-13T00:00:00Z",
	}

	if err := s.write(p); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read the bytes back from disk, not through any helper that might clamp
	// on the way out.
	raw, err := os.ReadFile(filepath.Join(s.Dir(), p.ID+".json"))
	if err != nil {
		t.Fatalf("read plan file: %v", err)
	}
	onDisk, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n := utf8.RuneCountInString(onDisk.HandoverText); n > theBound {
		t.Fatalf("a writer that skipped normalize() put %d characters of handover on disk; "+
			"the bound is %d and write() is supposed to be unbypassable", n, theBound)
	}
	if !strings.Contains(onDisk.HandoverText, "handover clamped:") {
		t.Errorf("on-disk handover carries no truncation marker: %q", onDisk.HandoverText)
	}
	// Cheap independent confirmation that the raw file really shrank rather
	// than the reader trimming it: 40k+ characters of JSON-escaped handover
	// cannot fit in a file this size.
	if len(raw) > 64*1024 {
		t.Errorf("plan file is %d bytes — the unbounded handover reached disk verbatim", len(raw))
	}

	// The caller's own *Plan must have been brought in line with disk, so it
	// cannot go on believing it persisted the long version.
	if p.HandoverText != onDisk.HandoverText {
		t.Errorf("write() left the caller's plan out of sync with disk\nin memory: %q\non disk:  %q",
			p.HandoverText, onDisk.HandoverText)
	}
}

// TestCreate_ClampsOverlongHandover covers the second write path. A plan is
// normally created with no handover, but the boot/replay paths hand whole
// Plan values to Create, so this path must bound the field too.
func TestCreate_ClampsOverlongHandover(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Created with a handover", "ws-1", "agent-a")
	p.HandoverText = judgeUnavailableNote(verboseProviderError())

	if err := s.Create(p); err != nil {
		t.Fatalf("Create with an over-long handover must SUCCEED, not be rejected: %v", err)
	}
	got, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n := utf8.RuneCountInString(got.HandoverText); n > theBound {
		t.Fatalf("persisted handover is %d characters, want at most %d", n, theBound)
	}
	if !strings.Contains(got.HandoverText, "The plan judge could not be reached") {
		t.Errorf("persisted handover no longer names the failure: %q", got.HandoverText)
	}
}

// TestUpdate_HandoverWithinTheBoundIsUntouched guards the other direction:
// the clamp must not rewrite text that fits. An over-eager clamp that stamped
// a marker on every note would be its own defect.
func TestUpdate_HandoverWithinTheBoundIsUntouched(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Normal", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	note := judgeUnavailableNote("context deadline exceeded")
	if utf8.RuneCountInString(note) > theBound {
		t.Fatalf("test setup: the short note is already over the bound")
	}
	if _, err := s.Update(p.ID, Patch{HandoverText: &note}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.HandoverText != note {
		t.Errorf("a handover within the bound was modified\nwant: %q\ngot:  %q", note, got.HandoverText)
	}
}

// TestUpdate_HandoverExactlyAtTheBoundIsUntouched pins the boundary itself:
// == bound is legal, > bound is cut.
func TestUpdate_HandoverExactlyAtTheBoundIsUntouched(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Boundary", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Multi-byte runes on purpose: the bound counts characters, not bytes, so
	// a byte-counting implementation would wrongly cut this.
	note := strings.Repeat("é", theBound)
	if _, err := s.Update(p.ID, Patch{HandoverText: &note}); err != nil {
		t.Fatalf("Update at exactly the bound must succeed: %v", err)
	}
	got, err := s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.HandoverText != note {
		t.Errorf("a handover of exactly %d characters was modified (got %d characters)",
			theBound, utf8.RuneCountInString(got.HandoverText))
	}

	oneOver := note + "é"
	if _, uerr := s.Update(p.ID, Patch{HandoverText: &oneOver}); uerr != nil {
		t.Fatalf("Update at bound+1 must succeed (clamped): %v", uerr)
	}
	got, err = s.Get(p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n := utf8.RuneCountInString(got.HandoverText); n > theBound {
		t.Errorf("bound+1 was persisted at %d characters, want at most %d", n, theBound)
	}
	if !strings.Contains(got.HandoverText, "handover clamped:") {
		t.Error("bound+1 was not marked as clamped")
	}
}

func TestClampHandoverText_PreservesTheHeadAndNeverSplitsARune(t *testing.T) {
	// A string that is multi-byte throughout, so ANY byte-oriented cut lands
	// mid-rune and shows up as U+FFFD.
	head := "判定に失敗しました: " // "adjudication failed:"
	long := head + strings.Repeat("エラー詳細…", 4000)

	out := ClampHandoverText(long)

	if n := utf8.RuneCountInString(out); n > theBound {
		t.Fatalf("clamped to %d characters, want at most %d", n, theBound)
	}
	if !strings.HasPrefix(out, head) {
		t.Errorf("the head was not preserved; got prefix %q", string([]rune(out)[:len([]rune(head))]))
	}
	if !utf8.ValidString(out) {
		t.Error("clamped text is not valid UTF-8 — the cut landed mid-rune")
	}
	if strings.ContainsRune(out, utf8.RuneError) {
		t.Error("clamped text contains U+FFFD — the cut landed mid-rune")
	}
}

func TestClampHandoverText_IsIdempotentAndDeterministic(t *testing.T) {
	long := judgeUnavailableNote(verboseProviderError())

	once := ClampHandoverText(long)
	twice := ClampHandoverText(once)
	if once != twice {
		t.Errorf("clamp is not idempotent:\nonce:  %d chars\ntwice: %d chars",
			utf8.RuneCountInString(once), utf8.RuneCountInString(twice))
	}
	if again := ClampHandoverText(long); again != once {
		t.Error("clamp is not deterministic — two calls on the same input differ")
	}
}

func TestClampHandoverText_ReportsHowMuchItElided(t *testing.T) {
	long := strings.Repeat("x", 20000)
	out := ClampHandoverText(long)

	// The marker must state BOTH numbers: what was dropped and what there was.
	marker := out
	if i := strings.LastIndex(out, "[handover clamped"); i >= 0 {
		marker = out[i:]
	}
	if !strings.Contains(out, "of 20000 characters elided") {
		t.Errorf("marker does not report the original size: %q", marker)
	}
	if !strings.Contains(out, "8000-character bound") {
		t.Errorf("marker does not report the bound: %q", marker)
	}
	// Dropping fewer characters than the input exceeded the bound by would
	// mean the result is still over.
	if n := utf8.RuneCountInString(out); n > theBound {
		t.Fatalf("clamped to %d characters, want at most %d", n, theBound)
	}
}

func TestClampHandoverText_LeavesShortTextByteIdentical(t *testing.T) {
	for _, in := range []string{"", "the judge found the DoD unmet", stallNotePrefix + "blocked on task-1"} {
		if out := ClampHandoverText(in); out != in {
			t.Errorf("ClampHandoverText(%q) = %q, want it unchanged", in, out)
		}
	}
}

// TestOtherBoundedFieldsStillReject pins the deliberate asymmetry: only
// HandoverText clamps. Title/Goal/Description/Rationale are authored at an
// interactive boundary where the caller CAN shorten the text, so a rejection
// is real feedback and must stay. This test is what stops the clamp being
// generalised into "pkg/plan no longer validates lengths".
func TestOtherBoundedFieldsStillReject(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Fields", "ws-1", "agent-a")
	p.Rationale = strings.Repeat("r", maxPlanRationaleRunes+1)
	if err := s.Create(p); err == nil {
		t.Error("an over-long rationale must still be REJECTED, not clamped")
	}

	p.Rationale = ""
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	tooLongTitle := strings.Repeat("t", maxPlanTitleRunes+1)
	if _, err := s.Update(p.ID, Patch{Title: &tooLongTitle}); err == nil {
		t.Error("an over-long title must still be REJECTED, not clamped")
	}
	tooLongGoal := strings.Repeat("g", maxPlanGoalRunes+1)
	if _, err := s.Update(p.ID, Patch{Goal: &tooLongGoal}); err == nil {
		t.Error("an over-long goal must still be REJECTED, not clamped")
	}
	tooLongDesc := strings.Repeat("d", maxPlanDescriptionRunes+1)
	if _, err := s.Update(p.ID, Patch{Description: &tooLongDesc}); err == nil {
		t.Error("an over-long description must still be REJECTED, not clamped")
	}
}
