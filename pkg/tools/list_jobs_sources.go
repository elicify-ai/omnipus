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
// IT MUST BE CALLED ON THE CALLER'S OWN RECORDS, AFTER the ownership /
// parentage predicate — never on the raw store result. The predicates are
// string compares over records already in memory, so running them first is
// free, and running them second spends the whole budget on OTHER principals'
// records before the caller's are even considered. Both stores sort
// adversarially for that mistake: plan.Store.List sorts CreatedAt ASCENDING
// (so a ceiling applied first keeps the OLDEST and discards exactly the
// current work this tool exists to report) and task.Store.List sorts
// EffectivePriority then CreatedAt ascending. A caller owning three recent
// plans in a workspace holding 6000 would get `rows: []` plus a
// notes.scan_truncated the model has no reason to interpret — an empty roster
// reads as "I have no outstanding work".
//
// SCOPE NOTE, stated so it is not mistaken for the full requirement: this
// bounds the work list_jobs itself performs per kind (normalization,
// redaction, sorting) and reports it honestly. It does NOT bound the store's
// own I/O, because all three stores load every record inside List before
// returning a slice — capping that needs a store-side limit in pkg/plan,
// pkg/task and pkg/session. `present` is therefore the number of records
// belonging to THIS CALLER that the store returned for this filter, which is
// both the population the ceiling actually bounds and the only one a
// scan_truncated marker can mean anything about.
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
	// OWNERSHIP FIRST, ceiling second. plan.Filter carries only WorkspaceID, so
	// this predicate is the only thing narrowing the store's result to the
	// caller — applying the ceiling before it would spend the entire budget on
	// other principals' plans (and, given List's CreatedAt-ascending order, on
	// the OLDEST of them). See applyScanCeiling's doc.
	owned := make([]*plan.Plan, 0, len(records))
	for i := range records {
		p := &records[i]
		// Fail closed on both sides: an unattributed plan is matched by
		// nobody, and the principal is already guaranteed non-empty.
		if p.OwnerAgentID == "" || p.OwnerAgentID != principal {
			continue
		}
		owned = append(owned, p)
	}
	kept, scanned, present, truncated := applyScanCeiling(owned, ceiling)
	res := collectResult{
		unreadable:    skipped,
		scanTruncated: truncated,
		scanned:       scanned,
		present:       present,
	}
	for _, p := range kept {
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
	// OWNERSHIP AND THE PLAN-MEMBER EXCLUSION FIRST, ceiling second. task.Filter
	// carries only WorkspaceID here, and the task store is the fastest-growing
	// of the three (every set_todos scratchpad card is a task), so a ceiling
	// applied first would spend the budget on other principals' tasks — and,
	// given List's EffectivePriority-ascending order, discard the caller's own
	// lower-priority ones first. See applyScanCeiling's doc.
	owned := make([]*task.Task, 0, len(records))
	for i := range records {
		t := &records[i]
		// Standalone only. task.Filter treats an empty PlanID as "filter off",
		// so this predicate is inexpressible at the store level today and is
		// applied here — before the ceiling and before the bounds, so both the
		// scan budget and the omission counts stay exact against the population
		// the caller actually owns.
		if t.PlanID != "" {
			continue
		}
		// CreatedByAgent is THE dispatched predicate. Never CreatedBy: it is
		// mixed-namespace (rest_tasks.go writes a username into it) and using
		// it would disclose a human's task titles to a same-named agent.
		if !taskAssignedTo(t, principal) && !t.CreatedByAgent(principal) {
			continue
		}
		owned = append(owned, t)
	}
	kept, scanned, present, truncated := applyScanCeiling(owned, ceiling)
	res := collectResult{
		unreadable:    skipped,
		scanTruncated: truncated,
		scanned:       scanned,
		present:       present,
	}
	for _, t := range kept {
		relation := relationDispatched
		if taskAssignedTo(t, principal) {
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

// taskAssignedTo is the `runs` half of the task ownership union, factored out
// so the admission predicate and the relation label can never drift apart:
// they are the SAME comparison evaluated at two points (once to decide whether
// the task is the caller's at all, once to label which reading matched), and a
// second hand-written copy of it is how "assigned to me" would silently become
// "dispatched by me" on a row.
func taskAssignedTo(t *task.Task, principal string) bool {
	return t.AgentID != "" && t.AgentID == principal
}

// collectSubagentRows returns the sessions this caller delegated.
//
// Parentage is LifecycleRecord.ParentAgentID and nothing else. It must never
// be inferred from ParentDurableKey, from ScopeID (empty for a top-level
// delegation), or from AgentID (the CHILD's id, not the parent's).
//
// [ADR-057 FR-022 doc correction] This comment used to justify excluding
// ParentDurableKey by claiming it is "shared with its children and every
// cousin in the subtree — inferring from it leaks grandchildren." That
// described the PRE-ADR-057 semantics only. Post-ADR-057 (D1), U13's
// redefinition (pkg/session/lifecycle.go's own ParentDurableKey doc
// comment) makes it name only the DIRECT parent — one hop, never
// re-inherited down the chain — so it is no longer shared across cousins
// at all, and the old justification is false as a description of the
// current field. The conclusion (do not use it here) still holds, for the
// simpler reason ParentAgentID already gives directly: it names an AGENT
// identity, which is what "did I delegate this" means, whereas
// ParentDurableKey names a SESSION id one hop up — the wrong kind of value
// for this predicate regardless of how many hops it spans.
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
	// PARENTAGE AND LINEAGE COLLAPSE FIRST, ceiling second — the same ordering
	// the other two kinds use, for the same reason (see applyScanCeiling's
	// doc). Unlike plans and tasks the store filter DOES carry the parentage
	// predicate here, so today the ceiling would already see only the caller's
	// records; the ordering is nonetheless made explicit because
	// newestGenerations re-checks parentage precisely so a widened store filter
	// cannot leak, and a budget spent before that re-check would re-open the
	// undercount half of the same hole. It also makes `present` mean the same
	// thing for all three kinds: the caller's own records, post-supersession.
	newest := newestGenerations(records, principal)
	kept, scanned, present, truncated := applyScanCeiling(newest, ceiling)
	res := collectResult{
		unreadable:    skipped,
		scanTruncated: truncated,
		scanned:       scanned,
		present:       present,
	}

	rows := make([]jobRow, 0, len(kept))
	ids := make([]string, 0, len(kept))
	for i := range kept {
		rec := kept[i]
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

// newestGenerations collapses each delegation LINEAGE to its newest record, so
// one delegation is one row (FR-034), and re-checks the parentage predicate on
// every record rather than trusting the store filter alone, so a store whose
// filter is ever widened cannot leak another principal's sessions through this
// path.
//
// There are TWO resume mechanisms and they need two different collapse rules —
// this is not belt-and-braces, each covers a case the other cannot see.
// spawnCorrectiveFollowUp (pkg/tools/delegate.go) branches on rec.Is3P:
//
//	NATIVE (warm resume) — reuses the session id verbatim and mints
//	Generation+1. Collapsed by the highest-generation rule below. Against the
//	concrete *session.LifecycleStore this is ALREADY collapsed upstream:
//	storage is one .jsonl per session id and List returns the tail via Load, so
//	that store can never hand us two records for one id and the rule is a no-op
//	on it. It is retained because the dependency here is the JobLifecycleLister
//	INTERFACE, not that struct, and because it is a map lookup over records
//	already in memory — not because the concrete store needs it.
//
//	3P (cold respawn) — mints a NEW session id (uuid.NewString) and links back
//	via ResumedFrom. Keying on session id CANNOT collapse this: the two records
//	have different ids, so both survive and one delegation shows as a stale
//	terminal row plus a live row under include_terminal=true — the exact
//	duplicate FR-034 forbids. That case is what the supersession rule below is
//	for, and it is the rule that actually fires against the real store today.
//
// A record named by another record's ResumedFrom is superseded and dropped;
// chains collapse transitively because every intermediate link is named by its
// successor. The self-link a native warm resume writes (ResumedFrom equal to
// its own SessionID) is deliberately NOT a supersession — it is the same
// session, already handled by the generation rule, and treating it as one
// would drop the live row along with the stale one.
//
// Supersession is only ever recorded from records that PASSED the parentage
// check, so another principal's record can never suppress one of the caller's.
func newestGenerations(records []session.LifecycleRecord, principal string) []*session.LifecycleRecord {
	byID := make(map[string]*session.LifecycleRecord, len(records))
	order := make([]string, 0, len(records))
	superseded := make(map[string]bool, len(records))
	for i := range records {
		rec := &records[i]
		if rec.ParentAgentID == "" || rec.ParentAgentID != principal {
			continue
		}
		if rec.ResumedFrom != "" && rec.ResumedFrom != rec.SessionID {
			superseded[rec.ResumedFrom] = true
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
		if superseded[id] {
			continue
		}
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
