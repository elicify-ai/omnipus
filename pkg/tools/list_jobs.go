package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// ListJobsTool answers one question for the calling agent: "what background
// work of mine is still outstanding, and what is its handle?"
//
// It unifies three kinds that are otherwise only reachable through three
// different tools with three different scoping rules — `plan` (plans this
// agent owns), `subagent` (sessions this agent delegated) and `task`
// (standalone tasks assigned to or created by this agent). It is strictly
// read-only.
//
// The ADR's fourth kind, `shell`, is NOT implemented by this file.
//
// [ADR-057 FR-022 doc correction] This comment used to justify that
// omission as an architectural impossibility: "a background shell carries
// no agent id, and a delegated child shares its parent's transcript
// session, so a shell row cannot be attributed to a principal at all."
// That premise is FALSE post-ADR-057 FR-027 — see pkg/tools/shell.go's
// runBackground doc comment: a background shell's OwnerSessionID is
// ToolTranscriptSessionID(ctx) at launch time, which inside a delegated
// sub-turn is the CHILD's own distinct session id, not a shared parent
// id. A shell row CAN be attributed to a principal via that session id's
// own lifecycle record (ParentAgentID/AgentID) — the same resolution
// collectSubagentRows already does for kind=subagent. The conclusion —
// kind=shell is not implemented here — still holds; it is simply undone
// work, tracked as issue #564, not an architectural dead end.
type ListJobsTool struct {
	BaseTool

	plans      JobPlanLister
	tasks      JobTaskLister
	lifecycles JobLifecycleLister

	// namer resolves a delegated agent's display name for a subagent label.
	// Read through a closure rather than captured at construction so a hot
	// config reload is reflected, matching the in-tree delegate/handoff
	// registry-accessor pattern.
	getNamer func() JobAgentNamer
	// getResolver reports which delegate session ids are live in THIS
	// process. Nil (or a nil return) means no subagent handle is actionable,
	// which is the honest answer rather than an error.
	getResolver func() JobSessionResolver
	// getLabelResolver reports the caller-supplied `delegate(label=...)`
	// value for delegate session ids still live in THIS process (UAT M3
	// fix). Nil (or a nil return) means no custom label is available for any
	// subagent row, so label_contains falls back to matching the row's
	// agent-display-name Label — never an error, and byte-identical to this
	// fix's own pre-existing behavior. See JobLabelResolver's doc comment.
	getLabelResolver func() JobLabelResolver
	// getCapSource reads the plan engine's published cap snapshot. Nil means
	// no engine is wired and the cap fields are omitted as a pair.
	getCapSource func() JobCapSnapshotSource

	// getConfig reads the config that drives redaction. A CLOSURE, never a
	// captured *config.Config, for the same reason getNamer is one — and this
	// field in particular is where a captured value was WRONG rather than
	// merely brittle. The gateway's hot reload re-registers every agent's
	// tools BEFORE it swaps the loop's config (registerSharedTools runs at
	// pkg/agent/loop.go's reload path, `al.cfg = cfg` runs after it), so a
	// value captured at wiring time is always one generation stale: the very
	// reload that turns tools.filter_sensitive_data ON would re-register this
	// tool with the pre-reload config and keep emitting plan and task titles
	// unredacted until some unrelated later reload happened to run.
	//
	// Making the SETTER take a closure is deliberate: it removes the ability
	// to pass a value at all, so the eager-capture mistake cannot be repeated
	// at a future wiring site.
	getConfig func() *config.Config

	auditLogger *audit.Logger

	// scanCeiling bounds the records one kind contributes to a single call.
	// Zero means the package default; see SetScanCeiling.
	scanCeiling int
	// capStaleness is how old a cap snapshot may be before the tick heartbeat
	// is read as "engine stopped". Zero means the package default.
	capStaleness time.Duration

	// now is injectable so cap-snapshot staleness is testable without
	// sleeping. Nil means time.Now.
	now func() time.Time
}

// defaultScanCeiling is the per-kind per-call record ceiling (FR-032(d)).
//
// The stores grow monotonically and nothing sweeps them, so exceeding this on
// a long-lived install is the steady state rather than the exception — which
// is why crossing it is REPORTED (notes.scan_truncated) rather than silently
// absorbed. Wire it from tools.list_jobs.max_records_scanned_per_kind via
// SetScanCeiling once that config key exists.
const defaultScanCeiling = 5000

// defaultCapStaleness is how stale a published cap snapshot may be before
// engine_running is reported false (FR-029(c)).
//
// 90s = 3x the plan engine's 30s tick. The derivation is stated so it is not
// re-guessed: the tick's overlap guard makes the pass after a slow tick a
// silent no-op, so two intervals is a NORMAL worst case and the third gives
// one interval of headroom for scheduler jitter. A bound below 30s would mark
// every snapshot stale on every call. Staleness LABELS, it never suppresses:
// omitting the cap pair when stale would hide it in exactly the state it
// exists for — a stopped engine is always stale.
const defaultCapStaleness = 90 * time.Second

// listJobsKnownArgs is the closed set of accepted argument names. Anything
// else is IGNORED and reported in notes.ignored_args rather than rejected:
// LLM tool calls include stray arguments routinely (a `relation` copied
// straight off a response row is the obvious one), and hard-erroring on that
// costs the caller a whole turn for a mistake more common than the one
// clamping was introduced to avoid. Hard errors are reserved for KNOWN
// arguments carrying invalid values.
var listJobsKnownArgs = map[string]bool{ //nolint:gochecknoglobals // fixed vocabulary, never mutated
	"kind":             true,
	"status":           true,
	"include_terminal": true,
	"include_drafts":   true,
	"limit":            true,
	"label_contains":   true,
}

// maxLabelContainsRunes bounds the substring filter so it cannot itself become
// an unbounded input.
const maxLabelContainsRunes = 64

// NewListJobsTool builds the tool over the three read-only store contracts.
// Any of them may be nil — a nil store means that kind is simply unavailable
// and reports a per-kind error entry rather than silently returning no rows,
// because a short list that looks complete is the worst possible output.
func NewListJobsTool(plans JobPlanLister, tasks JobTaskLister, lifecycles JobLifecycleLister) *ListJobsTool {
	return &ListJobsTool{plans: plans, tasks: tasks, lifecycles: lifecycles}
}

// SetConfig installs a LIVE accessor for the config used to redact free-text
// row fields. It takes a closure rather than a *config.Config so that a hot
// reload is reflected on the next call — see the getConfig field comment for
// why a captured value is not merely brittle here but wrong. A nil accessor,
// or one returning nil, disables redaction but never loosens any length bound.
func (t *ListJobsTool) SetConfig(get func() *config.Config) { t.getConfig = get }

// config reads the live config, or nil when none is wired.
func (t *ListJobsTool) config() *config.Config {
	if t.getConfig == nil {
		return nil
	}
	return t.getConfig()
}

// SetAuditLogger satisfies the auditLoggerAware contract so the registry can
// propagate the audit logger after construction. A nil logger is a
// best-effort no-op, never an error.
func (t *ListJobsTool) SetAuditLogger(logger *audit.Logger) { t.auditLogger = logger }

// SetAgentNamer installs the display-name resolver for subagent labels.
func (t *ListJobsTool) SetAgentNamer(get func() JobAgentNamer) { t.getNamer = get }

// SetSessionResolver installs the batch delegate-session resolver that decides
// whether a subagent handle is actionable in this process.
func (t *ListJobsTool) SetSessionResolver(get func() JobSessionResolver) { t.getResolver = get }

// SetLabelResolver installs the batch delegate-label resolver that lets
// label_contains match a subagent row's caller-supplied `delegate(label=...)`
// value instead of only its agent display name (UAT M3 fix). A nil accessor,
// or one returning nil, leaves matching exactly as it was before this setter
// existed — never an error, never a behavior change for a caller that never
// wires this.
func (t *ListJobsTool) SetLabelResolver(get func() JobLabelResolver) { t.getLabelResolver = get }

// SetCapSnapshotSource installs the plan engine's lock-free cap accessor.
func (t *ListJobsTool) SetCapSnapshotSource(get func() JobCapSnapshotSource) { t.getCapSource = get }

// SetScanCeiling overrides the per-kind per-call record ceiling. A value <= 0
// restores the default.
func (t *ListJobsTool) SetScanCeiling(n int) { t.scanCeiling = n }

// SetCapStaleness overrides the cap-snapshot staleness bound. A value <= 0
// restores the default.
func (t *ListJobsTool) SetCapStaleness(d time.Duration) { t.capStaleness = d }

// SetNow overrides the clock. Test seam only.
func (t *ListJobsTool) SetNow(fn func() time.Time) { t.now = fn }

func (t *ListJobsTool) Name() string     { return "list_jobs" }
func (t *ListJobsTool) Scope() ToolScope { return ScopeGeneral }

// Category is CategoryTasks, not CategoryDelegation: two of the three job
// kinds it reports (plan, task) are task-domain, and its load-bearing pairing
// is with stop_plan/execute_plan, which are both CategoryTasks. Category is
// purely presentational (manifest headings, /api/v1/tools grouping).
func (t *ListJobsTool) Category() ToolCategory { return CategoryTasks }

// Description is the LLM-facing contract. It is deliberately short: Omnipus
// sends every tool description on every request, so this text is a fixed
// per-request token cost for every agent that has the tool. Operator-facing
// material — the kill switch, the unreadable runbook, the ask-verdict caveat —
// lives in the operator documentation, never here.
func (t *ListJobsTool) Description() string {
	return "List your own outstanding background work: plans you own, subagents you delegated, " +
		"and standalone tasks assigned to or created by you.\n" +
		"Read-only, and a best-effort " +
		"near-snapshot taken while work is changing — never a transactional one. An id is " +
		"meaningful only paired with its kind. actionable=false means the row is informational " +
		"only and that kind's action tools will not accept its id. attention=caller means the row " +
		"waits on you; attention=elsewhere means another agent is already handling it (e.g. a plan " +
		"awaiting supervision) and you must not intervene; attention=none on a blocked row means " +
		"there is nothing for you to do. Bounded: at most 25 queued, 25 running, 25 blocked and 20 " +
		"terminal rows; limit is spread across those groups, and above 75 needs " +
		"include_terminal=true. notes is null when nothing is non-nominal; counters inside it are " +
		"omitted when zero."
}

func (t *ListJobsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind": map[string]any{
				"type":        "string",
				"enum":        []string{jobKindPlan, jobKindSubagent, jobKindTask},
				"description": "Restrict to one kind of job. Omit for all three.",
			},
			"status": map[string]any{
				"type": "string",
				"enum": []string{
					jobStatusQueued, jobStatusRunning, jobStatusBlocked,
					jobStatusFailed, jobStatusCompleted,
				},
				"description": "Restrict to one normalized status. 'failed' or 'completed' implies include_terminal.",
			},
			"include_terminal": map[string]any{
				"type":        "boolean",
				"description": "Include failed and completed rows. Default false.",
			},
			"include_drafts": map[string]any{
				"type":        "boolean",
				"description": "Include draft plans, which rank below approved ones. Default false.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum rows, spread across the status groups. Above 200 is clamped.",
			},
			"label_contains": map[string]any{
				"type":        "string",
				"description": "Case-insensitive substring match on the row label. Up to 64 characters.",
			},
		},
	}
}

// listJobsResponse is the normative envelope.
//
// not-wire-format: a tool-result shape returned to an LLM, not bytes crossing
// the gateway/SPA boundary — Constraint #8 does not apply and this change
// touches no file under contracts/.
//
// The cap quartet is TOP-LEVEL, never inside notes: those four are STATE, and
// notes' omit-when-zero convention applies only to diagnostics. cap_active=0
// in particular is load-bearing — it is the one number that separates "a dead
// engine will never start my plan" from "a healthy queue, wait" — so the pair
// is carried through pointers rather than through omitempty ints, which would
// delete exactly that signal.
type listJobsResponse struct {
	WorkspaceScoped bool           `json:"workspace_scoped"`
	CapActive       *int           `json:"cap_active,omitempty"`
	CapMax          *int           `json:"cap_max,omitempty"`
	CapObservedAt   string         `json:"cap_observed_at,omitempty"`
	EngineRunning   *bool          `json:"engine_running,omitempty"`
	Rows            []jobRow       `json:"rows"`
	Notes           *listJobsNotes `json:"notes"`
}

// listJobsNotes is the ONLY diagnostic container. A counter absent from it is
// zero. The whole object is null when nothing is non-nominal, which is what
// lets a caller tell "nothing to report" from a malformed response — there is
// no separate marker and no second always-present field.
type listJobsNotes struct {
	TotalOmitted       int                       `json:"total_omitted,omitempty"`
	Omitted            *omittedCounts            `json:"omitted,omitempty"`
	Unreadable         map[string]int            `json:"unreadable,omitempty"`
	TerminalSuppressed map[string]int            `json:"terminal_suppressed,omitempty"`
	Unmapped           map[string]int            `json:"unmapped,omitempty"`
	ScanTruncated      map[string]scanTruncation `json:"scan_truncated,omitempty"`
	LimitClampedTo     int                       `json:"limit_clamped_to,omitempty"`
	IgnoredArgs        []string                  `json:"ignored_args,omitempty"`
	Errors             []kindError               `json:"errors,omitempty"`
}

// omittedCounts reports truncation in BOTH key spaces. Neither is redundant:
// the sub-bounds are per status group and the per-kind error entries are per
// kind, so a caller reasoning about either axis needs its own numbers. Both
// sub-objects sum to TotalOmitted by construction.
type omittedCounts struct {
	ByKind   map[string]int `json:"by_kind,omitempty"`
	ByStatus map[string]int `json:"by_status,omitempty"`
}

// scanTruncation marks a kind whose scan hit the per-call ceiling. Its
// presence means every other count for that kind is a LOWER BOUND — it is the
// marker that says the exactness guarantees are suspended, so a response
// WITHOUT it is exact.
type scanTruncation struct {
	Scanned int `json:"scanned"`
	Present int `json:"present"`
}

// kindError is a store-level read failure for one kind. Other kinds still
// return their rows: a per-kind failure must never be laundered into a short
// list that looks complete.
type kindError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// listJobsArgs is the validated argument set.
type listJobsArgs struct {
	kind            string
	status          string
	includeTerminal bool
	includeDrafts   bool
	limit           int
	limitClampedTo  int
	labelContains   string
	ignored         []string
}

// Execute reads the caller's roster.
func (t *ListJobsTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	// FAIL CLOSED. All three stores treat an empty owner value as "filter
	// off", so an unguarded empty principal would return every plan, task and
	// delegated session in the installation. This guard is the single reason
	// that cannot happen, and it must never be relaxed into "return an empty
	// roster" — a silent empty success is indistinguishable from having no
	// work, and hides the misconfiguration that produced it.
	principal := strings.TrimSpace(ToolAgentID(ctx))
	if principal == "" {
		t.writeAudit(ctx, audit.DecisionError, "", "", false, nil, 0)
		return ErrorResult("list_jobs: cannot resolve the calling agent; refusing to list jobs")
	}

	// ToolWorkspaceID is conditionally injected and is empty for any turn
	// whose channel binding carries no workspace. That is a legitimate state,
	// so this is a deliberate exception to the fail-closed posture above: the
	// roster widens to every workspace FOR THIS PRINCIPAL ONLY, and says so
	// through workspace_scoped rather than presenting a cross-workspace
	// roster as a scoped one.
	//
	// Resolved BEFORE argument parsing so that a rejected call's audit entry
	// still records which workspace the caller was probing from.
	workspaceID := strings.TrimSpace(ToolWorkspaceID(ctx))
	scoped := workspaceID != ""

	parsed, err := parseListJobsArgs(args)
	if err != nil {
		// Every call leaves exactly one audit entry, including a rejected one:
		// a forensic trail with holes in it is not a forensic trail, and a
		// caller probing the argument surface is exactly the pattern the trail
		// exists to make visible.
		t.writeAudit(ctx, audit.DecisionError, principal, workspaceID, scoped, nil, 0)
		return ErrorResult(fmt.Sprintf("list_jobs: %v", err))
	}

	kinds := t.kindsToRead(parsed.kind)
	rows, notes := t.collect(kinds, principal, workspaceID, parsed)

	resp := listJobsResponse{
		WorkspaceScoped: scoped,
		Rows:            rows,
		Notes:           notes,
	}
	t.applyCapFields(&resp)

	payload, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(fmt.Sprintf("list_jobs: marshal response: %v", err))
	}
	t.writeAudit(ctx, audit.DecisionAllow, principal, workspaceID, scoped, kinds, len(rows))
	return NewToolResult(string(payload))
}

// kindsToRead returns the kinds this call reads, in FR-007 order. When `kind`
// is supplied only that kind's store is touched.
func (t *ListJobsTool) kindsToRead(kind string) []string {
	if kind != "" {
		return []string{kind}
	}
	return []string{jobKindPlan, jobKindSubagent, jobKindTask}
}

// collect gathers, filters, orders and bounds the roster.
func (t *ListJobsTool) collect(
	kinds []string,
	principal, workspaceID string,
	parsed listJobsArgs,
) ([]jobRow, *listJobsNotes) {
	red := newRedactor(t.config())
	acc := newNotesAccumulator()
	all := make([]jobRow, 0)

	for _, kind := range kinds {
		res := t.collectKind(kind, principal, workspaceID, red)
		acc.record(kind, res)
		all = append(all, res.rows...)
	}

	// Every narrowing predicate is applied BEFORE the bounds, so the omission
	// counts stay exact against the population the caller actually asked for.
	all = filterByLabel(all, parsed.labelContains)
	if !parsed.includeDrafts {
		all = filterOutDrafts(all)
	}
	if !parsed.includeTerminal {
		all = acc.suppressTerminal(all)
	}
	if parsed.status != "" {
		all = filterByStatus(all, parsed.status)
	}

	// `unmapped` is counted over the SURVIVING population, not over everything
	// the stores returned, so it answers the question the caller actually
	// asked: "of the jobs matching my query, how many carried a state this
	// build could not parse?" Counting it at collection time would report
	// records the caller filtered out, which reads as a defect in rows they
	// never asked about.
	acc.countUnmapped(all)

	sortJobRows(all)

	limit := parsed.limit
	if limit <= 0 {
		limit = maxRows
	}
	bounded := applyBounds(all, limit)
	// Truncation is applied LAST, to the selected rows only — see
	// boundRowFields for why the redact/truncate split is ordered this way.
	boundRowFields(bounded.selected)

	acc.recordOmissions(bounded)
	acc.limitClampedTo = parsed.limitClampedTo
	acc.ignoredArgs = parsed.ignored
	return bounded.selected, acc.build()
}

// collectKind dispatches to one kind's collector. A nil store is reported as a
// per-kind error entry, never as zero rows.
func (t *ListJobsTool) collectKind(kind, principal, workspaceID string, red redactor) collectResult {
	ceiling := t.scanCeiling
	if ceiling <= 0 {
		ceiling = defaultScanCeiling
	}
	switch kind {
	case jobKindPlan:
		if t.plans == nil {
			return collectResult{err: fmt.Errorf("plan store: not wired")}
		}
		return collectPlanRows(t.plans, principal, workspaceID, red, ceiling)
	case jobKindTask:
		if t.tasks == nil {
			return collectResult{err: fmt.Errorf("task store: not wired")}
		}
		return collectTaskRows(t.tasks, principal, workspaceID, red, ceiling)
	case jobKindSubagent:
		if t.lifecycles == nil {
			return collectResult{err: fmt.Errorf("lifecycle store: not wired")}
		}
		var namer JobAgentNamer
		if t.getNamer != nil {
			namer = t.getNamer()
		}
		var resolver JobSessionResolver
		if t.getResolver != nil {
			resolver = t.getResolver()
		}
		var labelResolver JobLabelResolver
		if t.getLabelResolver != nil {
			labelResolver = t.getLabelResolver()
		}
		return collectSubagentRows(t.lifecycles, principal, workspaceID, red, ceiling, namer, resolver, labelResolver)
	default:
		return collectResult{err: fmt.Errorf("%s: unknown kind", kind)}
	}
}

// applyCapFields fills the top-level cap quartet from the plan engine's
// published snapshot.
//
// Dispositions, exhaustively (FR-029(d)):
//
//	no engine wired            -> pair omitted, observed_at omitted, engine_running omitted
//	no snapshot published yet  -> pair omitted, observed_at omitted, engine_running emitted
//	snapshot unreliable        -> pair omitted, observed_at omitted, engine_running emitted
//	snapshot reliable (stale
//	  or fresh)                -> pair emitted INCLUDING cap_active=0, observed_at and
//	                              engine_running emitted
//
// Staleness never suppresses. A stopped engine's snapshot is always stale, and
// suppressing on staleness would hide the cap numbers in exactly the state
// they exist for: cap_active far below cap_max plus engine_running=false is
// the entire signal that says "nothing will ever start this plan" rather than
// "healthy queue, wait".
//
// Admit is never called: it takes the engine's mutex exclusively and re-scans
// the plan store, and a read-only visibility tool must not contend with the
// dispatch path.
func (t *ListJobsTool) applyCapFields(resp *listJobsResponse) {
	if t.getCapSource == nil {
		return
	}
	source := t.getCapSource()
	if source == nil {
		return
	}
	active, capMax, reliable, observedAt, lastTickAt := source.CapSnapshot()

	staleness := t.capStaleness
	if staleness <= 0 {
		staleness = defaultCapStaleness
	}
	running := !lastTickAt.IsZero() && t.clock().Sub(lastTickAt) <= staleness
	resp.EngineRunning = &running

	if observedAt.IsZero() || !reliable {
		return
	}
	resp.CapActive = &active
	resp.CapMax = &capMax
	resp.CapObservedAt = rfc3339UTC(observedAt)
}

func (t *ListJobsTool) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// writeAudit records exactly one entry per call. This is the security control,
// not a debugging aid: the tool enumerates another principal's labels and
// steerable handles if it is ever wrong, so a scoping bug must leave a
// forensic trail.
//
// Neither `label` nor `native_status` is ever recorded. The audit log is
// persisted and tamper-evident, and job titles are precisely the thing the
// scoping control protects.
func (t *ListJobsTool) writeAudit(
	ctx context.Context,
	decision, principal, workspaceID string,
	scoped bool,
	kinds []string,
	rowCount int,
) {
	if t.auditLogger == nil {
		return
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  decision,
		AgentID:   principal,
		SessionID: ToolTranscriptSessionID(ctx),
		Tool:      "list_jobs",
		Details: map[string]any{
			"workspace_id":     workspaceID,
			"workspace_scoped": scoped,
			"kinds":            kinds,
			"row_count":        rowCount,
		},
	}
	if err := t.auditLogger.Log(entry); err != nil {
		slog.Warn("list_jobs: audit log failed", "agent_id", principal, "error", err)
	}
}

// filterByLabel applies the caller's case-insensitive substring filter against
// the REDACTED, pre-truncation filterLabel — NOT necessarily the same string
// as the displayed Label (see filterLabel's own doc comment: for plan/task
// rows the two are always identical; for a subagent row filterLabel prefers
// the caller's own `delegate(label=...)` value when one is still resolvable,
// UAT M3 fix). It is never applied to native_status, which carries
// unvalidated runtime text — a filter over that would be a query interface
// onto wrapped error strings.
func filterByLabel(rows []jobRow, needle string) []jobRow {
	if needle == "" {
		return rows
	}
	lowered := strings.ToLower(needle)
	out := rows[:0:0]
	for _, row := range rows {
		if strings.Contains(strings.ToLower(row.filterLabel), lowered) {
			out = append(out, row)
		}
	}
	return out
}

// filterOutDrafts removes draft plans, which normalize to `queued`.
func filterOutDrafts(rows []jobRow) []jobRow {
	out := rows[:0:0]
	for _, row := range rows {
		if row.draftRank == 0 {
			out = append(out, row)
		}
	}
	return out
}

// filterByStatus narrows to one normalized status.
func filterByStatus(rows []jobRow, status string) []jobRow {
	out := rows[:0:0]
	for _, row := range rows {
		if row.Status == status {
			out = append(out, row)
		}
	}
	return out
}

// notesAccumulator gathers every diagnostic counter for one call.
type notesAccumulator struct {
	unreadable         map[string]int
	unmapped           map[string]int
	terminalSuppressed map[string]int
	scanTruncated      map[string]scanTruncation
	errors             []kindError
	omittedByKind      map[string]int
	omittedByStatus    map[string]int
	totalOmitted       int
	limitClampedTo     int
	ignoredArgs        []string
}

func newNotesAccumulator() *notesAccumulator {
	return &notesAccumulator{
		unreadable:         map[string]int{},
		unmapped:           map[string]int{},
		terminalSuppressed: map[string]int{},
		scanTruncated:      map[string]scanTruncation{},
	}
}

// record folds one kind's collection outcome into the counters, and emits the
// operator-facing Warn a degrading install needs — so an operator learns about
// corruption without waiting for a caller to report it.
func (a *notesAccumulator) record(kind string, res collectResult) {
	if res.err != nil {
		a.errors = append(a.errors, kindError{Kind: kind, Message: res.err.Error()})
		slog.Warn("list_jobs: store read failed", "kind", kind, "error", res.err)
		return
	}
	if res.unreadable > 0 {
		a.unreadable[kind] = res.unreadable
		slog.Warn("list_jobs: unreadable records skipped", "kind", kind, "count", res.unreadable)
	}
	if res.scanTruncated {
		a.scanTruncated[kind] = scanTruncation{Scanned: res.scanned, Present: res.present}
		slog.Warn("list_jobs: scan ceiling reached; counts for this kind are lower bounds",
			"kind", kind, "scanned", res.scanned, "present", res.present)
	}
}

// suppressTerminal drops terminal rows and counts them per kind plus a total.
//
// The count is what makes a default call after a restart distinguishable from
// a call by a caller with genuinely no background work. Without it, an agent
// whose sessions were all reconciled to failed(interrupted) at boot sees an
// empty roster byte-identical to "nothing was ever running" and concludes its
// work never existed.
func (a *notesAccumulator) suppressTerminal(rows []jobRow) []jobRow {
	out := rows[:0:0]
	total := 0
	for _, row := range rows {
		if terminalStatus(row.Status) {
			a.terminalSuppressed[row.Kind]++
			total++
			continue
		}
		out = append(out, row)
	}
	if total > 0 {
		a.terminalSuppressed["total"] = total
	}
	return out
}

// countUnmapped tallies rows whose native state matched no known value, per
// kind, over the post-filter population.
func (a *notesAccumulator) countUnmapped(rows []jobRow) {
	for _, row := range rows {
		if row.unmapped {
			a.unmapped[row.Kind]++
		}
	}
}

func (a *notesAccumulator) recordOmissions(b boundsResult) {
	a.totalOmitted = b.totalOmitted
	a.omittedByKind = b.omittedByKind
	a.omittedByStatus = b.omittedByStatus
}

// build returns the notes object, or nil when every counter is zero. A nil
// notes serializes as `null`, which is the always-present field the response
// contract requires.
func (a *notesAccumulator) build() *listJobsNotes {
	notes := &listJobsNotes{
		TotalOmitted:       a.totalOmitted,
		Unreadable:         nonEmptyCounts(a.unreadable),
		TerminalSuppressed: nonEmptyCounts(a.terminalSuppressed),
		Unmapped:           nonEmptyCounts(a.unmapped),
		LimitClampedTo:     a.limitClampedTo,
		IgnoredArgs:        a.ignoredArgs,
		Errors:             a.errors,
	}
	if len(a.scanTruncated) > 0 {
		notes.ScanTruncated = a.scanTruncated
	}
	if a.totalOmitted > 0 {
		notes.Omitted = &omittedCounts{
			ByKind:   nonEmptyCounts(a.omittedByKind),
			ByStatus: nonEmptyCounts(a.omittedByStatus),
		}
	}
	if notes.isNominal() {
		return nil
	}
	return notes
}

// isNominal reports whether nothing at all is worth telling the caller.
func (n *listJobsNotes) isNominal() bool {
	return n.TotalOmitted == 0 &&
		n.Omitted == nil &&
		len(n.Unreadable) == 0 &&
		len(n.TerminalSuppressed) == 0 &&
		len(n.Unmapped) == 0 &&
		len(n.ScanTruncated) == 0 &&
		n.LimitClampedTo == 0 &&
		len(n.IgnoredArgs) == 0 &&
		len(n.Errors) == 0
}

// nonEmptyCounts drops zero-valued entries and returns nil for an empty map,
// so a counter that is zero is ABSENT rather than present-and-zero.
func nonEmptyCounts(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		if v != 0 {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseListJobsArgs validates the argument set.
//
// Two dispositions differ deliberately and each is the one that does not cost
// the caller a turn: a `limit` above the hard maximum is CLAMPED and the clamp
// reported, and an UNKNOWN argument is IGNORED and reported. A KNOWN argument
// carrying an invalid value is the only hard error.
func parseListJobsArgs(args map[string]any) (listJobsArgs, error) {
	var out listJobsArgs

	for name := range args {
		if !listJobsKnownArgs[name] {
			out.ignored = append(out.ignored, name)
		}
	}
	sort.Strings(out.ignored)

	if raw, ok := args["kind"]; ok {
		kind, err := listJobsStringArg("kind", raw)
		if err != nil {
			return out, err
		}
		switch kind {
		case "", jobKindPlan, jobKindSubagent, jobKindTask:
			out.kind = kind
		default:
			return out, fmt.Errorf("kind must be one of plan, subagent, task (got %q)", kind)
		}
	}

	if raw, ok := args["status"]; ok {
		status, err := listJobsStringArg("status", raw)
		if err != nil {
			return out, err
		}
		switch status {
		case "":
		case jobStatusQueued, jobStatusRunning, jobStatusBlocked, jobStatusFailed, jobStatusCompleted:
			out.status = status
		default:
			return out, fmt.Errorf(
				"status must be one of queued, running, blocked, failed, completed (got %q)", status)
		}
	}

	var err error
	if out.includeTerminal, err = listJobsBoolArg(args, "include_terminal"); err != nil {
		return out, err
	}
	if out.includeDrafts, err = listJobsBoolArg(args, "include_drafts"); err != nil {
		return out, err
	}

	// Asking for a terminal status while terminal rows are excluded would
	// return an empty roster and teach the caller that nothing had failed.
	// Implying include_terminal is better than erroring, by the same reasoning
	// that makes an over-large limit a clamp.
	if terminalStatus(out.status) {
		out.includeTerminal = true
	}

	if raw, ok := args["limit"]; ok {
		limit, err := listJobsIntArg("limit", raw)
		if err != nil {
			return out, err
		}
		if limit <= 0 {
			return out, fmt.Errorf("limit must be a positive integer (got %d)", limit)
		}
		if limit > hardLimitMax {
			out.limitClampedTo = hardLimitMax
			limit = hardLimitMax
		}
		out.limit = limit
	}

	if raw, ok := args["label_contains"]; ok {
		needle, err := listJobsStringArg("label_contains", raw)
		if err != nil {
			return out, err
		}
		if n := len([]rune(needle)); n > maxLabelContainsRunes {
			return out, fmt.Errorf("label_contains must be at most %d characters (got %d)",
				maxLabelContainsRunes, n)
		}
		out.labelContains = needle
	}

	return out, nil
}

func listJobsStringArg(name string, raw any) (string, error) {
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", name)
	}
	return strings.TrimSpace(s), nil
}

func listJobsBoolArg(args map[string]any, name string) (bool, error) {
	raw, ok := args[name]
	if !ok {
		return false, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return b, nil
}

// intArg accepts the numeric shapes a JSON tool call can produce. A
// fractional value is rejected rather than silently truncated.
func listJobsIntArg(name string, raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		if v != float64(int(v)) {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		return int(v), nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		return int(n), nil
	default:
		return 0, fmt.Errorf("%s must be an integer", name)
	}
}
