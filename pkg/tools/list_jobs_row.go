package tools

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// This file holds the pure, store-independent half of `list_jobs`: the row
// shape, the per-kind native-state -> normalized-status mapping (FR-006,
// FR-006a, FR-006b), the `attention` derivation (FR-036), and the redaction +
// truncation pipeline (FR-019, FR-019a, FR-030).
//
// Everything here is a pure function of its inputs, so the properties the spec
// cares about (a stalled plan is never merely `running`; `attention`
// discriminates; a short secret is still redacted) are testable without a
// filesystem, a store or a context.

// Normalized status vocabulary (FR-006). Exactly five values — the vocabulary
// is NOT extended for unmappable native states (FR-006b maps those onto
// `blocked` plus a marker).
const (
	jobStatusQueued    = "queued"
	jobStatusRunning   = "running"
	jobStatusBlocked   = "blocked"
	jobStatusFailed    = "failed"
	jobStatusCompleted = "completed"
)

// Row `attention` vocabulary (FR-036). `blocked` alone conflates three
// unrelated situations; `attention` is what splits them:
//
//	none      — informational. A dependency-blocked task clears on its own and
//	            the caller cannot do anything about it (operator ruling 3).
//	caller    — the row is waiting on THIS caller (a stalled plan needing a
//	            correction, a subagent sitting in needs_input).
//	elsewhere — another principal is already adjudicating (a plan parked at
//	            awaiting_supervision). The caller MUST NOT intervene; under
//	            PlanSupervisor the only tool the Owner has is stop_plan, which
//	            would abort a healthy adjudication (cross-spec M6).
const (
	attentionNone      = "none"
	attentionCaller    = "caller"
	attentionElsewhere = "elsewhere"
)

// Job kinds. The `shell` kind of ADR-056 is NOT implemented here.
//
// [ADR-057 FR-022 doc correction] This comment used to justify that
// omission as an FR-008 fail-closed necessity: "background shells carry no
// agent id and a delegated child shares its parent's transcript session,
// so a shell row cannot be attributed to a principal at all — which would
// breach FR-008's fail-closed posture." That premise is FALSE post-
// ADR-057 FR-027 — see pkg/tools/shell.go's runBackground doc comment: a
// background shell's OwnerSessionID is the CHILD's own distinct session id
// when launched inside a delegated sub-turn, so it CAN be attributed to a
// principal via that session's own lifecycle record. The conclusion — not
// implemented here — still holds, for the ordinary reason of scope, not
// attribution impossibility. Tracked as issue #564.
const (
	jobKindPlan     = "plan"
	jobKindSubagent = "subagent"
	jobKindTask     = "task"
)

// Row `relation` vocabulary (FR-010). Constant for two of the three kinds by
// construction — that is deliberate uniformity, not redundancy: a future
// reader should not "simplify" the constant cases away, because the field's
// job is to tell the caller WHICH ownership reading matched, and a missing
// field would be read as "unknown" rather than "always runs".
const (
	relationRuns       = "runs"
	relationDispatched = "dispatched"
)

// Free-text field maxima (FR-030). Two bounds per field, and truncation must
// satisfy BOTH: runes for the truncation boundary (splitting a rune produces
// invalid UTF-8) and bytes for the size budget (the machine counts bytes, and
// a 120-rune CJK label is 360-480 bytes before JSON escaping inflates it).
// Do not "fix" either unit back into the other.
const (
	labelMaxRunes        = 120
	labelMaxBytes        = 480
	nativeStatusMaxRunes = 200
	nativeStatusMaxBytes = 800
)

// unknownNativeStatusPrefix marks a native state that maps to no normalized
// value (FR-006b). The raw value is carried after the prefix so an operator
// can see what arrived, and it is redacted and bounded like any other
// free text.
const unknownNativeStatusPrefix = "unknown:"

// jobRow is one row of the `list_jobs` roster.
//
// not-wire-format: this is a tool-result shape returned to an LLM, not bytes
// crossing the gateway/SPA boundary, so Constraint #8 does not apply and
// `list_jobs` introduces no contract change (FR-025). The normative shape is
// the spec's Response Shape section.
//
// `started_at`, `last_activity_at`, `workspace_id` and `attention` carry NO
// omitempty on purpose: they are state, not diagnostics, and FR-033's
// omit-when-zero convention applies only to the counters inside `notes`. A
// variable-shape row is a worse trade for an LLM consumer than a few
// duplicated short strings (FR-003, declining R2-OBS-003).
type jobRow struct {
	Kind                 string `json:"kind"`
	ID                   string `json:"id"`
	Label                string `json:"label"`
	Status               string `json:"status"`
	NativeStatus         string `json:"native_status"`
	Relation             string `json:"relation"`
	Attention            string `json:"attention"`
	StartedAt            string `json:"started_at"`
	LastActivityAt       string `json:"last_activity_at"`
	WorkspaceID          string `json:"workspace_id"`
	Actionable           bool   `json:"actionable"`
	IntentionallyStopped bool   `json:"intentionally_stopped"`
	Generation           int    `json:"generation,omitempty"`

	// draftRank ranks `draft` plans below `approved` ones inside the `queued`
	// group (FR-007). Not serialized: it is an ordering input, not a row
	// field. Abandoned drafts accumulate monotonically (nothing sweeps them),
	// so without this rank 26 stale drafts would evict every real cap-waiting
	// plan from the roster.
	draftRank int
	// unmapped records that this row's native state matched no known value,
	// so the caller's per-kind `unmapped` counter can be incremented without
	// re-parsing native_status (FR-006b). Not serialized.
	unmapped bool
}

// terminalStatus reports whether a normalized status is terminal. Terminal
// rows are excluded unless include_terminal=true (FR-016) and are never
// actionable (FR-011).
func terminalStatus(s string) bool {
	return s == jobStatusFailed || s == jobStatusCompleted
}

// redactor bundles the sensitive-value replacer with the operator's
// filter-enabled switch.
//
// It deliberately does NOT call config.Config.FilterSensitiveData: that helper
// returns content UNCHANGED when it is shorter than FilterMinLength (default
// 8, a BYTE comparison), so a 7-byte ASCII secret in a plan title would reach
// the caller's context and the persisted transcript untouched (FR-019a). The
// replacer itself has no such fast path, so calling it directly closes the
// bypass while still honouring the operator's tools.filter_sensitive_data
// switch.
type redactor struct {
	replacer *strings.Replacer
	enabled  bool
}

// newRedactor builds a redactor from cfg. A nil cfg yields a pass-through
// redactor — redaction is unavailable, but every FR-030 length bound is still
// enforced by boundRowFields, because a bound must never depend on whether
// redaction happened (FR-019a).
func newRedactor(cfg *config.Config) redactor {
	if cfg == nil {
		return redactor{}
	}
	return redactor{
		replacer: cfg.SensitiveDataReplacer(),
		enabled:  cfg.Tools.IsFilterSensitiveDataEnabled(),
	}
}

// redact applies the sensitive-value replacer at ANY length.
func (r redactor) redact(s string) string {
	if !r.enabled || r.replacer == nil || s == "" {
		return s
	}
	return r.replacer.Replace(s)
}

// boundRowFields is the TRUNCATION half of the free-text pipeline, applied to
// the rows that will actually be emitted.
//
// The two halves are deliberately split in time, and the order between them is
// load-bearing:
//
//  1. REDACTION happens at collection, on every candidate row. Doing it first
//     means `label_contains` matches the redacted text (so the filter can never
//     be used to probe for a secret), and — the real reason — truncating first
//     could split a registered secret across the boundary so the replacer no
//     longer matches either half, defeating the control entirely.
//
//  2. TRUNCATION happens here, last, on the selected rows only. Bounding
//     earlier would make the substring filter and every omission counter
//     operate on a shortened population, and would burn the work on rows the
//     bounds are about to drop.
func boundRowFields(rows []jobRow) {
	for i := range rows {
		rows[i].Label = truncateField(rows[i].Label, labelMaxRunes, labelMaxBytes)
		rows[i].NativeStatus = truncateField(
			rows[i].NativeStatus, nativeStatusMaxRunes, nativeStatusMaxBytes)
	}
}

// truncateField reduces s until BOTH bounds hold: at most runeMax runes, and
// at most byteMax bytes once JSON-encoded (excluding the surrounding quotes).
//
// Measuring the ENCODED length is what makes the byte bound true rather than
// approximately true: `"` becomes `\"`, a control character becomes `\uXXXX`,
// and a naive len(s) would understate the serialized cost by up to 6x on
// hostile input.
func truncateField(s string, runeMax, byteMax int) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) > runeMax {
		r = r[:runeMax]
	}
	if jsonEncodedLen(string(r)) <= byteMax {
		return string(r)
	}
	// jsonEncodedLen is monotonic non-decreasing in the prefix length, so the
	// largest acceptable prefix is found by binary search — O(log n) encodes
	// rather than O(n).
	lo, hi := 0, len(r) // lo always fits, hi never does
	for lo+1 < hi {
		mid := lo + (hi-lo)/2
		if jsonEncodedLen(string(r[:mid])) <= byteMax {
			lo = mid
		} else {
			hi = mid
		}
	}
	return string(r[:lo])
}

// jsonEncodedLen returns the length in bytes of s once JSON-encoded, excluding
// the two surrounding quotes. json.Marshal of a string cannot fail, so the
// error branch is unreachable defensive code rather than a swallowed failure;
// it degrades to the raw byte length, which is never larger than the encoded
// one and therefore never loosens the bound.
func jsonEncodedLen(s string) int {
	b, err := json.Marshal(s)
	if err != nil {
		return len(s)
	}
	return len(b) - 2
}

// rfc3339UTC renders a time.Time in the single normalized representation every
// row timestamp uses (FR-003). A zero time is the empty string, which is a
// legitimate value: a queued plan has never started.
func rfc3339UTC(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// normalizeRFC3339 re-renders an already-RFC3339 store string through the same
// representation as rfc3339UTC, so a row never mixes two precisions or two
// zones. FR-003 requires ONE shared helper precisely because FR-020's total
// ordering compares these strings.
//
// An unparseable value yields "" (indistinguishable from absent) rather than
// propagating a malformed string into a sorted key. That is a corrupt-record
// symptom, so it is logged at Debug and left to the store's own unreadable
// accounting rather than invented as a new counter.
func normalizeRFC3339(s string) string {
	if s == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		slog.Debug("list_jobs: unparseable timestamp on record", "value", s, "error", err)
		return ""
	}
	return parsed.UTC().Format(time.RFC3339)
}

// normalizedPlan is the derived status view of one plan.
type normalizedPlan struct {
	status       string
	nativeStatus string
	attention    string
	draftRank    int
	unmapped     bool
	stopped      bool
}

// normalizePlan maps a plan's native state onto the FR-006 vocabulary.
//
// Every enum substring in nativeStatus is interpolated from the exported
// pkg/plan constant, never hand-typed (FR-006a): PlanSupervisor's rename of
// the parked phase reaches this file through the compiler that way, whereas a
// literal "running/awaiting_supervision" would survive a rename sweep and ship
// a wrong native_status with every gate green.
func normalizePlan(p *plan.Plan) normalizedPlan {
	switch p.State {
	case plan.StateDraft:
		return normalizedPlan{
			status:       jobStatusQueued,
			nativeStatus: string(plan.StateDraft),
			attention:    attentionNone,
			draftRank:    1,
		}
	case plan.StateApproved:
		// There is no `queued` plan state — a plan waiting on the global
		// active-loop cap sits at `approved` and the cap-wait branch only
		// logs. `approved` IS the queue (FR-006).
		return normalizedPlan{
			status:       jobStatusQueued,
			nativeStatus: string(plan.StateApproved),
			attention:    attentionNone,
		}
	case plan.StateRunning:
		return normalizeRunningPlan(p)
	case plan.StateDone:
		return normalizedPlan{
			status:       jobStatusCompleted,
			nativeStatus: string(plan.StateDone),
			attention:    attentionNone,
		}
	case plan.StateFailed:
		return normalizeFailedPlan(p)
	default:
		return normalizedPlan{
			status:       jobStatusBlocked,
			nativeStatus: unknownNativeStatusPrefix + string(p.State),
			attention:    attentionNone,
			unmapped:     true,
		}
	}
}

// normalizeRunningPlan splits `running` into the three situations the bare
// state hides. A running plan that is parked, stalled or paused is NOT making
// progress, and reporting it as `running` is the silence US-2 exists to
// eliminate.
//
// Precedence is awaiting_supervision > stalled > paused_reason, matching
// pkg/plan's own documented precedence (surfaceStallIfAny refuses to touch
// PlanPhase while the plan is parked) — and it is what SC-002's
// "awaiting_supervision outranks stalled" asserts.
func normalizeRunningPlan(p *plan.Plan) normalizedPlan {
	phase := p.EffectivePlanPhase()
	running := string(plan.StateRunning)
	switch {
	case phase == plan.PhaseAwaitingSupervision:
		// Another principal is adjudicating. The caller must NOT intervene:
		// its only available verb is stop_plan, which would cascade into
		// cancelling the in-flight supervision turn.
		return normalizedPlan{
			status:       jobStatusBlocked,
			nativeStatus: running + "/" + string(plan.PhaseAwaitingSupervision),
			attention:    attentionElsewhere,
		}
	case phase == plan.PhaseStalled:
		return normalizedPlan{
			status:       jobStatusBlocked,
			nativeStatus: running + "/" + string(plan.PhaseStalled),
			attention:    attentionCaller,
		}
	case strings.TrimSpace(p.PausedReason) != "":
		// PausedReason is a bare string that no validator ever sees, so it can
		// carry a wrapped error, a path or upstream API text. It is free text
		// and is redacted with the rest of native_status downstream.
		return normalizedPlan{
			status:       jobStatusBlocked,
			nativeStatus: running + "/" + string(phase) + "/paused:" + p.PausedReason,
			attention:    attentionCaller,
		}
	default:
		return normalizedPlan{
			status:       jobStatusRunning,
			nativeStatus: running + "/" + string(phase),
			attention:    attentionNone,
		}
	}
}

// normalizeFailedPlan derives `intentionally_stopped` from the CLOSED portion
// of the failed-reason enum only — never by parsing native_status.
//
// FailedReasonDoDUnreachable reports false even though PlanSupervisor's
// `abandon` verb is a deliberate adjudicated termination: the same value is
// also written by the involuntary planCannotProgress path and nothing on the
// record distinguishes them. false is chosen because the harm is asymmetric —
// a false `true` suppresses a legitimate re-dispatch of work nobody stopped.
// Revisit if PlanSupervisor splits the value.
func normalizeFailedPlan(p *plan.Plan) normalizedPlan {
	native := string(plan.StateFailed)
	if p.FailedReason != "" {
		native += "/" + string(p.FailedReason)
	}
	return normalizedPlan{
		status:       jobStatusFailed,
		nativeStatus: native,
		attention:    attentionNone,
		stopped:      p.FailedReason == plan.FailedReasonStoppedByUser,
	}
}

// normalizedTask is the derived status view of one task.
type normalizedTask struct {
	status       string
	nativeStatus string
	attention    string
	unmapped     bool
}

// normalizeTask maps a task's status onto the FR-006 vocabulary.
//
// A `blocked` task reports attention="none": it has an unmet dependency, has
// not run yet, and clears on its own when the dependency resolves. This is the
// operator's case — it is just information, and it is the reason `blocked`
// sorting last is acceptable at all.
func normalizeTask(t *task.Task) normalizedTask {
	switch t.Status {
	case task.StatusInbox:
		return normalizedTask{jobStatusQueued, string(task.StatusInbox), attentionNone, false}
	case task.StatusNext:
		return normalizedTask{jobStatusQueued, string(task.StatusNext), attentionNone, false}
	case task.StatusInProgress:
		return normalizedTask{jobStatusRunning, string(task.StatusInProgress), attentionNone, false}
	case task.StatusBlocked:
		return normalizedTask{jobStatusBlocked, string(task.StatusBlocked), attentionNone, false}
	case task.StatusDone:
		return normalizedTask{jobStatusCompleted, string(task.StatusDone), attentionNone, false}
	case task.StatusFailed:
		return normalizedTask{jobStatusFailed, string(task.StatusFailed), attentionNone, false}
	default:
		return normalizedTask{
			status:       jobStatusBlocked,
			nativeStatus: unknownNativeStatusPrefix + string(t.Status),
			attention:    attentionNone,
			unmapped:     true,
		}
	}
}

// normalizedSubagent is the derived status view of one delegated session.
type normalizedSubagent struct {
	status       string
	nativeStatus string
	attention    string
	unmapped     bool
	stopped      bool
}

// normalizeSubagent maps a durable lifecycle state onto the FR-006
// vocabulary.
//
// `cancelled` maps to `failed` with intentionally_stopped=true rather than to
// a sixth status value: an agent that stopped a delegation on purpose and then
// lost context would otherwise see a bare `failed` and re-dispatch work the
// user deliberately cancelled.
func normalizeSubagent(rec *session.LifecycleRecord) normalizedSubagent {
	switch rec.State {
	case session.LifecycleQueued:
		return normalizedSubagent{
			status:       jobStatusQueued,
			nativeStatus: string(session.LifecycleQueued),
			attention:    attentionNone,
		}
	case session.LifecycleRunning:
		return normalizedSubagent{
			status:       jobStatusRunning,
			nativeStatus: string(session.LifecycleRunning),
			attention:    attentionNone,
		}
	case session.LifecycleNeedsInput:
		// Blocked ON THE CALLER: only this caller can supply the answer.
		return normalizedSubagent{
			status:       jobStatusBlocked,
			nativeStatus: string(session.LifecycleNeedsInput),
			attention:    attentionCaller,
		}
	case session.LifecyclePaused:
		return normalizedSubagent{
			status:       jobStatusBlocked,
			nativeStatus: string(session.LifecyclePaused),
			attention:    attentionCaller,
		}
	case session.LifecycleCompleted:
		return normalizedSubagent{
			status:       jobStatusCompleted,
			nativeStatus: string(session.LifecycleCompleted),
			attention:    attentionNone,
		}
	case session.LifecycleFailed:
		return normalizedSubagent{
			status:       jobStatusFailed,
			nativeStatus: withFailedReason(string(session.LifecycleFailed), rec.FailedReason),
			attention:    attentionNone,
		}
	case session.LifecycleTimedOut:
		return normalizedSubagent{
			status:       jobStatusFailed,
			nativeStatus: withFailedReason(string(session.LifecycleTimedOut), rec.FailedReason),
			attention:    attentionNone,
		}
	case session.LifecycleCancelled:
		return normalizedSubagent{
			status:       jobStatusFailed,
			nativeStatus: withFailedReason(string(session.LifecycleCancelled), rec.FailedReason),
			attention:    attentionNone,
			stopped:      true,
		}
	default:
		return normalizedSubagent{
			status:       jobStatusBlocked,
			nativeStatus: unknownNativeStatusPrefix + string(rec.State),
			attention:    attentionNone,
			unmapped:     true,
		}
	}
}

// withFailedReason appends a lifecycle failure reason when present.
// LifecycleRecord.FailedReason is documented as deliberately open (not a
// closed enum), so the appended half is unvalidated runtime text — wrapped
// errors, filesystem paths, upstream API strings — and is redacted with the
// rest of native_status downstream.
func withFailedReason(state, reason string) string {
	if strings.TrimSpace(reason) == "" {
		return state
	}
	return state + "/" + reason
}
