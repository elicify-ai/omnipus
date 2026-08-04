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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	Kind      InboxEntryKind            `json:"kind"`
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

func peekEnvelope(msg generated.SessionMessage) (envelopePeek, []byte, error) {
	data, err := msg.MarshalJSON()
	if err != nil {
		return envelopePeek{}, nil, fmt.Errorf("%w: %v", ErrInboxBadMessage, err)
	}
	var p envelopePeek
	if err := json.Unmarshal(data, &p); err != nil {
		return envelopePeek{}, nil, fmt.Errorf("%w: %v", ErrInboxBadMessage, err)
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

	// now is overridable for deterministic (fake-clock) tests.
	now func() time.Time

	rateMu      sync.Mutex
	rateWindows map[string][]time.Time // key = ownerKey + "\x00" + childSessionID
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
		now:                    time.Now,
		rateWindows:            make(map[string][]time.Time),
	}
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
	return entries, nil
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

	window := s.rateWindows[key]
	kept := window[:0]
	for _, t := range window {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	limit := s.ChildSendRatePerMinute
	if limit <= 0 {
		limit = DefaultChildSendRatePerMinute
	}
	if len(kept) >= limit {
		s.rateWindows[key] = kept
		return false
	}
	s.rateWindows[key] = append(kept, now)
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

	for _, e := range entries {
		if e.Kind == InboxEntryMessage && e.Message != nil {
			p2, _, perr := peekEnvelope(*e.Message)
			if perr == nil && p2.MessageID == peek.MessageID {
				return &AppendResult{Accepted: true, Deduped: true, MessageID: peek.MessageID}, nil
			}
		}
	}

	acked := foldAcked(entries)
	openTypeCount := 0
	openTotalCount := 0
	for _, e := range entries {
		if e.Kind != InboxEntryMessage || e.Message == nil {
			continue
		}
		p2, _, perr := peekEnvelope(*e.Message)
		if perr != nil || p2.SessionID != peek.SessionID {
			continue
		}
		if acked[p2.MessageID] {
			continue
		}
		openTotalCount++
		if questionOrBlockerKind(p2.Kind) {
			openTypeCount++
		}
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

	entry := InboxEntry{Kind: InboxEntryMessage, Message: &msg, CreatedAt: s.now().UTC()}
	if err := fileutil.AppendJSONL(s.path(ownerKey), entry); err != nil {
		return nil, fmt.Errorf("session: inbox: persist: %w", err)
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
	entries, err := s.readEntries(ownerKey)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Kind != InboxEntryMessage || e.Message == nil {
			continue
		}
		if p, _, perr := peekEnvelope(*e.Message); perr == nil {
			known[p.MessageID] = true
		}
	}
	result := &AckResult{}
	for _, id := range messageIDs {
		if known[id] {
			result.Acknowledged = append(result.Acknowledged, id)
		} else {
			result.Unknown = append(result.Unknown, id)
		}
	}

	entry := InboxEntry{Kind: InboxEntryAck, AckedIDs: append([]string(nil), messageIDs...), CreatedAt: s.now().UTC()}
	if err := fileutil.AppendJSONL(s.path(ownerKey), entry); err != nil {
		return nil, fmt.Errorf("session: inbox: ack: %w", err)
	}
	return result, nil
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
	sinceIdx := 0
	if sinceCursor != "" {
		if n, perr := strconv.Atoi(sinceCursor); perr == nil {
			sinceIdx = n
		}
	}

	// Correctness-MAJOR-1 (drain cursor): the pagination cursor MUST be the
	// ENTRY index in `entries` of the last EMITTED candidate + 1, NOT an
	// output-count offset (sinceIdx + max). Candidates skip acked /
	// wrong-session / non-message lines, so an output-count cursor under-
	// points and the next Drain re-scans (and re-delivers) the skipped tail
	// of the prior page until those lines get acked. candidateEntryIdx[i]
	// records the index in `entries` that produced candidates[i], letting
	// the truncation cursor resume scanning immediately AFTER the entry that
	// yielded the last emitted message.
	var candidates []generated.SessionMessage
	var candidateEntryIdx []int
	lastScanned := sinceIdx - 1
	for i := sinceIdx; i < len(entries); i++ {
		lastScanned = i
		e := entries[i]
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
		candidateEntryIdx = append(candidateEntryIdx, i)
	}

	if len(candidates) > maxMessages {
		lastEmittedEntryIdx := candidateEntryIdx[maxMessages-1]
		return candidates[:maxMessages], strconv.Itoa(lastEmittedEntryIdx + 1), true, nil
	}
	// Exhausted — cursor points just past the final scanned entry so the next
	// Drain does not re-scan it. lastScanned stays sinceIdx-1 when the loop
	// never ran (sinceIdx already past the end), so the cursor does not move
	// backwards.
	return candidates, strconv.Itoa(lastScanned + 1), false, nil
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
