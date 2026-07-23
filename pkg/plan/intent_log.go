// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// intent_log.go implements the write-ahead intent-log for transactional
// tail-append (ADR-053 M4/FR-148/INV-6, acceptance G-11/t3). Per-file JSONL
// storage gives only per-file temp+rename atomicity; a multi-entity plan
// correction appends N members + their edges + a revision entry + a
// plan-record patch across SEVERAL files, so the all-or-nothing guarantee is
// provided by THIS log rather than by the per-file writes.
//
// The intent record is SELF-CONTAINED (FR-148): it carries the full member
// BODIES (not just ids), the full edge list, the revision entry, and the
// plan-record patch — replay needs nothing else. Ordering:
//
//  1. AppendIntent writes the self-contained record at status=uncommitted.
//  2. MarkCommitted flips it to status=committed (fsync) — the linearization
//     point; up to this point boot discards the intent (exact pre-append DAG).
//  3. The caller applies each per-file write (temp+rename) via Apply.
//  4. MarkDone flips it to status=done — the apply is complete.
//
// ReplayAtBoot classifies every intent:
//   - uncommitted -> discard (delete partially-written members, wire no edges
//     -> exact pre-append DAG);
//   - committed-but-not-done -> replay forward idempotently (Apply is a no-op
//     when the per-intent done-marker already covers the write — the caller's
//     Apply must be idempotent, and the log's own done-marker is the
//     authoritative guard so a partial re-apply is safe);
//   - done -> no action.
//
// This wave builds the STORE + boot replay. The owner-loop WRITE path
// (AppendIntent -> MarkCommitted -> Apply -> MarkDone, driven by the Phase-2
// owner-correction handler) consumes it next.
package plan

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// IntentStatus is the commit state of an intent record (FR-148/INV-6).
type IntentStatus string

const (
	// IntentUncommitted: the intent record is written but the commit marker
	// has not been fsynced. A Stop/crash here leaves an uncommitted intent,
	// which boot discards (exact pre-append DAG).
	IntentUncommitted IntentStatus = "uncommitted"
	// IntentCommitted: the commit marker has been fsynced — the linearization
	// point. The per-file writes may or may not be fully applied yet; boot
	// replays them forward idempotently.
	IntentCommitted IntentStatus = "committed"
	// IntentDone: every per-file write has been applied and confirmed. Boot
	// needs to take no action.
	IntentDone IntentStatus = "done"
)

// IntentEdge is a single DAG dependency edge carried inside a self-contained
// intent record (FR-148: "the full edge list"). FromTaskID must complete
// before ToTaskID may start (mirrors task.BlockedBy).
type IntentEdge struct {
	FromTaskID string `json:"from_task_id"`
	ToTaskID   string `json:"to_task_id"`
}

// RevisionVerb is the owner-correction revision verb (spec §Contract Surface
// — PlanRevisionEntry).
type RevisionVerb string

const (
	RevisionAppend        RevisionVerb = "append"
	RevisionSupersede     RevisionVerb = "supersede"
	RevisionTargetedRetry RevisionVerb = "targeted_retry"
)

// RevisionEntry is the durable record of one owner correction, committed
// transactionally with the tail members + edges (INV-6). Mirrors the spec's
// PlanRevisionEntry shape.
type RevisionEntry struct {
	RevisionID          string       `json:"revision_id"`
	PlanID              string       `json:"plan_id"`
	Generation          int          `json:"generation"`
	Verb                RevisionVerb `json:"verb"`
	FalsifiedAssumption string       `json:"falsified_assumption,omitempty"`
	TailAdds            []string     `json:"tail_adds,omitempty"`
	SupersededMemberID  string       `json:"superseded_member_id,omitempty"`
	RetriedMemberID     string       `json:"retried_member_id,omitempty"`
	Reason              string       `json:"reason,omitempty"`
	CreatedAt           time.Time    `json:"created_at"`
}

// IntentRecordPatch is the plan-record patch carried inside an intent
// (FR-148: "the plan-record patch"). It is a narrow, serializable subset of
// plan.Patch covering exactly the fields a correction may transition — the
// plan-phase flip (awaiting_owner_correction -> dispatching on a fresh
// correction), clearing the durable unmet signature, and bumping activity.
// Kept as plain fields (not *pointers) because an intent record is an
// immutable, self-contained historical document, not a live Patch.
type IntentRecordPatch struct {
	// ClearLastUnmetTerminalSignature requests the plan's durable unmet
	// signature be cleared (a correction means new activity — the F2 gate
	// must allow a fresh round, INV-7).
	ClearLastUnmetTerminalSignature bool `json:"clear_last_unmet_terminal_signature,omitempty"`
	// PlanPhase, when non-empty, sets the plan's phase (e.g. dispatching on a
	// fresh correction).
	PlanPhase PlanPhase `json:"plan_phase,omitempty"`
}

// IntentRecord is the self-contained write-ahead intent (FR-148). It carries
// the full member bodies + edges + revision entry + plan-record patch so
// replay needs nothing else.
//
// not-wire-format: internal durability record.
type IntentRecord struct {
	IntentID  string            `json:"intent_id"`
	PlanID    string            `json:"plan_id"`
	Status    IntentStatus      `json:"status"`
	Members   []task.Task       `json:"members,omitempty"`
	Edges     []IntentEdge      `json:"edges,omitempty"`
	Revision  RevisionEntry     `json:"revision"`
	Patch     IntentRecordPatch `json:"patch,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	// CommittedAt is the fsync linearization point (status -> committed). Zero
	// while uncommitted.
	CommittedAt time.Time `json:"committed_at,omitempty"`
	// DoneAt is when the per-file apply completed (status -> done). Zero while
	// not done.
	DoneAt time.Time `json:"done_at,omitempty"`
	// Hmac is the tamper-evidence chain HMAC (sec-MINOR-3/#539), reusing the
	// exact v0.2 #155 audit-log mechanism (pkg/audit/hmac.go):
	// hex(HMAC-SHA256(chainKey, prev_hmac || canonical_json_without_hmac)).
	// prev_hmac is the previous LINE in this plan's file (audit.GenesisSeed()
	// for the first line), not the previous line for the same intent_id — the
	// chain covers the whole append-only file. Empty on any record written
	// before this field existed (pre-chain legacy row); see embedChainHMAC.
	Hmac string `json:"hmac,omitempty"`
}

// ReplayClassification groups the boot-replay disposition of an intent log's
// records (FR-148/INV-6).
type ReplayClassification struct {
	// Discard (uncommitted): delete any partially-written members, wire no
	// edges -> exact pre-append DAG.
	Discard []IntentRecord
	// ReplayForward (committed-but-not-done): apply idempotently to the
	// intended post-append DAG.
	ReplayForward []IntentRecord
	// AlreadyDone (done): no action.
	AlreadyDone []IntentRecord
}

// IntentLog is the write-ahead intent-log store. One JSONL file per plan_id
// (<dir>/<plan_id>.jsonl), append-only, with a striped per-plan lock. The log
// itself never applies a correction's per-file writes — it only tracks commit
// state; the caller's Apply function does the per-file work and the log's
// done-marker guards idempotent replay.
type IntentLog struct {
	dir      string
	lock     *StripedLock
	chainKey []byte // sec-MINOR-3/#539: HMAC-chain key, resolved once at construction
}

// NewIntentLog creates an IntentLog rooted at dir. By convention dir is
// "<OMNIPUS_HOME>/plan_intents".
//
// chainKey is the HMAC-chain tamper-evidence key (sec-MINOR-3/#539): pass a
// subkey derived via credentials.Store.DeriveSubkey(IntentLogChainKeyInfo) in
// production (mirrors how the gateway derives audit.AuditChainKeyInfo for
// the audit logger — see pkg/gateway/gateway.go's boot wiring). A nil/empty
// key falls back to a deterministic dev-only key with a sticky slog.Warn
// (mirrors pkg/audit's resolveChainKey precedent), so tests and early-dev
// boots without a credential store still run — loud, not silent.
func NewIntentLog(dir string, chainKeys ...[]byte) *IntentLog {
	var chainKey []byte
	if len(chainKeys) > 0 {
		chainKey = chainKeys[0]
	}
	return &IntentLog{dir: dir, lock: &StripedLock{}, chainKey: resolveChainKey(chainKey)}
}

// Dir returns the log's root directory.
func (l *IntentLog) Dir() string { return l.dir }

func (l *IntentLog) path(planID string) string {
	return filepath.Join(l.dir, planID+".jsonl")
}

func (l *IntentLog) lockFor(planID string) *sync.Mutex {
	return l.lock.Get(planID)
}

// AppendIntent writes rec as status=uncommitted (the pre-commit record). This
// is step (1) of the FR-148 ordering — the linearization point is the
// SUBSEQUENT MarkCommitted call, not this one. rec.Status is forced to
// IntentUncommitted regardless of the caller's input.
func (l *IntentLog) AppendIntent(rec IntentRecord) error {
	if rec.IntentID == "" {
		return fmt.Errorf("intent_log: intent_id must not be empty")
	}
	if rec.PlanID == "" {
		return fmt.Errorf("intent_log: plan_id must not be empty")
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	rec.Status = IntentUncommitted
	mu := l.lockFor(rec.PlanID)
	mu.Lock()
	defer mu.Unlock()
	// sec-MINOR-3/#539: 0700 (owner-only), matching the tamper-evidence
	// posture of other authoritative Omnipus state (master.key 0600 under a
	// 0700 home dir; credentials.Store's own MkdirAll calls) rather than the
	// prior world-/group-readable 0755.
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return fmt.Errorf("intent_log: mkdir: %w", err)
	}
	existing, err := l.readAllLocked(rec.PlanID)
	if err != nil {
		return fmt.Errorf("intent_log: read existing for hmac chain: %w", err)
	}
	if err := embedChainHMAC(existing, &rec, l.chainKey); err != nil {
		return fmt.Errorf("intent_log: chain embed: %w", err)
	}
	return appendJSONLSync(l.path(rec.PlanID), &rec)
}

// markLocked rewrites the log file for planID with the matching intent's
// status/timestamps updated. The caller MUST hold the per-plan lock. Because
// the log is append-only JSONL, a status update is itself an append of a new
// line carrying the SAME intent_id with the new status — Replay keeps only
// the LAST line per intent_id (the current state), exactly like the
// lifecycle store's tail convention. This preserves the audit trail and keeps
// the status update a single temp+rename-free append.
func (l *IntentLog) markLocked(planID, intentID string, status IntentStatus) error {
	records, err := l.readAllLocked(planID)
	if err != nil {
		return err
	}
	// Find the latest record for intentID (the current state).
	var latest *IntentRecord
	for i := range records {
		if records[i].IntentID == intentID {
			latest = &records[i]
		}
	}
	if latest == nil {
		return fmt.Errorf("intent_log: %w: intent %q not found in plan %q", errIntentNotFound, intentID, planID)
	}
	latest.Status = status
	switch status {
	case IntentCommitted:
		latest.CommittedAt = time.Now().UTC()
	case IntentDone:
		latest.DoneAt = time.Now().UTC()
	}
	if err := embedChainHMAC(records, latest, l.chainKey); err != nil {
		return fmt.Errorf("intent_log: chain embed: %w", err)
	}
	return appendJSONLSync(l.path(planID), latest)
}

// appendJSONLSync appends one intent record and synchronizes the file before
// returning. Commit and done markers use this helper so their durability is
// the linearization point documented above.
func appendJSONLSync(path string, record any) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("intent_log: marshal: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("intent_log: open for append: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("intent_log: append: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("intent_log: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("intent_log: close: %w", err)
	}
	return nil
}

// ErrIntentNotFound is returned when an intent id does not exist.
var errIntentNotFound = errors.New("intent not found")

// MarkCommitted flips intentID to status=committed (step 2 — the fsync
// linearization point). Returns errIntentNotFound if the intent was never
// appended.
func (l *IntentLog) MarkCommitted(planID, intentID string) error {
	mu := l.lockFor(planID)
	mu.Lock()
	defer mu.Unlock()
	return l.markLocked(planID, intentID, IntentCommitted)
}

// MarkDone flips intentID to status=done (step 4 — the per-file apply is
// complete). Returns errIntentNotFound if the intent was never appended.
func (l *IntentLog) MarkDone(planID, intentID string) error {
	mu := l.lockFor(planID)
	mu.Lock()
	defer mu.Unlock()
	return l.markLocked(planID, intentID, IntentDone)
}

// readAllLocked reads every record in planID's log file. The caller MUST hold
// the per-plan lock (or accept a best-effort read).
func (l *IntentLog) readAllLocked(planID string) ([]IntentRecord, error) {
	f, err := os.Open(l.path(planID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("intent_log: open %q: %w", planID, err)
	}
	defer f.Close()
	var out []IntentRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec IntentRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			// Skip a torn/corrupt trailing line (crash-safety — mirrors the
			// lifecycle store's own tolerance).
			continue
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("intent_log: scan %q: %w", planID, err)
	}
	return out, nil
}

// currentStates returns the latest record per intent_id (the current state of
// each intent), dropping superseded earlier lines for the same id.
func currentStates(records []IntentRecord) []IntentRecord {
	latest := make(map[string]IntentRecord, len(records))
	var order []string
	for i := range records {
		id := records[i].IntentID
		if _, ok := latest[id]; !ok {
			order = append(order, id)
		}
		latest[id] = records[i]
	}
	out := make([]IntentRecord, 0, len(order))
	for _, id := range order {
		out = append(out, latest[id])
	}
	return out
}

// List returns the current (latest-line) intent record for every intent_id in
// planID's log.
func (l *IntentLog) List(planID string) ([]IntentRecord, error) {
	mu := l.lockFor(planID)
	mu.Lock()
	defer mu.Unlock()
	records, err := l.readAllLocked(planID)
	if err != nil {
		return nil, err
	}
	return currentStates(records), nil
}

// Classify partitions planID's current intent records into the three boot
// dispositions (FR-148/INV-6).
func (l *IntentLog) Classify(planID string) (ReplayClassification, error) {
	records, err := l.List(planID)
	if err != nil {
		return ReplayClassification{}, err
	}
	return classifyRecords(records), nil
}

// classifyRecords is the pure classification of intent records into the three
// boot dispositions — extracted for direct unit testing.
func classifyRecords(records []IntentRecord) ReplayClassification {
	var c ReplayClassification
	for i := range records {
		switch records[i].Status {
		case IntentDone:
			c.AlreadyDone = append(c.AlreadyDone, records[i])
		case IntentCommitted:
			c.ReplayForward = append(c.ReplayForward, records[i])
		default: // uncommitted (or unknown — treat as uncommitted, fail-safe)
			c.Discard = append(c.Discard, records[i])
		}
	}
	return c
}

// ReplayResult records the outcome of a boot replay pass.
type ReplayResult struct {
	PlanID         string
	Classification ReplayClassification
	// Replayed is the count of committed-not-done intents whose Apply ran.
	Replayed int
	// Discarded is the count of uncommitted intents rolled back.
	Discarded int
}

// ApplyFunc applies one self-contained intent record's per-file writes
// idempotently (create the member tasks, wire the edges, patch the plan). It
// MUST be idempotent — ReplayAtBoot may call it for an intent whose writes
// partially landed before a crash. Return nil on success (or if the writes
// were already applied — a no-op); ReplayAtBoot then marks the intent done.
type ApplyFunc func(rec IntentRecord) error

// ReplayAtBoot classifies and replays planID's intent log. Uncommitted
// intents are discarded (rolled back); committed-not-done intents are passed
// to apply (replayed forward) and, on success, marked done. The done-marker
// makes a future re-apply a no-op (FR-148 idempotent replay-forward).
//
// apply may be nil, in which case the classification is returned with no
// writes applied — useful for a dry-run / inspection, or when the caller
// wants to apply the ReplayForward set itself.
func (l *IntentLog) ReplayAtBoot(planID string, apply ApplyFunc) (ReplayResult, error) {
	res := ReplayResult{PlanID: planID}

	// sec-MINOR-3/#539: verify the HMAC chain before replaying. A broken
	// chain is a loud ERROR alarm, not a boot-abort or a refused replay —
	// mirrors the audit-log precedent (chain verification is an on-demand
	// operator check via GET /settings/audit-log, pkg/gateway/rest_settings.go,
	// never a boot gate) so a tampered or corrupted record degrades to the
	// existing fail-safe discard/replay-forward classification (INV-6)
	// rather than bricking recovery.
	if chainRes, verr := l.VerifyChain(context.Background(), planID); verr != nil {
		slog.Warn("intent_log: HMAC chain verification could not run",
			"plan_id", planID, "error", verr)
	} else if chainRes != nil && !chainRes.Valid {
		slog.Error("intent_log: HMAC chain broken — a record may have been tampered with or corrupted",
			"plan_id", planID, "broken_at", chainRes.BrokenAt, "reason", chainRes.Reason,
			"file", chainRes.FailedFile)
	}

	class, err := l.Classify(planID)
	if err != nil {
		return res, err
	}
	res.Classification = class
	res.Discarded = len(class.Discard)

	if apply == nil {
		return res, nil
	}
	for i := range class.ReplayForward {
		rec := class.ReplayForward[i]
		if err := apply(rec); err != nil {
			return res, fmt.Errorf("intent_log: replay forward %q: %w", rec.IntentID, err)
		}
		// The apply succeeded (or was a no-op) — mark the intent done so a
		// later re-apply is skipped. A mark failure is returned: a missing
		// done-marker would cause a redundant re-apply on the NEXT boot (not
		// incorrect, since apply is idempotent, but noisy and worth surfacing).
		if err := l.MarkDone(planID, rec.IntentID); err != nil {
			return res, fmt.Errorf("intent_log: mark done %q: %w", rec.IntentID, err)
		}
		res.Replayed++
	}
	return res, nil
}

// ReplayAllPlans runs ReplayAtBoot across every plan_id that has an intent
// log file. This is the boot wiring entry point — called once at gateway
// startup, after the plan/task stores are open. A per-plan replay error is
// returned in the slice rather than aborting the whole pass (one corrupt log
// must not wedge the others).
func (l *IntentLog) ReplayAllPlans(apply ApplyFunc) ([]ReplayResult, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("intent_log: read dir: %w", err)
	}
	var results []ReplayResult
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") || strings.HasPrefix(name, ".tmp-") {
			continue
		}
		planID := strings.TrimSuffix(name, ".jsonl")
		res, rerr := l.ReplayAtBoot(planID, apply)
		if rerr != nil {
			results = append(results, ReplayResult{PlanID: planID, Classification: res.Classification})
			// Continue rather than abort; collect the error but keep going.
			if err == nil {
				err = fmt.Errorf("intent_log: plan %q: %w", planID, rerr)
			}
			continue
		}
		results = append(results, res)
	}
	return results, err
}

// CommitCorrection transactionally commits a correction via the 4-step
// write-ahead protocol (FR-148/INV-6/N-8). This is the owner-loop WRITE path
// the Phase-2 owner-correction handler drives: it sequences the self-contained
// intent record through AppendIntent → MarkCommitted → Apply → MarkDone as one
// all-or-nothing operation.
//
// Ordering (INV-6):
//  1. AppendIntent writes the self-contained record at status=uncommitted.
//  2. MarkCommitted flips it to committed (fsync) — the linearization point.
//     A Stop/crash before this point leaves an uncommitted intent which boot
//     discards (delete any partially-written members, wire no edges → exact
//     pre-append DAG).
//  3. apply performs the per-file writes (create member tasks, wire edges,
//     patch the plan record). It MUST be idempotent — boot may replay it.
//  4. MarkDone flips the intent to done. A crash after commit but before done
//     leaves a committed-but-not-done intent which boot replays forward
//     idempotently (the done-marker makes re-apply a no-op).
//
// If apply fails AFTER MarkCommitted succeeded, the correction IS durably
// committed (boot will finish the apply) but this call returns the apply error
// so the caller knows the in-process apply was incomplete. The caller should
// NOT attempt to roll back — boot's replay-forward is the correct completion
// path.
//
// apply may be nil for a commit-only intent (no per-file writes to apply —
// e.g. a supersede that records only a revision entry with no new members);
// the method skips straight to MarkDone.
func (l *IntentLog) CommitCorrection(rec IntentRecord, apply ApplyFunc) error {
	if rec.IntentID == "" {
		return fmt.Errorf("intent_log: CommitCorrection: intent_id must not be empty")
	}
	if rec.PlanID == "" {
		return fmt.Errorf("intent_log: CommitCorrection: plan_id must not be empty")
	}
	// Step 1: write the self-contained intent record (uncommitted).
	if err := l.AppendIntent(rec); err != nil {
		return fmt.Errorf("intent_log: CommitCorrection: append: %w", err)
	}
	// Step 2: mark committed (fsync — the linearization point).
	if err := l.MarkCommitted(rec.PlanID, rec.IntentID); err != nil {
		return fmt.Errorf("intent_log: CommitCorrection: mark committed: %w", err)
	}
	// Step 3: apply the per-file writes (idempotent).
	if apply != nil {
		if err := apply(rec); err != nil {
			// The correction is durably committed but the in-process apply
			// failed — boot will replay-forward idempotently. Return the
			// error so the caller knows, but do NOT attempt rollback.
			return fmt.Errorf("intent_log: CommitCorrection: apply (committed; boot will replay): %w", err)
		}
	}
	// Step 4: mark done.
	if err := l.MarkDone(rec.PlanID, rec.IntentID); err != nil {
		return fmt.Errorf("intent_log: CommitCorrection: mark done: %w", err)
	}
	return nil
}
