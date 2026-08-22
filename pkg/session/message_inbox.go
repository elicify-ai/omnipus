// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-053 §Contract Surface (S3) — the durable child->parent SessionMessage
// inbox. Reuses the SAME generated.SessionMessage discriminated union used
// everywhere a SessionMessage crosses a boundary (DoD-11 — never a second,
// narrower message shape here): every persisted line either wraps one raw
// generated.SessionMessage or an ack record referencing prior message_ids by
// id, so a read-back never has to reconcile a disk-only shape against the
// wire shape.
//
// Keying (D16): the inbox is durable, keyed to the PARENT's durable
// chat/plan id ("owner key") — NOT to any one child — so a parent Stop/Play
// never strands a child's undelivered question. One JSONL file per owner
// key: <dir>/<owner_key>.jsonl, append-only (message entries and ack
// entries interleaved in arrival order; acks are folded over messages on
// read, mirroring pkg/memory/jsonl.go's skip-offset convention rather than
// rewriting history).
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// Default caps (ADR-053 §Contract Surface "Caps", FR-195's 21 config keys).
// Exposed as MessageInboxStore fields with these defaults so a caller wiring
// the real session_messaging config section (another wave, config is
// outside this wave's write-set) can override them without touching this
// package's source.
const (
	DefaultChildSendRatePerMinute = 10             // session_messaging.child_send_rate
	DefaultChildSendBodyBytes     = 32 * 1024      // session_messaging.child_send_body
	DefaultChildSendMaxDepth      = 5              // session_messaging.child_send_depth
	DefaultInboxUnackedMax        = 200            // session_messaging.inbox_unacked_max
	DefaultInboxPerTypeCeiling    = 20             // session_messaging.inbox_per_type_ceiling (D15)
	DefaultSteerRatePerMinute     = 6              // session_messaging.steer_rate
	DefaultSteerBodyBytes         = 16 * 1024      // session_messaging.steer_body
	DefaultNeedsInputTTL          = 24 * time.Hour // session_messaging.needs_input_ttl (INV-5/G-6)
	DefaultNeedsInputEscalationT1 = 12 * time.Hour // half-TTL escalation point (T1, G-6)

	// DefaultInboxAckedRetentionMax bounds how many ACKED message entries a
	// compaction pass (compactLocked) keeps per owner key — the newest this
	// many, by append order. Every UNACKED message entry is retained
	// unconditionally regardless of this cap (compaction never drops
	// safety-critical open state). 200 mirrors DefaultInboxUnackedMax's own
	// order of magnitude: enough acked history to satisfy a child re-reading
	// its own recent traffic without keeping the acked tail forever.
	DefaultInboxAckedRetentionMax = 200 // session_messaging.inbox_acked_retention_max

	// DefaultInboxCompactionAckedTrigger is the acked-message-entry count
	// (per owner key) that opportunistically triggers a compaction pass on
	// the next Append/Ack. Deliberately DOUBLE DefaultInboxAckedRetentionMax
	// so compaction has hysteresis: once triggered, it drops back down to
	// the retention cap, and the NEXT trigger only fires after another full
	// retention-cap's worth of acks accumulates — never on every single
	// call once the file happens to sit exactly at the cap.
	DefaultInboxCompactionAckedTrigger = 400 // session_messaging.inbox_compaction_trigger
)

// Sentinel errors — every one of these MUST be surfaced to the calling child
// as a tool error (never-silent-drop, FR-125). They are deliberately
// distinguishable via errors.Is so pkg/tools/message_parent.go can render a
// specific, actionable message per failure mode.
var (
	ErrInboxEmptyOwnerKey   = errors.New("session: inbox: owner key must not be empty")
	ErrInboxBadMessage      = errors.New("session: inbox: message envelope is malformed")
	ErrInboxBodyTooLarge    = errors.New("session: inbox: message body exceeds the child-send cap")
	ErrInboxDepthExceeded   = errors.New("session: inbox: message hop depth exceeds the cap")
	ErrInboxRateLimited     = errors.New("session: inbox: child send rate exceeded")
	ErrInboxPerChildCeiling = errors.New("session: inbox: per-child unacked question+blocker ceiling reached — await answers")
	ErrInboxSessionFull     = errors.New("session: inbox: session unacked-message cap reached")
)

// InboxEntryKind discriminates a persisted inbox JSONL line.
type InboxEntryKind string

const (
	InboxEntryMessage InboxEntryKind = "message"
	InboxEntryAck     InboxEntryKind = "ack"
)

// InboxEntry is one line of an owner key's durable inbox file.
//
// not-wire-format: internal disk record. Message, when present, is the
// canonical generated.SessionMessage union — never re-shaped.
type InboxEntry struct {
	Kind InboxEntryKind `json:"kind"`

	// Seq is a per-owner-key, strictly-increasing, PERSISTED sequence number
	// assigned at write time (never renumbered). It exists so Drain's
	// pagination cursor survives compactLocked's rewrite: before compaction
	// existed, the cursor was simply the entry's position in the array
	// readEntries returned — safe only because entries were pure
	// append-only, so position was already a stable, monotonic ordering key.
	// Compaction breaks that assumption by physically dropping old acked
	// entries, which shifts every later entry's array POSITION — a
	// position-based cursor issued before a compaction would silently
	// resume scanning from the wrong logical point after one runs (either
	// skipping never-delivered messages or redelivering already-drained
	// ones). Seq is immune: it is written once, carried forward verbatim by
	// every compaction rewrite (see compactLocked), and never depends on the
	// entry's current position in the file.
	//
	// Zero means "predates this field" (real Seq values always start at 1) —
	// readEntries backfills a zero Seq to the entry's 1-based position
	// (backfillSeq), which is value-identical to the OLD position-based
	// cursor scheme for any file that has never been compacted, and becomes
	// permanent the first time compaction persists it.
	Seq       int64                     `json:"seq,omitempty"`
	Message   *generated.SessionMessage `json:"message,omitempty"`
	AckedIDs  []string                  `json:"acked_message_ids,omitempty"`
	CreatedAt time.Time                 `json:"created_at"`
}

// envelopePeek extracts the common SessionMessage envelope fields without
// needing to switch on the variant kind first — every SessionMessage
// variant flattens its envelope fields inline (ADR-034 precedent), so a
// plain json.Unmarshal of the marshaled union onto this shape always
// succeeds regardless of which of the 12 kinds it is.
type envelopePeek struct {
	MessageID       string  `json:"message_id"`
	SessionID       string  `json:"session_id"`
	ParentSessionID *string `json:"parent_session_id"`
	Kind            string  `json:"kind"`
	Direction       string  `json:"direction"`
	Depth           int     `json:"depth"`
	CorrelationID   string  `json:"correlation_id"`
}

// messageInboxPeekEnvelopeCalls counts peekEnvelope invocations process-wide
// — a test-only observability seam (Perf-HIGH-1) proving Append's combined
// dedupe+open-count pass costs exactly one peekEnvelope (Marshal+Unmarshal)
// call per stored message entry, not the two-separate-full-scans cost this
// fix replaced (a regression back to two loops would double the observed
// count). The atomic increment itself is unconditional and cheap (a single
// atomic add), so this is never gated behind a test-only code path — it is
// always live, exactly like this package's other production-seam-doubling-
// as-test-hook counters.
var messageInboxPeekEnvelopeCalls atomic.Int64

func peekEnvelope(msg generated.SessionMessage) (envelopePeek, []byte, error) {
	messageInboxPeekEnvelopeCalls.Add(1)
	data, err := msg.MarshalJSON()
	if err != nil {
		return envelopePeek{}, nil, fmt.Errorf("%w: %w", ErrInboxBadMessage, err)
	}
	var p envelopePeek
	if err := json.Unmarshal(data, &p); err != nil {
		return envelopePeek{}, nil, fmt.Errorf("%w: %w", ErrInboxBadMessage, err)
	}
	if p.MessageID == "" || p.SessionID == "" || p.Kind == "" {
		return envelopePeek{}, nil, fmt.Errorf("%w: message_id/session_id/kind required", ErrInboxBadMessage)
	}
	return p, data, nil
}

// questionOrBlockerKind reports whether kind counts toward the D15 per-child
// unacked ceiling ("20 open question+blocker per child" — literal per the
// ADR/spec wording; decision_request is deliberately NOT counted here,
// matching the spec's literal "question+blocker" phrasing).
func questionOrBlockerKind(kind string) bool {
	return kind == "question" || kind == "blocker"
}

// AppendResult reports the outcome of a successful (non-error) Append.
type AppendResult struct {
	Accepted  bool
	Deduped   bool
	MessageID string
}

// MessageInboxStore manages the durable child->parent SessionMessage inbox
// under a single directory, one JSONL file per owner key (D16). All
// read-modify-write paths are serialized by the store's own 64-shard
// striped lock keyed by owner key.
type MessageInboxStore struct {
	dir  string
	lock *lifecycleStripedLock

	// Caps — overridable by the caller wiring the real config (session_messaging
	// section, FR-195); default to the ADR §Contract Surface values.
	ChildSendRatePerMinute int
	ChildSendBodyBytes     int
	ChildSendMaxDepth      int
	InboxUnackedMax        int
	InboxPerTypeCeiling    int

	// AckedRetentionMax / CompactionAckedTrigger — the D-compaction knobs
	// (see the Default* consts above for the exact hysteresis reasoning).
	AckedRetentionMax      int
	CompactionAckedTrigger int

	// now is overridable for deterministic (fake-clock) tests.
	now func() time.Time

	rateMu      sync.Mutex
	rateWindows map[string][]time.Time // key = ownerKey + "\x00" + childSessionID
}

// rateWindowSweepThreshold bounds rateWindows' key count before an
// opportunistic full-map sweep removes fully-quiescent keys (LOW finding:
// every distinct childSessionID that ever sent even one message left a
// permanent map entry, since rateAllow only ever prunes/rewrites the ONE key
// it was called for — a key belonging to a child that never sends again is
// never revisited by any future call, so it survives forever holding a
// handful of now-expired timestamps). Once the map exceeds this many keys,
// the next rateAllow call also sweeps every OTHER key, pruning expired
// timestamps and deleting any key whose window is now fully empty. A `var`
// (not `const`) so an in-package test can lower it to observe the sweep
// without creating hundreds of real distinct child sessions.
var rateWindowSweepThreshold = 512

// pruneWindow returns window with every timestamp at or before cutoff
// removed, reusing window's backing array (same convention rateAllow always
// used).
func pruneWindow(window []time.Time, cutoff time.Time) []time.Time {
	kept := window[:0]
	for _, t := range window {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

// sweepQuiescentWindows prunes every window in windows against cutoff and
// deletes any key whose pruned window is now empty — the actual fix for the
// "never delete quiescent keys" leak, since a single key's own rateAllow
// call can only ever observe or clean up ITSELF.
func sweepQuiescentWindows(windows map[string][]time.Time, cutoff time.Time) {
	for k, w := range windows {
		kept := pruneWindow(w, cutoff)
		if len(kept) == 0 {
			delete(windows, k)
		} else {
			windows[k] = kept
		}
	}
}

// NewMessageInboxStore creates a MessageInboxStore rooted at dir with the
// default caps. By convention dir is
// "<OMNIPUS_HOME>/session_messages" — the caller wiring this store into the
// gateway/agent-loop boot sequence (outside this wave's write-set) should
// use that path so every consumer agrees on its location.
func NewMessageInboxStore(dir string) *MessageInboxStore {
	return &MessageInboxStore{
		dir:                    dir,
		lock:                   &lifecycleStripedLock{},
		ChildSendRatePerMinute: DefaultChildSendRatePerMinute,
		ChildSendBodyBytes:     DefaultChildSendBodyBytes,
		ChildSendMaxDepth:      DefaultChildSendMaxDepth,
		InboxUnackedMax:        DefaultInboxUnackedMax,
		InboxPerTypeCeiling:    DefaultInboxPerTypeCeiling,
		AckedRetentionMax:      DefaultInboxAckedRetentionMax,
		CompactionAckedTrigger: DefaultInboxCompactionAckedTrigger,
		now:                    time.Now,
		rateWindows:            make(map[string][]time.Time),
	}
}

// retentionMax returns the effective acked-retention cap, falling back to
// the default for an unset/invalid override (same convention every other
// cap field in this store already uses).
func (s *MessageInboxStore) retentionMax() int {
	if s.AckedRetentionMax > 0 {
		return s.AckedRetentionMax
	}
	return DefaultInboxAckedRetentionMax
}

// compactionTrigger returns the effective compaction trigger threshold,
// falling back to the default for an unset/invalid override.
func (s *MessageInboxStore) compactionTrigger() int {
	if s.CompactionAckedTrigger > 0 {
		return s.CompactionAckedTrigger
	}
	return DefaultInboxCompactionAckedTrigger
}

// SetClock overrides the store's time source for deterministic (fake-clock)
// tests of the rate cap (D15 message-caps dataset). Never call this outside
// tests.
func (s *MessageInboxStore) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

func (s *MessageInboxStore) path(ownerKey string) string {
	return filepath.Join(s.dir, sanitizeOwnerKey(ownerKey)+".jsonl")
}

// sanitizeOwnerKey mirrors validateLifecycleSessionID's path-traversal guard
// but additionally maps unsafe filename characters (an owner key MAY be a
// chat_id/plan_id containing ':' or other channel-specific separators, D16)
// to '_' rather than rejecting them outright — a durable inbox key is
// caller-supplied routing data, not a strict entity id.
func sanitizeOwnerKey(key string) string {
	key = strings.TrimSpace(key)
	replacer := strings.NewReplacer("/", "_", "\\", "_", "..", "__", "\x00", "_")
	return replacer.Replace(key)
}

func (s *MessageInboxStore) readEntries(ownerKey string) ([]InboxEntry, error) {
	f, err := os.Open(s.path(ownerKey))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: inbox: open %q: %w", ownerKey, err)
	}
	defer f.Close()

	var entries []InboxEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e InboxEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // skip a torn/corrupt line, mirroring lifecycle.go's tail()
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("session: inbox: scan %q: %w", ownerKey, err)
	}
	backfillSeq(entries)
	return entries, nil
}

// backfillSeq assigns an implied Seq (== 1-based append-order position) to
// any entry that predates the Seq field (Seq == 0 on disk — a real,
// persisted Seq is never 0). The backfilled value is exactly what the OLD
// position-based Drain cursor implicitly assumed, so this is a
// value-preserving upgrade for a file that has never been compacted, not a
// behavior change. compactLocked persists whatever Seq value is in memory
// at rewrite time, so a legacy entry's backfilled value becomes durable —
// and immune to any FUTURE position shift — the first time compaction
// touches its file. Mutates entries in place.
func backfillSeq(entries []InboxEntry) {
	for i := range entries {
		if entries[i].Seq == 0 {
			entries[i].Seq = int64(i + 1)
		}
	}
}

// nextSeqAfter returns the next Seq value to assign given the CURRENT
// (already Seq-backfilled) entries for an owner key. Entries are always in
// ascending Seq order — real persisted Seq values are assigned only via this
// function (strictly increasing) and backfilled ones exactly match file
// order — so the last entry always carries the highest Seq; this is O(1),
// never a scan.
func nextSeqAfter(entries []InboxEntry) int64 {
	if len(entries) == 0 {
		return 1
	}
	return entries[len(entries)-1].Seq + 1
}

// foldAcked returns the set of message_ids acknowledged anywhere in entries.
func foldAcked(entries []InboxEntry) map[string]bool {
	acked := make(map[string]bool)
	for _, e := range entries {
		if e.Kind == InboxEntryAck {
			for _, id := range e.AckedIDs {
				acked[id] = true
			}
		}
	}
	return acked
}

// rateAllow enforces the child-send rate cap (10/min default) using an
// in-memory sliding window keyed by (ownerKey, childSessionID). Deliberately
// in-memory (not replay-derived from the JSONL file) so the D15 fake-clock
// test dataset can drive it directly via SetClock without needing to seed
// file content.
func (s *MessageInboxStore) rateAllow(ownerKey, childSessionID string) bool {
	key := ownerKey + "\x00" + childSessionID
	now := s.now()
	cutoff := now.Add(-1 * time.Minute)

	s.rateMu.Lock()
	defer s.rateMu.Unlock()

	kept := pruneWindow(s.rateWindows[key], cutoff)
	limit := s.ChildSendRatePerMinute
	if limit <= 0 {
		limit = DefaultChildSendRatePerMinute
	}
	if len(kept) >= limit {
		if len(kept) == 0 {
			// Defensive: unreachable while limit stays > 0 (its only ever
			// path here), but a future limit<=0 override should not leave a
			// zero-length slice permanently keyed in the map either.
			delete(s.rateWindows, key)
		} else {
			s.rateWindows[key] = kept
		}
		return false
	}
	s.rateWindows[key] = append(kept, now)

	// LOW finding: sweep OTHER quiescent keys once the map has grown past
	// the threshold — see rateWindowSweepThreshold's doc comment. Amortized:
	// runs only every time the map crosses the threshold again after a
	// sweep brings it back down, not on every call.
	if len(s.rateWindows) > rateWindowSweepThreshold {
		sweepQuiescentWindows(s.rateWindows, cutoff)
	}
	return true
}

// Append validates and persists msg into ownerKey's durable inbox,
// enforcing (in order): well-formed envelope, depth cap, body cap, dedupe
// by message_id (idempotent no-op, not an error), the D15 per-child
// question+blocker unacked ceiling, the per-child inbox-wide unacked cap,
// and the child-send rate cap. A non-nil error is ALWAYS one of this file's
// sentinel Err* values (wrapped) — never-silent-drop (FR-125): the caller
// (pkg/tools/message_parent.go) turns it into a tool error the child sees.
func (s *MessageInboxStore) Append(ownerKey string, msg generated.SessionMessage) (*AppendResult, error) {
	if strings.TrimSpace(ownerKey) == "" {
		return nil, ErrInboxEmptyOwnerKey
	}
	peek, raw, err := peekEnvelope(msg)
	if err != nil {
		return nil, err
	}
	maxDepth := s.ChildSendMaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultChildSendMaxDepth
	}
	if peek.Depth > maxDepth {
		return nil, fmt.Errorf("%w: depth %d exceeds cap %d", ErrInboxDepthExceeded, peek.Depth, maxDepth)
	}
	bodyCap := s.ChildSendBodyBytes
	if bodyCap <= 0 {
		bodyCap = DefaultChildSendBodyBytes
	}
	if len(raw) > bodyCap {
		return nil, fmt.Errorf("%w: %d bytes exceeds cap %d", ErrInboxBodyTooLarge, len(raw), bodyCap)
	}

	mu := s.lock.Get(ownerKey)
	mu.Lock()
	defer mu.Unlock()

	entries, err := s.readEntries(ownerKey)
	if err != nil {
		return nil, err
	}

	// Correctness/Perf-HIGH-1: a SINGLE combined pass replaces what used to
	// be two separate full scans (a dedupe loop, then an open-count loop),
	// each calling peekEnvelope — a Marshal+Unmarshal — once per stored
	// message entry. At 9k entries that was ~2 extra codec round-trips per
	// message per Append call (quadratic over the chat's lifetime); this
	// pass pays exactly one peekEnvelope per message entry, and picks up
	// ackedMsgTotal (needed below to decide whether to opportunistically
	// compact) for free from the same acked-ness check the open-count
	// branch already had to make.
	acked := foldAcked(entries)
	openTypeCount := 0
	openTotalCount := 0
	ackedMsgTotal := 0
	deduped := false
	for _, e := range entries {
		if e.Kind != InboxEntryMessage || e.Message == nil {
			continue
		}
		p2, _, perr := peekEnvelope(*e.Message)
		if perr != nil {
			continue
		}
		if p2.MessageID == peek.MessageID {
			// message_id is unique by construction — no need to keep
			// scanning once the duplicate is found.
			deduped = true
			break
		}
		if acked[p2.MessageID] {
			ackedMsgTotal++
			continue
		}
		if p2.SessionID != peek.SessionID {
			continue
		}
		openTotalCount++
		if questionOrBlockerKind(p2.Kind) {
			openTypeCount++
		}
	}
	if deduped {
		return &AppendResult{Accepted: true, Deduped: true, MessageID: peek.MessageID}, nil
	}

	if questionOrBlockerKind(peek.Kind) {
		ceiling := s.InboxPerTypeCeiling
		if ceiling <= 0 {
			ceiling = DefaultInboxPerTypeCeiling
		}
		if openTypeCount >= ceiling {
			return nil, fmt.Errorf("%w (%d/%d for session %s)", ErrInboxPerChildCeiling, openTypeCount, ceiling, peek.SessionID)
		}
	}
	unackedMax := s.InboxUnackedMax
	if unackedMax <= 0 {
		unackedMax = DefaultInboxUnackedMax
	}
	if openTotalCount >= unackedMax {
		return nil, fmt.Errorf("%w (%d/%d for session %s)", ErrInboxSessionFull, openTotalCount, unackedMax, peek.SessionID)
	}

	if !s.rateAllow(ownerKey, peek.SessionID) {
		limit := s.ChildSendRatePerMinute
		if limit <= 0 {
			limit = DefaultChildSendRatePerMinute
		}
		return nil, fmt.Errorf("%w (%d/min for session %s)", ErrInboxRateLimited, limit, peek.SessionID)
	}

	entry := InboxEntry{Kind: InboxEntryMessage, Seq: nextSeqAfter(entries), Message: &msg, CreatedAt: s.now().UTC()}
	if err := fileutil.AppendJSONL(s.path(ownerKey), entry); err != nil {
		return nil, fmt.Errorf("session: inbox: persist: %w", err)
	}

	// D-compaction: opportunistically shrink the file once its acked-message
	// count crosses the trigger (see DefaultInboxCompactionAckedTrigger's
	// hysteresis reasoning). This new message is always unacked, so
	// ackedMsgTotal computed above is unaffected by it — no need to append
	// entry to entries before checking the trigger. A compaction failure
	// must never fail the Append itself: the message the caller actually
	// asked to send is already durably on disk; a failed opportunistic
	// compaction just means the file stays larger than ideal until the next
	// Append/Ack retries it.
	if ackedMsgTotal > s.compactionTrigger() {
		if _, cerr := s.compactLocked(ownerKey, append(entries, entry), acked); cerr != nil {
			slog.Warn("session: inbox: opportunistic compaction failed",
				"owner_key", ownerKey, "trigger", "append", "error", cerr)
		}
	}
	return &AppendResult{Accepted: true, MessageID: peek.MessageID}, nil
}

// AckResult reports which of the message_ids passed to AckDetailed actually
// correspond to a real message ever appended under the owner key
// ("Acknowledged") versus which do not ("Unknown") — M1 (2026-08 UAT): Ack's
// own `error`-only return cannot distinguish these, which is exactly why the
// delegate `inbox_ack` tool (pkg/tools/delegate.go's executeInboxAck) used to
// report `len(messageIDs)` as "Acknowledged N message(s)." even when some or
// all of those ids were wholly fabricated — a caller reconciling its inbox
// against that count would silently drift. Acknowledged/Unknown always
// partition messageIDs (every input id lands in exactly one of the two,
// duplicates included) so len(Acknowledged)+len(Unknown) == len(messageIDs).
type AckResult struct {
	// Acknowledged lists the requested message_ids that match a message
	// entry that was genuinely appended under this owner key at some point
	// (whether or not it was already acked before this call — re-acking an
	// already-acked real id is idempotent and still a genuine match).
	Acknowledged []string
	// Unknown lists requested message_ids that do not correspond to any
	// message ever appended under this owner key (a caller typo or a
	// fabricated id). Still recorded in the persisted ack entry below (the
	// audit-trail contract is unchanged — see the doc comment two lines
	// down), but callers MUST surface these rather than silently folding
	// them into a success count.
	Unknown []string
}

// Ack marks messageIDs as acknowledged for ownerKey (delegate.inbox_ack).
// Acked messages are excluded from future Drain results and from the
// per-child ceiling/inbox-cap counts, and — being an ordinary append —
// persist permanently in this same durable log (audit trail, FR-125).
//
// Ack's own underlying acknowledgement mechanics (which ids get folded into
// future Drain/UnackedCount reads) are UNCHANGED by AckDetailed below — both
// share ackDetailed's single code path — so this keeps its exact historical
// signature and behavior for existing callers (pkg/tools/delegate.go's
// DelegateInboxStore interface). Callers that need to report an accurate
// acknowledged count or surface unknown ids (M1) should call AckDetailed
// instead.
func (s *MessageInboxStore) Ack(ownerKey string, messageIDs []string) error {
	_, err := s.ackDetailed(ownerKey, messageIDs)
	return err
}

// AckDetailed is Ack's richer sibling (M1): it performs the EXACT SAME
// acknowledgement (same lock, same persisted InboxEntryAck record covering
// every requested id, same downstream Drain/UnackedCount effect for real
// ids) but additionally reports, via AckResult, which requested ids matched
// a real message under ownerKey and which did not — the information the
// delegate `inbox_ack` tool needs to report a truthful "Acknowledged N
// message(s)" count and surface unknown ids instead of silently absorbing
// them into that count.
func (s *MessageInboxStore) AckDetailed(ownerKey string, messageIDs []string) (*AckResult, error) {
	return s.ackDetailed(ownerKey, messageIDs)
}

// ackDetailed is the shared implementation backing both Ack and AckDetailed.
func (s *MessageInboxStore) ackDetailed(ownerKey string, messageIDs []string) (*AckResult, error) {
	if strings.TrimSpace(ownerKey) == "" {
		return nil, ErrInboxEmptyOwnerKey
	}
	if len(messageIDs) == 0 {
		return &AckResult{}, nil
	}
	mu := s.lock.Get(ownerKey)
	mu.Lock()
	defer mu.Unlock()

	// Determine which requested ids correspond to a message genuinely
	// appended under this owner key (across any child session — Ack has no
	// session_id parameter; the inbox is owner-keyed, not session-keyed).
	// This single pass ALSO tallies ackedMsgTotalBefore (messages already
	// acked prior to this call) so the compaction-trigger check below never
	// needs a second full scan.
	entries, err := s.readEntries(ownerKey)
	if err != nil {
		return nil, err
	}
	existingAcked := foldAcked(entries)
	known := make(map[string]bool, len(entries))
	ackedMsgTotal := 0
	for _, e := range entries {
		if e.Kind != InboxEntryMessage || e.Message == nil {
			continue
		}
		p, _, perr := peekEnvelope(*e.Message)
		if perr != nil {
			continue
		}
		known[p.MessageID] = true
		if existingAcked[p.MessageID] {
			ackedMsgTotal++
		}
	}
	result := &AckResult{}
	for _, id := range messageIDs {
		if known[id] {
			result.Acknowledged = append(result.Acknowledged, id)
			if !existingAcked[id] {
				ackedMsgTotal++ // newly acked by THIS call
			}
		} else {
			result.Unknown = append(result.Unknown, id)
		}
	}

	entry := InboxEntry{
		Kind:      InboxEntryAck,
		Seq:       nextSeqAfter(entries),
		AckedIDs:  append([]string(nil), messageIDs...),
		CreatedAt: s.now().UTC(),
	}
	if err := fileutil.AppendJSONL(s.path(ownerKey), entry); err != nil {
		return nil, fmt.Errorf("session: inbox: ack: %w", err)
	}

	// D-compaction: see Append's identical trigger for the hysteresis
	// reasoning. A compaction failure never fails the Ack itself — the ack
	// is already durably recorded.
	if ackedMsgTotal > s.compactionTrigger() {
		allEntries := append(entries, entry)
		if _, cerr := s.compactLocked(ownerKey, allEntries, foldAcked(allEntries)); cerr != nil {
			slog.Warn("session: inbox: opportunistic compaction failed",
				"owner_key", ownerKey, "trigger", "ack", "error", cerr)
		}
	}
	return result, nil
}

// compactLocked rewrites ownerKey's inbox file (HIGH finding: an
// append-only, never-compacted file grows forever since acks are themselves
// appended, never used to reclaim space). Every UNACKED message entry is
// kept untouched and unconditionally — this is safety-critical state
// (Drain/UnackedCount/the D15 per-child ceiling all depend on it being
// genuinely present, and FR-125 requires never-silent-drop). Among ACKED
// message entries, only the newest s.retentionMax() (by append order) are
// kept; older ones are the space this reclaims. Every original Ack entry is
// dropped and replaced by exactly one consolidated Ack entry covering the
// retained acked ids — an Ack entry that referenced only messages this
// rewrite is dropping (or that were always unknown/fabricated) is pure
// garbage after this rewrite and is not carried forward.
//
// Cursor-safety (see InboxEntry.Seq's doc comment): every retained entry's
// Seq is written to disk EXACTLY as it appears in the entries argument —
// never renumbered — so a Drain cursor issued before this compaction stays
// valid against the rewritten file. Callers MUST pass entries fresh from
// readEntries (Seq-backfilled), never a hand-built slice.
//
// Caller must hold ownerKey's striped lock. acked is the caller's own
// foldAcked(entries) — passed in rather than recomputed, since every caller
// already has it. Returns the new compacted entries slice on success.
func (s *MessageInboxStore) compactLocked(ownerKey string, entries []InboxEntry, acked map[string]bool) ([]InboxEntry, error) {
	retentionMax := s.retentionMax()

	// Pass 1: collect acked message ids in file order so the newest
	// retentionMax of them can be identified as a simple slice tail — no
	// sort needed, since entries (and therefore ackedIDsInOrder) are already
	// in ascending append order.
	var ackedIDsInOrder []string
	for _, e := range entries {
		if e.Kind != InboxEntryMessage || e.Message == nil {
			continue
		}
		p, _, perr := peekEnvelope(*e.Message)
		if perr != nil {
			continue // malformed historical entry — drop, matches readEntries' own tolerance
		}
		if acked[p.MessageID] {
			ackedIDsInOrder = append(ackedIDsInOrder, p.MessageID)
		}
	}
	cutoff := len(ackedIDsInOrder) - retentionMax
	if cutoff < 0 {
		cutoff = 0
	}
	keepAcked := make(map[string]bool, len(ackedIDsInOrder)-cutoff)
	for _, id := range ackedIDsInOrder[cutoff:] {
		keepAcked[id] = true
	}

	// Pass 2: rebuild the file in original order — every unacked message
	// entry untouched, every acked-and-retained message entry untouched
	// (including its original Seq), every acked-and-beyond-retention message
	// entry dropped, every Ack entry dropped (regenerated as one
	// consolidation below).
	compacted := make([]InboxEntry, 0, len(entries))
	var retainedAckedIDs []string
	for _, e := range entries {
		if e.Kind != InboxEntryMessage || e.Message == nil {
			continue
		}
		p, _, perr := peekEnvelope(*e.Message)
		if perr != nil {
			continue
		}
		if acked[p.MessageID] {
			if !keepAcked[p.MessageID] {
				continue // beyond retention — this is the space compaction reclaims
			}
			retainedAckedIDs = append(retainedAckedIDs, p.MessageID)
		}
		compacted = append(compacted, e)
	}
	if len(retainedAckedIDs) > 0 {
		compacted = append(compacted, InboxEntry{
			Kind:      InboxEntryAck,
			Seq:       nextSeqAfter(entries),
			AckedIDs:  retainedAckedIDs,
			CreatedAt: s.now().UTC(),
		})
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range compacted {
		if err := enc.Encode(e); err != nil {
			return nil, fmt.Errorf("session: inbox: compact: marshal entry: %w", err)
		}
	}
	if err := fileutil.WriteFileAtomic(s.path(ownerKey), buf.Bytes(), 0o600); err != nil {
		return nil, fmt.Errorf("session: inbox: compact: write %q: %w", ownerKey, err)
	}
	return compacted, nil
}

// Drain returns up to maxMessages unacked messages for childSessionID under
// ownerKey, oldest first, after the opaque sinceCursor (empty = from the
// start). nextCursor, when non-empty, is passed back on the next Drain call
// to continue where this one left off (delegate.inbox's since-cursor
// replay).
func (s *MessageInboxStore) Drain(ownerKey, childSessionID, sinceCursor string, maxMessages int) (msgs []generated.SessionMessage, nextCursor string, hasMore bool, err error) {
	if strings.TrimSpace(ownerKey) == "" {
		return nil, "", false, ErrInboxEmptyOwnerKey
	}
	if maxMessages <= 0 {
		maxMessages = DefaultInboxUnackedMax
	}

	mu := s.lock.Get(ownerKey)
	mu.Lock()
	entries, rerr := s.readEntries(ownerKey)
	mu.Unlock()
	if rerr != nil {
		return nil, "", false, rerr
	}

	acked := foldAcked(entries)
	var sinceSeq int64
	if sinceCursor != "" {
		if n, perr := strconv.ParseInt(sinceCursor, 10, 64); perr == nil {
			sinceSeq = n
		}
	}

	// Correctness-MAJOR-1 (drain cursor), extended for compaction-safety
	// (HIGH finding): the pagination cursor is the Seq of the last SCANNED
	// entry — NOT an output-count offset (sinceIdx + max), and, since
	// compactLocked below, NOT a raw array INDEX either. An output-count
	// cursor under-points because candidates skip acked/wrong-session/
	// non-message lines (the original Correctness-MAJOR-1 bug). A raw-index
	// cursor additionally breaks once compaction can physically remove old
	// acked entries: removing entries BEFORE a given logical position shifts
	// every later entry's array index, so a cursor recorded as an index
	// before a compaction would resume scanning from the wrong logical point
	// after one runs. Seq is immune to both: it is assigned once at write
	// time, carried forward verbatim through compaction (see
	// InboxEntry.Seq's doc comment and compactLocked), and entries are
	// always in ascending Seq order (backfillSeq/nextSeqAfter), so "resume
	// after Seq X" is well-defined regardless of what compaction has done to
	// the entries BEFORE X. candidateSeq[i] records the Seq that produced
	// candidates[i], letting the truncation cursor resume scanning
	// immediately AFTER the entry that yielded the last emitted message.
	var candidates []generated.SessionMessage
	var candidateSeq []int64
	lastScannedSeq := sinceSeq
	for _, e := range entries {
		if e.Seq <= sinceSeq {
			continue
		}
		lastScannedSeq = e.Seq
		if e.Kind != InboxEntryMessage || e.Message == nil {
			continue
		}
		p, _, perr := peekEnvelope(*e.Message)
		if perr != nil || (childSessionID != "" && p.SessionID != childSessionID) {
			continue
		}
		if acked[p.MessageID] {
			continue
		}
		candidates = append(candidates, *e.Message)
		candidateSeq = append(candidateSeq, e.Seq)
	}

	if len(candidates) > maxMessages {
		lastEmittedSeq := candidateSeq[maxMessages-1]
		return candidates[:maxMessages], strconv.FormatInt(lastEmittedSeq, 10), true, nil
	}
	// Exhausted — cursor points at the Seq of the final scanned entry so the
	// next Drain does not re-scan it. lastScannedSeq stays at sinceSeq when
	// the loop scans nothing new (an empty/fully-behind-cursor file), so the
	// cursor does not move backwards.
	return candidates, strconv.FormatInt(lastScannedSeq, 10), false, nil
}

// UnackedCount returns the current open question+blocker count for
// childSessionID under ownerKey — the value delegate.status surfaces
// against the D15 per-child ceiling.
func (s *MessageInboxStore) UnackedCount(ownerKey, childSessionID string) (int, error) {
	if strings.TrimSpace(ownerKey) == "" {
		return 0, ErrInboxEmptyOwnerKey
	}
	mu := s.lock.Get(ownerKey)
	mu.Lock()
	entries, err := s.readEntries(ownerKey)
	mu.Unlock()
	if err != nil {
		return 0, err
	}
	acked := foldAcked(entries)
	count := 0
	for _, e := range entries {
		if e.Kind != InboxEntryMessage || e.Message == nil {
			continue
		}
		p, _, perr := peekEnvelope(*e.Message)
		if perr != nil || p.SessionID != childSessionID || acked[p.MessageID] {
			continue
		}
		if questionOrBlockerKind(p.Kind) {
			count++
		}
	}
	return count, nil
}

// PeekSnapshot is the read-only Agent-View parity snapshot delegate.peek
// returns (m8) — the most recent checkpoint/progress for a child, without
// acking, steering, or consuming the per-child ceiling.
type PeekSnapshot struct {
	LatestCheckpointSummary string
	LatestProgressText      string
	LatestProgressPct       *int
	HasCheckpoint           bool
	HasProgress             bool
}

// Peek returns the latest checkpoint/progress entries for childSessionID
// under ownerKey WITHOUT any ack/ceiling side effect (m8 — distinct from
// the human-facing ActivityPanel Agent-View render surface, which is not
// this store's concern).
func (s *MessageInboxStore) Peek(ownerKey, childSessionID string) (*PeekSnapshot, error) {
	if strings.TrimSpace(ownerKey) == "" {
		return nil, ErrInboxEmptyOwnerKey
	}
	mu := s.lock.Get(ownerKey)
	mu.Lock()
	entries, err := s.readEntries(ownerKey)
	mu.Unlock()
	if err != nil {
		return nil, err
	}

	snap := &PeekSnapshot{}
	for _, e := range entries {
		if e.Kind != InboxEntryMessage || e.Message == nil {
			continue
		}
		p, raw, perr := peekEnvelope(*e.Message)
		if perr != nil || p.SessionID != childSessionID {
			continue
		}
		switch p.Kind {
		case "checkpoint":
			var cp generated.SessionMessageCheckpoint
			if json.Unmarshal(raw, &cp) == nil {
				snap.LatestCheckpointSummary = cp.Summary
				snap.HasCheckpoint = true
			}
		case "progress":
			var pr generated.SessionMessageProgress
			if json.Unmarshal(raw, &pr) == nil {
				snap.LatestProgressText = pr.Text
				snap.LatestProgressPct = pr.Pct
				snap.HasProgress = true
			}
		}
	}
	return snap, nil
}
