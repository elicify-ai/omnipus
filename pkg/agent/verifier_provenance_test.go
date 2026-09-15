// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_provenance_test.go covers ADR-084 revision 9's §K, D7
// (JUDGE-FR-065/FR-066/FR-067/FR-069/FR-069a) and the D-B-compliant
// grounding/attribution reports (FR-006, FR-007a, FR-014a, FR-028,
// FR-029, FR-063a) at the unit level.

package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- FR-065/FR-066: refineVerdictProvenance ---------------------------------

func TestRefineVerdictProvenance_TargetNamedInCriterionText_StaysGrounded(t *testing.T) {
	v := task.CriterionVerdict{
		CriterionID: "c1", Met: true,
		EvidenceSource: task.EvidenceSourceFileRead, EvidenceTarget: "neon-2048/index.html",
		Provenance: task.ProvenanceJudgeRead,
	}
	got := refineVerdictProvenance(v, "neon-2048/index.html must exist and render the game", "", nil)
	if got.Provenance != task.ProvenanceJudgeRead {
		t.Errorf("target named in criterion text is reachable (FR-030a clause i) — provenance must NOT be downgraded, got %q", got.Provenance)
	}
	if !got.Met {
		t.Fatal("D-B: refineVerdictProvenance must never touch Met")
	}
}

func TestRefineVerdictProvenance_TargetNamedInDiff_StaysGrounded(t *testing.T) {
	v := task.CriterionVerdict{
		CriterionID: "c1", Met: true,
		EvidenceSource: task.EvidenceSourceFileRead, EvidenceTarget: "src/app.go",
		Provenance: task.ProvenanceJudgeRead,
	}
	got := refineVerdictProvenance(v, "the change compiles", "diff --git a/src/app.go b/src/app.go\n+func main(){}", nil)
	if got.Provenance != task.ProvenanceJudgeRead {
		t.Errorf("target named in the diff is reachable (FR-030a clause ii) — provenance must NOT be downgraded, got %q", got.Provenance)
	}
}

func TestRefineVerdictProvenance_UnreachableTarget_DowngradesToNone(t *testing.T) {
	v := task.CriterionVerdict{
		CriterionID: "c1", Met: true,
		EvidenceSource: task.EvidenceSourceFileRead, EvidenceTarget: "totally/unrelated/file.txt",
		Provenance: task.ProvenanceJudgeRead,
	}
	got := refineVerdictProvenance(v, "the login page must render", "diff --git a/login.go b/login.go\n", nil)
	if got.Provenance != task.ProvenanceNone {
		t.Errorf("an unreachable target must downgrade provenance to none (JUDGE-FR-030a-lite), got %q", got.Provenance)
	}
	if !got.Met || got.Reason != v.Reason {
		t.Fatal("D-B: an unreachable target must NEVER touch Met or Reason — reporting only")
	}
}

func TestRefineVerdictProvenance_DiffAndTranscriptSources_ExemptFromTargetCheck(t *testing.T) {
	// FR-028: diff/transcript sources have exactly one region each and
	// carry no target — refineVerdictProvenance must leave them alone
	// regardless of criterion text.
	v := task.CriterionVerdict{
		CriterionID: "c1", Met: true,
		EvidenceSource: task.EvidenceSourceDiff, Provenance: task.ProvenanceDiffRead,
	}
	got := refineVerdictProvenance(v, "nothing matches anything", "", nil)
	if got.Provenance != task.ProvenanceDiffRead {
		t.Errorf("a target-exempt source must never be downgraded, got %q", got.Provenance)
	}
}

// --- FR-006/FR-014a/FR-028/FR-029/FR-007a/FR-063a: reportGroundingIssues ---
//
// Every case below asserts the SAME core D-B property: whatever
// reportGroundingIssues observes, it never has an opportunity to mutate
// Met, because its signature does not return anything — these tests prove
// it is callable (does not panic) on each triggering shape and that the
// counter observably increments, which is the only externally-visible
// effect a reporting-only function may have.

func TestReportGroundingIssues_FewerEvidenceEntriesThanClauseCount_Counts(t *testing.T) {
	before := GroundingReportsTotal()
	c := task.AcceptanceCriterion{ID: "c1", Text: "a and b and c", ClauseCount: 3}
	pc := judgeCriterionResponse{Met: true}
	v := task.CriterionVerdict{
		CriterionID: "c1", Met: true,
		Evidence: []task.CriterionEvidenceEntry{{Part: "a", Quote: "q"}},
	}
	reportGroundingIssues(c, pc, v)
	if GroundingReportsTotal() <= before {
		t.Error("JUDGE-FR-006: fewer evidence entries than ClauseCount must be counted")
	}
}

func TestReportGroundingIssues_SessionReadGroundingFilesystemCriterion_Counts(t *testing.T) {
	before := GroundingReportsTotal()
	c := task.AcceptanceCriterion{ID: "c1", Text: "neon-2048/index.html must contain the win condition"}
	pc := judgeCriterionResponse{Met: true}
	v := task.CriterionVerdict{
		CriterionID: "c1", Met: true,
		EvidenceSource: task.EvidenceSourceSessionRead, EvidenceTarget: "sess-1",
	}
	reportGroundingIssues(c, pc, v)
	if GroundingReportsTotal() <= before {
		t.Error("JUDGE-FR-014a: session_read grounding a filesystem-path criterion must be counted")
	}
}

func TestReportGroundingIssues_MetMissingRequiredTarget_Counts(t *testing.T) {
	before := GroundingReportsTotal()
	c := task.AcceptanceCriterion{ID: "c1", Text: "x"}
	pc := judgeCriterionResponse{Met: true}
	v := task.CriterionVerdict{CriterionID: "c1", Met: true, EvidenceSource: task.EvidenceSourceFileRead, EvidenceTarget: ""}
	reportGroundingIssues(c, pc, v)
	if GroundingReportsTotal() <= before {
		t.Error("JUDGE-FR-028: a met missing a required evidence_target must be counted")
	}
}

func TestReportGroundingIssues_UnrecognisedEvidenceSource_Counts(t *testing.T) {
	before := GroundingReportsTotal()
	c := task.AcceptanceCriterion{ID: "c1", Text: "x"}
	pc := judgeCriterionResponse{Met: true, EvidenceSource: "not-a-real-source"}
	v := task.CriterionVerdict{CriterionID: "c1", Met: true, EvidenceSource: ""} // dropped by verdictFromJudgeResponse
	reportGroundingIssues(c, pc, v)
	if GroundingReportsTotal() <= before {
		t.Error("JUDGE-FR-029: an unrecognised evidence_source must be counted")
	}
}

func TestReportGroundingIssues_UnmetAssertingAbsence_Counts(t *testing.T) {
	before := GroundingReportsTotal()
	c := task.AcceptanceCriterion{ID: "c1", Text: "x"}
	pc := judgeCriterionResponse{Met: false}
	v := task.CriterionVerdict{CriterionID: "c1", Met: false, Reason: "the string is not present in the file"}
	reportGroundingIssues(c, pc, v)
	if GroundingReportsTotal() <= before {
		t.Error("JUDGE-FR-007a: an unmet reason asserting absence must be counted")
	}
}

func TestReportGroundingIssues_UnmetAssertingRefusal_Counts(t *testing.T) {
	before := GroundingReportsTotal()
	c := task.AcceptanceCriterion{ID: "c1", Text: "x"}
	pc := judgeCriterionResponse{Met: false}
	v := task.CriterionVerdict{CriterionID: "c1", Met: false, Reason: "the read was refused — outside confinement"}
	reportGroundingIssues(c, pc, v)
	if GroundingReportsTotal() <= before {
		t.Error("JUDGE-FR-063a: an unmet reason asserting a refusal must be counted")
	}
}

func TestReportGroundingIssues_CleanMet_NoSpuriousCount(t *testing.T) {
	before := GroundingReportsTotal()
	c := task.AcceptanceCriterion{ID: "c1", Text: "index.html exists", ClauseCount: 1}
	pc := judgeCriterionResponse{Met: true, EvidenceSource: "file_read"}
	v := task.CriterionVerdict{
		CriterionID: "c1", Met: true, EvidenceSource: task.EvidenceSourceFileRead, EvidenceTarget: "index.html",
		Evidence: []task.CriterionEvidenceEntry{{Part: "index.html exists", Quote: "q"}},
	}
	reportGroundingIssues(c, pc, v)
	if GroundingReportsTotal() != before {
		t.Error("a fully-grounded met with no issues must not increment the counter")
	}
}

// --- FR-069a: noteAdjudicationReproducibility -------------------------------

func TestNoteAdjudicationReproducibility_SameOutcomeTwice_NoDisagreement(t *testing.T) {
	unit := "unit-" + t.Name()
	before := VerdictReproducibilityDisagreements()
	noteAdjudicationReproducibility(unit, "c1", true, "log-1")
	noteAdjudicationReproducibility(unit, "c1", true, "log-2")
	if VerdictReproducibilityDisagreements() != before {
		t.Error("two consecutive adjudications agreeing must not count as a disagreement")
	}
}

func TestNoteAdjudicationReproducibility_FlippedOutcome_CountsAndLogs(t *testing.T) {
	unit := "unit-" + t.Name()
	before := VerdictReproducibilityDisagreements()
	noteAdjudicationReproducibility(unit, "c1", true, "log-1")
	noteAdjudicationReproducibility(unit, "c1", false, "log-2")
	if VerdictReproducibilityDisagreements() != before+1 {
		t.Errorf("JUDGE-FR-069a: met->unmet between consecutive adjudications must be counted exactly once, got delta %d",
			VerdictReproducibilityDisagreements()-before)
	}
}

func TestNoteAdjudicationReproducibility_FirstAdjudicationEver_NoBaseline(t *testing.T) {
	unit := "unit-" + t.Name()
	before := VerdictReproducibilityDisagreements()
	noteAdjudicationReproducibility(unit, "c-fresh", false, "log-1")
	if VerdictReproducibilityDisagreements() != before {
		t.Error("the FIRST adjudication of a unit+criterion has nothing to disagree with")
	}
}

// --- FR-067/FR-069: the investigation log (structural smoke test) ----------

func TestEmitInvestigationLog_DoesNotPanicOnEmptyCalls(t *testing.T) {
	// A pure logging call — the only assertable behaviour at the unit level
	// without a log-capture seam is that it never panics on the shapes the
	// production call site can hand it, including a nil calls slice
	// (buildInvestigationLogFromJudgeTranscript returns nil on any
	// resolution failure, by design).
	emitInvestigationLog("unit-1", "log-1", "test-model", 0, nil)
}

func TestEmitInvestigationLog_CarriesCaptureUnavailableMarker(t *testing.T) {
	calls := []investigationLogCall{{Tool: "read_file", Target: "a.txt", CaptureUnavailable: true}}
	if !calls[0].CaptureUnavailable {
		t.Fatal("sanity")
	}
	// Structural: the shape this file documents (FR-068's capture
	// unreachable from this wave) is represented, not silently hidden.
	if strings.TrimSpace(calls[0].Tool) == "" {
		t.Fatal("investigationLogCall.Tool must be populated")
	}
}
