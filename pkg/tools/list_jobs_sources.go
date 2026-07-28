package tools

import (
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// This file holds `list_jobs`' three per-kind collectors and the narrow
// read-only store contracts they depend on.
//
// Every dependency is an interface rather than a concrete store, for two
// reasons: it keeps the tool honest about being read-only (there is no write
// method to call), and it lets the ordering, bounds and redaction properties
// be exercised against in-memory fakes with no filesystem at all.

// JobPlanLister is the read-only slice of *plan.Store that list_jobs needs.
type JobPlanLister interface {
	List(filter plan.Filter) ([]plan.Plan, error)
}

// JobTaskLister is the read-only slice of *task.Store that list_jobs needs.
type JobTaskLister interface {
	List(filter task.Filter) ([]task.Task, error)
}

// JobLifecycleLister is the read-only slice of *session.LifecycleStore that
// list_jobs needs.
type JobLifecycleLister interface {
	List(filter session.LifecycleFilter) ([]session.LifecycleRecord, error)
}

// The three *lenient* contracts below are OPTIONAL (FR-027). When a store also
// implements its lenient sibling, list_jobs uses it and reports a real
// `unreadable` count; when it does not, the strict List is used and
// `unreadable` stays 0 for that kind.
//
// This matters because the three stores have OPPOSITE per-record failure
// policies today: plan.Store.List and task.Store.List log a Warn and skip a
// corrupt file (so the caller never learns a record vanished), while
// session.LifecycleStore.List returns an error on the first corrupt record
// (so ONE bad file erases the whole kind). Neither is what FR-018 wants. The
// optional interfaces are the seam through which the lenient siblings, once
// they exist, light up `unreadable` without any change here.
type jobPlanLenientLister interface {
	ListLenient(filter plan.Filter) ([]plan.Plan, int, error)
}

type jobTaskLenientLister interface {
	ListLenient(filter task.Filter) ([]task.Task, int, error)
}

type jobLifecycleLenientLister interface {
	ListLenient(filter session.LifecycleFilter) ([]session.LifecycleRecord, int, error)
}

// JobAgentNamer resolves an agent id to its display name for a subagent row's
// label (FR-005). LifecycleRecord.AgentID stores an ID, not a name, so this is
// a required dependency rather than a nicety.
//
// A false second return is a NORMAL case, not a defect path: durable lifecycle
// records outlive the agents they name, so a deleted or renamed agent falls
// back to the raw id — never an empty label and never an error.
type JobAgentNamer interface {
	AgentDisplayName(agentID string) (string, bool)
}

// JobSessionResolver reports which delegate session ids are still resolvable
// in THIS process's in-memory delegate index, which is what decides whether a
// subagent row's handle can actually be acted on (FR-011).
//
// It is a BATCH accessor by contract (FR-028): the underlying index is guarded
// by the same mutex every delegate status/inbox/steer/respond/cancel call
// takes, and resolving one id per row would put a read-only visibility tool in
// contention with the live dispatch path — the exact thing FR-021 forbids for
// the plan engine's lock. list_jobs calls this at most once per invocation.
type JobSessionResolver interface {
	ResolvableSessionIDs(ids []string) map[string]bool
}

// JobCapSnapshotSource is FR-029's lock-free, read-only cap accessor on the
// plan engine.
//
// The reader MUST NOT take the engine's mutex. The snapshot is published from
// inside the engine's own admission path, which has already computed these
// values under that lock — so the number returned here is BY CONSTRUCTION the
// number admission used, not a second, lock-free re-derivation free to diverge
// from it. list_jobs must never call Admit: that takes the engine mutex
// exclusively and re-scans the plan store.
type JobCapSnapshotSource interface {
	CapSnapshot() (active, capMax int, reliable bool, observedAt, lastTickAt time.Time)
}

// collectResult is one kind's contribution to a roster.
type collectResult struct {
	rows []jobRow
	// unreadable counts records skipped because they could not be parsed
	// (FR-018). Non-zero only when the store implements its lenient sibling.
	unreadable int
	// scanTruncated is set when the per-kind scan ceiling bound this kind's
	// work, in which case every count above is a LOWER BOUND for this kind.
	scanTruncated bool
	scanned       int
	present       int
	// err is a STORE-level failure. It becomes an explicit per-kind error
	// entry in the response — never a silently short list, because a short
	// list that looks complete is the worst possible output.
	err error
}

// applyScanCeiling bounds the number of records one kind contributes to a
// single call (FR-032(d)).
//
// SCOPE NOTE, stated so it is not mistaken for the full requirement: this
// bounds the work list_jobs itself performs per kind (normalization,
// redaction, sorting) and reports it honestly. It does NOT bound the store's
// own I/O, because all three stores load every record inside List before
// returning a slice — capping that needs a store-side limit in pkg/plan,
// pkg/task and pkg/session. `present` is therefore the number of records the
// store actually returned for this filter, which is the largest population
// figure obtainable from this side of the API.
func applyScanCeiling[T any](records []T, ceiling int) (kept []T, scanned, present int, truncated bool) {
	present = len(records)
	if ceiling <= 0 || present <= ceiling {
		return records, present, present, false
	}
	return records[:ceiling], ceiling, present, true
}

// collectPlanRows returns the caller's own plan rows.
//
// Ownership is Plan.OwnerAgentID and nothing else. Plan.Owner and
// Plan.CreatedBy are mixed-namespace — the tool path writes an agent id and
// the REST path writes a username — so a human user whose username happens to
// equal a public agent id would otherwise have their plan titles disclosed to
// that agent. OwnerAgentID is required, validated and always an agent id on
// both write paths.
func collectPlanRows(
	store JobPlanLister,
	principal, workspaceID string,
	red redactor,
	ceiling int,
) collectResult {
	records, skipped, err := listPlansLeniently(store, plan.Filter{WorkspaceID: workspaceID})
	if err != nil {
		return collectResult{err: fmt.Errorf("plan store: %w", err)}
	}
	kept, scanned, present, truncated := applyScanCeiling(records, ceiling)
	res := collectResult{
		unreadable:    skipped,
		scanTruncated: truncated,
		scanned:       scanned,
		present:       present,
	}
	for i := range kept {
		p := &kept[i]
		// Fail closed on both sides: an unattributed plan is matched by
		// nobody, and the principal is already guaranteed non-empty.
		if p.OwnerAgentID == "" || p.OwnerAgentID != principal {
			continue
		}
		norm := normalizePlan(p)
		res.rows = append(res.rows, jobRow{
			Kind:                 jobKindPlan,
			ID:                   p.ID,
			Label:                red.redact(p.Title),
			Status:               norm.status,
			NativeStatus:         red.redact(norm.nativeStatus),
			Relation:             relationRuns,
			Attention:            norm.attention,
			StartedAt:            normalizeRFC3339(p.StartedAt),
			LastActivityAt:       normalizeRFC3339(p.LastActivityAt),
			WorkspaceID:          p.WorkspaceID,
			Actionable:           !terminalStatus(norm.status),
			IntentionallyStopped: norm.stopped,
			draftRank:            norm.draftRank,
			unmapped:             norm.unmapped,
		})
	}
	return res
}

// collectTaskRows returns the caller's own STANDALONE task rows — plan member
// tasks are deliberately excluded, because a plan already appears as its own
// row and listing its members again would bury the roster under one plan's
// decomposition.
//
// Ownership is the UNION of two agent-id-namespaced readings, applied in one
// pass so a task matching both appears exactly once:
//
//	AgentID == caller          -> relation "runs"       (work assigned to me)
//	CreatedByAgent(caller)     -> relation "dispatched" (work I created)
//
// `runs` wins when both match. The union matters: an agent executing a live
// in_progress task that a human assigned to it is the single most literal
// reading of "what am I still working on?", and a created-by-only predicate
// returns nothing for it.
func collectTaskRows(
	store JobTaskLister,
	principal, workspaceID string,
	red redactor,
	ceiling int,
) collectResult {
	records, skipped, err := listTasksLeniently(store, task.Filter{WorkspaceID: workspaceID})
	if err != nil {
		return collectResult{err: fmt.Errorf("task store: %w", err)}
	}
	kept, scanned, present, truncated := applyScanCeiling(records, ceiling)
	res := collectResult{
		unreadable:    skipped,
		scanTruncated: truncated,
		scanned:       scanned,
		present:       present,
	}
	for i := range kept {
		t := &kept[i]
		// Standalone only. task.Filter treats an empty PlanID as "filter off",
		// so this predicate is inexpressible at the store level today and is
		// applied here — before the bounds, so the omission counts stay exact
		// against the filtered population.
		if t.PlanID != "" {
			continue
		}
		assigned := t.AgentID != "" && t.AgentID == principal
		// CreatedByAgent is THE dispatched predicate. Never CreatedBy: it is
		// mixed-namespace (rest_tasks.go writes a username into it) and using
		// it would disclose a human's task titles to a same-named agent.
		created := t.CreatedByAgent(principal)
		if !assigned && !created {
			continue
		}
		relation := relationDispatched
		if assigned {
			relation = relationRuns
		}
		norm := normalizeTask(t)
		res.rows = append(res.rows, jobRow{
			Kind:         jobKindTask,
			ID:           t.ID,
			Label:        red.redact(t.Title),
			Status:       norm.status,
			NativeStatus: red.redact(norm.nativeStatus),
			Relation:     relation,
			Attention:    norm.attention,
			StartedAt:    normalizeRFC3339(t.StartedAt),
			// task.Task has no LastActivityAt; UpdatedAt is the documented
			// fallback (FR-003).
			LastActivityAt: normalizeRFC3339(t.UpdatedAt),
			WorkspaceID:    t.WorkspaceID,
			Actionable:     !terminalStatus(norm.status),
			// A deliberately stopped task is indistinguishable from a crashed
			// one: task.Status has no `cancelled` value and CancelReason is
			// written on one path only. This is a KNOWN BLIND SPOT stated as
			// such, not a derivation — revisit if CancelReason becomes a
			// closed, always-populated stop-intent enum.
			IntentionallyStopped: false,
			unmapped:             norm.unmapped,
		})
	}
	return res
}

// collectSubagentRows returns the sessions this caller delegated.
//
// Parentage is LifecycleRecord.ParentAgentID and nothing else. It must never
// be inferred from ParentDurableKey (which a parent SHARES with its children
// and every cousin in the subtree — inferring from it leaks grandchildren),
// from ScopeID (empty for a top-level delegation), or from AgentID (the
// CHILD's id, not the parent's).
func collectSubagentRows(
	store JobLifecycleLister,
	principal, workspaceID string,
	red redactor,
	ceiling int,
	namer JobAgentNamer,
	resolver JobSessionResolver,
) collectResult {
	filter := session.LifecycleFilter{WorkspaceID: workspaceID, ParentAgentID: principal}
	records, skipped, err := listLifecycleLeniently(store, filter)
	if err != nil {
		return collectResult{err: fmt.Errorf("lifecycle store: %w", err)}
	}
	kept, scanned, present, truncated := applyScanCeiling(records, ceiling)
	res := collectResult{
		unreadable:    skipped,
		scanTruncated: truncated,
		scanned:       scanned,
		present:       present,
	}

	// One row per SESSION, representing its newest generation (FR-034). A
	// resumed session mints a NEW record rather than mutating the terminal
	// one, so without this a followed-up session would show both a live row
	// and a stale terminal row under include_terminal=true.
	newest := newestGenerations(kept, principal)

	rows := make([]jobRow, 0, len(newest))
	ids := make([]string, 0, len(newest))
	for i := range newest {
		rec := newest[i]
		norm := normalizeSubagent(rec)
		rows = append(rows, jobRow{
			Kind:         jobKindSubagent,
			ID:           rec.SessionID,
			Label:        red.redact(subagentLabel(rec, namer)),
			Status:       norm.status,
			NativeStatus: red.redact(norm.nativeStatus),
			Relation:     relationDispatched,
			Attention:    norm.attention,
			// LifecycleRecord has no start timestamp at all; CreatedAt is the
			// documented fallback (FR-003).
			StartedAt:            rfc3339UTC(rec.CreatedAt),
			LastActivityAt:       rfc3339UTC(rec.UpdatedAt),
			WorkspaceID:          rec.WorkspaceID,
			IntentionallyStopped: norm.stopped,
			Generation:           rec.Generation,
			unmapped:             norm.unmapped,
		})
		ids = append(ids, rec.SessionID)
	}

	// Exactly ONE resolver call for the whole batch, never one per row.
	var resolvable map[string]bool
	if resolver != nil {
		resolvable = resolver.ResolvableSessionIDs(ids)
	}
	for i := range rows {
		// Terminal rows are never actionable, for every kind. A subagent row
		// is additionally not actionable when its session no longer resolves
		// in this process — a durable record survives a restart, the in-memory
		// index does not. With no delegate tool wired, nothing resolves, which
		// is the honest answer rather than an error.
		rows[i].Actionable = !terminalStatus(rows[i].Status) && resolvable[rows[i].ID]
	}
	res.rows = rows
	return res
}

// newestGenerations keeps, per session id, only the record with the highest
// generation — and re-checks the parentage predicate on every record rather
// than trusting the store filter alone, so a store whose filter is ever
// widened cannot leak another principal's sessions through this path.
func newestGenerations(records []session.LifecycleRecord, principal string) []*session.LifecycleRecord {
	byID := make(map[string]*session.LifecycleRecord, len(records))
	order := make([]string, 0, len(records))
	for i := range records {
		rec := &records[i]
		if rec.ParentAgentID == "" || rec.ParentAgentID != principal {
			continue
		}
		prev, ok := byID[rec.SessionID]
		if !ok {
			byID[rec.SessionID] = rec
			order = append(order, rec.SessionID)
			continue
		}
		if rec.Generation > prev.Generation {
			byID[rec.SessionID] = rec
		}
	}
	out := make([]*session.LifecycleRecord, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

// subagentLabel resolves the delegated agent's display name, falling back to
// the raw agent id when the agent no longer resolves.
func subagentLabel(rec *session.LifecycleRecord, namer JobAgentNamer) string {
	if namer != nil {
		if name, ok := namer.AgentDisplayName(rec.AgentID); ok && strings.TrimSpace(name) != "" {
			return name
		}
	}
	return rec.AgentID
}

// listPlansLeniently prefers the lenient sibling when the store has one.
func listPlansLeniently(store JobPlanLister, filter plan.Filter) ([]plan.Plan, int, error) {
	if lenient, ok := store.(jobPlanLenientLister); ok {
		return lenient.ListLenient(filter)
	}
	records, err := store.List(filter)
	return records, 0, err
}

// listTasksLeniently prefers the lenient sibling when the store has one.
func listTasksLeniently(store JobTaskLister, filter task.Filter) ([]task.Task, int, error) {
	if lenient, ok := store.(jobTaskLenientLister); ok {
		return lenient.ListLenient(filter)
	}
	records, err := store.List(filter)
	return records, 0, err
}

// listLifecycleLeniently prefers the lenient sibling when the store has one.
func listLifecycleLeniently(
	store JobLifecycleLister,
	filter session.LifecycleFilter,
) ([]session.LifecycleRecord, int, error) {
	if lenient, ok := store.(jobLifecycleLenientLister); ok {
		return lenient.ListLenient(filter)
	}
	records, err := store.List(filter)
	return records, 0, err
}
