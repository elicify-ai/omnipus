// Omnipus — AskUserQuestion pending registry (spec §0.4, §3, US-3, US-6)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package askuser

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// Sentinel errors — the tool and the future frame handler branch on these.
var (
	// ErrAlreadyPending: one pending set per routing session (§0.9, US-6 S3).
	ErrAlreadyPending = errors.New("askuser: a question set is already pending for this session")
	// ErrSaturated: the global cross-session cap is reached (spec §4).
	ErrSaturated = errors.New("askuser: pending-question registry is at capacity")
	// ErrDelegatedChild: owner sessions only (§0.8, EC-9).
	ErrDelegatedChild = errors.New("askuser: AskUserQuestion is owner-session-only — a delegated session asks its parent via message_parent(kind=question, wait=true)")
	// ErrNoPending: unknown/stale card id (EC-8).
	ErrNoPending = errors.New("askuser: no pending question set for that card id")
	// ErrAlreadyResolved: first-valid-wins — a later submission loses (EC-7).
	ErrAlreadyResolved = errors.New("askuser: this question set was already answered or cancelled")
	// ErrNotOwner: the submitting user does not own the session (§3).
	ErrNotOwner = errors.New("askuser: only the session owner may answer this question set")
	// ErrSessionMismatch: card id exists but belongs to a different session.
	ErrSessionMismatch = errors.New("askuser: card does not belong to that session")
)

// MetaStore is the narrow slice of *session.UnifiedStore the registry needs
// for durable persistence (M-R2-1: pending state lives in UnifiedMeta).
type MetaStore interface {
	GetMeta(sessionID string) (*session.UnifiedMeta, error)
	SetMeta(sessionID string, patch session.MetaPatch) error
}

// ResumeDispatcher starts the RESUME turn (§0.2): a correlated user-role
// message beginning `Answers to your questions (card_id=<id>): {...}`. The
// gateway wires an implementation that publishes into the owner session's
// turn machinery; tests substitute a recorder.
type ResumeDispatcher interface {
	DispatchResume(set *PendingSet, resumeText string) error
}

// CardSink receives the card emission at park time and on state changes. The
// WS frame implementation is a later stream; NopCardSink keeps everything
// compiling and testable without frames.
type CardSink interface {
	EmitCard(set *PendingSet)
}

// NopCardSink is the default no-frames CardSink.
type NopCardSink struct{}

// EmitCard is a no-op.
func (NopCardSink) EmitCard(*PendingSet) {}

// AuditSink records an audit-log entry per auto-default resolution (spec §4
// non-behaviors / STRIDE note). The gateway adapts pkg/audit; nil skips.
type AuditSink interface {
	RecordAutoDefault(cardID, sessionID, header, label string)
}

// Registry is the in-process pending registry, modeled on pkg/gateway's
// approvalRegistryV2 shape (§0.4): a mutex-guarded map with a saturation
// cap, plus (unlike approvals) durable UnifiedMeta persistence and boot
// re-arm — the registry is a mirror of durable state, not the source of
// truth for restart survival.
type Registry struct {
	mu        sync.Mutex
	entries   map[string]*PendingSet // card_id → set (pending only)
	byRouting map[string]string      // routing_session_key → card_id
	bySession map[string]string      // transcript_session_id → card_id
	timers    map[string][]*time.Timer
	// timerWG tracks in-flight timer callbacks so Quiesce (gateway
	// shutdown, tests) can wait for their persists to finish rather than
	// racing a directory teardown or process exit.
	timerWG sync.WaitGroup

	maxPending       int
	defaultSafeDelay time.Duration

	meta   MetaStore
	resume ResumeDispatcher
	sink   CardSink
	audit  AuditSink

	now func() time.Time
}

// Options configures a Registry.
type Options struct {
	// MaxPending is the global cross-session cap; 0 selects DefaultGlobalCap.
	MaxPending int
	// DefaultSafeDelay overrides the 30-minute default-safe timer (tests).
	DefaultSafeDelay time.Duration
	// Sink receives card emissions; nil selects NopCardSink.
	Sink CardSink
	// Audit records auto-default resolutions; nil skips.
	Audit AuditSink
	// Now overrides the clock (tests).
	Now func() time.Time
}

// NewRegistry constructs a Registry. meta and resume are required for full
// function; a nil meta disables durable persistence (unit tests only), a nil
// resume makes resume dispatch an error surfaced to the caller.
func NewRegistry(meta MetaStore, resume ResumeDispatcher, opts Options) *Registry {
	if opts.MaxPending <= 0 {
		opts.MaxPending = DefaultGlobalCap
	}
	if opts.DefaultSafeDelay <= 0 {
		opts.DefaultSafeDelay = DefaultSafeDelay
	}
	if opts.Sink == nil {
		opts.Sink = NopCardSink{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Registry{
		entries:          make(map[string]*PendingSet),
		byRouting:        make(map[string]string),
		bySession:        make(map[string]string),
		timers:           make(map[string][]*time.Timer),
		maxPending:       opts.MaxPending,
		defaultSafeDelay: opts.DefaultSafeDelay,
		meta:             meta,
		resume:           resume,
		sink:             opts.Sink,
		audit:            opts.Audit,
		now:              opts.Now,
	}
}

// CreatePending admits a validated pending set: enforces owner-session-only
// (via the durable ParentSessionID field — a delegated child session always
// carries one), one-per-routing-session, and the global cap; persists the
// set into the owner session's UnifiedMeta; arms the default-safe timers;
// and emits the card via the sink. Implements the pkg/tools
// AskUserQuestionRegistry seam.
func (r *Registry) CreatePending(set *PendingSet) error {
	if set == nil || set.CardID == "" || set.TranscriptSessionID == "" {
		return fmt.Errorf("askuser: CreatePending: card id and transcript session id are required")
	}
	if err := ValidateQuestions(set.Questions); err != nil {
		return err
	}
	// Owner-session gate on DURABLE state (EC-9 second layer; the tool also
	// rejects on the ctx delegation-depth seam): a delegated child session
	// records its parent in SessionMeta.ParentSessionID.
	if r.meta != nil {
		meta, err := r.meta.GetMeta(set.TranscriptSessionID)
		if err != nil {
			return fmt.Errorf("askuser: CreatePending: cannot resolve session %s: %w", set.TranscriptSessionID, err)
		}
		if meta.ParentSessionID != "" {
			return ErrDelegatedChild
		}
	}

	set = set.Clone()
	set.Status = StatusPending
	if set.CreatedAt.IsZero() {
		set.CreatedAt = r.now().UTC()
	}
	routingKey := set.RoutingSessionKey
	if routingKey == "" {
		routingKey = set.TranscriptSessionID
	}
	// snapshot is a private, immutable copy taken BEFORE the map insert:
	// once `set` is in r.entries it is registry-owned and may be mutated
	// under r.mu by a firing timer at any moment, so every read after the
	// insert (persist, timer arming, the card emission) goes through this
	// snapshot, never through `set` itself (-race-verified).
	snapshot := set.Clone()

	r.mu.Lock()
	if _, exists := r.byRouting[routingKey]; exists {
		r.mu.Unlock()
		return ErrAlreadyPending
	}
	if _, exists := r.bySession[set.TranscriptSessionID]; exists {
		r.mu.Unlock()
		return ErrAlreadyPending
	}
	if r.maxPending > 0 && len(r.entries) >= r.maxPending {
		r.mu.Unlock()
		return ErrSaturated
	}
	r.entries[set.CardID] = set
	r.byRouting[routingKey] = set.CardID
	r.bySession[set.TranscriptSessionID] = set.CardID
	r.mu.Unlock()

	if err := r.persist(snapshot); err != nil {
		// Roll the reservation back — a set that is not durably persisted
		// must not park a turn (restart would lose it silently).
		r.mu.Lock()
		r.removeLocked(snapshot)
		r.mu.Unlock()
		return fmt.Errorf("askuser: CreatePending: persist failed: %w", err)
	}

	r.armTimers(snapshot)
	r.sink.EmitCard(snapshot.Clone())
	return nil
}

// PendingForSession returns a clone of the pending set for the given
// transcript session id, if one exists.
func (r *Registry) PendingForSession(sessionID string) (*PendingSet, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.bySession[sessionID]
	if !ok {
		return nil, false
	}
	return r.entries[id].Clone(), true
}

// Get returns a clone of the pending set with the given card id.
func (r *Registry) Get(cardID string) (*PendingSet, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.entries[cardID]
	if !ok {
		return nil, false
	}
	return s.Clone(), true
}

// PendingCount returns the number of pending sets across all sessions.
func (r *Registry) PendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// PendingAll returns a clone of every pending set, for the gateway's
// session_state reconnect snapshot (spec US-6 S1/FR-9). Order is undefined.
func (r *Registry) PendingAll() []*PendingSet {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*PendingSet, 0, len(r.entries))
	for _, s := range r.entries {
		out = append(out, s.Clone())
	}
	return out
}

// EffectiveDefaultSafeDelay reports the configured default-safe timer delay
// (the fixed 30 minutes in production; tests may shorten it) so the wire
// card can carry the concrete default_safe_at instant.
func (r *Registry) EffectiveDefaultSafeDelay() time.Duration {
	return r.defaultSafeDelay
}

// Submit is the server-side submission path (§3), callable by the future
// ask_user_answer frame handler and by tests. sessionID is the session the
// submitting client is attached to; user is the authenticated username ("" =
// unauthenticated single-user install). Validation failures are rejected
// WITHOUT consuming the pending set; the first VALID submission wins.
func (r *Registry) Submit(cardID, sessionID, user string, answers []SubmittedAnswer) error {
	r.mu.Lock()
	set, ok := r.entries[cardID]
	if !ok {
		r.mu.Unlock()
		return ErrNoPending
	}
	if set.TranscriptSessionID != sessionID {
		r.mu.Unlock()
		return ErrSessionMismatch
	}
	if set.Owner != "" && user != "" && user != set.Owner {
		r.mu.Unlock()
		return ErrNotOwner
	}
	if set.Status != StatusPending {
		// Unreachable while terminal sets are removed synchronously, but kept
		// as the explicit first-valid-wins guard should retention be added.
		r.mu.Unlock()
		return ErrAlreadyResolved
	}
	final, err := validateSubmission(set.Questions, answers)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	// First valid submission wins — consume under the lock.
	set.Status = StatusAnswered
	set.Answers = final
	consumed := set.Clone()
	r.removeLocked(set)
	r.mu.Unlock()

	r.stopTimers(cardID)
	r.persistTerminal(consumed)
	// Terminal state-change emission (spec §3): the SPA collapses the card
	// to the answered record and unlocks the composer on this frame.
	r.sink.EmitCard(consumed.Clone())
	return r.dispatchResume(consumed)
}

// CancelByUser cancels the pending set from the card's Cancel affordance
// (US-1 S4): selections are discarded, no answers, and a submission-free
// resume turn carries {"status":"cancelled"} so the agent decides next steps.
func (r *Registry) CancelByUser(cardID, sessionID string) error {
	set, err := r.cancelCommon(cardID, sessionID)
	if err != nil {
		return err
	}
	return r.dispatchResume(set)
}

// CancelOnSessionStop cancels any pending set reachable by the given key —
// matched against BOTH the routing session key and the transcript session id
// (US-6 S2: session Stop / channel `/cancel`). No resume turn is dispatched:
// the user asked everything to stop. Returns true when a set was cancelled.
func (r *Registry) CancelOnSessionStop(key string) bool {
	if key == "" {
		return false
	}
	r.mu.Lock()
	id, ok := r.byRouting[key]
	if !ok {
		id, ok = r.bySession[key]
	}
	if !ok {
		r.mu.Unlock()
		return false
	}
	set := r.entries[id]
	set.Status = StatusCancelled
	consumed := set.Clone()
	r.removeLocked(set)
	r.mu.Unlock()

	r.stopTimers(consumed.CardID)
	r.persistTerminal(consumed)
	// Terminal state-change emission: collapse the card + unlock the
	// composer on every connected client.
	r.sink.EmitCard(consumed.Clone())
	return true
}

// cancelCommon validates + consumes a pending set into cancelled state.
func (r *Registry) cancelCommon(cardID, sessionID string) (*PendingSet, error) {
	r.mu.Lock()
	set, ok := r.entries[cardID]
	if !ok {
		r.mu.Unlock()
		return nil, ErrNoPending
	}
	if sessionID != "" && set.TranscriptSessionID != sessionID {
		r.mu.Unlock()
		return nil, ErrSessionMismatch
	}
	set.Status = StatusCancelled
	set.Answers = nil // EC-1: cancel discards selections, uniformly
	consumed := set.Clone()
	r.removeLocked(set)
	r.mu.Unlock()

	r.stopTimers(cardID)
	r.persistTerminal(consumed)
	// Terminal state-change emission (spec §3): collapse to the cancelled
	// record on every connected client.
	r.sink.EmitCard(consumed.Clone())
	return consumed, nil
}

// RearmSession re-hydrates one session's persisted pending set into the
// in-process registry after a restart (US-6 S1 / FR-9): timers re-arm from
// the persisted CreatedAt (already-elapsed timers fire immediately). A
// session with no persisted set is a no-op.
func (r *Registry) RearmSession(sessionID string) error {
	if r.meta == nil {
		return fmt.Errorf("askuser: RearmSession: no meta store wired")
	}
	meta, err := r.meta.GetMeta(sessionID)
	if err != nil {
		return err
	}
	if meta.PendingAskJSON == "" {
		return nil
	}
	var set PendingSet
	if err := json.Unmarshal([]byte(meta.PendingAskJSON), &set); err != nil {
		return fmt.Errorf("askuser: RearmSession: corrupt pending_ask on %s: %w", sessionID, err)
	}
	if set.Status != StatusPending {
		// Terminal (answered/cancelled) record: nothing to re-arm. Leave it
		// persisted — §0.6: the collapsed card renders from this record on
		// history reload.
		return nil
	}
	routingKey := set.RoutingSessionKey
	if routingKey == "" {
		routingKey = set.TranscriptSessionID
	}
	r.mu.Lock()
	if _, exists := r.entries[set.CardID]; exists {
		r.mu.Unlock()
		return nil // already live
	}
	if _, exists := r.byRouting[routingKey]; exists {
		r.mu.Unlock()
		return nil
	}
	cp := set.Clone()
	r.entries[cp.CardID] = cp
	r.byRouting[routingKey] = cp.CardID
	r.bySession[cp.TranscriptSessionID] = cp.CardID
	r.mu.Unlock()

	// Arm from the local `set` value, never the registry-owned `cp` — same
	// post-insert aliasing rule as CreatePending's snapshot.
	r.armTimers(&set)
	return nil
}

// --- timers (US-3): server-side, over durable state ---

// armTimers arms ONE timer per SET, not one per question: every default-safe
// question in a set shares the same CreatedAt+defaultSafeDelay deadline (the
// 30:00 clock is per set, US-3 S2), so per-question timers only produced N
// near-simultaneous callbacks each doing its own persist + card emission —
// N-1 redundant fsync-bound writes and WS broadcasts for an N-question set.
// The single callback (fireDefaultSafeSet) resolves every due header at once
// with one persist, one emission, and — when the whole set is default-safe —
// the terminal server auto-submit path. An already-elapsed deadline (boot
// re-arm) fires near-immediately; a set with no unresolved default-safe
// question arms nothing.
func (r *Registry) armTimers(set *PendingSet) {
	due := false
	for i := range set.Questions {
		q := set.Questions[i]
		if !q.DefaultSafe {
			continue
		}
		if _, done := set.AutoResolved[q.Header]; done {
			continue
		}
		due = true
		break
	}
	if !due {
		return
	}
	delay := set.CreatedAt.Add(r.defaultSafeDelay).Sub(r.now())
	if delay < 0 {
		delay = 0
	}
	cardID := set.CardID
	r.timerWG.Add(1)
	t := time.AfterFunc(delay, func() {
		defer r.timerWG.Done()
		r.fireDefaultSafeSet(cardID)
	})
	r.mu.Lock()
	r.timers[cardID] = append(r.timers[cardID], t)
	r.mu.Unlock()
}

func (r *Registry) stopTimers(cardID string) {
	r.mu.Lock()
	timers := r.timers[cardID]
	delete(r.timers, cardID)
	r.mu.Unlock()
	for _, t := range timers {
		if t.Stop() {
			// Callback will never run; release its WaitGroup slot.
			r.timerWG.Done()
		}
	}
}

// Quiesce blocks until every in-flight default-safe timer callback (and its
// persist/resume work) has completed. Timers already stopped are not waited
// on. Intended for gateway shutdown and test teardown.
func (r *Registry) Quiesce() {
	r.timerWG.Wait()
}

// fireDefaultSafeSet is the single per-set timer callback: it marks EVERY
// still-unresolved default-safe question in the set resolved-pending-submit
// (US-3 S2) in one pass — they all share the set's one deadline — records one
// audit entry per resolved header, then performs exactly ONE persist and ONE
// card emission for the whole batch. When every question in the set is
// default-safe and now resolved with no client submission landed, the server
// auto-submit (US-3 S3) takes the terminal path instead: terminal persist,
// terminal emission, resume dispatch.
func (r *Registry) fireDefaultSafeSet(cardID string) {
	r.mu.Lock()
	set, ok := r.entries[cardID]
	if !ok || set.Status != StatusPending {
		r.mu.Unlock()
		return
	}
	if set.AutoResolved == nil {
		set.AutoResolved = make(map[string]time.Time)
	}
	now := r.now().UTC()
	type autoResolution struct{ header, label string }
	var resolved []autoResolution
	for i := range set.Questions {
		q := &set.Questions[i]
		if !q.DefaultSafe {
			continue
		}
		if _, done := set.AutoResolved[q.Header]; done {
			continue
		}
		set.AutoResolved[q.Header] = now
		resolved = append(resolved, autoResolution{header: q.Header, label: q.Recommended})
	}
	if len(resolved) == 0 {
		// Everything already resolved (e.g. a re-arm raced a client action)
		// — nothing to persist or emit.
		r.mu.Unlock()
		return
	}
	sessionID := set.TranscriptSessionID

	// Server auto-submit condition: every question default-safe AND resolved.
	serverSubmit := allDefaultSafe(set.Questions) && len(set.AutoResolved) == len(set.Questions)

	var consumed *PendingSet
	if serverSubmit {
		set.Status = StatusAnswered
		set.Answers = buildAllDefaultAnswers(set.Questions)
		consumed = set.Clone()
		r.removeLocked(set)
	}
	snapshot := set.Clone()
	r.mu.Unlock()

	if r.audit != nil {
		// Audit stays per question — each auto-default is its own auditable
		// resolution (spec §4 STRIDE note) even though they land in one batch.
		for _, res := range resolved {
			r.audit.RecordAutoDefault(cardID, sessionID, res.header, res.label)
		}
	}

	if serverSubmit {
		r.stopTimers(cardID)
		r.persistTerminal(consumed)
		r.sink.EmitCard(consumed.Clone())
		if err := r.dispatchResume(consumed); err != nil {
			slog.Warn("askuser: server auto-submit resume dispatch failed",
				"card_id", cardID, "session_id", sessionID, "error", err)
		}
		return
	}
	// Non-terminal state change: persist the resolved-pending-submit marks so
	// a restart re-derives them instead of re-arming a fired timer.
	if err := r.persist(snapshot); err != nil {
		slog.Warn("askuser: failed to persist auto-default resolution",
			"card_id", cardID, "session_id", sessionID, "error", err)
	}
	// Non-terminal state-change emission: the connected card marks these
	// questions resolved-pending-submit (auto badge) live — one frame for the
	// whole batch.
	r.sink.EmitCard(snapshot.Clone())
}

// buildAllDefaultAnswers builds the all-recommendation submission for the
// server auto-submit: each answer is the question's recommended label —
// under multi_select, a one-element list (spec §2) — marked auto_default.
func buildAllDefaultAnswers(qs []Question) []Answer {
	out := make([]Answer, 0, len(qs))
	for i := range qs {
		out = append(out, Answer{
			Header:       qs[i].Header,
			QuestionText: qs[i].Question,
			Selected:     []string{qs[i].Recommended},
			AutoDefault:  true,
		})
	}
	return out
}

// --- internals ---

// removeLocked drops a set from every index. Caller holds r.mu.
func (r *Registry) removeLocked(set *PendingSet) {
	delete(r.entries, set.CardID)
	routingKey := set.RoutingSessionKey
	if routingKey == "" {
		routingKey = set.TranscriptSessionID
	}
	if r.byRouting[routingKey] == set.CardID {
		delete(r.byRouting, routingKey)
	}
	if r.bySession[set.TranscriptSessionID] == set.CardID {
		delete(r.bySession, set.TranscriptSessionID)
	}
}

// persist writes the set into the owner session's UnifiedMeta.
func (r *Registry) persist(set *PendingSet) error {
	if r.meta == nil {
		return nil
	}
	data, err := json.Marshal(set)
	if err != nil {
		return err
	}
	s := string(data)
	return r.meta.SetMeta(set.TranscriptSessionID, session.MetaPatch{PendingAskJSON: &s})
}

// persistTerminal updates the terminal registry/session-meta record (§0.6:
// the collapsed record renders from THIS record) — the terminal set is
// persisted so history reload can reconstruct the collapsed card, and the
// pending flag is thereby consumed (Status != pending).
func (r *Registry) persistTerminal(set *PendingSet) {
	if err := r.persist(set); err != nil {
		slog.Warn("askuser: failed to persist terminal question-set record",
			"card_id", set.CardID, "session_id", set.TranscriptSessionID, "error", err)
	}
}

// dispatchResume builds and dispatches the §0.2 resume message.
func (r *Registry) dispatchResume(set *PendingSet) error {
	if r.resume == nil {
		return fmt.Errorf("askuser: no resume dispatcher wired — answers recorded but the session cannot resume")
	}
	text, err := ResumeMessage(set)
	if err != nil {
		return err
	}
	return r.resume.DispatchResume(set.Clone(), text)
}

// resumePayload is the JSON payload embedded in the resume message (spec §2:
// the result schema is the resume-message payload).
type resumePayload struct {
	Status  SetStatus `json:"status"`
	Answers []Answer  `json:"answers,omitempty"`
}

// resumeMessagePrefix opens every §0.2 resume message. ResumeMessage renders
// with it and ParseResumeCardID recognizes it — keep the two in lockstep.
const resumeMessagePrefix = "Answers to your questions (card_id="

// ResumeMessage renders the §0.2 correlated user-role resume message:
//
//	Answers to your questions (card_id=<id>): {"status":...,"answers":[...]}
//
// with each answered question's TEXT echoed alongside its answer (o-R2-1).
// A cancelled set carries no answers (FR-4).
func ResumeMessage(set *PendingSet) (string, error) {
	p := resumePayload{Status: set.Status}
	if set.Status == StatusAnswered {
		p.Answers = cloneAnswers(set.Answers)
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("askuser: marshal resume payload: %w", err)
	}
	return fmt.Sprintf("%s%s): %s", resumeMessagePrefix, set.CardID, string(data)), nil
}

// ParseResumeCardID recognizes a §0.2 resume message (the exact shape
// ResumeMessage renders) and extracts its card id. It exists for the SPA
// presentation rule's server-side half (§0.2: the resume message is rendered
// AS the collapsed answer record, never as raw JSON): the gateway's session
// replay uses it to identify persisted resume messages so it can suppress
// the raw-JSON user bubble and emit the reconstructed collapsed card in its
// place. Returns ok=false for any content that is not a resume message.
func ParseResumeCardID(content string) (cardID string, ok bool) {
	if !strings.HasPrefix(content, resumeMessagePrefix) {
		return "", false
	}
	rest := content[len(resumeMessagePrefix):]
	end := strings.Index(rest, ")")
	if end <= 0 {
		return "", false
	}
	return rest[:end], true
}

// ResumeAnswers is a §0.2 resume message fully decoded: the card id plus its
// JSON payload (status + answers).
type ResumeAnswers struct {
	CardID  string
	Status  SetStatus
	Answers []Answer
}

// ParseResumeMessage recognizes a §0.2 resume message (the exact shape
// ResumeMessage renders, mirroring ParseResumeCardID's prefix/card-id
// recognition) and additionally decodes its JSON payload. Exists for
// consumers that need the answers themselves, not just the card id — e.g.
// ADR-079 D3's goal-compile resume, which must key on the card id (C1: a
// non-matching or non-resume message must pass through untouched) and then
// feed the parsed answers into the resumed compile.
//
// ok=false means content is not a §0.2 resume message at all (mirrors
// ParseResumeCardID's contract exactly — same prefix/card-id recognition, so
// the two never disagree on "is this a resume message"). ok=true with a
// non-nil err means content WAS recognized as a resume message but its JSON
// payload failed to decode — callers should treat this as a corrupt/foreign
// message, never silently consume it as a valid answer.
func ParseResumeMessage(content string) (ResumeAnswers, bool, error) {
	cardID, ok := ParseResumeCardID(content)
	if !ok {
		return ResumeAnswers{}, false, nil
	}
	const sep = "): "
	rest := content[len(resumeMessagePrefix)+len(cardID):]
	if !strings.HasPrefix(rest, sep) {
		return ResumeAnswers{}, true, fmt.Errorf("askuser: resume message missing %q separator", sep)
	}
	var p resumePayload
	if err := json.Unmarshal([]byte(rest[len(sep):]), &p); err != nil {
		return ResumeAnswers{}, true, fmt.Errorf("askuser: resume message payload: %w", err)
	}
	return ResumeAnswers{CardID: cardID, Status: p.Status, Answers: p.Answers}, true, nil
}

// validateSubmission applies the §3 server-side submission validation over a
// full submission: every question answered exactly once (matched by header),
// no unknown headers, per-answer label membership, arity respecting
// multi_select, free-text-presence-is-the-flag exclusivity (EC-3's
// last-interaction-wins means a valid client submission never carries both),
// and the free-text size cap. Returns the finalized answers with the
// question-text echo filled in.
func validateSubmission(qs []Question, answers []SubmittedAnswer) ([]Answer, error) {
	if len(answers) != len(qs) {
		return nil, fmt.Errorf("askuser: submission must answer every question exactly once (%d questions, %d answers)", len(qs), len(answers))
	}
	seen := make(map[string]bool, len(answers))
	out := make([]Answer, 0, len(answers))
	for i, a := range answers {
		q := questionByHeader(qs, a.Header)
		if q == nil {
			return nil, fmt.Errorf("askuser: answers[%d]: unknown question header %q", i, a.Header)
		}
		if seen[a.Header] {
			return nil, fmt.Errorf("askuser: answers[%d]: duplicate answer for header %q", i, a.Header)
		}
		seen[a.Header] = true

		hasFree := a.FreeText != nil
		hasSel := len(a.Selected) > 0
		switch {
		case hasFree && hasSel:
			return nil, fmt.Errorf("askuser: answers[%d] (%q): free_text and selected are mutually exclusive (last interaction wins client-side)", i, a.Header)
		case !hasFree && !hasSel:
			return nil, fmt.Errorf("askuser: answers[%d] (%q): needs a selection or free text", i, a.Header)
		}
		if hasFree {
			if len([]rune(*a.FreeText)) > MaxFreeTextChars {
				return nil, fmt.Errorf("askuser: answers[%d] (%q): free text exceeds %d characters", i, a.Header, MaxFreeTextChars)
			}
			if *a.FreeText == "" {
				return nil, fmt.Errorf("askuser: answers[%d] (%q): free text must not be empty", i, a.Header)
			}
		}
		if hasSel {
			if !q.MultiSelect && len(a.Selected) > 1 {
				return nil, fmt.Errorf("askuser: answers[%d] (%q): multiple selections on a single-select question", i, a.Header)
			}
			selSeen := make(map[string]bool, len(a.Selected))
			for _, label := range a.Selected {
				if questionOptionByLabel(q, label) == nil {
					return nil, fmt.Errorf("askuser: answers[%d] (%q): %q is not an option of this question", i, a.Header, label)
				}
				if selSeen[label] {
					return nil, fmt.Errorf("askuser: answers[%d] (%q): duplicate selection %q", i, a.Header, label)
				}
				selSeen[label] = true
			}
		}
		fin := Answer{
			Header:       a.Header,
			QuestionText: q.Question,
			AutoDefault:  a.AutoDefault,
		}
		if hasFree {
			v := *a.FreeText
			fin.FreeText = &v
		} else {
			fin.Selected = append([]string(nil), a.Selected...)
		}
		out = append(out, fin)
	}
	return out, nil
}

func questionOptionByLabel(q *Question, label string) *Option {
	for i := range q.Options {
		if q.Options[i].Label == label {
			return &q.Options[i]
		}
	}
	return nil
}
