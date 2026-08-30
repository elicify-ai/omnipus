// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-053 §Contract Surface (S2) — the durable 8-state session-lifecycle
// record. This is the single source of truth for "is anything still
// working?" for a goal-bearing or delegated session — distinct from
// SessionStatus (daypartition.go — active/archived/interrupted, chat-
// transcript metadata) and from plan.State (the 5-state plan lifecycle).
// Do not conflate any of the three.
//
// Persistence model: one JSONL file per durable session_id
// (<dir>/<session_id>.jsonl), append-only. Every state transition — plus a
// generation bump on follow_up/Play — appends a full-snapshot line; the
// CURRENT record is always the last valid line in the file. This gives the
// audit trail "for free" (every prior state is still on disk, immutable),
// is crash-safe (a torn last write leaves the prior line intact and
// readable), and mirrors the per-entity-file convention pkg/task and
// pkg/plan use, adapted to JSONL per the ADR §Contract Surface wording
// ("Persisted per-entity JSONL (like tasks/pins), 64-shard mutex pool").
//
// The immutable-terminal invariant (L-3/MAJ-1/N-7) is enforced at Persist:
// once the tail record for a session_id is terminal, a further Persist call
// naming the SAME generation is rejected — only a strictly-incrementing
// Generation (minted by a follow_up/Play, ADR-053 D-the-generation-mint) may
// be appended after a terminal tail.
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ErrLifecycleNotFound is returned when a durable session_id has no
// persisted lifecycle record.
var ErrLifecycleNotFound = errors.New("session: lifecycle record not found")

// ErrLifecycleTerminalImmutable is returned when a caller attempts to
// Persist a mutation onto a terminal record's own generation (L-3/MAJ-1/N-7
// — a terminal record is never mutated in place; a follow_up/Play must mint
// a new generation instead).
var ErrLifecycleTerminalImmutable = errors.New("session: lifecycle: terminal record is immutable")

// LifecycleState is the durable 8-state session lifecycle (ADR-053 S2 / the
// S4 interlock state machine's authority).
type LifecycleState string

// The eight canonical lifecycle states. These are the ONLY valid values.
const (
	LifecycleQueued     LifecycleState = "queued"
	LifecycleRunning    LifecycleState = "running"
	LifecycleNeedsInput LifecycleState = "needs_input"
	// LifecyclePaused covers BOTH cooperative cancel-soft grace AND a
	// plan-owner session idling while its plan is durably
	// plan_phase=awaiting_supervision (that condition itself lives on
	// the Plan record — pkg/plan — not as a 9th state here; see
	// R§8.10's lifecycle-to-pill crosswalk). This package does not persist
	// or interpret plan_phase; a caller layering the plan-owner semantics on
	// top (Phase 2 / another wave) reads OwnsPlanID to resolve that link.
	LifecyclePaused    LifecycleState = "paused"
	LifecycleCompleted LifecycleState = "completed"
	LifecycleFailed    LifecycleState = "failed"
	LifecycleCancelled LifecycleState = "cancelled"
	LifecycleTimedOut  LifecycleState = "timed_out"
)

// validLifecycleStates is the set of allowed LifecycleState values.
var validLifecycleStates = map[LifecycleState]bool{ //nolint:gochecknoglobals
	LifecycleQueued:     true,
	LifecycleRunning:    true,
	LifecycleNeedsInput: true,
	LifecyclePaused:     true,
	LifecycleCompleted:  true,
	LifecycleFailed:     true,
	LifecycleCancelled:  true,
	LifecycleTimedOut:   true,
}

// IsValidLifecycleState reports whether s is one of the eight canonical
// lifecycle states.
func IsValidLifecycleState(s LifecycleState) bool { return validLifecycleStates[s] }

// terminalLifecycleStates is the set of states after which a record is
// frozen (immutable-terminal invariant, L-3).
var terminalLifecycleStates = map[LifecycleState]bool{ //nolint:gochecknoglobals
	LifecycleCompleted: true,
	LifecycleFailed:    true,
	LifecycleCancelled: true,
	LifecycleTimedOut:  true,
}

// IsTerminalLifecycleState reports whether s is one of the four terminal
// states (completed/failed/cancelled/timed_out).
func IsTerminalLifecycleState(s LifecycleState) bool { return terminalLifecycleStates[s] }

// OwnerScopeKind discriminates LifecycleRecord.OwnerScopeID's meaning (N-9 —
// a bare oneOf of untagged strings has no discriminator, so this mirrors the
// generated SessionLifecycleRecordOwnerScopeKind split). The domain string
// values are PINNED to the generated wire enum one-for-one
// (parent_session/plan/human) so a natural cast (OwnerScopeKind →
// generated.SessionLifecycleRecordOwnerScopeKind) produces schema-valid JSON
// without an explicit conversion (MAJOR-4 / DoD-11). TestOwnerScopeKind_
// MirrorsWireEnum pins this correspondence.
type OwnerScopeKind string

const (
	// OwnerScopeParentSession — OwnerScopeID names the parent session_id.
	OwnerScopeParentSession OwnerScopeKind = "parent_session"
	// OwnerScopePlan — OwnerScopeID names the owning plan_id.
	OwnerScopePlan OwnerScopeKind = "plan"
	// OwnerScopeHuman — no single owning id; a top-level chat-goal session
	// owned by the human/chat-principal. OwnerScopeID is empty.
	OwnerScopeHuman OwnerScopeKind = "human"
)

// IsValidOwnerScopeKind reports whether k is one of the three owner-scope
// discriminators.
func IsValidOwnerScopeKind(k OwnerScopeKind) bool {
	switch k {
	case OwnerScopeParentSession, OwnerScopePlan, OwnerScopeHuman:
		return true
	default:
		return false
	}
}

// NeedsInput is present iff LifecycleRecord.State == LifecycleNeedsInput.
// Reconstructable is a PARK-TIME HINT ONLY (m5) — the authoritative
// determination for warm-resume eligibility at boot is
// isNeedsInputReconstructable (a Phase-2/boot-sweep concern, R§8.6), never
// this stored value.
type NeedsInput struct {
	CorrelationID   string    `json:"correlation_id"`
	Reconstructable bool      `json:"reconstructable"`
	TTLDeadline     time.Time `json:"ttl_deadline"`
}

// Expired reports whether now is at or past n's TTL deadline (INV-5 bounded
// park — G-6). A zero TTLDeadline is treated as not-yet-expired (never
// silently expire an unset deadline).
func (n *NeedsInput) Expired(now time.Time) bool {
	if n == nil || n.TTLDeadline.IsZero() {
		return false
	}
	return !now.Before(n.TTLDeadline)
}

// LifecycleRecord is the durable, per-generation session-lifecycle record
// (ADR-053 §Contract Surface — SessionLifecycleRecord). Field shapes mirror
// the generated pkg/api/generated.SessionLifecycleRecord one-for-one (minus
// the retired launch_profile field, removed by the ADR-053 Amendment — see
// that ADR) so a caller mapping this disk record onto the wire type
// (delegate.status, DoD-11) never has to reconcile diverging field names.
//
// not-wire-format: internal disk record; a caller (pkg/tools/delegate.go)
// maps it onto generated.SessionLifecycleRecord at the tool-result boundary.
type LifecycleRecord struct {
	SessionID   string         `json:"session_id"`
	Generation  int            `json:"generation"`
	ResumedFrom string         `json:"resumed_from,omitempty"`
	State       LifecycleState `json:"state"`

	OwnerScopeKind OwnerScopeKind `json:"owner_scope_kind"`
	OwnerScopeID   string         `json:"owner_scope_id,omitempty"`
	// OwnsPlanID is set when THIS session is a plan's OWNER session — the
	// reciprocal of plan.Plan.OwnerSessionID (m-3/FR-147, a pkg/plan field
	// this wave does not add — see the final report's flagged pkg/plan
	// seam). Lets a boot sweep (another wave) exempt a paused owner session
	// whose OwnerScopeKind==human but which is legitimately idle awaiting a
	// correction on the named plan.
	OwnsPlanID string `json:"owns_plan_id,omitempty"`

	GoalRef     string `json:"goal_ref,omitempty"`
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	Is3P        bool   `json:"is_3p"`

	// ParentAgentID is the id of the agent that DELEGATED this session into
	// existence — the CALLER of `delegate.run`, captured at mint time from
	// tools.ToolAgentID(ctx) (FR-013). It is the ONLY parent linkage on this
	// record, and therefore the sole legal predicate for "the subagents I
	// started" (list_jobs). Not part of the generated wire shape: this is a
	// disk-only internal scoping predicate with no SPA consumer, and
	// contracts/components/schemas/SessionLifecycleRecord.yaml deliberately
	// does NOT declare it.
	//
	// Declared WITHOUT `omitempty`, deliberately (FR-015). An empty value
	// must serialise as a PRESENT-but-empty key so it is a visible,
	// greppable bug on disk; with `omitempty` it would serialise to an
	// absent key — byte-identical to a record written before this field
	// existed — and such rows would be silently undroppable-yet-invisible
	// with no counter anywhere. Do not add `omitempty` to this tag.
	//
	// MUST NOT be inferred from any other field, ever:
	//   - ParentDurableKey (D1) is the caller's own live routing/session id
	//     at spawn time. Post-ADR-057 it is a STRICT direct-parent EDGE
	//     (LifecycleFilter.ParentDurableKey, FR-019/FR-020, answers
	//     "children of X" one level deep) — but it says nothing about WHICH
	//     AGENT delegated, and it is legitimately shared by every SIBLING a
	//     single parent starts. Inferring ParentAgentID from it would
	//     collapse every sibling onto one value regardless of which agent
	//     actually ran each `delegate.run` call;
	//   - OwnerScopeID is "" for a top-level delegation (OwnerScopeHuman);
	//   - AgentID is the CHILD's id (the delegate target), not the parent's.
	// The mint site fails closed on an empty value, so an empty
	// ParentAgentID on disk is never expected on a greenfield install.
	ParentAgentID string `json:"parent_agent_id"`

	// ParentDurableKey is the durable chat/plan id (D16) this session's
	// PARENT inbox is keyed to — ALWAYS populated at spawn time with the
	// caller's own live transcript/routing session id, regardless of
	// OwnerScopeKind (unlike OwnerScopeID, which per the wire contract is
	// empty when OwnerScopeKind==human — see that field's own doc comment).
	// A durable inbox routing key must always resolve to something
	// addressable even for a human-owned top-level parent, which is exactly
	// the case OwnerScopeID cannot serve.
	//
	// Post-ADR-057 (D1) this is ALSO the durable PARENTAGE edge:
	// LifecycleFilter.ParentDurableKey (FR-019) matches records whose OWN
	// ParentDurableKey equals a given session id, and List maintains a
	// secondary in-memory index keyed on it (FR-020, lifecycle_index.go) so
	// "children of X" is one indexed lookup rather than a full-store scan.
	// A child's ParentDurableKey names its DIRECT parent only — it is NOT
	// re-inherited down the chain, so a grandchild's value is its own
	// parent's id, never the grandparent's (this is what makes "children of
	// X" return exactly depth-1, never siblings' descendants). It IS,
	// correctly, shared by every SIBLING a single parent starts — that is
	// the mechanism, not a defect — which is why this remains a
	// parentage-by-SESSION predicate and is never a substitute for
	// ParentAgentID's parentage-by-AGENT predicate (see that field's own doc
	// comment). Not part of the generated wire shape.
	ParentDurableKey string `json:"parent_durable_key,omitempty"`

	// OriginChannel/OriginChatID are the channel + chat_id the delegating
	// `delegate.run` call originated FROM (the PARENT's own live
	// conversation — mirrors pkg/tools/delegate.go's pre-existing
	// DelegateTaskState.OriginChannel/OriginChatID fields, captured the same
	// way via ToolChannel(ctx)/ToolChatID(ctx) at spawn time). Not part of
	// the generated wire shape (this record is not-wire-format) — carried
	// here so a child's own message_parent call can resolve "which live
	// conversation does a bounded typed wake (ADR-053 S3) need to land in"
	// without a separate lookup. Empty when the parent had no
	// channel/chatID context at spawn time (e.g. a programmatic call).
	OriginChannel string `json:"origin_channel,omitempty"`
	OriginChatID  string `json:"origin_chat_id,omitempty"`

	LastCheckpointRef     string   `json:"last_checkpoint_ref,omitempty"`
	UndeliveredMessageIDs []string `json:"undelivered_message_ids,omitempty"`

	NeedsInput *NeedsInput `json:"needs_input,omitempty"`

	// FailedReason is set only when State==LifecycleFailed. Left open
	// (not a closed enum) per the generated type's own field doc — e.g.
	// "interrupted", "budget_exhausted", "judge_rounds_exhausted".
	FailedReason string `json:"failed_reason,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Terminal reports whether r's State is one of the four terminal states.
func (r *LifecycleRecord) Terminal() bool {
	if r == nil {
		return false
	}
	return IsTerminalLifecycleState(r.State)
}

// validateLifecycleSessionID rejects IDs containing path separators, "..", or null
// bytes (mirrors pkg/task/store.go's validateID exactly).
func validateLifecycleSessionID(id string) error {
	if id == "" {
		return fmt.Errorf("session_id must not be empty")
	}
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") || strings.ContainsRune(id, 0) {
		return fmt.Errorf("invalid session_id %q", id)
	}
	return nil
}

// LifecycleStore manages the durable, per-entity JSONL session-lifecycle
// records under a single directory. All read-modify-write paths are
// serialized by the store's own 64-shard striped lock keyed by session_id,
// plus an advisory flock on the file itself (fileutil.AppendJSONL's
// O_APPEND semantics).
type LifecycleStore struct {
	dir  string
	lock *lifecycleStripedLock

	// parentIndex is the ADR-057 W6/FR-020 secondary parent index: an
	// in-memory map from ParentDurableKey to its direct children's
	// session_ids, maintained inside Persist (persistLocked) and consulted
	// by List when LifecycleFilter.ParentDurableKey is set — see
	// lifecycle_index.go. It is a property of THIS store instance (like
	// lock), not shared across LifecycleStore values.
	parentIndex *lifecycleParentIndex
}

// NewLifecycleStore creates a LifecycleStore rooted at dir. By convention
// dir is "<OMNIPUS_HOME>/session_lifecycle" — the caller wiring this store
// into the gateway/agent-loop boot sequence (outside this wave's write-set)
// should use that path so every consumer of the durable record agrees on
// its location.
func NewLifecycleStore(dir string) *LifecycleStore {
	return &LifecycleStore{
		dir:         dir,
		lock:        &lifecycleStripedLock{},
		parentIndex: newLifecycleParentIndex(),
	}
}

// Dir returns the store's root directory.
func (s *LifecycleStore) Dir() string { return s.dir }

func (s *LifecycleStore) path(sessionID string) string {
	return filepath.Join(s.dir, sessionID+".jsonl")
}

// Lock returns the per-session striped mutex for sessionID. Callers
// performing a manual read-modify-write (read tail, decide next state,
// Persist) MUST hold this for the whole RMW to avoid two writers racing a
// transition (e.g. two concurrent `respond`s against the same needs_input
// session).
func (s *LifecycleStore) Lock(sessionID string) *sync.Mutex {
	return s.lock.Get(sessionID)
}

// tail reads every line of sessionID's JSONL file and returns the last
// successfully-parsed record (the current state). Returns found=false when
// the file does not exist (no record yet — a fresh session_id) or every line
// was unparsable. A malformed trailing line (a torn write from a crash
// mid-append) is skipped with a fall-back to the last GOOD line rather than
// failing the read outright — crash-safety mirrors fileutil.WriteFileAtomic's
// own "never worse than the last durable write" guarantee.
func (s *LifecycleStore) tail(sessionID string) (rec *LifecycleRecord, found bool, err error) {
	f, err := os.Open(s.path(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("session: lifecycle: open %q: %w", sessionID, err)
	}
	defer f.Close()

	var last *LifecycleRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var r LifecycleRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			// Skip a torn/corrupt line; keep the last good record. This is
			// deliberately non-fatal — see the doc comment above.
			continue
		}
		rc := r
		last = &rc
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("session: lifecycle: scan %q: %w", sessionID, err)
	}
	return last, last != nil, nil
}

// Load returns the current (tail) LifecycleRecord for sessionID.
// Returns ErrNotFound when no record exists yet.
func (s *LifecycleStore) Load(sessionID string) (*LifecycleRecord, error) {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return nil, err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	rec, found, err := s.tail(sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrLifecycleNotFound
	}
	return rec, nil
}

// Persist appends rec as the new current state for rec.SessionID, enforcing
// the immutable-terminal invariant (L-3): if the existing tail record is
// terminal AND rec.Generation == tail.Generation, the write is rejected —
// only a strictly-greater Generation (a follow_up/Play mint) may be
// appended after a terminal tail. rec.CreatedAt is preserved from the first
// record of this generation (or set to now for a new generation);
// rec.UpdatedAt is always stamped to now.
//
// Callers performing a read-then-decide-then-write sequence MUST use Mutate
// (the atomic RMW primitive) rather than a manual Load+Persist pair — a
// naked Load+Persist races a concurrent transition on the same session_id
// (Correctness-MAJOR-3 / S4 INV-3: cancel-vs-complete). Persist is the right
// primitive only for a fire-and-forget append where the caller already knows
// the full next record (e.g. the initial `queued` seed at run time, or
// spawnCorrectiveFollowUp's new-generation mint).
func (s *LifecycleStore) Persist(rec *LifecycleRecord) error {
	if rec == nil {
		return fmt.Errorf("session: lifecycle: nil record")
	}
	if err := validateLifecycleSessionID(rec.SessionID); err != nil {
		return err
	}
	mu := s.Lock(rec.SessionID)
	mu.Lock()
	defer mu.Unlock()
	return s.persistLocked(rec)
}

// persistLocked validates rec and appends it as the new current state. It is
// the shared write half of Persist (which takes the lock) and Mutate (which
// holds the lock across its whole tail→fn→write RMW). The caller MUST hold
// Lock(rec.SessionID); persistLocked does not take the lock itself (taking
// it again would self-deadlock — sync.Mutex is not reentrant).
func (s *LifecycleStore) persistLocked(rec *LifecycleRecord) error {
	if rec == nil {
		return fmt.Errorf("session: lifecycle: nil record")
	}
	if !IsValidLifecycleState(rec.State) {
		return fmt.Errorf("session: lifecycle: invalid state %q", rec.State)
	}
	if rec.State == LifecycleNeedsInput && rec.NeedsInput == nil {
		return fmt.Errorf("session: lifecycle: state needs_input requires NeedsInput to be set")
	}
	if rec.State != LifecycleNeedsInput {
		rec.NeedsInput = nil
	}
	if rec.State == LifecycleFailed && strings.TrimSpace(rec.FailedReason) == "" {
		return fmt.Errorf("session: lifecycle: state failed requires failed_reason")
	}
	// OwnerScopeKind is a REQUIRED discriminator (m5/Comments-MINOR-3): every
	// record must declare parent_session/plan/human so ownership resolution
	// (verifyCallerOwnsSession, ownerKeyFor) never silently falls through on
	// an unset value. Strict like State (empty is not one of the three valid
	// kinds). Safe to require because every creation path sets it
	// (delegate.go run mints OwnerScopeHuman/OwnerScopeParentSession), and
	// transitions copy it forward from the loaded tail.
	if rec.OwnerScopeKind == "" {
		return fmt.Errorf("session: lifecycle: owner_scope_kind is required")
	}
	if !IsValidOwnerScopeKind(rec.OwnerScopeKind) {
		return fmt.Errorf("session: lifecycle: invalid owner_scope_kind %q", rec.OwnerScopeKind)
	}

	prev, found, err := s.tail(rec.SessionID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	switch {
	case !found:
		if rec.CreatedAt.IsZero() {
			rec.CreatedAt = now
		}
	case prev.Terminal() && rec.Generation == prev.Generation:
		return fmt.Errorf(
			"%w: session %q generation %d is terminal (%s); a follow_up/Play must mint generation %d via resumed_from",
			ErrLifecycleTerminalImmutable, rec.SessionID, prev.Generation, prev.State, prev.Generation+1,
		)
	case rec.Generation == prev.Generation:
		if rec.CreatedAt.IsZero() {
			rec.CreatedAt = prev.CreatedAt
		}
	default:
		// A genuine new generation (follow_up/Play). CreatedAt starts fresh
		// for this generation unless the caller already set one.
		if rec.CreatedAt.IsZero() {
			rec.CreatedAt = now
		}
	}
	rec.UpdatedAt = now

	if err := fileutil.AppendJSONL(s.path(rec.SessionID), rec); err != nil {
		return err
	}
	// FR-020 — maintain the secondary parent index inside Persist, under the
	// per-session striped lock persistLocked's caller (Persist/Mutate)
	// already holds. add() itself is a no-op when ParentDurableKey is empty
	// (an unattributable record, FR-015's degraded-mode mint), and is
	// idempotent across a session's later generations, which all carry the
	// same ParentDurableKey.
	s.parentIndex.add(rec.ParentDurableKey, rec.SessionID)
	return nil
}

// Mutate is the atomic read-modify-write primitive for one session's
// lifecycle record (Correctness-MAJOR-3 / S4 INV-3): it holds the per-session
// striped lock across tail→fn→persist, so two concurrent transitions on the
// SAME session_id serialize and the immutable-terminal guard resolves the
// loser (the cancel-vs-complete race — previously a naked Load+Persist left
// the loser either overwriting the winner or being rejected by the guard
// non-atomically).
//
// fn receives a pointer to a COPY of the CURRENT tail record (or nil when no
// record exists for sessionID yet) which fn may mutate in place; whatever fn
// leaves there is what Mutate persists (after the same validation and
// immutable-terminal guard Persist applies). If fn returns a non-nil error,
// Mutate aborts the write (nothing is persisted) and returns that error. If
// fn sets the record pointer to nil, Mutate writes nothing and returns nil
// (a "no transition needed" signal).
//
// The immutable-terminal invariant (L-3) is enforced inside this primitive:
// if fn mutates a terminal tail's OWN generation, persistLocked rejects with
// ErrLifecycleTerminalImmutable and Mutate returns that error — exactly the
// outcome the concurrent loser of a cancel/complete race must see.
//
// Callers MUST NOT already hold Lock(sessionID): sync.Mutex is not
// reentrant, and Mutate takes the lock ONCE internally (this is why Mutate
// does not delegate to the public Load/Persist, which each take the lock
// themselves — calling them under a held lock self-deadlocks). Callers that
// need an atomic RMW over a lifecycle record should ALWAYS use Mutate rather
// than a manual Load+Persist pair.
func (s *LifecycleStore) Mutate(sessionID string, fn func(*LifecycleRecord) error) error {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	cur, found, err := s.tail(sessionID)
	if err != nil {
		return err
	}
	var next *LifecycleRecord
	if found {
		c := *cur // copy so fn mutates the copy, not the tail record
		next = &c
	}
	if err := fn(next); err != nil {
		return err
	}
	if next == nil {
		return nil
	}
	return s.persistLocked(next)
}

// LifecycleFilter narrows the result of List. All fields are optional
// (zero value = skip that filter).
type LifecycleFilter struct {
	WorkspaceID string
	AgentID     string
	// ParentAgentID, when non-empty, restricts results to records whose
	// ParentAgentID is exactly this agent — the "subagents I started"
	// predicate (FR-013). Empty means "filter off", matching every other
	// field on this struct; it NEVER means "match records with an empty
	// parent" (the ADR-053 boot sweep's own queries leave it unset and must
	// stay unfiltered). Note this is a DIFFERENT axis from AgentID, which
	// matches the CHILD (the delegate target) — filtering by AgentID
	// answers "sessions run BY x", filtering by ParentAgentID answers
	// "sessions x STARTED".
	ParentAgentID string
	// ParentDurableKey, when non-empty, restricts results to records whose
	// OWN ParentDurableKey equals this value — the DIRECT children of the
	// session/chat this key names (D1's strict-direct-parent edge,
	// FR-019/FR-020). Backed by an in-memory secondary index maintained
	// inside Persist (lifecycle_index.go), so setting this field routes
	// List through an O(descendants) index lookup instead of a full-store
	// scan. This is a DIFFERENT axis from ParentAgentID: ParentDurableKey
	// answers "children of X" (session hierarchy, one level deep);
	// ParentAgentID answers "sessions x STARTED" (agent identity). Empty
	// means "filter off", like every other field on this struct.
	ParentDurableKey string
	// States, when non-empty, restricts results to records whose State is
	// in this set.
	States map[LifecycleState]bool
	// NonTerminalOnly, when true, is shorthand for "State is not one of the
	// four terminal states" — the boot sweep's primary query (another
	// wave), exposed here since it is the store's natural output shape.
	NonTerminalOnly bool
}

func (f LifecycleFilter) matches(r *LifecycleRecord) bool {
	if f.WorkspaceID != "" && r.WorkspaceID != f.WorkspaceID {
		return false
	}
	if f.AgentID != "" && r.AgentID != f.AgentID {
		return false
	}
	// ParentAgentID is matched against the record's OWN ParentAgentID and
	// nothing else — never against ParentDurableKey (a same-level SESSION
	// parentage edge post-D1, matched separately below by its own filter
	// field), OwnerScopeID ("" at top level) or AgentID (the child's id).
	// Inferring from any of those answers "which session is my direct
	// parent" or "who am I", not "which agent started me".
	if f.ParentAgentID != "" && r.ParentAgentID != f.ParentAgentID {
		return false
	}
	// ParentDurableKey answers a DIFFERENT question — "is r a DIRECT child
	// of session X" (D1/FR-019/FR-020) — matched against the record's OWN
	// ParentDurableKey, never inferred from ParentAgentID, OwnerScopeID or
	// AgentID. This is the sole clause FR-023's static gate (#106) expects
	// to find reading ParentDurableKey inside this function.
	if f.ParentDurableKey != "" && r.ParentDurableKey != f.ParentDurableKey {
		return false
	}
	if len(f.States) > 0 && !f.States[r.State] {
		return false
	}
	if f.NonTerminalOnly && r.Terminal() {
		return false
	}
	return true
}

// scanSessionIDs lists every session_id with a persisted record (one .jsonl
// file per id) under the store's directory.
func (s *LifecycleStore) scanSessionIDs() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: lifecycle: read dir: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") || strings.HasPrefix(name, ".tmp-") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".jsonl"))
	}
	sort.Strings(ids)
	return ids, nil
}

// List returns the current (tail) record for every persisted session_id
// matching filter. This is the primitive a boot sweep (another wave) uses
// to enumerate every non-terminal session with no live runtime turn
// (FR-118) — pass LifecycleFilter{NonTerminalOnly: true}.
//
// FR-019/FR-020 — when filter.ParentDurableKey is set, List is routed
// through listByParentDurableKey, which resolves the candidate id set from
// the in-memory parent index (lifecycle_index.go) instead of scanning every
// persisted session_id: the "children of X" query's file-read count is
// O(descendants of X), never O(all sessions ever) — see BDD-19.
func (s *LifecycleStore) List(filter LifecycleFilter) ([]LifecycleRecord, error) {
	if filter.ParentDurableKey != "" {
		return s.listByParentDurableKey(filter)
	}
	ids, err := s.scanSessionIDs()
	if err != nil {
		return nil, err
	}
	out := make([]LifecycleRecord, 0, len(ids))
	for _, id := range ids {
		rec, err := s.Load(id)
		if err != nil {
			if errors.Is(err, ErrLifecycleNotFound) {
				continue
			}
			return nil, err
		}
		if filter.matches(rec) {
			out = append(out, *rec)
		}
	}
	return out, nil
}

// listByParentDurableKey implements the FR-020 index-backed path for
// List(LifecycleFilter{ParentDurableKey: X, ...}). It never calls
// scanSessionIDs: the candidate child ids come entirely from the in-memory
// parent index (warmed once per store instance from disk, then kept current
// incrementally by every Persist call — see lifecycle_index.go), so cost
// scales with len(candidates), not with the total number of persisted
// session_ids. Every other filter field is still applied via filter.matches
// exactly as the full-scan path does, so combining ParentDurableKey with
// e.g. NonTerminalOnly or States behaves identically either way.
func (s *LifecycleStore) listByParentDurableKey(filter LifecycleFilter) ([]LifecycleRecord, error) {
	if err := s.parentIndex.ensureWarm(s); err != nil {
		return nil, err
	}
	childIDs := s.parentIndex.children(filter.ParentDurableKey)
	out := make([]LifecycleRecord, 0, len(childIDs))
	for _, id := range childIDs {
		rec, err := s.Load(id)
		if err != nil {
			if errors.Is(err, ErrLifecycleNotFound) {
				// Stale index entry — e.g. PruneTerminal removed this
				// child's file between children() snapshotting the set and
				// this Load. Self-heal rather than fail the whole query.
				s.parentIndex.remove(filter.ParentDurableKey, id)
				continue
			}
			return nil, err
		}
		if filter.matches(rec) {
			out = append(out, *rec)
		}
	}
	return out, nil
}

// Exists reports whether a lifecycle record file exists for sessionID.
func (s *LifecycleStore) Exists(sessionID string) bool {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return false
	}
	_, err := os.Stat(s.path(sessionID))
	return err == nil
}

// PruneTerminal deletes the persisted .jsonl file for every session_id whose
// TAIL record is terminal (completed/failed/cancelled/timed_out) and whose
// UpdatedAt is older than retentionDays*24h — explicit retention for what
// would otherwise be an unbounded, append-only-per-session-id store with no
// prune path at all (mirrors UnifiedStore.RetentionSweep's own contract and
// "retentionDays<=0 is a no-op" convention; see the caller in
// pkg/agent/boot_sweep.go for why this store specifically needed one).
//
// A NON-terminal record is NEVER pruned regardless of age, full stop: an old
// but still non-terminal record is exactly what the boot sweep exists to
// reconcile to failed(interrupted), and deleting it out from under that
// reconciliation would silently drop a stranded session instead of failing
// it closed — the one outcome this whole package exists to prevent.
//
// Returns the count of session files removed. Per-file errors (a load
// failure on a corrupt/torn record, a remove failure) are logged at Warn and
// the sweep continues with the next id; an error is returned only if the
// directory listing itself fails.
//
// FIXED (this change — BUG 2: check-then-act was not atomic): the previous
// implementation called s.Load(id) — which internally takes AND RELEASES
// the per-session striped lock — to decide eligibility, then separately
// re-acquired the SAME lock just to os.Remove the file. Between those two
// acquisitions no lock was held for that session_id at all: a follow_up/Play
// legitimately minting a new, non-terminal generation onto a terminal tail
// in that exact window (the package doc explicitly allows this) would have
// its brand-new generation silently destroyed when this function then
// deleted the whole .jsonl — violating this very function's own invariant
// ("a NON-terminal record is NEVER pruned"). See pruneTerminalOne's doc
// comment for how this is now closed.
func (s *LifecycleStore) PruneTerminal(retentionDays int, now time.Time) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
	ids, err := s.scanSessionIDs()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, id := range ids {
		if s.pruneTerminalOne(id, cutoff) {
			removed++
		}
	}
	return removed, nil
}

// pruneTerminalRaceHook is a test-only synchronization seam (always nil in
// production, never set outside a _test.go file): when non-nil it is
// invoked by pruneTerminalOne right after it decides a session_id is
// eligible for removal (tail is terminal and past the cutoff) and
// immediately before the file is actually removed. Where that call lands
// relative to the per-session lock (inside vs. outside the single critical
// section below) is exactly the fact BUG 2's regression test exercises.
var pruneTerminalRaceHook func(sessionID string) //nolint:gochecknoglobals // test-only seam, see doc comment

// pruneTerminalOne evaluates, and if eligible removes, a single session_id's
// persisted file. BUG 2 fix: the tail read, the terminal/cutoff
// re-validation, and the os.Remove all happen under ONE acquisition of the
// per-session striped lock — the same lock Persist/Mutate take for every
// OTHER writer of this session_id (see LifecycleStore's own doc comment).
// That makes "decide to prune" and "actually prune" atomic with respect to
// any concurrent follow_up/Play reopen of the SAME session: either the
// reopen's Mutate call happens-before this function's lock acquisition (in
// which case the tail read here observes the fresh, non-terminal
// generation and correctly skips pruning), or it happens-after this
// function releases the lock having already removed the file (in which
// case the reopen's own Mutate observes "no tail" and is responsible for
// treating that as "nothing to resume" rather than silent data loss) — the
// old two-lock-acquisition shape allowed neither ordering to be guaranteed,
// which is precisely how a freshly-reopened generation could be deleted out
// from under it.
//
// Deliberately scoped to ONE session_id per call (not the whole sweep) so
// the striped lock is never held across unrelated I/O for other sessions —
// each iteration of PruneTerminal's loop acquires and releases its own id's
// lock independently.
func (s *LifecycleStore) pruneTerminalOne(id string, cutoff time.Time) bool {
	mu := s.Lock(id)
	mu.Lock()
	defer mu.Unlock()

	rec, found, err := s.tail(id)
	if err != nil {
		slog.Warn("session: lifecycle: prune_terminal: load failed, skipping", "session_id", id, "error", err)
		return false
	}
	if !found {
		// No record for this id (already removed, or never had one) —
		// mirrors the ErrLifecycleNotFound "continue" the old Load-based
		// code silently handled the same way.
		return false
	}
	if !rec.Terminal() || rec.UpdatedAt.After(cutoff) {
		return false
	}

	if pruneTerminalRaceHook != nil {
		pruneTerminalRaceHook(id)
	}

	if rmErr := os.Remove(s.path(id)); rmErr != nil {
		slog.Warn("session: lifecycle: prune_terminal: remove failed", "session_id", id, "error", rmErr)
		return false
	}
	// Keep the FR-020 parent index consistent with disk: id's own file is
	// gone, so it must stop being returned as a child of its ParentDurableKey
	// (listByParentDurableKey also self-heals on a stale hit, but removing it
	// here means a query issued after this prune never has to pay that
	// ErrLifecycleNotFound round trip at all).
	s.parentIndex.remove(rec.ParentDurableKey, id)
	return true
}
