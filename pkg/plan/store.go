// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

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

	"github.com/oklog/ulid/v2"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Store manages per-entity JSON plan files under a single directory (by
// universal convention across every call site, "$OMNIPUS_HOME/plans"). It
// mirrors pkg/task/store.go's Store exactly: striped-lock read-modify-write,
// atomic writes with an advisory flock, 0700 dir / 0600 file permissions.
type Store struct {
	dir  string
	lock *StripedLock

	// OnChange, when non-nil, is invoked after every successful Create or
	// Update write with the just-persisted plan (Wave 2-C1, spec Part B
	// R3/Part A C19-adjacent "plan_status" WS emission). This is the single
	// choke point BOTH the plan engine (pkg/agent's PlanEngine, which holds
	// this same *Store) and the gateway's REST handlers (approve/stop/edit)
	// write through, so wiring emission here — rather than duplicating a
	// broadcast call at every mutating call site in both packages — makes
	// every plan state/phase/progress-relevant change observable without
	// pkg/plan importing pkg/agent or pkg/gateway (the callback type is a
	// plain func(*Plan); the closure itself is supplied by gateway boot code
	// and may compute live progress via ComputeProgress before emitting).
	// Best-effort by contract: OnChange must not be given a slow or
	// panicking implementation — it runs synchronously on the writer's
	// goroutine (Create/Update caller), which may be the plan engine's
	// planDecisionMu-held critical section. Nil is a valid no-op (default;
	// tests and any Store constructed via New() before boot wiring).
	OnChange func(*Plan)
}

// New creates a Store rooted at dir using the process-wide PlanFileLock.
func New(dir string) *Store {
	return &Store{dir: dir, lock: PlanFileLock}
}

// Dir returns the store's plan directory.
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

// path returns the absolute path for a plan file.
func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// load reads and parses a single plan file. It never rewrites on read.
func (s *Store) load(id string) (*Plan, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("plan: read %q: %w", id, err)
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("plan: parse %q: %w", id, err)
	}
	if p.ID == "" {
		p.ID = id
	}
	return &p, nil
}

// write persists a plan atomically under the per-plan lock with an advisory
// flock. Callers that perform a read-modify-write MUST already hold the
// per-plan lock (Lock); write itself does not take it.
func (s *Store) write(p *Plan) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("plan: create dir: %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("plan: marshal %q: %w", p.ID, err)
	}
	path := s.path(p.ID)
	return fileutil.WithFlock(path, func() error {
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}

// Lock returns the per-plan striped mutex for id. Callers performing a manual
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
}

// matches reports whether p passes every active filter field.
func (f Filter) matches(p *Plan) bool {
	if f.WorkspaceID != "" && p.WorkspaceID != f.WorkspaceID {
		return false
	}
	return true
}

// scanPlanIDs returns the plan IDs present in the store directory, derived
// from every regular *.json file's name.
func (s *Store) scanPlanIDs() ([]string, error) {
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

// List returns all plans matching filter, sorted by created_at ASC.
// Unreadable/corrupt files are logged at Warn and skipped.
func (s *Store) List(filter Filter) ([]Plan, error) {
	ids, err := s.scanPlanIDs()
	if err != nil {
		if os.IsNotExist(err) {
			return []Plan{}, nil
		}
		return nil, fmt.Errorf("plan: list dir: %w", err)
	}

	result := make([]Plan, 0, len(ids))
	for _, id := range ids {
		p, err := s.load(id)
		if err != nil {
			slog.Warn("plan: skip unreadable plan file", "id", id, "error", err)
			continue
		}
		if !filter.matches(p) {
			continue
		}
		result = append(result, *p)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt < result[j].CreatedAt
	})
	return result, nil
}

// Get returns the plan with the given id, or ErrNotFound if absent.
func (s *Store) Get(id string) (*Plan, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return s.load(id)
}

// Exists reports whether a plan with id exists on disk.
func (s *Store) Exists(id string) bool {
	if validateID(id) != nil {
		return false
	}
	_, err := os.Stat(s.path(id))
	return err == nil
}

// Create persists a new plan. It generates a ULID when ID is empty
// (ulid.Make().String(), mirroring the milestone ID generation this entity
// replaces and the convention used at every other entity-creation call site:
// pkg/sysagent/tools/task.go, pkg/gateway/rest_workspaces.go), stamps
// CreatedAt/UpdatedAt, applies defaults, and validates fields. The new plan
// always lands in StateDraft unless the caller set a different valid state.
// Create takes the per-plan lock internally.
func (s *Store) Create(p *Plan) error {
	if p.ID == "" {
		p.ID = ulid.Make().String()
	}
	if err := validateID(p.ID); err != nil {
		return err
	}
	if err := p.normalize(); err != nil {
		return err
	}

	mu := s.lock.Get(p.ID)
	mu.Lock()
	defer mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	p.CreatedAt = now
	p.UpdatedAt = now
	if err := s.write(p); err != nil {
		return err
	}
	if s.OnChange != nil {
		s.OnChange(p)
	}
	return nil
}

// Patch is a partial update applied by Update. Only non-nil fields are written.
type Patch struct {
	Title        *string
	Goal         *string
	Description  *string
	OwnerAgentID *string
	DoD          *[]task.AcceptanceCriterion
	// Bounds is a double pointer (mirrors task.Patch's Trigger/MaxAttempts
	// convention): outer nil = unchanged, *outer nil = clear the override
	// (inherit every field from the global PlanningConfig), *outer non-nil =
	// set to that value.
	Bounds **PlanBounds

	// State requests a lifecycle transition, validated against
	// ValidateStateTransition (SD-A2). A genuine (non-no-op) transition
	// stamps the corresponding lifecycle timestamp(s) and ActiveLoop per the
	// spec Part A "State machine" closing paragraph:
	//   -> approved: ApprovedAt
	//   -> running:  StartedAt, LastActivityAt, ActiveLoop=true
	//   -> done/failed: CompletedAt, ActiveLoop=false
	State *State

	// --- persisted counters (ADR D4 MAJ-004) ---
	JudgeRounds    *int
	ActiveLoop     *bool
	PausedReason   *string
	LastActivityAt *string
	PlanPhase      *PlanPhase
	FailedReason   *FailedReason
	// HandoverText sets Plan.HandoverText (Wave 2-B engine steering/handover
	// note). See that field's doc comment on Plan.
	HandoverText *string
	// LastUnmetTerminalSignature sets Plan.LastUnmetTerminalSignature (ADR-053
	// C1/FR-147 — durable F2 round-burn gate). A nil pointer leaves the field
	// unchanged; a pointer to "" clears it (e.g. on a fresh
	// approved->running admission).
	LastUnmetTerminalSignature *string
	// OwnerSessionID sets Plan.OwnerSessionID (ADR-053 m-3/FR-147 — named
	// plan<->owner-session linkage).
	OwnerSessionID *string

	// --- supervision state (ADR-055/FR-050) ---
	//
	// FIVE DISCRETE POINTERS, DELIBERATELY NOT ONE `Supervision **Supervision`.
	// Patch's whole contract is "only non-nil fields are written", and these
	// five values are mutated INDEPENDENTLY and with DIFFERENT semantics —
	// stamp (WakeAt), record (WakeError), increment (Attempts,
	// CorrectionRounds), reset-to-zero (Attempts) and set-once (SessionID).
	// Modelling them as a single whole-object pointer (the Bounds convention
	// above) would make every one of those a read-modify-write over a struct
	// the caller read earlier — safe only while the engine's plan-decision
	// mutex is held. The REST layer's Store.Update callers do NOT hold it, so
	// one concurrent REST update of an unrelated field would clobber an
	// in-flight supervision write. That is a lost update on precisely the
	// counters that must not lose one: CorrectionRounds is cumulative for the
	// life of the plan and drives which terminal message a failed plan gets.
	//
	// Set semantics only — no delta/increment fields. The engine reads,
	// computes and sets under its own lock. A REST-shaped patch leaves all
	// five nil and the whole supervision object is left untouched on disk.
	//
	// A pointer to the zero value is meaningful and distinct from nil:
	// &"" clears WakeAt/WakeError, and &0 resets Attempts. There is no way to
	// clear SessionID, and that is intentional (see the Supervision type).
	SupervisionWakeAt           *string
	SupervisionWakeError        *string
	SupervisionAttempts         *int
	SupervisionCorrectionRounds *int
	SupervisionSessionID        *string
}

// Update applies patch to the plan identified by id and persists the result.
// It validates field constraints and, when State is set, the state
// transition (SD-A2). It takes the per-plan lock internally.
func (s *Store) Update(id string, patch Patch) (*Plan, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()
	return s.updateLocked(id, patch)
}

// updateLocked is the body of Update; the caller must hold the per-plan lock.
func (s *Store) updateLocked(id string, patch Patch) (*Plan, error) {
	p, err := s.load(id)
	if err != nil {
		return nil, err
	}

	// Fix-wave finding #4 (restart-guard ordering / crafted-patch attack):
	// capture the ON-DISK FailedReason BEFORE any patch field is applied.
	// patch.FailedReason is applied to p.FailedReason further down (the
	// per-field block below), which runs BEFORE the State-transition block
	// evaluates the restart guard — if that guard read p.FailedReason at
	// that point, a crafted Patch{State: approved, FailedReason:
	// "stopped_by_user"} could restart a plan whose REAL on-disk reason was
	// e.g. judge_rounds_exhausted (never restartable) by supplying a forged
	// reason in the same patch. The restart guard below is validated against
	// this captured pre-patch value, never the patch-mutated one.
	onDiskFailedReason := p.FailedReason

	if patch.Title != nil {
		// Trim before validating (S2 UAT finding B sibling — see plan.go's
		// normalize() matching comment, mirrors task.Store.updateLocked's
		// identical fix): a whitespace-only patch title is rejected as empty;
		// a legitimate title's incidental leading/trailing whitespace is
		// normalized away rather than persisted verbatim. This runs for every
		// Store.Update caller, not just the REST PUT handler.
		// task.HasVisibleContent then catches the invisible/zero-width/format
		// case TrimSpace itself misses (UAT round-2 S3 finding — see that
		// function's doc comment): a patch title made ENTIRELY of zero-width/
		// format codepoints (or the Cf-adjacent BRAILLE PATTERN BLANK) is
		// rejected the same as "".
		trimmedTitle := strings.TrimSpace(*patch.Title)
		if trimmedTitle == "" || !task.HasVisibleContent(trimmedTitle) {
			return nil, verr("title must not be empty")
		}
		if len([]rune(trimmedTitle)) > maxPlanTitleRunes {
			return nil, verr("title must be %d characters or fewer", maxPlanTitleRunes)
		}
		p.Title = trimmedTitle
	}
	if patch.Goal != nil {
		if len([]rune(*patch.Goal)) > maxPlanGoalRunes {
			return nil, verr("goal must be %d characters or fewer", maxPlanGoalRunes)
		}
		p.Goal = *patch.Goal
	}
	if patch.Description != nil {
		if len([]rune(*patch.Description)) > maxPlanDescriptionRunes {
			return nil, verr("description must be %d characters or fewer", maxPlanDescriptionRunes)
		}
		p.Description = *patch.Description
	}
	if patch.OwnerAgentID != nil {
		if *patch.OwnerAgentID == "" {
			return nil, verr("owner_agent_id must not be empty")
		}
		p.OwnerAgentID = *patch.OwnerAgentID
	}
	if patch.DoD != nil {
		normalized, err := task.NormalizeCriteria(*patch.DoD)
		if err != nil {
			// See plan.go's normalize() comment: wrap ErrValidation on top of
			// task.NormalizeCriteria's task.ErrValidation so both sentinels
			// are satisfiable via errors.Is.
			return nil, fmt.Errorf("%w: %w", ErrValidation, err)
		}
		p.DoD = normalized
		if len(p.DoD) == 0 {
			p.DoD = nil
		}
	}
	if patch.Bounds != nil {
		newBounds := *patch.Bounds
		if err := validatePlanBounds(newBounds); err != nil {
			return nil, err
		}
		p.Bounds = newBounds
	}
	if patch.JudgeRounds != nil {
		if *patch.JudgeRounds < 0 {
			return nil, verr("judge_rounds must not be negative")
		}
		p.JudgeRounds = *patch.JudgeRounds
	}
	if patch.PausedReason != nil {
		p.PausedReason = *patch.PausedReason
	}
	if patch.LastActivityAt != nil {
		p.LastActivityAt = *patch.LastActivityAt
	}
	if patch.PlanPhase != nil {
		if *patch.PlanPhase != "" && !IsValidPlanPhase(*patch.PlanPhase) {
			return nil, verr("invalid plan_phase %q", *patch.PlanPhase)
		}
		p.PlanPhase = *patch.PlanPhase
	}
	if patch.FailedReason != nil {
		if *patch.FailedReason != "" && !IsValidFailedReason(*patch.FailedReason) {
			return nil, verr("invalid failed_reason %q", *patch.FailedReason)
		}
		p.FailedReason = *patch.FailedReason
	}
	if patch.HandoverText != nil {
		if len([]rune(*patch.HandoverText)) > maxPlanHandoverRunes {
			return nil, verr("handover_text must be %d characters or fewer", maxPlanHandoverRunes)
		}
		p.HandoverText = *patch.HandoverText
	}
	if patch.LastUnmetTerminalSignature != nil {
		p.LastUnmetTerminalSignature = *patch.LastUnmetTerminalSignature
	}
	if patch.OwnerSessionID != nil {
		p.OwnerSessionID = *patch.OwnerSessionID
	}
	if err := applySupervisionPatch(p, patch); err != nil {
		return nil, err
	}
	if patch.State != nil {
		if !IsValidState(*patch.State) {
			return nil, verr("invalid state %q", *patch.State)
		}
		from, to := p.State, *patch.State

		// restarting recognizes the ADR-052 §6.7 / spec FR-016/DS-1 RESTART
		// transition (failed -> approved, gated to FailedReason ==
		// stopped_by_user). The canonical legalPlanTransitions matrix
		// (plan.go) stays reason-free and unconditionally rejects
		// failed->approved (grill M2/R3-4: it is NEVER widened) — this
		// store-level guard is evaluated ONLY for this specific (from, to)
		// pair, via ValidateRestartTransition rather than
		// ValidateStateTransition. failed->running is never routed through
		// here (to != StateApproved), so it always falls to the normal
		// matrix check below and stays illegal.
		restarting := from == StateFailed && to == StateApproved
		if restarting {
			// Fix-wave finding #4: reject an explicit FailedReason/
			// JudgeRounds supplied alongside a restart transition in the
			// SAME patch, rather than silently overwriting/ignoring it.
			// Restart is always a clean-slate reset (FailedReason cleared,
			// JudgeRounds zeroed below) — a caller has no legitimate reason
			// to also set either field in the same patch, and allowing it
			// is exactly the shape of the crafted-patch attack this guard
			// closes (see onDiskFailedReason's doc comment above).
			if patch.FailedReason != nil {
				return nil, verr("failed_reason must not be set alongside a restart (failed[stopped_by_user] -> approved) transition")
			}
			if patch.JudgeRounds != nil {
				return nil, verr("judge_rounds must not be set alongside a restart (failed[stopped_by_user] -> approved) transition")
			}
			// Validate against the CAPTURED on-disk reason — never
			// p.FailedReason at this point, which (for a legitimate
			// non-restart caller that combines State+FailedReason in one
			// patch, e.g. running->failed) may already reflect THIS same
			// patch's own FailedReason field.
			if err := ValidateRestartTransition(from, onDiskFailedReason); err != nil {
				return nil, err
			}
		} else if err := ValidateStateTransition(from, to); err != nil {
			return nil, err
		}

		if from != to {
			now := time.Now().UTC().Format(time.RFC3339)
			switch to {
			case StateApproved:
				p.ApprovedAt = now
			case StateRunning:
				p.StartedAt = now
				p.LastActivityAt = now
				p.ActiveLoop = true
			case StateDone, StateFailed:
				p.CompletedAt = now
				p.ActiveLoop = false
			}
		}
		p.State = to

		if restarting {
			// ADR-052 §6.7 / spec FR-016-FR-017 (A4): a restart is a clean
			// slate at the plan level — clear the discriminator reason (a
			// restarted plan is no longer "stopped by user"; a later
			// genuine failure records its own reason) and reset the
			// plan-level JudgeRounds counter to 0 (otherwise a plan
			// restarted near its judge-round cap would fail immediately).
			// Fix-wave finding #4 tightened this: the same Patch is no
			// longer PERMITTED to also carry an explicit FailedReason/
			// JudgeRounds value at all (rejected above, before this point
			// is ever reached) — this reset therefore always starts from a
			// patch that touched neither field, never from "restart
			// overwriting a conflicting explicit value". PausedReason is deliberately
			// NOT touched here: it is orthogonal (a running+paused plan is
			// not in State==failed at all, so it can never reach this
			// branch). Per-member Task.attempt_count reset (FR-017) is the
			// restart HANDLER's job over pkg/task's store — out of this
			// package's ownership (pkg/plan never imports pkg/task's
			// Store, only its types/Filter via the TaskLister interface).
			p.FailedReason = ""
			p.JudgeRounds = 0
		}
	}
	// ActiveLoop is applied AFTER the State-transition stamping above so an
	// explicit caller-supplied value in the same patch wins over the
	// transition's own stamped value (mirrors task.Update's StartedAt-wins
	// pattern for the in_progress transition).
	if patch.ActiveLoop != nil {
		p.ActiveLoop = *patch.ActiveLoop
	}

	// review r1 type-design: route the fully-patched plan through the SAME
	// normalize() Create uses (apply patch -> normalize -> write) rather than
	// relying solely on the per-field checks above, which each validate only
	// the field THEY touch and can miss a cross-field invariant a patch
	// combination violates (e.g. FailedReason set without State==failed in
	// the same call — the per-field FailedReason check above only validates
	// the enum value, not this coupling; normalize() re-checks the whole
	// object). Every field normalize() re-validates (title/goal/description/
	// workspace_id/owner_agent_id/state/DoD/bounds) already has an on-disk
	// value that passed normalize() at Create time or a prior Update, so
	// these re-checks are a no-op whenever the field itself wasn't patched
	// this call.
	if err := p.normalize(); err != nil {
		return nil, err
	}

	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.write(p); err != nil {
		return nil, err
	}
	if s.OnChange != nil {
		s.OnChange(p)
	}
	return p, nil
}

// applySupervisionPatch applies the five discrete supervision pointers of
// patch to p (ADR-055/FR-050). It is the ONLY writer of Plan.Supervision.
//
// The all-nil case — every REST-shaped patch, and every engine patch that does
// not touch supervision — returns immediately WITHOUT allocating, so a plan
// that has never been supervised keeps a nil Supervision and serialises with no
// `supervision` key at all. That early return is the property test #57c pins:
// a patch of unrelated fields must leave the whole object byte-identical.
//
// A pointer to the zero value is meaningful and NOT the same as nil: &"" clears
// WakeAt/WakeError and &0 resets Attempts, which is how the engine disarms the
// deadline and the attempt counter when a plan leaves the supervision-eligible
// phase set. CorrectionRounds is settable but never set backwards by the
// engine (it is cumulative for the life of the plan), and SessionID has no
// clearing path by design — see the Supervision type.
func applySupervisionPatch(p *Plan, patch Patch) error {
	if patch.SupervisionWakeAt == nil &&
		patch.SupervisionWakeError == nil &&
		patch.SupervisionAttempts == nil &&
		patch.SupervisionCorrectionRounds == nil &&
		patch.SupervisionSessionID == nil {
		return nil
	}
	// Validate BEFORE mutating: updateLocked's error paths return without
	// writing, but p is the loaded in-memory record and a half-applied patch
	// would be visible to anything holding it.
	if patch.SupervisionWakeAt != nil && *patch.SupervisionWakeAt != "" {
		if _, err := time.Parse(time.RFC3339, *patch.SupervisionWakeAt); err != nil {
			// Rejected rather than stored: `supervision.wake_at` is
			// `format: date-time` in Plan.yaml and the SPA edge validates it
			// with a strict datetime schema, so a malformed value would drop
			// the ENTIRE plan payload at the client rather than just this
			// field.
			return verr("supervision.wake_at must be an RFC 3339 timestamp")
		}
	}
	if patch.SupervisionAttempts != nil && *patch.SupervisionAttempts < 0 {
		return verr("supervision.attempts must not be negative")
	}
	if patch.SupervisionCorrectionRounds != nil && *patch.SupervisionCorrectionRounds < 0 {
		return verr("supervision.correction_rounds must not be negative")
	}

	if p.Supervision == nil {
		p.Supervision = &Supervision{}
	}
	if patch.SupervisionWakeAt != nil {
		p.Supervision.WakeAt = *patch.SupervisionWakeAt
	}
	if patch.SupervisionWakeError != nil {
		p.Supervision.WakeError = *patch.SupervisionWakeError
	}
	if patch.SupervisionAttempts != nil {
		p.Supervision.Attempts = *patch.SupervisionAttempts
	}
	if patch.SupervisionCorrectionRounds != nil {
		p.Supervision.CorrectionRounds = *patch.SupervisionCorrectionRounds
	}
	if patch.SupervisionSessionID != nil {
		p.Supervision.SessionID = *patch.SupervisionSessionID
	}
	return nil
}

// Delete removes the plan file for id. A running plan cannot be deleted
// (FR-006) — stop or let it complete first. Member tasks' Task.PlanID
// references are NOT cleared by this call: pkg/plan deliberately does not
// hold a task-store handle (see the TaskLister interface below and the
// package doc's one-way pkg/plan -> pkg/task import rule), so clearing
// plan_id off member tasks on delete (FR-007) is the REST/tool layer's
// responsibility, exactly as Task.PlanID's same-workspace FK is validated at
// that layer via ValidatePlanWorkspace rather than inside this store.
func (s *Store) Delete(id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	mu := s.lock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	p, err := s.load(id)
	if err != nil {
		return err
	}
	if p.State == StateRunning {
		return verr("cannot delete a running plan %q; stop it or wait for it to complete first", id)
	}
	if rmErr := os.Remove(s.path(id)); rmErr != nil {
		if os.IsNotExist(rmErr) {
			return ErrNotFound
		}
		return fmt.Errorf("plan: delete %q: %w", id, rmErr)
	}
	return nil
}

// ValidatePlanWorkspace enforces the same-workspace FK a Task.PlanID
// reference must satisfy (spec Part A "Membership"): a task may only
// reference a plan that lives in the task's own workspace. Returns nil when
// planID is empty (no reference to validate — plan_id is optional on a
// Task). Returns an error wrapping ErrValidation for a cross-workspace
// reference or a plan_id that does not exist.
func (s *Store) ValidatePlanWorkspace(planID, taskWorkspaceID string) error {
	if planID == "" {
		return nil
	}
	p, err := s.Get(planID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return verr("plan_id %q does not exist", planID)
		}
		return err
	}
	if p.WorkspaceID != taskWorkspaceID {
		return verr("plan_id %q belongs to a different workspace", planID)
	}
	return nil
}

// TaskLister is the minimal task-listing capability the plan package needs to
// compute read-time plan membership/progress. task.Store satisfies this
// trivially (task.Store.List has exactly this signature); it is kept as an
// interface — rather than a direct *task.Store parameter — so pkg/plan's
// read path is not coupled to a single store implementation, while still
// letting pkg/plan import pkg/task's Filter/Task/Status types directly (see
// the package doc: pkg/plan -> pkg/task is a one-way, non-cyclic import).
type TaskLister interface {
	List(filter task.Filter) ([]task.Task, error)
}

// ComputeProgress scans lister for tasks with PlanID == planID and returns
// (done, total, progress). progress is done/total, or 0 when total is 0 (an
// empty plan has no members yet — that is 0% done, not 100%). This is
// read-time only (spec Part A "Membership"): the Plan's own disk record is
// never written by this function, matching Progress's doc comment on the
// Plan struct.
func ComputeProgress(planID string, lister TaskLister) (done, total int, progress float64, err error) {
	tasks, err := lister.List(task.Filter{PlanID: planID})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("plan: compute progress: list tasks: %w", err)
	}
	total = len(tasks)
	for _, t := range tasks {
		if t.Status == task.StatusDone {
			done++
		}
	}
	if total == 0 {
		return 0, 0, 0, nil
	}
	return done, total, float64(done) / float64(total), nil
}
