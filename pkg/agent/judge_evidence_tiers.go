// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_evidence_tiers.go implements ADR-084 revision 9's §Q, D14 — the
// three-tier evidence-before-judgement ordering (JUDGE-FR-104/FR-105/
// FR-106/FR-108) that applies to every kind of goal, coding or not:
//
//	Tier 1 (universal):    the working agent's own tool-call record —
//	                        rendered into the Judge's evidence block
//	                        (renderTranscriptEntriesForWindow,
//	                        verifier_adjudication.go — FR-105/FR-106).
//	Tier 2 (opt-in, coding-shaped): a criterion-DECLARED check
//	                        (task.KindCheck, rung 1 — judge.go's
//	                        runMachineCheck, unchanged in substance,
//	                        FR-036/FR-038/FR-039/FR-040) or a criterion-
//	                        DECLARED behaviour payload (task.KindBehavior,
//	                        rung 2 — behavior_scan.go's runBehaviorScan,
//	                        which this file's applyBehaviorContradiction
//	                        VETO layers a second, narrower contradiction
//	                        check onto — FR-108).
//	Tier 3 (default):      the Judge, dispatched with tiers 1 and 2 already
//	                        in its evidence block (runVerifierAdjudication,
//	                        verifier_adjudication.go).
//
// FR-104's "consulted in order, once, when a claim arrives — not
// continuously" is already the shape of JudgeCriteria's existing rung
// dispatch (judge.go): machineCriteria and behaviorCriteria are evaluated
// exactly once, before proseCriteria's single windowText/diffText
// assembly (computed once at the top of runVerifierAdjudication, BEFORE
// its retry loop — a retried attempt reuses the same tier-1/tier-2
// evidence rather than re-assembling it). This file adds no new
// orchestration loop; it adds the rendering upgrade (FR-105/FR-106) and
// the one genuinely new veto (FR-108 case 2) that D14 requires beyond
// what rung 1/rung 2 already compute.
//
// D-B (operator decision, corroborated by GOAL-FR-038/FR-039): every
// value this file produces for a PROSE (tier-3) criterion is a REPORTING
// obligation, never a gate — it exists to make a weak `met` visible, not
// to overrule the Judge. The one exception, and it is deliberately NOT a
// grounding/reporting control: FR-108's contradiction veto is a
// mechanically-decidable fact about a DECLARED tier-2 payload
// (task.KindBehavior), settled before the Judge is ever asked about that
// criterion — the Judge never sees it and there is nothing to overrule.
package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- FR-105/FR-106: tier-1 tool-call rendering bounds -----------------------

const (
	// tierOneParamRuneLimit is FR-105's "parameters truncated to 512 runes
	// per call with an explicit elision marker" bound. Applied to the
	// rendered parameters JSON and to a rendered Error string alike (both
	// are UNTRUSTED, model/tool-controlled text reaching the Judge's own
	// prompt).
	tierOneParamRuneLimit = 512

	// tierOneBlockByteLimit is FR-105's "the whole tier-1 block to 32 KiB,
	// dropping oldest-first with a stated count of what was dropped" bound.
	// Applied only to the tool-call-summary messages renderTranscript
	// EntriesForWindow produces — never to the plain narration/content
	// messages, which are bounded separately by the existing token-budget
	// trim in renderVerifierWindowText.
	tierOneBlockByteLimit = 32 * 1024

	// tierOneTruncationMarker is FR-105's "explicit elision marker".
	tierOneTruncationMarker = "…[truncated, FR-105]"
)

// truncateRunesWithMarker returns s truncated to at most limit code points
// (rune-safe — never splits mid-rune, matching judge.go's
// truncateEvidenceQuote precedent), appending tierOneTruncationMarker when
// truncation actually occurred. Unlike truncateEvidenceQuote (which must
// stay verbatim evidence for grounding), this rendering is informational
// only, so an explicit marker is required rather than forbidden.
func truncateRunesWithMarker(s string, limit int) string {
	if limit <= 0 || s == "" {
		return s
	}
	n := 0
	cut := -1
	for i := range s {
		if n == limit {
			cut = i
			break
		}
		n++
	}
	if cut == -1 {
		return s // at or under the limit already
	}
	return s[:cut] + tierOneTruncationMarker
}

// tierOneRenderOptions carries the optional, production-only behaviour
// renderTranscriptEntriesForWindow/renderVerifierWindowText accept via a
// variadic functional-option tail — added THIS way, rather than as new
// required parameters, so every pre-existing call site (including
// pkg/agent/verifier_adjudication_test.go's four direct calls, which are
// E9's write-set and outside this wave's) keeps compiling unchanged.
type tierOneRenderOptions struct {
	// redact is applied to every rendered parameters/error string before
	// it reaches the Judge's prompt — the "existing RegisterSensitiveValues
	// path" FR-105 requires (config.Config.FilterSensitiveData, the same
	// redactor persistEvidence's EvidenceStore already uses, judge.go). nil
	// (the zero-value default, e.g. in every existing test call) means no
	// redaction — identical to today's behaviour.
	redact func(string) string
}

// tierOneRenderOption mutates a tierOneRenderOptions being assembled.
type tierOneRenderOption func(*tierOneRenderOptions)

// withTierOneRedact supplies FR-105's redaction hook. Production call
// sites (verifier_adjudication.go's sessionWindowText) pass
// al.tierOneRedactFn(); test call sites normally omit this option
// entirely.
func withTierOneRedact(fn func(string) string) tierOneRenderOption {
	return func(o *tierOneRenderOptions) { o.redact = fn }
}

// tierOneRedactFn returns al's FR-105 redaction hook: config's own
// sensitive-value filter (RegisterSensitiveValues' consuming side,
// config.Config.FilterSensitiveData) when a config is reachable, or the
// identity function otherwise — mirrors judge.go's evidenceStore() redact
// closure precedent exactly (same source of truth, same fallback).
func (al *AgentLoop) tierOneRedactFn() func(string) string {
	return func(s string) string {
		if al == nil {
			return s
		}
		if cfg := al.GetConfig(); cfg != nil {
			return cfg.FilterSensitiveData(s)
		}
		return s
	}
}

// formatTierOneToolCall renders one session.ToolCall as FR-105's
// "{tool, parameters, status, error}" — durable fields ONLY (FR-106):
// Tool, Status, Parameters and Error are never touched by
// empty_in_place.go's mid-turn Result rewrite (ADR-066 D5), unlike Result
// itself, which this function never reads. When ContentState records that
// Result WAS projected away ("emptied"/"capped"), an explicit marker is
// rendered instead of silence, so the tier-1 fact reads as "this call's
// result was redacted from view", never as "this tool returned nothing" —
// the FR-106 failure mode (a rewritten Result read naively as absence,
// contradicting and vetoing a true claim) this function exists to close.
func formatTierOneToolCall(tc session.ToolCall, redact func(string) string) string {
	if redact == nil {
		redact = func(s string) string { return s }
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "[tool_call] %s -> %s", tc.Tool, tc.Status)
	if len(tc.Parameters) > 0 {
		if b, err := json.Marshal(tc.Parameters); err == nil {
			params := truncateRunesWithMarker(redact(string(b)), tierOneParamRuneLimit)
			fmt.Fprintf(&sb, " params=%s", params)
		}
	}
	switch tc.ContentState {
	case "emptied", "capped":
		// FR-106: never rendered as absence — an explicit projection marker,
		// distinct from "no result was ever produced".
		fmt.Fprintf(&sb, " result=[projected away from this window — durable fields only, FR-106]")
	}
	if strings.TrimSpace(tc.Error) != "" {
		errText := truncateRunesWithMarker(redact(tc.Error), tierOneParamRuneLimit)
		fmt.Fprintf(&sb, " error=%s", errText)
	}
	return sb.String()
}

// --- FR-108: the tier-1 contradiction veto ----------------------------------

// applyBehaviorContradictionVeto implements FR-108 case 2 for a KindBehavior
// criterion whose rung-2 verdict (runBehaviorScan, behavior_scan.go —
// UNMODIFIED, per this wave's Must-NOT-touch) already resolved Met.
//
// Case 1 ("the criterion declares a KindBehavior payload and the observed
// count violates it") is explicitly "already rung 2's own verdict; nothing
// new" (FR-108) — runBehaviorScan already fails MinCount/MaxCount closed,
// so nothing is added here for it.
//
// Case 2 ("every recorded call of the declared tool has Status == error —
// the agent tried and it failed") is genuinely new: it matters exactly
// when MinCount permits zero calls (an optional tool) and every attempt
// the agent actually made errored. runBehaviorScan counts only SUCCESSFUL
// calls (DS-7 "failed-calls-not-counted"), so that scenario reads as
// Observed==0, which satisfies MinCount==0 and comes back Met:true — a
// pass that is technically correct on the count alone but silently hides
// a real, repeated failure the operator would want surfaced. This veto
// closes exactly that gap, mechanically and narrowly: it fires ONLY when
// (a) the criterion declares a Behavior payload, (b) at least one call of
// that tool was recorded, and (c) EVERY recorded call of it has
// Status=="error". A tool never called at all, or called with at least
// one success or denial, is left untouched (D14's "everything else is
// informative, not decisive").
func applyBehaviorContradictionVeto(
	entries []session.TranscriptEntry, c task.AcceptanceCriterion, v task.CriterionVerdict,
) task.CriterionVerdict {
	if c.Kind != task.KindBehavior || c.Behavior == nil || strings.TrimSpace(c.Behavior.Tool) == "" {
		return v
	}
	if !v.Met {
		return v // nothing to veto — already unmet
	}
	seen := 0
	allErrored := true
	var lastErr string
	for _, e := range entries {
		for _, tc := range e.ToolCalls {
			if tc.Tool != c.Behavior.Tool {
				continue
			}
			seen++
			if tc.Status != "error" {
				allErrored = false
				continue
			}
			lastErr = tc.Error
		}
	}
	if seen == 0 || !allErrored {
		return v // "never called" and "any non-error call" are both informative-only (D14)
	}
	logger.WarnCF("agent",
		"judge: tier-1 contradiction veto — every recorded call of a criterion's declared "+
			"behaviour tool errored (JUDGE-FR-108 case 2)",
		map[string]any{"criterion_id": c.ID, "tool": c.Behavior.Tool, "calls": seen, "last_error": lastErr})
	v.Met = false
	v.Reason = fmt.Sprintf(
		"tier-1 contradiction (FR-108): every recorded call of %q errored (last error: %s); original reason: %s",
		c.Behavior.Tool, lastErr, v.Reason,
	)
	return v
}

// resolveBehaviorScanEntries mirrors behavior_scan.go::runBehaviorScan's own
// session resolution EXACTLY (task scope -> the task's own session via the
// task store; goal scope -> in.GoalSessionID; read via
// al.GetAgentStore(in.AssigneeAgentID) — deliberately NOT the shared-store-
// first fallback the window-text feeds use, so FR-108's veto scans the
// IDENTICAL entries rung 2's own count already scanned) so
// applyBehaviorContradictionVeto sees the same tool-call log
// runBehaviorScan itself just read. behavior_scan.go stays untouched
// (FR-107) — this is a second, independent read of the same session, not a
// call into that file. Best-effort: any resolution failure returns nil
// (the veto then correctly no-ops, same as "tool never called").
func (al *AgentLoop) resolveBehaviorScanEntries(in JudgeCriteriaInput) []session.TranscriptEntry {
	var sessionID string
	switch {
	case in.TaskID != "":
		ts := GetTaskStore(al)
		if ts == nil {
			return nil
		}
		t, err := ts.Get(in.TaskID)
		if err != nil || t == nil {
			return nil
		}
		sessionID = t.SessionID
	case in.GoalSessionID != "":
		sessionID = in.GoalSessionID
	default:
		return nil
	}
	if sessionID == "" || in.AssigneeAgentID == "" {
		return nil
	}
	store := al.GetAgentStore(in.AssigneeAgentID)
	if store == nil {
		return nil
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		return nil
	}
	return entries
}
