// Omnipus — Skill-call audit records (ADR-072 D3.1, MAJ-006)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// skill_call.go implements the observability half of ADR-072's biggest
// accepted risk (§3: "a badly-described skill simply never fires", D2's
// mitigation). D2 has no way to tell, in production, whether it is working —
// so every `Skill` tool call is audited here: the slug requested, the mode
// (load/search), the outcome (loaded/denied/not_found), the shelf it
// resolved from, and the acting agent and workspace
// (docs/internal/architecture/ADR-072-skill-activation-and-loading.md D3.1;
// docs/internal/specs/skill-activation-and-loading-spec.md FR-018, FR-018a,
// FR-018b, FR-019, FR-020).
//
// Render-visibility and audit are different questions (N6): D3 hides a
// SUCCESSFUL `Skill` call from the chat thread by default
// (src/toolVisibility.ts), but that is a rendering decision only — every
// outcome, including a denial the user never sees in the thread, is recorded
// here (FR-019).
//
// # Record shape vs. the FR-071a write-audit sibling
//
// FR-018c: "FR-018's call records and FR-071a's write records are two
// distinct record shapes under one audit event kind, distinguished by their
// own field sets; neither is a superset of the other and neither uses
// optional fields to impersonate the other." The FR-071a write-audit sibling
// (pkg/tools/resolvepath.go's emitSkillPathWriteAudit) already exists and
// uses the classic Entry{Event, Decision, AgentID, SessionID, Tool, Details}
// shape with SkillWriteAuditEvent = "skill.write" — that file's own doc
// comment flags the shared-kind reconciliation as work for "the phase
// implementing D3.1/FR-018" (this file). Reconciled here: EventSkillCall
// ("skill.call") is registered in IsValidEventName alongside "skill.write"
// (previously unregistered, which meant every write-audit entry it wrote
// tripped IsValidEventName's warn-once). The two record shapes share the
// "skill.*" event-name family and the classic Entry envelope, but carry
// disjoint Details keys — a call record's {slug, mode, outcome, shelf,
// workspace_id} never includes {path, op}, and vice versa.
//
// # Wiring gap (documented, not this file's job to close)
//
// EmitSkillCall is the primitive; nothing in this codebase calls it yet.
// pkg/tools/skill.go's SkillTool (ADR-072 D1) implements the tool's shape
// and search but is deliberately not registered or resolver-wired — its own
// header comment defers that to "a later integration phase that also wires
// SetResolver". Wiring EmitSkillCall into SkillTool's execLoad/execSearch
// (or into whatever loop.go integration point ends up calling SkillTool) is
// that same later integration phase's job, not this one's — this phase's
// assigned scope is the reminder (skill_reminder.go) and the audit
// primitives themselves. See this task's final report for the explicit
// call-site contract the integration phase should follow.
package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"time"
)

// SkillCallMode is the closed set of modes a Skill-tool call audit record
// may carry (ADR-072 D3.1, FR-018a). A D9 delegate preload audits as
// SkillCallModeLoad (FR-018b) — it is a load performed on the child's
// behalf, not a fourth mode.
type SkillCallMode string

const (
	// SkillCallModeLoad — the tool's load-by-slug path (`name` argument).
	SkillCallModeLoad SkillCallMode = "load"
	// SkillCallModeSearch — the tool's search-by-query path (`query` argument).
	SkillCallModeSearch SkillCallMode = "search"
)

// IsValidSkillCallMode reports whether m is one of the two recognized modes.
// FR-018a: "No other values are permitted; a test MUST assert against this
// closed set rather than against 'N distinct values'" — see
// TestAudit_ModeAndOutcomeClosedSet.
func IsValidSkillCallMode(m SkillCallMode) bool {
	switch m {
	case SkillCallModeLoad, SkillCallModeSearch:
		return true
	}
	return false
}

// SkillCallOutcome is the closed set of outcomes a Skill-tool call audit
// record may carry (ADR-072 D3.1, FR-018a). Mirrors pkg/tools.SkillLoadStatus
// (SkillLoadLoaded/SkillLoadDenied/SkillLoadNotFound) one layer up, as plain
// strings so the audit package carries no dependency on pkg/tools.
type SkillCallOutcome string

const (
	// SkillCallOutcomeLoaded — the requested skill was resolved and its
	// content returned for this turn.
	SkillCallOutcomeLoaded SkillCallOutcome = "loaded"
	// SkillCallOutcomeDenied — the requested skill exists but the acting
	// agent is not granted it (ADR-072 D4's per-shelf grant check).
	SkillCallOutcomeDenied SkillCallOutcome = "denied"
	// SkillCallOutcomeNotFound — no skill by that slug exists on any shelf
	// visible to the acting agent (ADR-072 D4/FR-054's SkillNotFoundCode).
	SkillCallOutcomeNotFound SkillCallOutcome = "not_found"
)

// IsValidSkillCallOutcome reports whether o is one of the three recognized
// outcomes. See IsValidSkillCallMode's doc comment for the closed-set test
// requirement this mirrors (FR-018a).
func IsValidSkillCallOutcome(o SkillCallOutcome) bool {
	switch o {
	case SkillCallOutcomeLoaded, SkillCallOutcomeDenied, SkillCallOutcomeNotFound:
		return true
	}
	return false
}

// EmitSkillCall records one Skill-tool call (ADR-072 D3.1, FR-018/FR-018a/
// FR-018b/FR-019). slug is the slug requested — the load-path's exact `name`
// argument, or "" for a search call (a free-text query names no single slug;
// FR-018's "slug requested" language is the load path's own identifier).
// shelf is one of pkg/skills' three shelf discriminators ("project" |
// "registry" | "builtin"), or "" when the call never resolved to one (a
// denied or not-found load, or a search call — search ranks across every
// shelf and its match list is not attributable to a single one).
//
// Best-effort, mirrors emitSkillPathWriteAudit's contract exactly: a nil
// logger is a silent no-op (audit disabled), and a Log failure is logged via
// slog rather than returned or blocking the caller — this MUST fire even for
// an outcome hidden from the chat thread by default (D3's toolVisibility
// hide-on-success), and it must never be able to fail the turn itself over
// an audit-append failure (FR-019: render-hiding and audit are different
// questions).
//
// An invalid mode or outcome (should never happen from a caller using the
// SkillCallMode/SkillCallOutcome constants) is warned once via slog and the
// entry is still written with whatever value was passed — losing audit data
// over a caller bug is worse than logging an out-of-set value, matching
// IsValidEventName's own "warn-once, never reject" philosophy.
func EmitSkillCall(l *Logger, agentID, workspaceID, slug string, mode SkillCallMode, outcome SkillCallOutcome, shelf string) {
	if l == nil {
		return
	}
	if !IsValidSkillCallMode(mode) {
		slog.Warn("audit: EmitSkillCall called with an out-of-set mode", "mode", string(mode), "slug", slug)
	}
	if !IsValidSkillCallOutcome(outcome) {
		slog.Warn("audit: EmitSkillCall called with an out-of-set outcome", "outcome", string(outcome), "slug", slug)
	}

	decision := DecisionDeny
	if outcome == SkillCallOutcomeLoaded {
		decision = DecisionAllow
	}

	entry := &Entry{
		Timestamp: time.Now().UTC(),
		Event:     EventSkillCall,
		Decision:  decision,
		AgentID:   agentID,
		Details: map[string]any{
			"slug":         slug,
			"mode":         string(mode),
			"outcome":      string(outcome),
			"shelf":        shelf,
			"workspace_id": workspaceID,
		},
	}
	if err := l.Log(entry); err != nil {
		slog.Warn("audit: EmitSkillCall log write failed", "error", err, "agent_id", agentID, "slug", slug)
	}
}

// LastInvokedForSkill answers ADR-072 D3.1's cheapest observability question
// for a granted skill: when was it last actually REQUESTED by name through
// the Skill tool's load path. This is the primitive a Skills-view "last
// invoked" column (FR-020, test 54, frontend-owned) is built on — it is
// deliberately NOT the REST/API shape itself (this package has no HTTP
// concerns), just the queryable answer: given a slug, when (if ever) was it
// last loaded.
//
// Only mode == "load" entries count. A search match only ranks a skill into
// a result list — it is not a request for that skill by name, so it does not
// move last_invoked (a skill that only ever shows up in search results but
// is never actually loaded is exactly the "stale last_invoked" signal D3.1
// wants visible, and counting search hits here would mask it). Both
// "loaded" and "denied" load outcomes count: a denied load still proves the
// model asked for the skill by name — the description worked, the grant did
// not — which is precisely what ADR-072 A12's runbook needs to distinguish
// "never fires" (check 1: bad description) from "granted wrong" (check 2:
// missing grant). A "not_found" load naming this exact slug cannot occur for
// a slug that itself resolves to something (not_found means no skill on any
// shelf matches the requested name at all), so it never competes here.
//
// Scope bound: matches LastWriterForPath's own documented, deliberate
// current-file + single-most-recent-rotated-file bound (see that function's
// doc comment in query.go) — this is a UI-informational lookup, not a
// security decision, so the same cost/benefit line applies. A call further
// back than that reads as "never invoked" rather than as a hard error.
//
// Best-effort, never blocking: returns (zero time, false) on a nil logger,
// an empty slug, a degraded logger, an unreadable/unscannable audit file, or
// no match — mirroring LastWriterForPath's no-error-return contract exactly.
func (l *Logger) LastInvokedForSkill(slug string) (invokedAt time.Time, found bool) {
	if l == nil || slug == "" {
		return time.Time{}, false
	}

	l.mu.Lock()
	degraded := l.degraded
	dir := l.dir
	auditFile := l.auditPath()
	l.mu.Unlock()

	if degraded {
		slog.Debug("audit: LastInvokedForSkill skipped — logger is degraded", "slug", slug)
		return time.Time{}, false
	}

	files := make([]string, 0, 2)
	if rotated, ok := mostRecentRotatedFile(dir); ok {
		files = append(files, rotated)
	}
	files = append(files, auditFile)

	for _, filePath := range files {
		matches, err := scanFileForSkillCallLoads(filePath, slug)
		if err != nil {
			slog.Warn("audit: LastInvokedForSkill could not scan a file — the answer may be stale or missing",
				"file", filePath, "slug", slug, "error", err)
			continue
		}
		for _, ts := range matches {
			if !found || ts.After(invokedAt) {
				invokedAt = ts
				found = true
			}
		}
	}

	return invokedAt, found
}

// scanFileForSkillCallLoads reads filePath fully into memory (closed before
// scanning — see LastWriterForPath's Windows note in query.go, which applies
// identically here) and returns the Timestamp of every EventSkillCall entry
// whose Details["mode"] == "load" and Details["slug"] == slug, regardless of
// decision (see LastInvokedForSkill's doc comment for why both outcomes
// count).
func scanFileForSkillCallLoads(filePath, slug string) ([]time.Time, error) {
	// #nosec G304 -- filePath is always either l.auditPath() or the result of
	// mostRecentRotatedFile(l.dir), both rooted in the deployment-configured
	// audit directory, never request-derived. Mirrors scanFileForLastWriter's
	// identical justification in query.go.
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var out []time.Time
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if jsonErr := json.Unmarshal(line, &e); jsonErr != nil {
			continue
		}
		if e.Event != EventSkillCall {
			continue
		}
		mode, modeOK := e.Details["mode"].(string)
		if !modeOK || mode != string(SkillCallModeLoad) {
			continue
		}
		entrySlug, slugOK := e.Details["slug"].(string)
		if !slugOK || entrySlug != slug {
			continue
		}
		out = append(out, e.Timestamp)
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, scanErr
	}

	return out, nil
}
