// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U2 (Wave C) — the strict store-facing API layer: AppendTranscriptStrict
// (FR-001, W3 store half) and CreateSessionWithID (FR-005/FR-006/FR-082/FR-096,
// W1 store half).
//
// Both entry points exist to close the SAME defect class this migration's
// governing note names: a predicate that returns "nothing to do" (or worse,
// silently DOES something nobody asked for) and every caller proceeds
// happily. AppendTranscriptStrict refuses a write against a session that was
// never created, rather than minting an orphan directory for it.
// CreateSessionWithID refuses to adopt an id that collides with something
// already on disk, rather than silently overwriting or merging into it.
//
// This file is this unit's ENTIRE surface (ownership Rule 1/2): it does not
// touch pkg/session/unified.go, which is owned by the U4->U5->U6 chain and is
// being edited concurrently by U5 in this same wave. Every primitive this
// file depends on — lockSession (unified_lock.go, U4), readMetaLocked,
// writeMetaLocked, createSessionLocked, validateSessionID (unified.go) — is
// consumed as a frozen or already-published contract, never modified here.
package session

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// AppendTranscriptStrict appends entry to sessionID's transcript.jsonl,
// refusing loudly — and creating nothing on disk — when sessionID does not
// name a real, store-backed session (FR-001; BDD-01, BDD-02).
//
// THE DEFECT THIS CLOSES. Today's lenient UnifiedStore.AppendTranscript
// (pkg/session/unified.go) calls fileutil.AppendJSONL FIRST, which itself
// begins with os.MkdirAll — so a write against a session id with no
// meta.json creates an orphan directory, writes the line into it, then fails
// the follow-up meta-stats read, logs a WARN, and returns nil. An assertion
// of the form "the append succeeded" can therefore never fail against that
// primitive, which is exactly the "silent create, not a silent drop"
// mechanism this migration exists to close (see this spec's governing
// note and ADR-057 US-1/AC-1). AppendTranscriptStrict inverts the order: it
// resolves the session's existence FIRST, under the session's own shard, and
// only touches the filesystem once that check has succeeded.
//
// Existence predicate — frozen cross-unit contract with U5 (spec line
// ~1008): "AppendTranscriptStrict's existence predicate is `readMetaLocked`
// returned a non-nil error." U5 is independently converting AppendTranscript
// ITSELF to this same strictness (FR-002/W3a) by deleting its lenient
// branch; this method does not depend on that landing in any particular
// order — it is self-contained, exactly as this wave's coordination
// discipline requires ("coordinate by contract, not by editing each other's
// files").
//
// The stats-accumulation block below calls the SAME accumulateEntryStats
// helper (entry_stats.go) UnifiedStore.AppendTranscript calls — it used to
// carry its own byte-for-byte copy of that logic, a deliberate, temporary
// concession to this wave's "coordinate by contract, not by editing each
// other's files" discipline (each unit owned a different file and this one
// was told not to touch unified.go). That constraint ended with the wave;
// the duplication was extracted into a single shared implementation once
// the concurrent-edit risk it existed to avoid was gone. U5 has since landed
// FR-002 (unified.go's AppendTranscript now routes its own stats mutation
// through u6MarkStatsDirtyLocked, the FR-061 in-memory-only throttle), and
// this method converges on the exact same call per FR-001's own text — "a
// name, not a second behavior" (14-reviewer fix wave finding #4).
//
// THE PROBLEM THAT CONVERGENCE CLOSES. Before this fix, this method still
// called writeMetaLocked — which, to decide which of the four on-disk field
// groups actually changed, unconditionally re-reads meta via a SECOND
// readMetaLocked call and marshals all four groups' prev-vs-current JSON
// (u5SameJSON) even though this method's own mutation scope is statically
// known to touch ONLY Stats+UpdatedAt. At the turn engine's hottest call
// sites (a streamed transcript line per token) that meant a redundant clone,
// 8 marshal calls whose answer never varies, and a synchronous stats.json
// fsync on every single line — the exact per-token cost AppendTranscript's
// own FR-061 throttle exists to avoid, silently reintroduced here. Calling
// u6MarkStatsDirtyLocked directly (see below) is not just cheaper — it also
// means Strict inherits the throttle for the first time, matching the
// non-strict path's cost profile exactly.
//
// What "strict" still means after this convergence: the pre-flight
// existence check above (refusing to write against an unknown session,
// creating nothing) and this call's own transcript.jsonl append error
// propagation. u6MarkStatsDirtyLocked is pure in-memory bookkeeping and can
// never fail, so there is no longer a synchronous meta-write error to
// surface for the stats half — an unflushed delta is persisted by the
// periodic flusher or the next forced-flush point (FlushSessionStats /
// FlushAndEvictSessionMeta), exactly like every other u6MarkStatsDirtyLocked
// caller in this store.
func (us *UnifiedStore) AppendTranscriptStrict(sessionID string, entry TranscriptEntry) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	h := us.lockSession(sessionID)
	defer h.Unlock()

	// The strict existence check MUST run before any filesystem write:
	// fileutil.AppendJSONL below begins with os.MkdirAll, which would
	// otherwise silently mint the orphan directory this method exists to
	// refuse (BDD-01; dataset rows 2, 4, 7 — missing session, corrupted
	// session directory with no meta.json, and a session deleted between
	// resolve and append all resolve to the SAME non-nil-error path here,
	// since DeleteSession also takes sessionID's own shard and therefore
	// cannot interleave mid-call).
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return fmt.Errorf("unified_store: append transcript strict: session %q does not exist: %w", sessionID, err)
	}

	transcriptPath := filepath.Join(us.baseDir, sessionID, "transcript.jsonl")
	if err := fileutil.AppendJSONL(transcriptPath, entry); err != nil {
		return fmt.Errorf("unified_store: append transcript: %w", err)
	}

	// Stats + UpdatedAt bookkeeping — calls the same accumulateEntryStats
	// helper AppendTranscript uses (entry_stats.go); see this method's doc
	// comment above for the extraction history.
	accumulateEntryStats(&meta.Stats, entry)
	meta.UpdatedAt = entry.Timestamp

	// FR-061 throttle (see this method's doc comment for the convergence
	// history): mutate ONLY the cached entry and mark the session dirty for
	// the periodic flusher (or the next forced-flush point) to persist —
	// the SAME call AppendTranscript's own hot path already makes, never a
	// second stats-writing mechanism. What remains STRICT about this method
	// is unchanged: the pre-flight existence check above, and the
	// transcript.jsonl append error propagated earlier in this function.
	us.u6MarkStatsDirtyLocked(sessionID, meta)
	return nil
}

// CreateSessionWithID mints a session under the EXACT id supplied — never a
// freshly generated one — carrying the calling parent's Owner verbatim
// (FR-005, FR-006). It is the exported wrapper delegation spawn (U7,
// pkg/agent/subturn.go) uses so a child's session id is the very id the rest
// of the delegation machinery already computed, mirroring the in-house
// exact-id precedent at GetOrCreateScheduledSession (pkg/session/unified.go).
//
// FR-082 — the read -> RELEASE -> create protocol (grill finding C-6).
// Copying the parent's Owner makes this look like a single operation
// touching two sessions, which FR-050 forbids by default (two session
// shards must never be held simultaneously, with only the ClearAll/
// RetentionSweep all-64-shards exception). This function honours the
// prohibition literally rather than being exempted from it: it reads the
// parent's meta under lockSession(parentID), releases that shard COMPLETELY,
// and only THEN acquires lockSession(childID) to create the child. At no
// instant are two session shards held at once — this is a *sequential*
// two-step operation, not a nested one. Acquiring both at once would use
// HASH order (shard(child) may sort below or above shard(parent)
// arbitrarily), inverting against ClearAll/RetentionSweep's fixed INDEX
// order — the exact defect ADR-057 names as R-19. The resulting TOCTOU on
// Owner (the parent's Owner could in principle change between the read and
// the child's creation) is accepted and documented per FR-082: the field is
// immutable after a session is created.
//
// This two-step "create, then patch under a second, later lockSession
// acquisition" shape is not new to this store: NewChannelSession
// (pkg/session/unified.go) already creates via NewSession and then re-locks
// the same session's shard for a follow-up writeMetaLocked call to stamp
// PeerID/Title. CreateSessionWithID follows that exact precedent for the
// Owner stamp.
//
// FR-096 / BDD-107 (STRIDE: tampering) — a childID that collides with an
// existing session directory is a LOUD failure, never a silent adopt or
// overwrite. createSessionLocked itself (relied on by NewSession and
// GetOrCreateScheduledSession) calls os.MkdirAll, which is idempotent and
// silently succeeds against a directory that already exists — safe for
// those two callers only because NewSession always mints a fresh UUID and
// GetOrCreateScheduledSession's caller-supplied ids are reserved,
// self-consistent conventions (e.g. "sched-main-<owner>"). CreateSessionWithID's
// caller-supplied id comes from delegation call-graph state instead, so this
// function checks for a pre-existing directory BEFORE calling
// createSessionLocked and refuses outright — the child must never adopt
// another session's transcript.jsonl, meta.json, owner, or stats.
//
// FR-006 / BDD-08 — an empty parent Owner is not silently swallowed: this
// function logs the absence at spawn time (rather than letting it manifest
// only later as an unstamped entity somewhere downstream), while still
// creating the child session (an owner-less parent is a valid, if unusual,
// state — not a reason to refuse delegation).
func (us *UnifiedStore) CreateSessionWithID(
	childID string,
	parentID string,
	sessionType UnifiedSessionType,
	channel string,
	creatingAgentID string,
) (*UnifiedMeta, error) {
	if err := validateSessionID(childID); err != nil {
		return nil, fmt.Errorf("unified_store: create session with id: invalid child id: %w", err)
	}
	if err := validateSessionID(parentID); err != nil {
		return nil, fmt.Errorf("unified_store: create session with id: invalid parent id: %w", err)
	}

	// Step 1 (FR-082): read the parent's Owner under ONLY the parent's shard,
	// then release it completely before touching the child's shard at all.
	parentOwner, err := u2ReadParentOwner(us, parentID)
	if err != nil {
		return nil, fmt.Errorf("unified_store: create session with id: resolve parent %q: %w", parentID, err)
	}
	if parentOwner == "" {
		// FR-006/BDD-08: make the absence observable NOW rather than only as
		// an unstamped entity discovered later.
		slog.Warn("unified_store: create session with id: parent has no owner to inherit",
			"child_id", childID, "parent_id", parentID)
	}

	// Step 2 (FR-082): acquire ONLY the child's shard to create it. The
	// parent's shard was already fully released above — two session shards
	// are never held simultaneously.
	h := us.lockSession(childID)
	defer h.Unlock()

	// FR-096/BDD-107: collision check BEFORE any write. A directory check
	// (rather than a readMetaLocked/meta.json check) is deliberate: it also
	// catches an orphan directory that exists WITHOUT a meta.json — e.g. one
	// left behind by the lenient AppendTranscript's MkdirAll bug this
	// migration is closing elsewhere — which a meta-only check would miss
	// and silently adopt.
	sessionDir := filepath.Join(us.baseDir, childID)
	if _, statErr := os.Stat(sessionDir); statErr == nil {
		return nil, fmt.Errorf("unified_store: create session with id: session %q already exists", childID)
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("unified_store: create session with id: stat %q: %w", sessionDir, statErr)
	}

	meta, err := us.createSessionLocked(childID, sessionType, channel, creatingAgentID)
	if err != nil {
		return nil, err
	}

	if parentOwner != "" {
		meta.Owner = parentOwner
		if err := us.writeMetaLocked(childID, meta); err != nil {
			return nil, fmt.Errorf("unified_store: create session with id: persist inherited owner: %w", err)
		}
	}
	return meta, nil
}

// u2ReadParentOwner reads parentID's Owner field under ONLY the parent's
// session shard, releasing it before returning — the "read, then RELEASE"
// half of FR-082's protocol. A free function (not a method) so its single
// acquire/release pair is visible as exactly that in a lock-order trace,
// mirroring lockSession's own free-standing FR-101 seam.
//
// Unexported package-level helper prefixed u2 per ownership Rule 6: this
// file and U5's unified_meta_files.go/unified.go land in the same package in
// the same wave, so an unprefixed name like readParentOwner risks a silent
// collision neither unit would see until integration.
func u2ReadParentOwner(us *UnifiedStore, parentID string) (string, error) {
	h := us.lockSession(parentID)
	defer h.Unlock()
	meta, err := us.readMetaLocked(parentID)
	if err != nil {
		return "", err
	}
	return meta.Owner, nil
}
