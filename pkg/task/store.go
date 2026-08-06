// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/adhocore/gronx"
	"github.com/google/uuid"
	rrule "github.com/teambition/rrule-go"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ErrNotFound is returned when a task ID does not exist on disk.
var ErrNotFound = errors.New("task not found")

// ErrStatusConflict is returned by UpdateIfStatus when the on-disk task's
// CURRENT status no longer matches the caller's `expected` status — i.e.
// some OTHER writer (most commonly a concurrent user Stop landing via
// PlanEngine.StopTask/StopPlan) already moved the task out of the state the
// caller observed before it decided what to write. It is a control-flow
// sentinel (mirrors ErrAlreadyClaimed/ErrAlreadyRunning/ErrNotRestartable),
// deliberately NOT wrapping ErrValidation: this is an expected, legitimate
// race outcome, not malformed caller input, so it must never map to the
// same 400 the validation sentinels do. Callers use errors.Is to detect the
// conflict and drop their own pending write rather than treating it as a
// hard failure (ADR-052 FR-014/§6.4(b) — see UpdateIfStatus's doc comment).
var ErrStatusConflict = errors.New("task: status conflict: task is no longer in the expected status")

// ErrValidation is the sentinel wrapped by every user-facing field/transition
// validation error the store returns (vs. an internal I/O failure). The REST
// seam uses errors.Is(err, ErrValidation) to map to HTTP 400 rather than
// matching error substrings. Cycle/self-edge/depth sentinels (blocked_by.go)
// also wrap ErrValidation.
var ErrValidation = errors.New("task validation")

// ErrIllegalTransition is returned when a status PATCH requests a transition the
// lifecycle does not allow (e.g. done→inbox, or a client-supplied `blocked`).
// It wraps ErrValidation so the REST seam maps it to HTTP 400.
var ErrIllegalTransition = fmt.Errorf("%w: illegal status transition", ErrValidation)

// ErrBlockedNotSettable is returned when a client tries to set status=blocked
// directly. `blocked` is a derived side-state: the store sets it when a
// dependency is unmet and clears it to `next` when every blocker reaches done.
// It wraps ErrValidation so the REST seam maps it to HTTP 400.
var ErrBlockedNotSettable = fmt.Errorf(
	"%w: status %q is a derived side-state and cannot be set directly",
	ErrValidation,
	StatusBlocked,
)

// verr wraps a formatted message as a user-facing validation error (ErrValidation).
func verr(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrValidation}, args...)...)
}

// ValidatePriority rejects any priority value outside 1..5, with NO exception
// for 0.
//
// This is the single shared range-check every entry point that holds
// EXPLICIT-presence information about a caller-supplied priority must call:
// REST's task-create handler (which discards a wire *int's presence into a
// plain int before ever building a Task — see rest_tasks.go's
// handleTaskCreate), the create_task / create_task_in_workspace tools (which
// know presence via `args["priority"]`'s map "ok" — a key that IS present with
// value 0 must still validate), and this store's own updateLocked (whose
// Patch.Priority is already a *int, so presence is never in question there).
//
// Callers MUST NOT call this against a bare Task.Priority struct field read
// back from disk/defaults, where 0 legitimately means "unset" (see
// Task.Priority's own doc comment and EffectivePriority) — normalize() below
// guards its own call with `t.Priority != 0` for exactly that reason. A
// pointer/presence check at the seam is what makes "field absent" and "field
// explicitly 0" distinguishable in the first place; this function is only the
// shared range-check once that distinction has already been made.
func ValidatePriority(p int) error {
	if p < 1 || p > 5 {
		return verr("priority must be between 1 and 5, got %d", p)
	}
	return nil
}

// Store manages per-entity JSON task files under a single directory
// (~/.omnipus/tasks/). It is the unified task store. All read-modify-write
// paths are serialized by the process-wide TaskFileLock keyed by task ID, plus
// an advisory flock on the file itself.
type Store struct {
	dir  string
	lock *StripedLock
}

// New creates a Store rooted at dir using the process-wide TaskFileLock. dir
// is, by universal convention across every call site in this codebase,
// "<home>/tasks" — New derives home from it to run the one-way Milestone→Tag
// migration (ADR-049 D1, migrate_milestones.go) and the `planning`→`next` task
// status backfill (ADR-051 D5, migrate_planning_status.go) exactly once each,
// guarded by their own completion sentinels, before the store is used. New's
// signature cannot itself return an error without a breaking change rippling
// across ~30 call sites in other packages, so a migration failure is logged at
// Error and does NOT prevent the Store from being constructed — each
// migration is safe to retry on the next call/boot (idempotent, crash-safe).
func New(dir string) *Store {
	home := filepath.Dir(dir)
	if err := MigrateMilestonesToTags(home); err != nil {
		slog.Error("task: milestone migration failed", "error", err)
	}
	if err := MigratePlanningStatusToNext(home); err != nil {
		slog.Error("task: planning-status migration failed", "error", err)
	}
	return &Store{dir: dir, lock: TaskFileLock}
}

// Dir returns the store's task directory.
func (s *Store) Dir() string { return s.dir }

// validateID rejects IDs containing path separators, "..", or null bytes.
func validateID(id string) error {
	if id == "" {
		return fmt.Errorf("id must not be empty")
	}
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") || strings.ContainsRune(id, 0) {
		return fmt.Errorf("invalid id %q", id)
	}
	return nil
}

// path returns the absolute path for a task file.
func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// load reads and parses a single task file. It never rewrites on read.
func (s *Store) load(id string) (*Task, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("task: read %q: %w", id, err)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("task: parse %q: %w", id, err)
	}
	if t.ID == "" {
		t.ID = id
	}
	return &t, nil
}

// write persists a task atomically under the per-task lock with an advisory
// flock. Callers that perform a read-modify-write MUST already hold the
// per-task lock (Lock); write itself does not take it.
func (s *Store) write(t *Task) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("task: create dir: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("task: marshal %q: %w", t.ID, err)
	}
	p := s.path(t.ID)
	return fileutil.WithFlock(p, func() error {
		return fileutil.WriteFileAtomic(p, data, 0o600)
	})
}

// Lock returns the per-task striped mutex for id. Callers performing a manual
// read-modify-write outside Create/Update should hold this for the whole RMW.
func (s *Store) Lock(id string) interface {
	Lock()
	Unlock()
} {
	return s.lock.Get(id)
}

// Filter narrows the result of List. All fields are optional (zero = skip).
type Filter struct {
	WorkspaceID string
	Status      Status
	AgentID     string
	// CreatedBy filters on Task.CreatedBy, which is MIXED-NAMESPACE (a username
	// on the REST path, an agent id on the tool path — see Task.CreatedBy's
	// namespace warning). It MUST NOT be used as an ownership or authorization
	// predicate; use CreatedByAgentID for that. This field survives only for
	// display/diagnostic listings that are already scoped by something else.
	CreatedBy string
	// CreatedByAgentID filters on the agent-id-namespaced Task.CreatedByAgentID
	// (FR-037). This is the filter an "agent-owned work" query must use. Like
	// every other Filter field an empty value means "filter off" — so a caller
	// resolving a caller identity MUST reject an empty agent id BEFORE building
	// the Filter, or it silently asks for every task in the store. Tasks whose
	// own CreatedByAgentID is empty never match a non-empty value here (see
	// Task.CreatedByAgent, which fails closed on both sides).
	CreatedByAgentID string
	PlanID           string
	Surface          Surface
	ParentTaskID     string
	// ParentTaskIDSet, when true, applies the ParentTaskID filter even when
	// ParentTaskID is empty — i.e. "only top-level tasks" (no parent).
	ParentTaskIDSet bool
	// BlockedByID, when non-empty, returns only tasks whose BlockedBy contains
	// the given ID.
	BlockedByID string
	// Tag, when non-empty, returns only tasks whose (normalized) Tags contain
	// this exact tag.
	Tag string
}

// matches reports whether t passes every active filter field.
func (f Filter) matches(t *Task) bool {
	if f.WorkspaceID != "" && t.WorkspaceID != f.WorkspaceID {
		return false
	}
	if f.Status != "" && t.Status != f.Status {
		return false
	}
	if f.AgentID != "" && t.AgentID != f.AgentID {
		return false
	}
	if f.CreatedBy != "" && t.CreatedBy != f.CreatedBy {
		return false
	}
	// FR-037: an empty Task.CreatedByAgentID is never attributed to anyone, so
	// it can never satisfy a non-empty filter value — CreatedByAgent enforces
	// that fail-closed on both sides rather than comparing "" == "".
	if f.CreatedByAgentID != "" && !t.CreatedByAgent(f.CreatedByAgentID) {
		return false
	}
	if f.PlanID != "" && t.PlanID != f.PlanID {
		return false
	}
	if f.Tag != "" && !containsString(t.Tags, f.Tag) {
		return false
	}
	if f.Surface != "" && t.EffectiveSurface() != f.Surface {
		return false
	}
	if f.ParentTaskIDSet && t.ParentTaskID != f.ParentTaskID {
		return false
	}
	if !f.ParentTaskIDSet && f.ParentTaskID != "" && t.ParentTaskID != f.ParentTaskID {
		return false
	}
	if f.BlockedByID != "" && !containsString(t.BlockedBy, f.BlockedByID) {
		return false
	}
	return true
}

// scanTaskIDs returns the task IDs present in the store directory, derived
// from every regular *.json file's name (ReadDir → skip subdirectories and
// non-.json entries → trim the .json suffix). It is the shared "scan the task
// dir" idiom used by List and the blocked_by dependency-cascade scans
// (AdvanceBlockedDependents, cascadeDeleteEdges, DropOrphanEdges). It returns
// the raw os.ReadDir error as-is (including a not-exist error) so each caller
// keeps its own missing-dir handling and error-wrapping context.
func (s *Store) scanTaskIDs() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	return ids, nil
}

// List returns all tasks matching filter, sorted by priority ASC then
// created_at ASC. Unreadable/corrupt files are logged at Warn and skipped.
// Delegates to ListWithUnreadable and discards the unreadable-ID set —
// callers that need to know WHICH IDs were skipped (H2: the recovery-sweep
// rediscovery path) use ListWithUnreadable directly.
func (s *Store) List(filter Filter) ([]Task, error) {
	tasks, _, err := s.ListWithUnreadable(filter)
	return tasks, err
}

// ListWithUnreadable behaves exactly like List (same filtering, same sort,
// same Warn log per skipped file) but additionally returns the IDs of task
// files that exist on disk but could not be read/parsed (corrupt,
// permission-denied, mid-write, etc.).
//
// H2: List's silent Warn+skip made a persistently-unreadable RRULE-trigger
// task invisible to the one mechanism meant to rediscover and rescue it —
// the trigger scheduler's boot Reconcile and crash-recovery sweep both
// enumerate candidates via a List-shaped call, so a corrupted task file was
// never a candidate for recovery, forever, with no attribution beyond a
// generic unattributed WARN. Callers that need to escalate on persistent
// unreadability (RunRecoverySweep, Reconcile) use this method instead of
// List so they can log loudly, by task ID, when a file stays unreadable
// across repeated observations.
func (s *Store) ListWithUnreadable(filter Filter) (tasks []Task, unreadableIDs []string, err error) {
	ids, err := s.scanTaskIDs()
	if err != nil {
		if os.IsNotExist(err) {
			return []Task{}, nil, nil
		}
		return nil, nil, fmt.Errorf("task: list dir: %w", err)
	}

	result := make([]Task, 0, len(ids))
	var unreadable []string
	for _, id := range ids {
		t, err := s.load(id)
		if err != nil {
			slog.Warn("task: skip unreadable task file", "id", id, "error", err)
			unreadable = append(unreadable, id)
			continue
		}
		if !filter.matches(t) {
			continue
		}
		result = append(result, *t)
	}

	sort.Slice(result, func(i, j int) bool {
		pi, pj := result[i].EffectivePriority(), result[j].EffectivePriority()
		if pi != pj {
			return pi < pj
		}
		return result[i].CreatedAt < result[j].CreatedAt
	})
	return result, unreadable, nil
}

// Get returns the task with the given id, or ErrNotFound if absent.
func (s *Store) Get(id string) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return s.load(id)
}

// HasVisibleContent reports whether s contains at least one rune a human
// would perceive as visible content (UAT round-2 S3 finding "invisible
// codepoints bypass title validation"). strings.TrimSpace only strips
// unicode.IsSpace, which is false for zero-width/format characters —
// ZERO WIDTH SPACE (U+200B), ZERO WIDTH NON-JOINER (U+200C), ZERO WIDTH
// JOINER (U+200D), WORD JOINER (U+2060), BOM / ZERO WIDTH NO-BREAK SPACE
// (U+FEFF), and SOFT HYPHEN (U+00AD) are all Unicode category Cf (Format);
// BRAILLE PATTERN BLANK (U+2800) renders as a blank glyph but is category So
// (Symbol, other), not Cf, so it needs an explicit exception alongside Cf.
// A title built entirely from these survives TrimSpace unchanged (every rune
// has IsSpace()==false) and produces a visually-blank, unfindable/
// unfilterable title/chip. This checks the semantic CLASS (whitespace +
// format-control + the one Cf-adjacent exception), not a blacklist of the
// specific codepoints a tester happened to try — a future zero-width
// addition to Unicode is caught for free. Exported so pkg/plan (which
// already imports this package for AcceptanceCriterion/NormalizeCriteria)
// can apply the identical rule to Plan titles.
func HasVisibleContent(s string) bool {
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			continue
		case r == '\u2800': // BRAILLE PATTERN BLANK — blank glyph, category So not Cf
			continue
		case unicode.In(r, unicode.Cf):
			continue
		}
		return true
	}
	return false
}

// normalize applies field defaults and validates the entity. It does NOT touch
// the filesystem (cycle checks are the caller's responsibility via the DAG
// validator). Returns a user-facing error on invalid input.
func (t *Task) normalize() error {
	// Trim before validating (S2 UAT finding B sibling: the same
	// untrimmed-`== ""` pattern that let a whitespace-only Plan title through
	// also existed here). A whitespace-only title (" \t ") is not user-facing
	// content — reject it as empty rather than persisting a blank-looking
	// board card; a legitimate title's incidental leading/trailing whitespace
	// is silently normalized rather than rejected. HasVisibleContent then
	// catches the invisible/zero-width/format case TrimSpace itself misses
	// (round-2 S3 finding above) — a title made ENTIRELY of those codepoints
	// is rejected the same as "".
	t.Title = strings.TrimSpace(t.Title)
	if t.Title == "" || !HasVisibleContent(t.Title) {
		return verr("title is required")
	}
	if len([]rune(t.Title)) > 200 {
		return verr("title must be 200 characters or fewer")
	}
	if t.Action == "" {
		t.Action = ActionLLM
	}
	if !IsValidAction(t.Action) {
		return verr("invalid action %q (only %q in Tier 2)", t.Action, ActionLLM)
	}
	if t.Status == "" {
		t.Status = StatusInbox
	}
	if !IsValidStatus(t.Status) {
		return verr("invalid status %q", t.Status)
	}
	if t.CancelReason != "" {
		if !IsValidCancelReason(t.CancelReason) {
			return verr("invalid cancel_reason %q", t.CancelReason)
		}
		if t.Status != StatusFailed {
			return verr("cancel_reason is only valid when status is failed")
		}
	}
	// t.Priority == 0 always means "unset" at THIS layer (Task.Priority is a
	// plain int with no way to carry presence information once the struct is
	// built) — see ValidatePriority's doc comment for why the explicit-vs-
	// absent distinction must be made by the CALLER (REST/tool seam) before
	// ever populating this field, not here.
	if t.Priority != 0 {
		if err := ValidatePriority(t.Priority); err != nil {
			return err
		}
	}
	if t.WorkspaceID == "" {
		return verr("workspace_id is required")
	}
	if t.Surface == "" {
		t.Surface = SurfaceUser
	}
	if !IsValidSurface(t.Surface) {
		return verr("invalid surface %q", t.Surface)
	}
	if len(t.Description) > 2000 {
		return verr("description must be 2000 characters or fewer")
	}
	if len(t.Prompt) > 10000 {
		return verr("prompt must be 10000 characters or fewer")
	}
	if len(t.Result) > 50000 {
		return verr("result must be 50000 characters or fewer")
	}
	if err := validateTodos(t.Todos); err != nil {
		return err
	}
	if t.Trigger != nil {
		if err := ValidateTrigger(t.Trigger); err != nil {
			return err
		}
	}
	normalizedTags, err := normalizeTags(t.Tags)
	if err != nil {
		return err
	}
	t.Tags = normalizedTags
	if t.Criteria != nil {
		normalizedCriteria, err := normalizeCriteria(t.Criteria)
		if err != nil {
			return err
		}
		t.Criteria = normalizedCriteria
	}
	if t.MaxAttempts != nil && *t.MaxAttempts < 1 {
		return verr("max_attempts must be at least 1")
	}
	if err := t.validateScheduledAgentAssignment(); err != nil {
		return err
	}
	return nil
}

// validateScheduledAgentAssignment rejects a task that would fire on its own
// schedule with no agent to execute it. A trigger is AUTO-FIRING — the task
// executor dispatches it with no human present to step in — for
// once/every/recurring; `manual` (or no trigger at all) starts only when a
// human explicitly runs it, so an empty AgentID there is a legitimate
// human-only task (the executor's own dispatch guard,
// pkg/agent/task_executor.go, treats AgentID=="" as exactly that). Combined
// with an agent-executable action (ActionLLM — Tier 2's only action), an
// auto-firing trigger with no assigned agent is a dead task: it will fire and
// have nothing to run. Operator decision: reject this at the API/store layer
// (Create and Update alike) rather than silently persisting a task that can
// never be dispatched — this also closes the raw-API/agent-tool path, not
// just the SPA form.
func (t *Task) validateScheduledAgentAssignment() error {
	if t.Trigger == nil {
		return nil
	}
	switch t.Trigger.Type {
	case TriggerOnce, TriggerEvery, TriggerRecurring:
	default:
		return nil
	}
	if t.Action != ActionLLM {
		return nil
	}
	if t.AgentID == "" {
		return verr(
			"a scheduled (once/every/recurring) task must be assigned to an agent (agent_id is required when trigger.type=%q)",
			t.Trigger.Type,
		)
	}
	return nil
}

// maxTagRunes and maxTagsPerTask bound Task.Tags (spec Part A §B). Also used
// by the milestone migration (migrate_milestones.go) when sizing the
// generated "milestone:<name>" tag.
const (
	maxTagRunes    = 64
	maxTagsPerTask = 16
)

// normalizeTags normalizes (lowercase+trim), validates, and dedups tags, per
// the exact ordering documented in spec Part A §B: (1) normalize, (2) reject
// empty-after-trim, (3) reject >64 runes, (4) reject >16 tags — checked
// against the normalized-but-NOT-YET-deduped count (so 17 raw entries that
// would dedup down to fewer than 16 distinct tags are still rejected; this
// matches the spec's literal numbered order, which places the count check
// before dedup), (5) dedup case-fold collisions preserving first-seen order.
// A nil/empty input returns (nil, nil) — tags are optional.
func normalizeTags(tags []string) ([]string, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	normalized := make([]string, len(tags))
	for i, raw := range tags {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			return nil, verr("tags[%d]: tag must not be empty", i)
		}
		if n := len([]rune(tag)); n > maxTagRunes {
			return nil, verr("tags[%d]: tag must be %d characters or fewer", i, maxTagRunes)
		}
		normalized[i] = tag
	}
	if len(normalized) > maxTagsPerTask {
		return nil, verr("tags: at most %d tags per task", maxTagsPerTask)
	}
	seen := make(map[string]bool, len(normalized))
	out := make([]string, 0, len(normalized))
	for _, tag := range normalized {
		if seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out, nil
}

// validateTodos checks each todo's text length (1..500) and that status is a
// known tri-state value. The UnmarshalJSON default-to-pending is only for
// legacy disk reads; tool/REST input must supply a valid status explicitly.
func validateTodos(todos []Todo) error {
	for i, td := range todos {
		n := len([]rune(td.Text))
		if n < 1 {
			return verr("todo[%d]: text is required", i)
		}
		if n > 500 {
			return verr("todo[%d]: text must be 500 characters or fewer", i)
		}
		if !IsValidTodoStatus(td.Status) {
			return verr("todo[%d]: status %q is not valid (must be pending, in_progress, or completed)", i, td.Status)
		}
	}
	return nil
}

// minTriggerIntervalSeconds is the floor on a recurring/every trigger's
// effective interval (LOW-1). A trigger that would fire more often than once per
// minute is rejected to prevent a `* * * * * *` self-DoS, mirroring the
// every_ms >= 1000 intent at the cron layer.
const minTriggerIntervalSeconds = 60

// ValidateTrigger validates a trigger's type and the config required for that
// type (Detail #3). Empty/unset config keys for the wrong type are ignored. For
// a recurring trigger, config must carry exactly one of cron_expr (legacy,
// validated with the same cron library the trigger executor uses — gronx —
// enforcing a >= 60s effective interval per LOW-1) or rrule (RFC 5545, Calendar
// Recurrence Redesign — see validateRRULE and the spec's normative
// "Validation (ValidateTrigger, RRULE path)" section). The rrule/dtstart_ms/tz
// keys are legal only on a recurring trigger — present on any other type they
// are rejected outright (spec Validation §1).
func ValidateTrigger(tr *Trigger) error {
	if !IsValidTriggerType(tr.Type) {
		return verr("invalid trigger type %q", tr.Type)
	}
	if tr.Type != TriggerRecurring &&
		(tr.Config.Rrule != nil || tr.Config.DtstartMs != nil || tr.Config.Tz != nil) {
		return verr(
			"trigger %q: config.rrule/dtstart_ms/tz are only valid on a %q trigger",
			tr.Type, TriggerRecurring,
		)
	}
	switch tr.Type {
	case TriggerOnce:
		if tr.Config.AtMs == nil {
			return verr("trigger %q requires config.at_ms", tr.Type)
		}
	case TriggerEvery:
		if tr.Config.EveryMs == nil {
			return verr("trigger %q requires config.every_ms", tr.Type)
		}
		if *tr.Config.EveryMs < 1000 {
			return verr("trigger %q config.every_ms must be at least 1000ms", tr.Type)
		}
	case TriggerRecurring:
		hasCron := tr.Config.CronExpr != nil && *tr.Config.CronExpr != ""
		hasRrule := tr.Config.Rrule != nil && *tr.Config.Rrule != ""
		switch {
		case hasCron && hasRrule:
			return verr(
				"trigger %q config must carry exactly one of config.cron_expr or config.rrule, not both",
				tr.Type,
			)
		case hasRrule:
			if err := validateRRULE(tr.Config); err != nil {
				return err
			}
		case hasCron:
			if err := validateCronExpr(*tr.Config.CronExpr); err != nil {
				return err
			}
		default:
			return verr("trigger %q requires config.cron_expr or config.rrule", tr.Type)
		}
	case TriggerManual:
		// no config required
	}
	return nil
}

// validateCronExpr rejects an unparseable cron expression and enforces the
// minTriggerIntervalSeconds floor by computing the next two fire instants from a
// fixed reference and rejecting when they are < 60s apart (LOW-1 self-DoS guard).
func validateCronExpr(expr string) error {
	if !gronx.IsValid(expr) {
		return verr("trigger config.cron_expr %q is not a valid cron expression", expr)
	}
	// Use a fixed reference instant so the floor check is deterministic. The
	// second field of a 6-field expr is the only way to fire sub-minute; a
	// 5-field expr can never fire more than once per minute.
	ref := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	first, err := gronx.NextTickAfter(expr, ref, false)
	if err != nil {
		return verr("trigger config.cron_expr %q could not be evaluated: %v", expr, err)
	}
	second, err := gronx.NextTickAfter(expr, first, false)
	if err != nil {
		return verr("trigger config.cron_expr %q could not be evaluated: %v", expr, err)
	}
	if second.Sub(first) < minTriggerIntervalSeconds*time.Second {
		return verr(
			"trigger config.cron_expr %q fires more often than once per %ds (self-DoS guard)",
			expr,
			minTriggerIntervalSeconds,
		)
	}
	return nil
}

// rruleMaxLen is the maximum allowed length, in characters, of an RRULE body
// (Calendar Recurrence Redesign spec, Validation §2 input bounds).
const rruleMaxLen = 512

// rruleLivenessYears is the liveness-bound horizon (Validation §5): a rule
// producing zero occurrences within this many years of its DTSTART is
// rejected as "never fires" (bounds pathological rules such as
// FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31).
const rruleLivenessYears = 5

// rruleMaxCount is the upper bound on an RRULE's COUNT value (Validation §6);
// it bounds every COUNT-exhaustion check and DTSTART skip-walk elsewhere in
// the RRULE engine (pkg/task/rrule.go).
const rruleMaxCount = 100000

// validateRRULE validates the RRULE branch of a `recurring` trigger's config
// per the normative "Validation (ValidateTrigger, RRULE path)" section of
// docs/internal/specs/calendar-recurrence-redesign-spec.md (§1-6). Called
// only when cfg.Rrule is a non-empty string; ValidateTrigger has already
// established exactly-one-of cron_expr/rrule for the caller.
//
// It reuses the unexported loadTZ/parseOption plumbing from rrule.go (same
// package) so the validator's interpretation of the rule (FREQ, DTSTART,
// COUNT, UNTIL, BYSECOND) can never drift from the expansion/scheduler
// engine's interpretation of the same string (Timezone Semantics §4).
func validateRRULE(cfg TriggerConfig) error {
	rruleBody := *cfg.Rrule

	// §1: dtstart_ms and tz are required siblings of rrule.
	if cfg.DtstartMs == nil {
		return verr("trigger config.rrule requires config.dtstart_ms")
	}
	if cfg.Tz == nil || *cfg.Tz == "" {
		return verr("trigger config.rrule requires config.tz")
	}

	// §2: length bound, checked before parsing (cheap defense-in-depth against
	// a pathologically large payload).
	if len(rruleBody) > rruleMaxLen {
		return verr("trigger config.rrule must be %d characters or fewer", rruleMaxLen)
	}

	// §1: tz must load (embedded tzdata, Constraint #1).
	loc, err := loadTZ(*cfg.Tz)
	if err != nil {
		return verr("trigger config.tz %q could not be loaded: %v", *cfg.Tz, err)
	}

	// §1: the RRULE body must parse.
	opt, err := parseOption(rruleBody, *cfg.DtstartMs, loc)
	if err != nil {
		return verr("trigger config.rrule %q could not be parsed: %v", rruleBody, err)
	}

	// §2: FREQ=SECONDLY is rejected outright.
	if opt.Freq == rrule.SECONDLY {
		return verr("trigger config.rrule must not use FREQ=SECONDLY")
	}

	// §2: any BYSECOND value other than the DTSTART second is rejected — the
	// editor never emits BYSECOND at all, so this only fires on hand-crafted
	// API payloads (spec note).
	dtstartSecond := opt.Dtstart.Second()
	for _, sec := range opt.Bysecond {
		if sec != dtstartSecond {
			return verr(
				"trigger config.rrule BYSECOND value %d does not match the DTSTART second (%d)",
				sec, dtstartSecond,
			)
		}
	}

	// §3: UNTIL, when present, must not precede DTSTART.
	if !opt.Until.IsZero() && opt.Until.Before(opt.Dtstart) {
		return verr("trigger config.rrule UNTIL must not precede config.dtstart_ms")
	}

	// §4: bounded-window minimum-gap scan (defense-in-depth; §2's hard rejects
	// are the operative sub-minute mechanism — see rrule.go's ExpandForValidation
	// doc comment). Rules yielding fewer than two occurrences in the window
	// (e.g. COUNT=1) trivially pass.
	instants, err := ExpandForValidation(rruleBody, *cfg.DtstartMs, *cfg.Tz)
	if err != nil {
		return verr("trigger config.rrule could not be validated: %v", err)
	}
	for i := 1; i < len(instants); i++ {
		if instants[i]-instants[i-1] < minTriggerIntervalSeconds*1000 {
			return verr(
				"trigger config.rrule fires more often than once per %ds (self-DoS guard)",
				minTriggerIntervalSeconds,
			)
		}
	}

	// §5: liveness bound — a rule that never fires within rruleLivenessYears
	// of DTSTART is rejected (also bounds worst-case work on never-matching
	// rules such as Feb 31).
	live, err := HasOccurrenceWithinYears(rruleBody, *cfg.DtstartMs, *cfg.Tz, rruleLivenessYears)
	if err != nil {
		return verr("trigger config.rrule could not be validated: %v", err)
	}
	if !live {
		return verr(
			"trigger config.rrule never fires within %d years of config.dtstart_ms (rule never fires)",
			rruleLivenessYears,
		)
	}

	// §6: COUNT bound.
	if opt.Count > rruleMaxCount {
		return verr("trigger config.rrule COUNT must be %d or fewer", rruleMaxCount)
	}

	return nil
}

// Create persists a new task. It generates a UUID when ID is empty, stamps
// CreatedAt/UpdatedAt, applies defaults, validates fields, and validates the
// blocked_by DAG (rejecting cycles). The new task always lands per its Status
// default (inbox) unless the caller set a different valid status.
//
// Create takes the per-task lock internally.
func (s *Store) Create(t *Task) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if err := validateID(t.ID); err != nil {
		return err
	}
	if err := t.normalize(); err != nil {
		return err
	}

	mu := s.lock.Get(t.ID)
	mu.Lock()
	defer mu.Unlock()

	// Reject a blocked_by set that would create a cycle (or reference missing
	// tasks). The task is not yet on disk, so no inbound edges to it exist.
	if len(t.BlockedBy) > 0 {
		if err := s.validateBlockedByLocked(t.ID, t.BlockedBy); err != nil {
			return err
		}
	}
	// Reject a parent edge that would create a cycle in the parent chain.
	if t.ParentTaskID != "" {
		if err := s.checkParentAcyclicLocked(t.ID, t.ParentTaskID); err != nil {
			return err
		}
	}

	// S2 UAT finding A: derive the `blocked` side-state at create time too, not
	// just on later Update/AddDependency/RestartReset. The create_task agent
	// tool (pkg/tools/task.go) seeds new tasks directly into `next` (not the
	// REST-only `inbox` default) and accepts blocked_by in the same call — a
	// task created that way with an unmet dependency must land `blocked`
	// immediately rather than surfacing as a dispatchable `next` task until
	// some later write happens to touch it. recomputeBlockedStateLocked is a
	// no-op for every status other than next/blocked (inbox included), so the
	// REST create path (always `inbox`) is unaffected by this call.
	s.recomputeBlockedStateLocked(t)

	now := time.Now().UTC().Format(time.RFC3339)
	t.CreatedAt = now
	t.UpdatedAt = now
	return s.write(t)
}

// CreateByAgent persists a new task created BY an agent, stamping the
// agent-id-namespaced attribution field Task.CreatedByAgentID (FR-037) before
// delegating to Create. It is the sanctioned agent creation path: the agent
// tools (pkg/tools/task.go's create_task, pkg/tools/todos.go's set_todos, and
// pkg/sysagent/tools/task.go) call this with tools.ToolAgentID(ctx) instead of
// setting the field by hand, so the one field an ownership predicate is allowed
// to read has exactly one writer.
//
// It fails closed on an empty agentID rather than writing an empty attribution
// that no predicate could ever match — an unresolved caller identity is a bug
// at the call site, not a task with anonymous provenance.
//
// Every other creation path (REST/human via Create, and the internal
// clone/derive paths in pkg/agent) leaves CreatedByAgentID exactly as it found
// it: empty for a human-created task, and carried over verbatim for a clone,
// which keeps a cloned task attributed to the agent that authored the original.
func (s *Store) CreateByAgent(t *Task, agentID string) error {
	if agentID == "" {
		return verr("created_by_agent_id: agent id is required on the agent creation path")
	}
	t.CreatedByAgentID = agentID
	return s.Create(t)
}

// Patch is a partial update applied by Update. Only non-nil fields are written.
//
// There is deliberately no CreatedByAgentID field here: FR-037 attribution is
// set once at creation and is not reassignable, so no update path can move a
// task into another agent's roster.
type Patch struct {
	Title       *string
	Description *string
	Prompt      *string
	Status      *Status
	// CancelReason is the write path for Task.CancelReason (ADR-052 FR-028).
	// nil = unchanged; pointing at "" clears it; pointing at a valid
	// CancelReason value sets it (mirrors plan.Patch.FailedReason,
	// pkg/plan/store.go:259 — a single, non-double pointer, since "" is
	// itself a legal target value, not a "no clear" sentinel).
	CancelReason *CancelReason
	AgentID      *string
	Priority     *int
	BlockedBy    *[]string
	Todos        *[]Todo
	Trigger      **Trigger // double pointer: outer nil = unchanged, *outer nil = clear
	Due          *string
	PlanID       *string
	Tags         *[]string
	Criteria     *[]AcceptanceCriterion
	// WriteSet, Stream, and IsJoin are the ADR-053 plan-member fields
	// (§Contract Surface — "write_sets + rationale on create_plan", US-11
	// G-16). Meaningful only when the task has a non-empty PlanID; plan-lint
	// (pkg/plan) reads them at approve time to reject overlapping parallel
	// streams and join-less convergence points. A nil pointer leaves the
	// field unchanged; *WriteSet pointing at an empty/nil slice CLEARS the
	// declared write-set (an exploratory member, D10); *Stream pointing at
	// "" CLEARS the stream label; IsJoin is a plain overwrite (mirrors the
	// AttemptCount/Surface bool/int single-pointer convention above — unlike
	// Trigger/MaxAttempts there is no meaningful "unset vs explicit false"
	// distinction for IsJoin worth a double pointer).
	WriteSet *[]string
	Stream   *string
	IsJoin   *bool
	// MaxAttempts is a double pointer (mirrors Trigger): outer nil = unchanged,
	// *outer nil = clear the override (inherit the global PlanningConfig
	// default), *outer non-nil = set to that value.
	MaxAttempts **int
	// ResumeFromCommit is the runtime plan engine's write path for
	// Task.ResumeFromCommit (ADR-053 D13/G-12). A non-nil pointer to "" clears
	// it (fresh attempt — the explicit no-commit fallback). Server-set only:
	// the REST/PATCH wire seam MUST NOT accept this field from a client (it is
	// scoped out of the client PATCH surface); only the plan engine writes it
	// during Play.
	ResumeFromCommit *string
	// AttemptCount is the runtime goal-loop's own write path for
	// Task.AttemptCount (ADR-049 D7/R4/C17). Server-set only — the REST/PATCH
	// wire boundary must never accept this field from a client; only the
	// runtime engine (pkg/agent's TaskExecutor) writes it, once per attempt.
	AttemptCount  *int
	Surface       *Surface
	Result        *string
	Artifacts     *[]string
	SessionID     *string
	StartedAt     *string
	CompletedAt   *string
	FollowedUp    *bool
	SourceChannel *string
	SourceChatID  *string
	// PendingJudgeClaim is the runtime/tool-layer write path for
	// Task.PendingJudgeClaim (ADR-049 C1/SD-B2, review r1). A non-nil pointer
	// to "" clears it (adjudication finished, either outcome).
	PendingJudgeClaim *string

	// allowBlockedSet is the internal escape hatch that permits a Status patch to
	// set or clear the derived `blocked` side-state. It is NEVER set from the wire
	// (the REST/PATCH seam leaves it false); only the dependency-recompute paths
	// (recomputeBlockedStateLocked) set it. Unexported so callers outside this
	// package cannot bypass the ErrBlockedNotSettable guard.
	allowBlockedSet bool
}

// validateTransition reports whether moving from→to is a legal lifecycle
// transition (N1). The policy is deliberately permissive — the lifecycle is a
// workflow aid, not a strict state machine — and only forbids the transitions
// that would corrupt invariants:
//
//   - `done` is terminal and FROZEN: no transition out of `done` is permitted
//     (rejects the reviewer's example, done→inbox). A successful task is final;
//     re-running is normally done via a fresh trigger fire (SpawnReset), not a
//     status edit. (`failed` is NOT frozen — a failed task may be re-queued
//     for retry.) Narrow carve-out: when repeating is true (the task's
//     trigger is recurring/every — see Trigger.IsRepeating), `done`→
//     `in_progress` IS permitted. A repeating task's series survives a
//     per-run `done` status (task_trigger.go's OnTaskUpserted keeps its next
//     occurrence armed), so the task is not really "final" the way a
//     manual/`once` task is — the manual "Run now" action (PATCH
//     status=in_progress → StartTaskNow) must be able to do the same thing an
//     autonomous re-fire can already do. The carve-out is intentionally
//     narrow: only the exact done→in_progress edge, only for a repeating
//     trigger; every other done→* transition stays frozen.
//   - `blocked` is a derived side-state: it is entered/left only through the
//     internal allowBlockedSet hatch (the dependency-recompute paths). A direct
//     wire move TO `blocked` is rejected upstream (ErrBlockedNotSettable);
//     leaving `blocked` other than via the hatch is rejected here.
//
// A no-op (from == to) is always allowed. When internal is true any transition
// into or out of `blocked` is permitted. Returns ErrIllegalTransition (wrapping
// ErrValidation) on rejection.
func validateTransition(from, to Status, internal, repeating bool) error {
	if from == to {
		return nil
	}
	if internal && (to == StatusBlocked || from == StatusBlocked) {
		return nil
	}
	// `done` is frozen — a completed task is final — except the narrow
	// repeating-trigger carve-out documented above.
	if from == StatusDone {
		if repeating && to == StatusInProgress {
			return nil
		}
		return fmt.Errorf("%w: %q → %q is not permitted (done is terminal)", ErrIllegalTransition, from, to)
	}
	// Leaving `blocked` is only legal via the internal recompute hatch.
	if from == StatusBlocked && !internal {
		return fmt.Errorf(
			"%w: %q → %q is not permitted (blocked clears automatically when dependencies complete)",
			ErrIllegalTransition,
			from,
			to,
		)
	}
	return nil
}

// validateStopGuard enforces the Stop guarantee's store-level backstop
// (ADR-052 FR-014/§6.4(b)): once a task is failed WITH
// CancelReason==stopped_by_user, no write may complete it as `done` — a
// user-initiated Stop is a terminal outcome that no subsequent write
// (engine or otherwise) is ever sanctioned to silently erase into a false
// "done". This closes interleaving (b) from the FR-014 TOCTOU postmortem: a
// judge verdict computed just before a Stop landed could otherwise still
// resolve `failed[stopped_by_user] -> done` via a plain Update (the
// lifecycle matrix has never frozen `failed` — a genuinely-failed task MUST
// remain retryable, so `validateTransition` alone cannot distinguish "retry
// a real failure" from "complete a cancelled one"). Re-queuing the task
// (failed[stopped_by_user] -> next/in_progress, via the Play/restart route
// — RestartReset, which bypasses this check entirely by writing fields
// directly rather than going through Update/updateLocked — or the plain
// PATCH "Run" re-run route) remains legal: a resumed/restarted task is no
// longer "stopped", it is running again by the USER's own action, and
// nothing here (or in validateTransition) blocks that direction.
//
// `current` is the ON-DISK task as loaded at the top of updateLocked,
// before any patch field has been merged onto it — CancelReason is only
// ever non-empty when Status==failed (normalize's own invariant, re-checked
// post-patch by the cross-field guard further down in updateLocked), so
// checking CancelReason alone is sufficient; the explicit Status==failed
// check below is belt-and-suspenders documentation of that invariant, not
// a load-bearing second condition.
func validateStopGuard(current *Task, to Status) error {
	if current.Status == StatusFailed && current.CancelReason == CancelReasonStoppedByUser && to == StatusDone {
		return fmt.Errorf(
			"%w: task is failed(stopped_by_user); done is not a permitted transition "+
				"(a user-cancelled task can never be silently completed)",
			ErrIllegalTransition,
		)
	}
	return nil
}

// Update applies patch to the task identified by id and persists the result.
// It validates field constraints and the blocked_by DAG (when blocked_by is
// changed). It takes the per-task lock internally. Delegates to
// UpdateWithPrior and discards the prior-state snapshot — callers that need
// the immediately-prior task (M-BE1: an atomic audit diff) use
// UpdateWithPrior directly.
func (s *Store) Update(id string, patch Patch) (*Task, error) {
	updated, _, err := s.UpdateWithPrior(id, patch)
	return updated, err
}

// UpdateWithPrior behaves exactly like Update but additionally returns a
// snapshot of the task as it stood IMMEDIATELY BEFORE patch was applied,
// captured under the SAME per-task lock as the write.
//
// M-BE1: a separate pre-patch store.Get() (taken before, not under, the
// write's lock) has a TOCTOU window — under two concurrent PATCHes to the
// same task, the second call's "prior" read can complete before the first
// call's write lands, then the first call's write lands, then the second
// call's own write lands on top of it; the second call's recorded "prior"
// is stale (it never reflects the first call's write) even though it was
// read before its own write. Loading `prior` here, inside the same
// mu.Lock()/Unlock() span that updateLocked's own write executes in, closes
// that window: because the per-task StripedLock fully serializes every
// read-modify-write on this task ID, whichever call's critical section runs
// second is guaranteed to load the state the first call's write just
// produced.
//
// prior is a value copy, safe to read after this call returns even though
// updateLocked performs its own independent load+mutate+write against a
// separate in-memory Task built from the same file content.
func (s *Store) UpdateWithPrior(id string, patch Patch) (updated *Task, prior *Task, err error) {
	if err = validateID(id); err != nil {
		return nil, nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	before, err := s.load(id)
	if err != nil {
		return nil, nil, err
	}
	priorCopy := *before

	updated, err = s.updateLocked(id, patch)
	if err != nil {
		return nil, nil, err
	}
	return updated, &priorCopy, nil
}

// updateLocked is the body of Update; the caller must hold the per-task lock.
func (s *Store) updateLocked(id string, patch Patch) (*Task, error) {
	t, err := s.load(id)
	if err != nil {
		return nil, err
	}

	if patch.Title != nil {
		// Trim before validating (S2 UAT finding B sibling — see normalize()'s
		// matching comment): a whitespace-only patch title is rejected as
		// empty; a legitimate title's incidental leading/trailing whitespace
		// is normalized away rather than persisted verbatim. HasVisibleContent
		// then catches the invisible/zero-width/format case TrimSpace itself
		// misses (round-2 S3 finding, see HasVisibleContent's doc comment) — a
		// patch title made ENTIRELY of those codepoints is rejected the same
		// as "".
		trimmedTitle := strings.TrimSpace(*patch.Title)
		if trimmedTitle == "" || !HasVisibleContent(trimmedTitle) {
			return nil, verr("title must not be empty")
		}
		if len([]rune(trimmedTitle)) > 200 {
			return nil, verr("title must be 200 characters or fewer")
		}
		t.Title = trimmedTitle
	}
	if patch.Description != nil {
		if len(*patch.Description) > 2000 {
			return nil, verr("description must be 2000 characters or fewer")
		}
		t.Description = *patch.Description
	}
	if patch.Prompt != nil {
		if len(*patch.Prompt) > 10000 {
			return nil, verr("prompt must be 10000 characters or fewer")
		}
		t.Prompt = *patch.Prompt
	}
	if patch.Status != nil {
		if !IsValidStatus(*patch.Status) {
			return nil, verr("invalid status %q", *patch.Status)
		}
		// `blocked` is a derived side-state — it is never settable through the
		// public update path. The store sets it when a dependency is unmet and
		// clears it to `next` when every blocker reaches done. allowBlockedSet is
		// the internal escape hatch used by the dependency-recompute paths only.
		if *patch.Status == StatusBlocked && !patch.allowBlockedSet {
			return nil, ErrBlockedNotSettable
		}
		// Reject illegal lifecycle transitions (N1). A no-op (same status) and any
		// transition out of the derived `blocked` state via the internal hatch are
		// always allowed; done→in_progress is additionally allowed when t's
		// trigger repeats (see validateTransition's doc comment).
		repeatingTrigger := t.Trigger.IsRepeating()
		if err := validateTransition(t.Status, *patch.Status, patch.allowBlockedSet, repeatingTrigger); err != nil {
			return nil, err
		}
		// ADR-052 FR-014/§6.4(b) Stop guarantee backstop: reject
		// failed[stopped_by_user] -> done unconditionally, even though
		// validateTransition's own matrix permits failed -> * generally (a
		// genuine failure must stay retryable). See validateStopGuard's doc
		// comment for the full TOCTOU rationale this closes.
		if err := validateStopGuard(t, *patch.Status); err != nil {
			return nil, err
		}
		// A genuine transition INTO in_progress (not a same-status no-op) stamps
		// the task's real execution start, unless the caller already supplied an
		// explicit StartedAt in this same patch (which wins). This is the single
		// choke point for every Update-based path that flips a task to
		// in_progress: it closes the gap where the REST PATCH "Start" action
		// (rest_tasks.go's handleTaskPatch, which only sets status and then
		// hands off to TaskExecutor.StartTaskNow) left started_at permanently
		// empty because neither of those steps stamped it. The scheduler/
		// heartbeat dispatch path (TaskExecutor.ExecuteTask) does not go through
		// Update at all — it claims via ClaimForRun, which already stamps
		// StartedAt itself — so this is unaffected there. A retry (failed →
		// in_progress) re-stamps to the new attempt's start time, which is the
		// correct "most recent execution start" semantics.
		if *patch.Status == StatusInProgress && t.Status != StatusInProgress && patch.StartedAt == nil {
			t.StartedAt = time.Now().UTC().Format(time.RFC3339)
		}
		t.Status = *patch.Status
	}
	// Fix-wave finding #3 (run_task on a stopped task breaks): when THIS
	// patch moves Status OFF failed and the caller did NOT also touch
	// CancelReason in the same call, auto-clear the now-stale reason before
	// the merged cross-field check further down — mirrors RestartReset's
	// CancelReason clear on restart (a task no longer sitting at failed
	// cannot still be "stopped_by_user"). Without this, a run_task-shaped
	// patch that only sets Status (e.g. failed+stopped_by_user ->
	// in_progress, to resume a stopped task) would fail the cross-field
	// check purely because it forgot to also clear the now-irrelevant
	// reason. An explicit CancelReason supplied ALONGSIDE a non-failed
	// Status in the SAME patch is deliberately NOT auto-cleared here — it
	// falls through to patch.CancelReason's own handling below and is caught
	// by the merged check as before (explicit conflicting data is rejected,
	// never silently dropped; the merged check stays the backstop).
	if patch.Status != nil && *patch.Status != StatusFailed && patch.CancelReason == nil {
		t.CancelReason = ""
	}
	// Deliberate NON-decision on AttemptCount (ADR-052 spec, "Run vs. Restart
	// split" deviation note #1): unlike CancelReason immediately above,
	// AttemptCount is intentionally NOT reset when Status leaves failed via
	// this (or any) plain Update-based patch. The spec's Run/Play split gives
	// a genuinely-failed standalone task (attempts exhausted, NOT
	// user-cancelled) only the "Run" route — a plain PATCH status:
	// "in_progress" that lands exactly on this code path — never "Play"
	// (POST /tasks/{id}/restart -> RestartReset, gated to
	// failed[stopped_by_user] only by ValidateStandaloneRestart, which
	// bypasses Update/updateLocked entirely and DOES reset AttemptCount to
	// 0): "A genuinely-failed standalone task cannot Play — same 'author
	// fresh' posture FR-018 gives plans" (genuine plan failures get no
	// restart affordance at all). Resetting AttemptCount here would grant
	// exactly the amnesty that posture denies — Run means "continue from
	// here, at your own remaining/exhausted budget", Play/RestartReset is
	// the ONLY "start over completely fresh" route (it alone also wipes
	// Result/Artifacts/SessionID/timestamps, which this path deliberately
	// preserves as re-run context for the worker). Concretely: a task that
	// exhausted at AttemptCount==maxAttempts, re-run via this route and
	// producing another unmet outcome, immediately re-exhausts in
	// consumeAttemptOrExhaust (newAttempt==maxAttempts+1 already fails the
	// `< maxAttempts` gate) — one supervised extra shot per Run click, never
	// a free budget refill. This is judged defensible and is NOT changed;
	// see TestAttemptCount_NotResetOnRunRoute for the pinned regression.
	if patch.CancelReason != nil {
		if *patch.CancelReason != "" && !IsValidCancelReason(*patch.CancelReason) {
			return nil, verr("invalid cancel_reason %q", *patch.CancelReason)
		}
		t.CancelReason = *patch.CancelReason
	}
	if patch.AgentID != nil {
		t.AgentID = *patch.AgentID
	}
	if patch.Priority != nil {
		// Patch.Priority is already a *int, so presence is never ambiguous here:
		// a non-nil pointer to 0 IS an explicit priority:0 and must be rejected,
		// unlike normalize()'s Create-time check above (which operates on a bare
		// int with no such signal). See ValidatePriority's doc comment.
		if err := ValidatePriority(*patch.Priority); err != nil {
			return nil, err
		}
		t.Priority = *patch.Priority
	}
	if patch.BlockedBy != nil {
		newDeps := *patch.BlockedBy
		if len(newDeps) > 0 {
			if err := s.validateBlockedByLocked(t.ID, newDeps); err != nil {
				return nil, err
			}
		}
		t.BlockedBy = newDeps
		if len(t.BlockedBy) == 0 {
			t.BlockedBy = nil
		}
		// The derived `blocked` side-state is recomputed once, unconditionally,
		// at the end of this function (after every patch field — including a
		// same-call Status change — has been merged); see that call's doc
		// comment for why a second recompute here would be redundant.
	}
	if patch.Todos != nil {
		if err := validateTodos(*patch.Todos); err != nil {
			return nil, err
		}
		t.Todos = *patch.Todos
		if len(t.Todos) == 0 {
			t.Todos = nil
		}
	}
	if patch.Trigger != nil {
		newTrigger := *patch.Trigger
		if newTrigger != nil {
			if err := ValidateTrigger(newTrigger); err != nil {
				return nil, err
			}
		}
		t.Trigger = newTrigger
	}
	if patch.Due != nil {
		t.Due = *patch.Due
	}
	if patch.PlanID != nil {
		t.PlanID = *patch.PlanID
	}
	if patch.Tags != nil {
		normalizedTags, err := normalizeTags(*patch.Tags)
		if err != nil {
			return nil, err
		}
		t.Tags = normalizedTags
	}
	if patch.Criteria != nil {
		normalizedCriteria, err := normalizeCriteria(*patch.Criteria)
		if err != nil {
			return nil, err
		}
		t.Criteria = normalizedCriteria
		if len(t.Criteria) == 0 {
			t.Criteria = nil
		}
	}
	if patch.WriteSet != nil {
		newWriteSet := *patch.WriteSet
		if len(newWriteSet) == 0 {
			t.WriteSet = nil
		} else {
			t.WriteSet = newWriteSet
		}
	}
	if patch.Stream != nil {
		t.Stream = *patch.Stream
	}
	if patch.IsJoin != nil {
		t.IsJoin = *patch.IsJoin
	}
	if patch.MaxAttempts != nil {
		newMax := *patch.MaxAttempts
		if newMax != nil && *newMax < 1 {
			return nil, verr("max_attempts must be at least 1")
		}
		t.MaxAttempts = newMax
	}
	if patch.AttemptCount != nil {
		if *patch.AttemptCount < 0 {
			return nil, verr("attempt_count must not be negative")
		}
		t.AttemptCount = *patch.AttemptCount
	}
	if patch.ResumeFromCommit != nil {
		t.ResumeFromCommit = *patch.ResumeFromCommit
	}
	if patch.Surface != nil {
		if !IsValidSurface(*patch.Surface) {
			return nil, verr("invalid surface %q", *patch.Surface)
		}
		t.Surface = *patch.Surface
	}
	if patch.Result != nil {
		if len(*patch.Result) > 50000 {
			return nil, verr("result must be 50000 characters or fewer")
		}
		t.Result = *patch.Result
	}
	if patch.Artifacts != nil {
		t.Artifacts = *patch.Artifacts
		if len(t.Artifacts) == 0 {
			t.Artifacts = nil
		}
	}
	if patch.SessionID != nil {
		t.SessionID = *patch.SessionID
	}
	if patch.StartedAt != nil {
		t.StartedAt = *patch.StartedAt
	}
	if patch.CompletedAt != nil {
		t.CompletedAt = *patch.CompletedAt
	}
	if patch.FollowedUp != nil {
		t.FollowedUp = *patch.FollowedUp
	}
	if patch.SourceChannel != nil {
		t.SourceChannel = *patch.SourceChannel
	}
	if patch.SourceChatID != nil {
		t.SourceChatID = *patch.SourceChatID
	}
	if patch.PendingJudgeClaim != nil {
		t.PendingJudgeClaim = *patch.PendingJudgeClaim
	}

	// Cross-field invariant (ADR-052 FR-028, mirrors plan.Plan's normalize()-
	// enforced FailedReason/State coupling — pkg/plan/plan.go:299-306):
	// CancelReason is only meaningful on a failed task. Checked here against
	// the FULLY merged t.Status/t.CancelReason regardless of which patch
	// field(s) were actually touched this call (unlike plan's Update, this
	// store does not re-run the whole normalize() on every patch — see
	// normalize()'s own per-field checks for the Create-time equivalent), so
	// e.g. a caller that patches Status away from failed without also
	// clearing CancelReason in the same call is rejected rather than landing
	// an inconsistent record on disk.
	if t.CancelReason != "" && t.Status != StatusFailed {
		return nil, verr("cancel_reason is only valid when status is failed")
	}

	// S2 UAT finding A: derive the `blocked` side-state from the (now fully
	// merged) blocked_by set as the single terminal step of every persisted
	// Update, regardless of which patch field(s) triggered this call. Before
	// this, only a patch that touched BlockedBy itself (or the AddDependency/
	// RestartReset call paths) ever recomputed it — a plain
	// `Patch{Status: next}` on a task whose EXISTING (on-disk, unmodified)
	// blocked_by set still had an unmet dependency sailed through as a
	// dispatchable `next` instead of the derived `blocked` the contract
	// promises (Task.yaml: "set automatically on an unmet dependency"). Client
	// requests are still never allowed to SET `blocked` directly — that 400
	// (ErrBlockedNotSettable) is enforced above, before this point is ever
	// reached; this only ever silently redirects a `next` outcome to
	// `blocked` (or the reverse, once every blocker is done), exactly
	// mirroring what AddDependency/RestartReset already do.
	// recomputeBlockedStateLocked is a no-op for every other status
	// (in_progress/done/failed/inbox unaffected).
	s.recomputeBlockedStateLocked(t)

	// Re-check with the fully-patched task: a patch that ARMS an auto-firing
	// trigger on an agentless task, or CLEARS the agent off an already-scheduled
	// task, must be rejected the same as Create would reject it (normalize).
	if err := t.validateScheduledAgentAssignment(); err != nil {
		return nil, err
	}

	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
}

// UpdateIfStatus is the compare-and-swap write primitive that closes the
// EXECUTOR-side half of the ADR-052 FR-014/§6.4(b) Stop guarantee's TOCTOU
// window (see pkg/agent/task_executor.go's adjudicate/finish-path outcome
// writers — completeTaskWithResult, consumeAttemptOrExhaust,
// rejectBareEvidenceClaim). Those callers each re-read a task, decide an
// outcome (possibly after an unlocked, potentially slow judge/verifier
// call), and only THEN write it — a separate re-check (e.g.
// taskVerdictStillApplicable) followed by a LATER, SEPARATE write leaves a
// gap where a concurrent Stop can land in between the two and be silently
// overwritten (reviving a stopped task, or completing one). UpdateIfStatus
// closes that gap by making the re-check and the write ATOMIC under the
// SAME per-task lock acquisition: it loads the task, verifies its CURRENT
// on-disk status equals `expected`, and — only if so — applies patch via
// the same validated updateLocked path Update uses (so the Stop guard,
// transition matrix, and every other field validation still apply
// identically). On a mismatch it writes nothing and returns
// ErrStatusConflict (deliberately NOT ErrValidation — this is an expected
// race outcome, not malformed input); the caller's documented contract is
// to drop its own pending outcome (log + return, never re-dispatch) rather
// than treat the conflict as a hard failure.
func (s *Store) UpdateIfStatus(id string, expected Status, patch Patch) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	current, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if current.Status != expected {
		return nil, fmt.Errorf("%w: task %q is %q, expected %q", ErrStatusConflict, id, current.Status, expected)
	}
	return s.updateLocked(id, patch)
}

// ErrNotRestartable is returned by RestartReset when the task is not
// currently `failed` — restart (ADR-052 FR-026/028) only applies to a failed
// task, for ANY failure reason (a genuine attempt-limit exhaustion or a user
// cancel alike — task-level `failed→next` is un-frozen unconditionally,
// unlike the plan-level restart guard which is reason-gated). A task in any
// other status has nothing to restart. Control-flow sentinel (mirrors
// ErrAlreadyClaimed/ErrAlreadyRunning), NOT a wire validation error — the
// restart handler maps it to its own HTTP status (spec FR-026: 409).
var ErrNotRestartable = errors.New("task: not restartable: task is not failed")

// RestartReset atomically resets a failed task to a fresh runnable state for
// a plan/task restart (ADR-052 FR-016/017/028) in a single write: it resets
// AttemptCount to 0 (a restarted task's goal loop starts its attempt count
// over — the "attempt_count reset primitive" restart needs), clears
// CancelReason (mirrors the plan's FailedReason clear on restart — a re-run
// task is no longer "stopped by user"), and clears the previous run's
// session/result/artifacts/timestamps — the same "fresh" field set
// SpawnReset clears for a trigger re-fire, reached here via a distinct path
// because SpawnReset (a) does not touch AttemptCount/CancelReason and (b) is
// scoped to a `next` re-fire, not a restart of a `failed` task. The task
// lands on `next`, or on the derived `blocked` side-state if a blocked_by
// dependency is not yet `done` — the same dependency recompute Update applies
// when BlockedBy is patched (DS-5: restart resets a member to "next/blocked").
//
// Returns ErrNotRestartable when the task is not currently `failed`. Takes
// the per-task lock once internally.
func (s *Store) RestartReset(id string) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if t.Status != StatusFailed {
		return nil, fmt.Errorf("%w: task %q is %q", ErrNotRestartable, id, t.Status)
	}
	t.Status = StatusNext
	t.CancelReason = ""
	t.AttemptCount = 0
	t.Result = ""
	t.Artifacts = nil
	t.SessionID = ""
	// Clear any stale Play resume baseline (D13/G-12). PlayPlan calls
	// recordMemberResumePoint immediately after this reset to set a fresh
	// value (the last boundary commit, or "" for a fresh attempt); clearing
	// here also covers the standalone-restart path, where the field is
	// meaningless.
	t.ResumeFromCommit = ""
	t.StartedAt = ""
	t.CompletedAt = ""
	t.FollowedUp = false
	t.PendingJudgeClaim = ""
	s.recomputeBlockedStateLocked(t)
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
}

// ErrStandaloneRestartNotPermitted is returned by ValidateStandaloneRestart
// when a standalone-task restart (POST /tasks/{id}/restart, ADR-052 §6.7/
// §6.8, spec FR-026) is requested on a task that is not, right now,
// Status==failed with CancelReason==stopped_by_user. It wraps
// ErrIllegalTransition (and therefore ErrValidation), mirroring the shape of
// plan.ErrRestartNotPermitted (pkg/plan/plan.go), so any existing
// errors.Is(err, ErrValidation) 400-mapping still holds; callers that need to
// distinguish "not restartable" (spec FR-026: HTTP 409 — wrong status or
// wrong reason) from a malformed request (400) should check
// errors.Is(err, ErrStandaloneRestartNotPermitted) specifically.
var ErrStandaloneRestartNotPermitted = fmt.Errorf(
	"%w: restart (failed -> next) is only permitted from failed(stopped_by_user)",
	ErrIllegalTransition,
)

// ValidateStandaloneRestart reports whether a STANDALONE-task RESTART
// (POST /tasks/{id}/restart -> RestartReset, ADR-052 FR-026, the ▶ Play
// route for a task the user previously Stopped) is legal, given the task's
// CURRENT status and cancel reason. This is the pkg/task mirror of
// plan.ValidateRestartTransition (pkg/plan/plan.go) — a single, independently
// table-testable source of truth for the gate, replacing the equivalent
// inline check that previously lived only in the REST handler
// (rest_tasks.go's handleTaskRestart).
//
// Restart is legal ONLY when status == StatusFailed AND
// cancelReason == CancelReasonStoppedByUser (a user Stop, not a genuine
// failure). This is DELIBERATELY narrower than RestartReset itself, which
// stays reason-agnostic on purpose: RestartReset is also the reset primitive
// the PLAN-member restart path uses (FR-016/FR-017), and that path
// un-freezes a failed member for ANY failure reason. This gate applies ONLY
// to the standalone-task restart route — it is a caller-side precondition on
// RestartReset, not a change to RestartReset's own contract.
//
// The sole caller today is handleTaskRestart (rest_tasks.go), which
// continues to own the specific, actionable 409 message returned to the
// client; this helper's error is available via errors.Is for that mapping.
func ValidateStandaloneRestart(status Status, cancelReason CancelReason) error {
	if status != StatusFailed {
		return fmt.Errorf(
			"%w: restart is only valid from status %q, got %q",
			ErrStandaloneRestartNotPermitted, StatusFailed, status,
		)
	}
	if cancelReason != CancelReasonStoppedByUser {
		return fmt.Errorf(
			"%w: got cancel_reason %q, want %q",
			ErrStandaloneRestartNotPermitted, cancelReason, CancelReasonStoppedByUser,
		)
	}
	return nil
}

// Delete removes the task file for id and cascade-cleans inbound blocked_by
// edges (every other task that depended on id loses that edge). It returns the
// IDs of tasks that became fully unblocked (their blocked_by list emptied).
func (s *Store) Delete(id string) (unblockedIDs []string, err error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	rmErr := os.Remove(s.path(id))
	mu.Unlock()
	if rmErr != nil {
		if os.IsNotExist(rmErr) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("task: delete %q: %w", id, rmErr)
	}
	// Cascade: remove the task's evidence directory (FR-023, SD-A10). The
	// task's own file is already gone at this point; a failure here is
	// best-effort/logged, not fatal to the delete (mirrors the cascade-edge
	// cleanup failure handling below).
	evidenceDir := filepath.Join(evidenceDirForTaskStoreDir(s.dir), id)
	if rmEvErr := os.RemoveAll(evidenceDir); rmEvErr != nil {
		slog.Warn("task: delete: could not remove evidence dir", "id", id, "dir", evidenceDir, "error", rmEvErr)
	}
	return s.cascadeDeleteEdges(id)
}

// AppendTodo atomically appends a checklist item to the task's embedded todos
// under the per-task lock taken ONCE internally (load + mutate + validate +
// write). It must NOT be called while already holding the per-task lock — the
// store mutex is non-reentrant. Returns the updated task. This is the atomic
// append primitive (the full-replace counterpart is SetTodos, the primary path
// for the set_todos scratchpad tool); both avoid the Lock()+Update()
// re-entrancy deadlock.
func (s *Store) AppendTodo(id string, td Todo) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, err
	}
	newTodos := append(append([]Todo{}, t.Todos...), td)
	if err := validateTodos(newTodos); err != nil {
		return nil, err
	}
	t.Todos = newTodos
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
}

// SetTodos replaces the task's entire checklist (full-replace, idempotent).
// It validates the todos, then atomically writes. This is the primary path
// for the set_todos scratchpad tool (replaces append-only AppendTodo).
func (s *Store) SetTodos(id string, todos []Todo) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return s.Update(id, Patch{Todos: &todos})
}

// AddDependency atomically adds a single blocked_by edge (id depends on
// blockerID) under the per-task lock taken ONCE internally. It is idempotent —
// re-adding an existing edge is a no-op success. It validates the new edge set
// against the DAG (self-edge / missing / cycle / depth) before persisting, and
// recomputes the derived `blocked` side-state. Must NOT be called while holding
// the per-task lock. Returns the updated task and whether the edge was newly
// added.
func (s *Store) AddDependency(id, blockerID string) (updated *Task, added bool, err error) {
	if idErr := validateID(id); idErr != nil {
		return nil, false, idErr
	}
	if idErr := validateID(blockerID); idErr != nil {
		return nil, false, verr("blocked_by contains invalid ID %q", blockerID)
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, false, err
	}
	if containsString(t.BlockedBy, blockerID) {
		return t, false, nil
	}
	newDeps := append(append([]string{}, t.BlockedBy...), blockerID)
	if err := s.validateBlockedByLocked(t.ID, newDeps); err != nil {
		return nil, false, err
	}
	t.BlockedBy = newDeps
	s.recomputeBlockedStateLocked(t)
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, false, err
	}
	return t, true, nil
}

// recomputeBlockedStateLocked derives the `blocked` side-state from the task's
// current blocked_by set. A `next` task with at least one not-`done` blocker
// becomes `blocked`; a `blocked` task whose blockers are now all `done` (or
// empty) returns to `next`. It mutates t in place and never touches a task that
// is past dispatch (in_progress/terminal). The caller holds the
// per-task lock for t.ID; this reads OTHER task files (the blockers) without
// their locks, which is safe because it only reads their Status.
func (s *Store) recomputeBlockedStateLocked(t *Task) {
	if t.Status != StatusNext && t.Status != StatusBlocked {
		return
	}
	anyUnmet := false
	for _, depID := range t.BlockedBy {
		dep, derr := s.load(depID)
		if derr != nil || dep.Status != StatusDone {
			anyUnmet = true
			break
		}
	}
	switch {
	case anyUnmet && t.Status == StatusNext:
		t.Status = StatusBlocked
	case !anyUnmet && t.Status == StatusBlocked:
		t.Status = StatusNext
	}
}

// SpawnReset atomically resets a task to a fresh runnable state for a trigger
// re-fire: it clears the previous run's session/result/artifacts/timestamps and
// moves the task to `next` so the executor can claim it. It returns
// ErrAlreadyRunning when the task is already `in_progress` (a trigger fire must
// not stomp an in-flight run — the overlap guard). Used by the trigger executor
// to spawn a fresh run for once/every/recurring triggers. Takes the per-task
// lock once internally.
func (s *Store) SpawnReset(id string) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if t.Status == StatusInProgress {
		return nil, ErrAlreadyRunning
	}
	t.Status = StatusNext
	t.Result = ""
	t.Artifacts = nil
	t.SessionID = ""
	t.StartedAt = ""
	t.CompletedAt = ""
	t.FollowedUp = false
	// Regression fix (code review on bc66345f): SpawnReset was the one
	// status-writing path the S2 UAT finding A fix missed — Create,
	// updateLocked, RestartReset, and AddDependency all derive the `blocked`
	// side-state as their terminal step, but SpawnReset landed a task
	// straight on `next` even when its blocked_by set has an unmet
	// dependency. Reached from the trigger scheduler (a recurring/once/every
	// re-fire), this let a recurring-trigger task with a still-unmet
	// dependency persist as a dispatchable `next` task indefinitely. A no-op
	// for a task with no blocked_by (or every blocker already `done`).
	s.recomputeBlockedStateLocked(t)
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(t); err != nil {
		return nil, err
	}
	return t, nil
}

// AdvanceUnblocked transitions a task from the derived `blocked` side-state to
// `next` once its dependencies are satisfied (e.g. after a blocker was deleted).
// It is the exported, hatch-using counterpart the REST/tool delete paths call —
// a plain Update(Status: next) from `blocked` is rejected by the transition
// guard because `blocked` is cleared only via this internal path. It is a no-op
// success when the task is not currently `blocked`. Takes the per-task lock once.
func (s *Store) AdvanceUnblocked(id string) (*Task, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	t, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if t.Status != StatusBlocked {
		return t, nil
	}
	next := StatusNext
	return s.updateLocked(id, Patch{Status: &next, allowBlockedSet: true})
}

// Exists reports whether a task with id exists on disk.
func (s *Store) Exists(id string) bool {
	if validateID(id) != nil {
		return false
	}
	_, err := os.Stat(s.path(id))
	return err == nil
}

// containsString reports whether slice contains v.
func containsString(slice []string, v string) bool {
	for _, x := range slice {
		if x == v {
			return true
		}
	}
	return false
}
