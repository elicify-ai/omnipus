// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-20260928 sub-agent control plane (asset cd20cf8b), D2 CRIT-001 /
// round-4 R4-MAJ-002: the protected terminal/outbox commit tuple and its
// DELIVERY-ONLY journal.
//
// One LifecycleStore.Mutate commits done/failed AND an unpublished
// final-delivery outbox entry (LifecycleRecord.FinalDelivery). Everything
// that happens AFTER that commit — delivery progress and payload
// retirement — is appended to the session's lifecycle JSONL as a typed
// `final_delivery_update` envelope by UpdateFinalDelivery, never as a
// LifecycleRecord and never through persistLocked: terminal outcome records
// stay immutable (ErrLifecycleTerminalImmutable is untouched), and a
// delivery envelope must never become a session state or reset a newer
// generation.
//
// not-wire-format: internal storage only — no gateway/SPA bytes cross this
// surface (ADR D4 wire table, "lifecycle-internal final_delivery").
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// JournalKindFinalDeliveryUpdate is the `kind` discriminator of a
// delivery-only journal envelope line. A LifecycleRecord line carries no
// "kind" key at all (every line written before envelopes existed, and every
// record line since), so the discriminator is value equality against this
// constant: "" (absent) means record line, this value means envelope line.
const JournalKindFinalDeliveryUpdate = "final_delivery_update"

// ErrFinalDelivery* are UpdateFinalDelivery's visible refusals (D2: store
// validation rejects unknown identities, regressing progress, stale
// revisions and premature retirement — never an implicit acknowledgment or
// silent suppression).
var (
	// ErrFinalDeliveryUnknownCommit — no committed final matches
	// (session, generation, commit_id).
	ErrFinalDeliveryUnknownCommit = errors.New("session: lifecycle: final delivery: no committed final matches this identity")
	// ErrFinalDeliveryRevisionConflict — expected_delivery_revision does not
	// match the journal's current revision (CAS); the publisher reloads and
	// retries without losing progress.
	ErrFinalDeliveryRevisionConflict = errors.New("session: lifecycle: final delivery: stale expected delivery revision")
	// ErrFinalDeliveryRetireNotEarned — retire_payload demanded before the
	// durable delivery prerequisites (inbox appended AND wake recorded or
	// inbox id acknowledged).
	ErrFinalDeliveryRetireNotEarned = errors.New("session: lifecycle: final delivery: payload retirement before durable delivery")
	// ErrFinalDeliveryRetired — the payload is already retired; no further
	// progress facts are legal (the compaction summary is complete).
	ErrFinalDeliveryRetired = errors.New("session: lifecycle: final delivery: payload already retired")
	// ErrFinalDeliveryOrphanEnvelope — a delivery envelope (or a
	// final_delivery on a non-terminal record) with no matching committed
	// terminal record: a visible consistency error that can never authorize
	// publication or retirement.
	ErrFinalDeliveryOrphanEnvelope = errors.New("session: lifecycle: final delivery: orphan or mismatching delivery envelope")
	// ErrFinalDeliveryProgressRegression — an advance snapshot sets a fact
	// to false after the journal recorded it true. The snapshot is a full
	// claim, not a delta: FinalDeliveryProgress has no presence flags, so
	// false is not "omitted".
	ErrFinalDeliveryProgressRegression = errors.New("session: lifecycle: final delivery: progress regression refused")
)

// FinalDeliveryCommit is the protected, immutable terminal/outbox tuple one
// outcome/publication commit writes onto the terminal LifecycleRecord
// (D2: "commits {session_id, generation, commit_id, execution_id, message_id,
// outcome, parent_session_id, payload_hash, exact_upward_message}").
// Outcome, parent, message id, destination and payload identity are
// immutable once committed; only the payload bytes are ever removed, and
// only by the retire_payload command after its durable prerequisites.
//
// CommitID is the producing run's execution identity. Until the D2
// round-4 R4-MAJ-001 execution-identity seam lands (admission-persisted
// (session_id, generation, boot_seq, run_id), boot epoch from the
// BootEpochStore), the completion site supplies a per-completion
// correlation id here; it is a stand-in for run_id ONLY — boot_seq is
// deliberately absent from this tuple rather than invented.
type FinalDeliveryCommit struct {
	Generation      int    `json:"generation"`
	CommitID        string `json:"commit_id"`
	MessageID       string `json:"message_id"`
	Outcome         string `json:"outcome"`
	ParentSessionID string `json:"parent_session_id"`
	// PayloadHash identifies the exact stored upward envelope.
	PayloadHash string `json:"payload_hash"`
	// Payload is the exact upward message JSON. Removed from disk only by
	// payload retirement; ListPendingFinalDeliveries reports it nil once
	// the durable retirement marker exists.
	Payload []byte `json:"payload,omitempty"`
}

// ReplayID returns the deterministic `<child>:<generation>:final` id this
// commit must always carry — the replay-prevention identity retained even
// after payload retirement.
func (c *FinalDeliveryCommit) ReplayID(sessionID string) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d:final", sessionID, c.Generation)
}

// FinalDeliveryProgress holds the monotonic durable delivery facts (D2).
// Facts refer to one exact committed message and only ever flip
// false → true; missing facts mean "not yet observed", never "false claim".
// A publisher records successful receipts, not attempted calls.
type FinalDeliveryProgress struct {
	InboxAppended   bool `json:"inbox_appended,omitempty"`
	FramesPersisted bool `json:"frames_persisted,omitempty"`
	WakeRecorded    bool `json:"wake_recorded,omitempty"`
	AckObserved     bool `json:"ack_observed,omitempty"`
}

// Merge returns the union of p and n (n's true facts win — monotonic).
func (p FinalDeliveryProgress) Merge(n FinalDeliveryProgress) FinalDeliveryProgress {
	return FinalDeliveryProgress{
		InboxAppended:   p.InboxAppended || n.InboxAppended,
		FramesPersisted: p.FramesPersisted || n.FramesPersisted,
		WakeRecorded:    p.WakeRecorded || n.WakeRecorded,
		AckObserved:     p.AckObserved || n.AckObserved,
	}
}

// Equal reports whether two progress snapshots record the same facts.
func (p FinalDeliveryProgress) Equal(n FinalDeliveryProgress) bool {
	return p == n
}

// Regresses reports whether next turns any fact already recorded on p back
// to false. A false field is an explicit claim, not an omitted delta.
func (p FinalDeliveryProgress) Regresses(next FinalDeliveryProgress) bool {
	return (p.InboxAppended && !next.InboxAppended) ||
		(p.FramesPersisted && !next.FramesPersisted) ||
		(p.WakeRecorded && !next.WakeRecorded) ||
		(p.AckObserved && !next.AckObserved)
}

// Delivered reports whether the durable facts show this final fully
// delivered — D2's retirement precondition, read strictly: the parent inbox
// append is durable AND either its required frames AND wake are durably
// recorded, or the matching inbox id is durably acknowledged. A final whose
// frames fact is missing is NOT delivered, whatever the wake did — the
// publisher records a fact only on a real receipt, never on an assumption.
func (p FinalDeliveryProgress) Delivered() bool {
	return p.InboxAppended && ((p.FramesPersisted && p.WakeRecorded) || p.AckObserved)
}

// FinalDeliveryCommand is UpdateFinalDelivery's whole command surface
// (D2: "Its command is only advance_progress or retire_payload; it accepts
// no lifecycle record or replacement outcome/payload").
type FinalDeliveryCommand struct {
	// Advance records additional durable facts (idempotent, monotonic).
	Advance *FinalDeliveryProgress
	// Retire retires the committed payload bytes; legal only after the
	// durable delivery prerequisites (see ErrFinalDeliveryRetireNotEarned).
	Retire bool
}

// finalDeliveryUpdateEnvelope is the typed journal line UpdateFinalDelivery
// appends. It is NOT a LifecycleRecord: tail/List readers skip it, so
// delivery progress can never become a session state or reset a newer
// generation (D2).
type finalDeliveryUpdateEnvelope struct {
	Kind             string                `json:"kind"`
	SessionID        string                `json:"session_id"`
	Generation       int                   `json:"generation"`
	CommitID         string                `json:"commit_id"`
	DeliveryRevision int64                 `json:"delivery_revision"`
	DeliveryProgress FinalDeliveryProgress `json:"delivery_progress"`
	// PayloadRetired is set on the envelope that records the durable
	// retirement (and on every later envelope, which cannot exist —
	// retirement completes the summary).
	PayloadRetired bool `json:"payload_retired,omitempty"`
	// Protected retains the protected tuple (identity, hash, replay id —
	// never the payload bytes) on the durable retirement marker, so the
	// replay-prevention identity survives any later compaction of the
	// record line's payload bytes (D2).
	Protected *FinalDeliveryCommit `json:"protected,omitempty"`
}

// finalDeliveryState is the joined view of one committed final and every
// delivery envelope written for it.
type finalDeliveryState struct {
	commit   FinalDeliveryCommit
	progress FinalDeliveryProgress
	revision int64
	retired  bool
}

// lifecycleJournalLine is one decoded line of a session's lifecycle JSONL:
// exactly one of record/envelope is non-nil.
type lifecycleJournalLine struct {
	record   *LifecycleRecord
	envelope *finalDeliveryUpdateEnvelope
}

// decodeJournalLine decodes one JSONL line, discriminating record lines
// (no `kind` key) from final-delivery envelope lines. A line that parses
// as neither (a torn write) returns a nil line the caller skips — the same
// crash-safety posture tail() has always had.
func decodeJournalLine(line []byte) *lifecycleJournalLine {
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return nil
	}
	if probe.Kind == JournalKindFinalDeliveryUpdate {
		var env finalDeliveryUpdateEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			return nil
		}
		return &lifecycleJournalLine{envelope: &env}
	}
	var rec LifecycleRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return nil
	}
	return &lifecycleJournalLine{record: &rec}
}

// finalDeliveryKey identifies one committed final across the journal.
type finalDeliveryKey struct {
	generation int
	commitID   string
}

// joinFinalDelivery walks one session's journal IN ORDER and joins every
// committed final_delivery with its latest delivery envelope. An envelope
// with no matching committed terminal record — or a final_delivery on a
// record that is not terminal — is ErrFinalDeliveryOrphanEnvelope: it can
// never authorize publication or retirement (D2).
func joinFinalDelivery(sessionID string, lines []lifecycleJournalLine) (map[finalDeliveryKey]*finalDeliveryState, error) {
	joined := map[finalDeliveryKey]*finalDeliveryState{}
	seenEnvelope := map[finalDeliveryKey]bool{}
	for _, line := range lines {
		switch {
		case line.record != nil && line.record.FinalDelivery != nil:
			fd := line.record.FinalDelivery
			if !line.record.Terminal() {
				return nil, fmt.Errorf("%w: session %q generation %d commit %q carries final_delivery on non-terminal state %q",
					ErrFinalDeliveryOrphanEnvelope, sessionID, fd.Generation, fd.CommitID, line.record.State)
			}
			key := finalDeliveryKey{generation: fd.Generation, commitID: fd.CommitID}
			// The terminal record is the commit; a later same-key record line
			// cannot exist (terminal generations are immutable), but if a
			// corrupt journal ever produced one the identity must agree.
			if prev, ok := joined[key]; ok && prev.commit.PayloadHash != fd.PayloadHash {
				return nil, fmt.Errorf("%w: session %q generation %d commit %q has two commits with different payload hashes",
					ErrFinalDeliveryOrphanEnvelope, sessionID, fd.Generation, fd.CommitID)
			}
			joined[key] = &finalDeliveryState{commit: *fd}
		case line.envelope != nil:
			env := line.envelope
			if env.SessionID != sessionID {
				return nil, fmt.Errorf("%w: envelope names session %q inside session %q's journal",
					ErrFinalDeliveryOrphanEnvelope, env.SessionID, sessionID)
			}
			key := finalDeliveryKey{generation: env.Generation, commitID: env.CommitID}
			if _, committed := joined[key]; !committed {
				return nil, fmt.Errorf("%w: session %q generation %d commit %q has a delivery envelope with no committed final",
					ErrFinalDeliveryOrphanEnvelope, sessionID, env.Generation, env.CommitID)
			}
			seenEnvelope[key] = true
			st := joined[key]
			if env.DeliveryRevision <= st.revision {
				return nil, fmt.Errorf("%w: session %q generation %d commit %q envelope revision %d does not advance %d",
					ErrFinalDeliveryOrphanEnvelope, sessionID, env.Generation, env.CommitID, env.DeliveryRevision, st.revision)
			}
			// The protected tuple may ride ONLY on a durable retirement
			// marker, and then only as a restatement of the identity already
			// on the committed record — never as a mutation of it. Any drift
			// between marker and commit is a visible consistency error that
			// can never authorize publication or retirement (D2).
			if env.Protected != nil {
				p := env.Protected
				if !env.PayloadRetired {
					return nil, fmt.Errorf("%w: session %q generation %d commit %q carries a protected tuple on a non-retirement envelope",
						ErrFinalDeliveryOrphanEnvelope, sessionID, env.Generation, env.CommitID)
				}
				if p.Generation != st.commit.Generation || p.CommitID != st.commit.CommitID ||
					p.MessageID != st.commit.MessageID || p.Outcome != st.commit.Outcome ||
					p.ParentSessionID != st.commit.ParentSessionID || p.PayloadHash != st.commit.PayloadHash {
					return nil, fmt.Errorf("%w: session %q generation %d commit %q retirement marker identity does not match the committed tuple",
						ErrFinalDeliveryOrphanEnvelope, sessionID, env.Generation, env.CommitID)
				}
			}
			st.revision = env.DeliveryRevision
			st.progress = env.DeliveryProgress
			st.retired = st.retired || env.PayloadRetired
		}
	}
	_ = seenEnvelope
	return joined, nil
}

// PendingFinalDelivery is one committed final and its joined delivery
// state, as reported by ListPendingFinalDeliveries.
type PendingFinalDelivery struct {
	SessionID  string
	Generation int
	Commit     FinalDeliveryCommit
	Progress   FinalDeliveryProgress
	Revision   int64
	Retired    bool
}

// Pending reports whether this final still needs delivery work: not
// retired, and its durable facts do not yet show it delivered. A retired
// final is never pending; its replay id remains the duplicate guard.
func (p PendingFinalDelivery) Pending() bool {
	return !p.Retired && !p.Progress.Delivered()
}

// UpdateFinalDelivery appends a delivery-only journal envelope for one
// committed final (D2 round-4 R4-MAJ-002). It is the ONLY legal writer of
// post-terminal delivery metadata: no lifecycle record is read-modified or
// rewritten, no outcome/payload/identity can change, and the per-session
// lifecycle lock serialises publishers so the revision CAS is exact.
//
// expectedRevision must equal the journal's current delivery revision for
// this commit (0 when no envelope exists yet). An identical already-recorded
// effect is an idempotent no-op. Retire is legal only once the durable
// facts show the delivery earned it; the envelope then carries the
// protected tuple (identity + hash + replay id, never the payload bytes)
// as the durable retirement marker.
func (s *LifecycleStore) UpdateFinalDelivery(sessionID string, generation int, commitID string, expectedRevision int64, cmd FinalDeliveryCommand) error {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return err
	}
	if commitID == "" {
		return fmt.Errorf("session: lifecycle: final delivery: commit_id is required")
	}
	if cmd.Advance == nil && !cmd.Retire {
		return fmt.Errorf("session: lifecycle: final delivery: empty command (advance_progress or retire_payload required)")
	}
	if cmd.Advance != nil && cmd.Retire {
		return fmt.Errorf("session: lifecycle: final delivery: one command per update (advance_progress or retire_payload, never both)")
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	lines, err := s.readJournal(sessionID)
	if err != nil {
		return err
	}
	joined, err := joinFinalDelivery(sessionID, lines)
	if err != nil {
		return err
	}
	key := finalDeliveryKey{generation: generation, commitID: commitID}
	state, found := joined[key]
	if !found {
		return fmt.Errorf("%w: session %q generation %d commit %q", ErrFinalDeliveryUnknownCommit, sessionID, generation, commitID)
	}

	merged := state.progress
	if cmd.Advance != nil {
		merged = merged.Merge(*cmd.Advance)
	}
	if state.retired {
		// Retirement completes the delivery summary. A repeat retire does
		// not append a second marker; if a crash left the payload bytes
		// after the marker, this retry finishes compaction instead of
		// reporting success while those bytes remain.
		if cmd.Retire && merged.Equal(state.progress) {
			return s.compactRetiredPayloadLocked(sessionID, generation, commitID)
		}
		return fmt.Errorf("%w: session %q generation %d commit %q", ErrFinalDeliveryRetired, sessionID, generation, commitID)
	}
	if cmd.Advance != nil && state.progress.Regresses(*cmd.Advance) {
		// Stale CAS stays a revision conflict. A matching revision with an
		// explicit true->false fact is a regression and must not append.
		if expectedRevision != state.revision {
			return fmt.Errorf("%w: session %q generation %d commit %q expected revision %d, journal at %d",
				ErrFinalDeliveryRevisionConflict, sessionID, generation, commitID, expectedRevision, state.revision)
		}
		return fmt.Errorf("%w: session %q generation %d commit %q", ErrFinalDeliveryProgressRegression, sessionID, generation, commitID)
	}
	if cmd.Retire && !merged.Delivered() {
		return fmt.Errorf("%w: session %q generation %d commit %q progress %+v", ErrFinalDeliveryRetireNotEarned, sessionID, generation, commitID, merged)
	}
	if merged.Equal(state.progress) && !cmd.Retire {
		// Identical already-recorded effect: idempotent, no envelope.
		return nil
	}
	if expectedRevision != state.revision {
		return fmt.Errorf("%w: session %q generation %d commit %q expected revision %d, journal at %d",
			ErrFinalDeliveryRevisionConflict, sessionID, generation, commitID, expectedRevision, state.revision)
	}

	env := &finalDeliveryUpdateEnvelope{
		Kind:             JournalKindFinalDeliveryUpdate,
		SessionID:        sessionID,
		Generation:       generation,
		CommitID:         commitID,
		DeliveryRevision: state.revision + 1,
		DeliveryProgress: merged,
		PayloadRetired:   cmd.Retire,
	}
	if cmd.Retire {
		identity := state.commit
		identity.Payload = nil
		env.Protected = &identity
	}
	if err := s.appendJournalEnvelope(sessionID, env); err != nil {
		return err
	}
	if !cmd.Retire {
		return nil
	}
	// Marker first. Compaction failure stays visible: the marker is
	// already durable, the payload bytes are harmless until a retry.
	return s.compactRetiredPayloadLocked(sessionID, generation, commitID)
}

// FinalDeliveryState reads one committed final's joined delivery state —
// the publisher's CAS input (current progress + revision) and its
// retired guard. Unknown identities return ErrFinalDeliveryUnknownCommit.
func (s *LifecycleStore) FinalDeliveryState(sessionID string, generation int, commitID string) (progress FinalDeliveryProgress, revision int64, retired bool, err error) {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return FinalDeliveryProgress{}, 0, false, err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	lines, err := s.readJournal(sessionID)
	if err != nil {
		return FinalDeliveryProgress{}, 0, false, err
	}
	joined, err := joinFinalDelivery(sessionID, lines)
	if err != nil {
		return FinalDeliveryProgress{}, 0, false, err
	}
	state, found := joined[finalDeliveryKey{generation: generation, commitID: commitID}]
	if !found {
		return FinalDeliveryProgress{}, 0, false, fmt.Errorf("%w: session %q generation %d commit %q", ErrFinalDeliveryUnknownCommit, sessionID, generation, commitID)
	}
	return state.progress, state.revision, state.retired, nil
}

// ListPendingFinalDeliveries scans every session's journal — every
// generation, not just each tail (D2 round-4 R4-MAJ-002) — and returns
// every committed final joined with its latest delivery state. Explicit
// RESUME of a committed done/failed G may have created G+1: G's committed
// final stays discoverable here independently of the current execution,
// and callers use Pending() to find the ones still needing delivery work.
// Payload bytes are reported nil once retired.
//
// One orphan/corrupt envelope fails the whole scan visibly
// (ErrFinalDeliveryOrphanEnvelope) — a consistency error can never be
// skipped past silently.
func (s *LifecycleStore) ListPendingFinalDeliveries() ([]PendingFinalDelivery, error) {
	ids, err := s.scanSessionIDs()
	if err != nil {
		return nil, err
	}
	out := make([]PendingFinalDelivery, 0, len(ids))
	for _, id := range ids {
		mu := s.Lock(id)
		mu.Lock()
		lines, err := s.readJournal(id)
		if err != nil {
			mu.Unlock()
			return nil, err
		}
		joined, err := joinFinalDelivery(id, lines)
		mu.Unlock()
		if err != nil {
			return nil, err
		}
		for key, state := range joined {
			item := PendingFinalDelivery{
				SessionID:  id,
				Generation: key.generation,
				Commit:     state.commit,
				Progress:   state.progress,
				Revision:   state.revision,
				Retired:    state.retired,
			}
			if state.retired {
				item.Commit.Payload = nil
			}
			out = append(out, item)
		}
	}
	return out, nil
}

// readJournal reads EVERY parseable line of sessionID's lifecycle JSONL in
// append order, discriminating record lines from final-delivery envelope
// lines. A torn trailing line is skipped with a fall-back to the lines
// before it — the same crash-safety posture tail() applies to records.
// The caller MUST hold Lock(sessionID) (the journal is append-only, but a
// concurrent compaction or prune must not race the read).
func (s *LifecycleStore) readJournal(sessionID string) ([]lifecycleJournalLine, error) {
	f, err := os.Open(s.path(sessionID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: lifecycle: open journal %q: %w", sessionID, err)
	}
	defer f.Close()

	var lines []lifecycleJournalLine
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		if line := decodeJournalLine([]byte(raw)); line != nil {
			lines = append(lines, *line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("session: lifecycle: scan journal %q: %w", sessionID, err)
	}
	return lines, nil
}

// appendJournalEnvelope appends one typed delivery envelope line to the
// session's journal. The caller MUST hold Lock(sessionID): the revision
// CAS in UpdateFinalDelivery is only exact because every envelope append
// happens under the same per-session lock every other writer uses.
func (s *LifecycleStore) appendJournalEnvelope(sessionID string, env *finalDeliveryUpdateEnvelope) error {
	return fileutil.AppendJSONL(s.path(sessionID), env)
}

// compactRetiredPayloadLocked removes one retired final's payload bytes and
// its redundant pre-retirement delivery envelopes from the session journal.
// The caller MUST hold Lock(sessionID). It refuses to rewrite when the
// durable retirement marker is absent. A no-op (bytes already gone) returns
// nil without writing. A write failure is returned; the previous journal,
// marker included, stays in place for a retry.
func (s *LifecycleStore) compactRetiredPayloadLocked(sessionID string, generation int, commitID string) error {
	path := s.path(sessionID)
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("session: lifecycle: final delivery: read journal for compaction %q: %w", sessionID, err)
	}
	lines := splitJournalLines(raw)
	if !journalHasRetirementMarker(lines, generation, commitID) {
		return fmt.Errorf("session: lifecycle: final delivery: compaction refused for session %q generation %d commit %q: retirement marker is not in the journal", sessionID, generation, commitID)
	}
	var buf bytes.Buffer
	changed := false
	for _, line := range lines {
		next, drop, lineChanged, lineErr := compactRetiredFinalLine(line, generation, commitID)
		if lineErr != nil {
			return fmt.Errorf("session: lifecycle: final delivery: compact session %q generation %d commit %q: %w", sessionID, generation, commitID, lineErr)
		}
		if drop {
			changed = true
			continue
		}
		if lineChanged {
			changed = true
		}
		buf.Write(next)
		buf.WriteByte('\n')
	}
	if !changed {
		return nil
	}
	if err := fileutil.WriteFileAtomicSyncDir(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("session: lifecycle: final delivery: atomic compaction for session %q generation %d commit %q: %w", sessionID, generation, commitID, err)
	}
	return nil
}

func splitJournalLines(raw []byte) [][]byte {
	if len(raw) == 0 {
		return nil
	}
	parts := bytes.Split(raw, []byte("\n"))
	out := make([][]byte, 0, len(parts))
	for _, part := range parts {
		if len(bytes.TrimSpace(part)) == 0 {
			continue
		}
		out = append(out, part)
	}
	return out
}

func journalHasRetirementMarker(lines [][]byte, generation int, commitID string) bool {
	for _, line := range lines {
		decoded := decodeJournalLine(bytes.TrimSpace(line))
		if decoded == nil || decoded.envelope == nil {
			continue
		}
		env := decoded.envelope
		if env.PayloadRetired && env.Generation == generation && env.CommitID == commitID {
			return true
		}
	}
	return false
}

// compactRetiredFinalLine strips this final's payload from its commit record
// and drops a pre-retirement delivery envelope for the same commit. Every
// other line, including the retirement marker and other generations, is kept.
func compactRetiredFinalLine(line []byte, generation int, commitID string) (next []byte, drop bool, changed bool, err error) {
	trimmed := bytes.TrimSpace(line)
	decoded := decodeJournalLine(trimmed)
	if decoded == nil {
		return trimmed, false, false, nil
	}
	if rec := decoded.record; rec != nil && rec.FinalDelivery != nil {
		fd := rec.FinalDelivery
		if fd.Generation == generation && fd.CommitID == commitID && len(fd.Payload) > 0 {
			fd.Payload = nil
			encoded, mErr := json.Marshal(rec)
			if mErr != nil {
				return nil, false, false, mErr
			}
			return encoded, false, true, nil
		}
	}
	if env := decoded.envelope; env != nil && env.Generation == generation && env.CommitID == commitID && !env.PayloadRetired {
		return nil, true, true, nil
	}
	return trimmed, false, false, nil
}
