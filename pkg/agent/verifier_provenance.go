// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_provenance.go implements ADR-084 revision 9's §K, D7
// (JUDGE-FR-065 – FR-069a): the "real grounding-based derivation" E9 left
// for this wave (deriveVerdictProvenance's own doc comment,
// verifier_adjudication.go), the grounding/attribution REPORTING signals
// D-B repoints from a code-level rewrite to a WARN-and-count observation
// (JUDGE-FR-006, FR-007a, FR-014a, FR-028, FR-029, FR-063a), and FR-069a's
// reproducibility-disagreement WARN.
//
// D-B, restated once more because it is the single most important
// constraint on every function in this file: NOTHING here ever mutates a
// task.CriterionVerdict's Met or Reason in the direction of the OLD
// unable_to_verify rewrite ladder. Every function either (a) sets a
// reporting-only field (Provenance) to a value that is itself never
// consulted by anything that gates a verdict, or (b) logs at WARN and
// increments an observability counter. The Judge's own Met, decided
// before any of this runs, is authoritative.
//
// A genuine, reported scope boundary: JUDGE-FR-068 specifies the
// investigation log MUST be "derived from FR-030's in-memory capture" —
// pkg/agent/tool_result_admit.go's VerifierCapture (E2's mechanism). That
// capture is registered per turnID
// (RegisterVerifierCapture/RegisterVerifierBudget), and turnID is minted
// exclusively inside pkg/agent/loop.go::newTurnEventScope — a private,
// unexported counter with no seam this wave's write-set can reach
// (loop.go belongs to the E2 -> B123 -> E13 chain; none of those regions
// is this wave's). Predicting the NEXT turnID from outside loop.go before
// dispatch is unsafe (the counter is shared across every concurrent turn
// in the whole AgentLoop, not scoped to one adjudication) and this wave's
// write-set has no path to a safe fix. emitInvestigationLog below is
// therefore built against the Judge's OWN session transcript (durable
// fields only — Tool and, where present, a target parameter) rather than
// the capture, and explicitly marks bytes_returned/truncated as
// "capture_unavailable" rather than fabricating numbers from a
// possibly-stale, mid-turn-rewritten Result (ADR-066 D5) — reported here,
// not silently narrowed.
package agent

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- FR-065/FR-066: grounding-aware provenance refinement -------------------

// groundingReportsTotal backs the observability counter every REPORTING
// function in this file increments on a detected issue (FR-006, FR-007a,
// FR-014a, FR-028, FR-029, FR-030a-lite, FR-063a) — mirrors
// pkg/agent/tool_result_admit.go's toolResultLargeTotal precedent (a
// package-level atomic.Int64 plus an exported getter for a future
// /metrics render), not a per-FR counter: the value that matters is "how
// often is this install producing weak/ungrounded mets", not which
// specific rule fired.
var groundingReportsTotal atomic.Int64

// GroundingReportsTotal returns the count of grounding/attribution issues
// detected and reported (never gated) since process start.
func GroundingReportsTotal() int64 { return groundingReportsTotal.Load() }

// filesystemPathLikePattern is FR-014a's cheap "names a filesystem path"
// heuristic: contains a path separator or a dotted extension. Deliberately
// loose (over-matching is safe — it only WIDENS reporting, never gates)
// rather than a strict path-syntax parser.
func looksLikeFilesystemPath(s string) bool {
	if strings.ContainsAny(s, "/\\") {
		return true
	}
	// "index.html", "package.json" — a bare filename with an extension and
	// no spaces.
	if !strings.Contains(s, " ") {
		if i := strings.LastIndex(s, "."); i > 0 && i < len(s)-1 {
			return true
		}
	}
	return false
}

// refineVerdictProvenance is JUDGE-FR-065/FR-066's "real" half: E9's
// deriveVerdictProvenance (verifier_adjudication.go) is the literal
// schema-only mapping from evidence_source, unconditionally pinned by
// TestDeriveVerdictProvenance_SchemaMapping (E9's write-set) to stay
// exactly that for every plain (source, no context) call — so this
// function runs AFTER it, at the one production call site this wave
// owns (runVerifierAdjudication's mapping loop), with context
// deriveVerdictProvenance alone never has: the criterion's own text, the
// workspace diff, and this attempt's machine-check evidence records.
//
// Downgrades v.Provenance to task.ProvenanceNone — REPORTING only, Met and
// Reason untouched — when v.EvidenceSource requires a target (file_read,
// session_read, machine_check; diff/transcript are exempt, FR-028) and
// that target is unreachable by a loose form of FR-030a's three clauses:
// (i) the criterion's own text names it, (ii) the diff's changed-file text
// contains it, (iii) an evidence record's own target matches it. This is
// FR-030a's REACHABILITY test at reduced fidelity (plain case-insensitive
// substring containment, not FR-030's one normalisation function) — full
// FR-030 normalisation and its capture-backed authenticity check are E9's
// D2c region (verifier_adjudication.go's per-criterion mapping loop),
// consuming FR-030's VerifierCapture wiring this wave found architecturally
// unreachable (see file doc comment) and did not attempt.
func refineVerdictProvenance(
	v task.CriterionVerdict, criterionText, diffText string, evidence []task.EvidenceRecord,
) task.CriterionVerdict {
	requiresTarget := v.EvidenceSource == task.EvidenceSourceFileRead ||
		v.EvidenceSource == task.EvidenceSourceSessionRead ||
		v.EvidenceSource == task.EvidenceSourceMachineCheck
	if !requiresTarget || strings.TrimSpace(v.EvidenceTarget) == "" {
		return v
	}
	target := v.EvidenceTarget
	lowerTarget := strings.ToLower(target)
	reachable := strings.Contains(strings.ToLower(criterionText), lowerTarget) ||
		strings.Contains(strings.ToLower(diffText), lowerTarget)
	if !reachable {
		for _, ev := range evidence {
			if strings.EqualFold(strings.TrimSpace(ev.CriterionID), "") {
				continue
			}
			if strings.Contains(strings.ToLower(ev.Command), lowerTarget) ||
				strings.Contains(strings.ToLower(ev.Output), lowerTarget) {
				reachable = true
				break
			}
		}
	}
	if reachable {
		return v
	}
	groundingReportsTotal.Add(1)
	logger.WarnCF("agent",
		"judge: met verdict's evidence_target is not reachable from the criterion text, the "+
			"workspace diff, or this attempt's machine-check evidence — provenance downgraded to "+
			"'none' (reporting only, JUDGE-FR-030a-lite); the Judge's Met/Reason are unchanged",
		map[string]any{
			"criterion_id": v.CriterionID, "evidence_source": string(v.EvidenceSource), "target": target,
		})
	v.Provenance = task.ProvenanceNone
	return v
}

// --- FR-006/FR-007a/FR-014a/FR-028/FR-029/FR-063a: grounding reports --------

// absenceAssertingPhrases mirrors JUDGE-FR-007a's shipped, closed phrase
// set for detecting an `unmet` reason that asserts an ABSENCE.
var absenceAssertingPhrases = []string{
	"not present", "does not contain", "no such", "absent", "missing",
	"could not find", "nowhere in",
}

// refusalAssertingPhrases is FR-063a's companion set: text FR-064 requires
// be stable and distinguishable from a not-found error — filesystem.go's
// refusal text (E0's write-set, not this wave's) is the authority on the
// EXACT string; this is the closed, shipped detector this wave owns.
var refusalAssertingPhrases = []string{
	"outside confinement", "confinement", "policy denied", "policy-denied", "refused", "not authorized",
}

func reasonAssertsAny(reason string, phrases []string) bool {
	lower := strings.ToLower(reason)
	for _, p := range phrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// reportGroundingIssues is the single REPORTING entry point for
// JUDGE-FR-006 (evidence entries below the criterion's persisted clause
// count), FR-014a (a session_read grounding a criterion that names a
// filesystem path), FR-028 (a met missing evidence_target where its
// source requires one), FR-029 (a met with no recognised evidence_source),
// FR-007a (an unmet whose reason asserts an absence — reduced fidelity,
// see below) and FR-063a (an unmet whose reason asserts a refusal —
// same). Every branch is WARN-and-count ONLY (D-B) — v is never mutated
// and is not even accepted by value-that-could-be-returned-differently;
// callers pass the ALREADY-FINAL v purely so this function can read it.
//
// FR-007a/FR-063a reduced fidelity, reported rather than silently
// narrowed: the spec's mechanical form needs to know whether THIS
// adjudication's read of evidence_target was truncated (FR-007a) or
// refused (FR-063a) — data that lives in the Judge's OWN investigation,
// which is FR-030's capture (see this file's doc comment for why that
// capture is unreachable from this wave's write-set). Absent that signal,
// this function detects the REASON-TEXT half of each rule only (the
// closed phrase sets above) — a criterion asserting absence/refusal in
// its own words — which is weaker than the mechanical trigger but still a
// real, reportable signal, and never claims to be the full mechanism.
func reportGroundingIssues(c task.AcceptanceCriterion, pc judgeCriterionResponse, v task.CriterionVerdict) {
	if v.Met {
		if c.ClauseCount > 0 && len(v.Evidence) > 0 && len(v.Evidence) < c.ClauseCount {
			groundingReportsTotal.Add(1)
			logger.WarnCF("agent",
				"judge: met verdict's evidence array carries fewer entries than the criterion's "+
					"persisted clause count — reported, not rewritten (D-B, JUDGE-FR-006)",
				map[string]any{
					"criterion_id": c.ID, "evidence_entries": len(v.Evidence), "clause_count": c.ClauseCount,
				})
		}
		if v.EvidenceSource == task.EvidenceSourceSessionRead && looksLikeFilesystemPath(c.Text) {
			groundingReportsTotal.Add(1)
			logger.WarnCF("agent",
				"judge: met verdict grounded via session_read for a criterion that names a "+
					"filesystem path — inspect_session cannot show file contents; reported, not "+
					"rewritten (D-B, JUDGE-FR-014a)",
				map[string]any{"criterion_id": c.ID, "target": v.EvidenceTarget})
		}
		requiresTarget := v.EvidenceSource == task.EvidenceSourceFileRead ||
			v.EvidenceSource == task.EvidenceSourceSessionRead ||
			v.EvidenceSource == task.EvidenceSourceMachineCheck
		if requiresTarget && strings.TrimSpace(v.EvidenceTarget) == "" {
			groundingReportsTotal.Add(1)
			logger.WarnCF("agent",
				"judge: met verdict's evidence_source requires a target but none was given — "+
					"reported, not rewritten (D-B, JUDGE-FR-028)",
				map[string]any{"criterion_id": c.ID, "evidence_source": string(v.EvidenceSource)})
		}
		if strings.TrimSpace(pc.EvidenceSource) != "" && v.EvidenceSource == "" {
			// A non-empty raw value that verdictFromJudgeResponse dropped as
			// unrecognised (task.IsValidVerdictEvidenceSource rejected it).
			groundingReportsTotal.Add(1)
			logger.WarnCF("agent",
				"judge: met verdict carries an unrecognised evidence_source — reported, not "+
					"rewritten (D-B, JUDGE-FR-029)",
				map[string]any{"criterion_id": c.ID, "raw_evidence_source": pc.EvidenceSource})
		}
		return
	}
	// v.Met == false: FR-007a/FR-063a's reduced-fidelity, reason-text-only
	// detectors.
	if reasonAssertsAny(v.Reason, absenceAssertingPhrases) {
		groundingReportsTotal.Add(1)
		logger.WarnCF("agent",
			"judge: unmet verdict's reason asserts an absence — reported at reduced fidelity "+
				"(reason-text only, the mechanical truncated-read trigger needs FR-030's capture, "+
				"unreachable from this wave — D-B, JUDGE-FR-007a)",
			map[string]any{"criterion_id": c.ID, "target": v.EvidenceTarget})
	}
	if reasonAssertsAny(v.Reason, refusalAssertingPhrases) {
		groundingReportsTotal.Add(1)
		logger.WarnCF("agent",
			"judge: unmet verdict's reason asserts a refusal, not a genuine not-found — reported "+
				"at reduced fidelity (reason-text only — D-B, JUDGE-FR-063a)",
			map[string]any{"criterion_id": c.ID, "target": v.EvidenceTarget})
	}
}

// --- FR-069a: the reproducibility trade, observed not merely asserted ------

// verdictOutcomeMemo is one unit+criterion's last-seen Met outcome and the
// investigation-log id that produced it (FR-069a: "both investigation-log
// ids").
type verdictOutcomeMemo struct {
	met   bool
	logID string
}

var (
	reproducibilityMu   sync.Mutex
	reproducibilityLast = map[string]verdictOutcomeMemo{}
)

// verdictReproducibilityDisagreements backs
// omnipus_judge_verdict_disagreement_total (JUDGE-FR-069a).
var verdictReproducibilityDisagreements atomic.Int64

// VerdictReproducibilityDisagreements returns the count of consecutive-
// adjudication disagreements observed since process start.
func VerdictReproducibilityDisagreements() int64 { return verdictReproducibilityDisagreements.Load() }

// noteAdjudicationReproducibility implements JUDGE-FR-069a: on a DIFFERENT
// outcome (Met) for the same unitID+criterionID than the immediately
// preceding adjudication recorded, log at WARN with both outcomes, both
// investigation-log ids and the criterion id, and increment the counter.
//
// Scope note (a reported, deliberate simplification of FR-069a's "already
// on disk" phrasing): this wave found no exported on-disk verdict-history
// reader in pkg/task (grep for SaveVerdict/LoadVerdict/VerdictHistory
// returns nothing), and pkg/task/store.go is outside this wave's
// write-set to add one to. The comparison is instead tracked in an
// in-process map, keyed identically to the disk-based comparison the spec
// describes (unitID+criterionID) — this satisfies FR-069a's actual
// requirement (the disagreement is observed and counted, not merely
// asserted) within one running process; it resets across a process
// restart, where the disk-based form would not.
func noteAdjudicationReproducibility(unitID, criterionID string, met bool, logID string) {
	if unitID == "" || criterionID == "" {
		return
	}
	key := unitID + "/" + criterionID
	reproducibilityMu.Lock()
	prev, had := reproducibilityLast[key]
	reproducibilityLast[key] = verdictOutcomeMemo{met: met, logID: logID}
	reproducibilityMu.Unlock()
	if !had || prev.met == met {
		return
	}
	verdictReproducibilityDisagreements.Add(1)
	logger.WarnCF("agent",
		"judge: verdict disagreement between consecutive adjudications of the same unit "+
			"(ADR-084 revision 9 trades reproducibility for auditability, deliberately — JUDGE-FR-069a)",
		map[string]any{
			"criterion_id": criterionID, "previous_met": prev.met, "current_met": met,
			"previous_investigation_log_id": prev.logID, "current_investigation_log_id": logID,
		})
}

// --- FR-067/FR-068/FR-069: the investigation log ----------------------------

// investigationLogCall is one entry of FR-067's ordered
// "(tool, target, bytes_returned, truncated)" log — target and
// bytes_returned/truncated are best-effort (see captureUnavailable).
type investigationLogCall struct {
	Tool               string `json:"tool"`
	Target             string `json:"target,omitempty"`
	BytesReturned      int    `json:"bytes_returned"`
	Truncated          bool   `json:"truncated"`
	CaptureUnavailable bool   `json:"capture_unavailable"`
}

// emitInvestigationLog implements FR-067/FR-069: one structured WARN-level
// (matching this file's other reporting lines — an install that wants
// these visible already filters at WARN) log line per adjudication,
// carrying the ordered tool-call summary, the model and the resolved
// timeout. NEVER added to the wire contract (FR-069) — this is a
// log.InfoCF/WarnCF call only, nothing persisted, nothing returned to a
// caller that could reach the SPA.
//
// calls is built by the caller (runVerifierAdjudication) from the Judge's
// OWN session transcript after its turn completes — durable Tool/Status
// fields only; bytes_returned/truncated are marked CaptureUnavailable
// (see file doc comment for why FR-068's capture-backed form is
// unreachable from this wave) rather than read from a stale Result.
func emitInvestigationLog(unitID, logID, model string, timeout time.Duration, calls []investigationLogCall) {
	logger.InfoCF("agent", "judge: investigation log (JUDGE-FR-067/FR-069)",
		map[string]any{
			"unit_id": unitID, "investigation_log_id": logID, "model": model,
			"resolved_timeout_s": timeout.Seconds(), "calls": calls,
		})
}

// buildInvestigationLogFromJudgeTranscript reads the Judge's OWN
// just-completed verifier-turn session (chatID) and renders it as FR-067's
// ordered call list. Best-effort: a read failure returns nil (never an
// error — the log is an observability nicety, never a hard requirement).
func (al *AgentLoop) buildInvestigationLogFromJudgeTranscript(judgeAgentID, chatID string) []investigationLogCall {
	if chatID == "" {
		return nil
	}
	var entries []session.TranscriptEntry
	if shared := al.GetSessionStore(); shared != nil {
		if e, err := shared.ReadTranscript(chatID); err == nil {
			entries = e
		}
	}
	if len(entries) == 0 {
		if store := al.GetAgentStore(judgeAgentID); store != nil {
			if e, err := store.ReadTranscript(chatID); err == nil {
				entries = e
			}
		}
	}
	if len(entries) == 0 {
		return nil
	}
	out := make([]investigationLogCall, 0, len(entries))
	for _, e := range entries {
		for _, tc := range e.ToolCalls {
			target := ""
			if v, ok := tc.Parameters["path"]; ok {
				if s, ok := v.(string); ok {
					target = s
				}
			} else if v, ok := tc.Parameters["session_id"]; ok {
				if s, ok := v.(string); ok {
					target = s
				}
			}
			out = append(out, investigationLogCall{
				Tool: tc.Tool, Target: target, CaptureUnavailable: true,
			})
		}
	}
	return out
}
