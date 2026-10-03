package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const (
	watcherBackoffBase   = 60 * time.Second
	watcherBackoffCap    = 15 * time.Minute
	watcherCycleInterval = time.Minute
)

// WatcherConfig configures one mailbox's new-mail watcher.
type WatcherConfig struct {
	// AgentID and WorkspaceID key the watcher state.
	AgentID     string
	WorkspaceID string
	// Transport is the mailbox's mail edge. When it exposes MailboxStatus
	// (the production *Client), the cycle reads unseen/uidnext/uidvalidity
	// with one STATUS command; otherwise it falls back to a bounded
	// unseen-only envelope read.
	Transport Transport
	// StateDir is the directory the state file lives under (the data dir).
	StateDir string
	// Budget, when set, gates the cycle's dial through the shared mail
	// operation budget (A8): a TryCall skip (no free slot, or account in
	// backoff) is NOT a cycle failure — the cycle returns without recording
	// anything, so the watcher's own success/failure bookkeeping and
	// NextAttemptAt logic are untouched. Nil = ungated (the RED unit tests
	// drive Watcher directly without a budget).
	Budget *MailBudget
	// Now, when set, overrides the clock (tests).
	Now func() time.Time
}

// NewWatcher validates the config and returns a watcher for one mailbox. An
// empty config is not a mailbox: agent, workspace, transport and state dir
// are required — the cycle must fail visibly rather than pretend it checked
// mail (MC-18).
func NewWatcher(cfg WatcherConfig) (*Watcher, error) {
	var missing []string
	if cfg.AgentID == "" {
		missing = append(missing, "agent")
	}
	if cfg.WorkspaceID == "" {
		missing = append(missing, "workspace")
	}
	if cfg.Transport == nil {
		missing = append(missing, "transport")
	}
	if cfg.StateDir == "" {
		missing = append(missing, "state dir")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("email watcher: missing %s", strings.Join(missing, ", "))
	}
	return &Watcher{
		cfg:       cfg,
		statePath: filepath.Join(cfg.StateDir, "email-watch", keyFor(cfg.AgentID, cfg.WorkspaceID)+".json"),
	}, nil
}

// Watcher polls one mailbox for new mail on the heartbeat cadence. It never
// mutates flags, never creates tasks, never starts a turn (D20/D27): it only
// updates the per-mailbox state file the Mail panel badge reads.
type Watcher struct {
	mu        sync.Mutex
	cfg       WatcherConfig
	statePath string
	state     WatcherState
}

// WatcherState is the persisted per-mailbox watcher state (MC-23/MC-31):
// UIDs, counts and error metadata only, never message content (D6/D21).
type WatcherState struct {
	AgentID        string `json:"agent_id"`
	WorkspaceID    string `json:"workspace_id"`
	UIDValidity    uint32 `json:"uidvalidity"`
	LastSeenUID    uint32 `json:"last_seen_uid"`
	UnseenTotal    int    `json:"unseen_total"`
	State          string `json:"watcher_state"`
	LastErrorClass string `json:"last_error_class"`
	LastErrorText  string `json:"last_error_text"`
	LastSuccessAt  string `json:"last_success_at"`
	NextAttemptAt  string `json:"next_attempt_at"`
	Attempt        int    `json:"attempt"`
}

// EffectiveState derives the summary state of one persisted state — the ONE
// derivation point for the summary's watcher_state enum (MC-23/MC-31):
// "backoff" when the last cycle failed AND the next attempt is still in the
// future (MC-33: no dial until then), "error" when it failed but the watcher
// is due again, "ok" otherwise. Backoff is a RENDER-TIME fact: recordFailure
// persists "error" plus a future NextAttemptAt, and the reader derives
// "backoff" from them at read time — a persisted "backoff" value would go
// stale the moment NextAttemptAt passed, which is why nothing ever writes
// the literal "backoff" into the state file.
func (s *WatcherState) EffectiveState(now time.Time) string {
	if s == nil {
		return "ok"
	}
	if s.State == "error" {
		if s.NextAttemptAt != "" {
			if next, err := time.Parse(time.RFC3339, s.NextAttemptAt); err == nil && now.Before(next) {
				return "backoff"
			}
		}
		return "error"
	}
	return "ok"
}

// WatcherBackoff is the per-mailbox reconnect backoff (MC-33/B-43).
// auth_failed goes straight to the 15 min cap. The ladder doubles in a loop
// that stops at the cap (round-8 F1): the old `base << shift` overflowed
// time.Duration at shift ≥ 28 (attempt 29+), went NEGATIVE — the cap check
// could not catch a negative — and recordFailure then persisted a
// NextAttemptAt in the past, so the mailbox dialed on every tick forever.
// The loop keeps d ≤ 2×cap at every step, so no attempt number can overflow.
func WatcherBackoff(attempt int, errClass string, randUnit float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := watcherBackoffBase
	for shift := attempt - 1; shift > 0; shift-- {
		if d >= watcherBackoffCap {
			break
		}
		d *= 2
	}
	if d > watcherBackoffCap || errClass == "auth_failed" {
		d = watcherBackoffCap
	}
	if randUnit < 0 {
		randUnit = 0
	}
	if randUnit > 1 {
		randUnit = 1
	}
	return time.Duration(float64(d) * (0.8 + 0.4*randUnit))
}

// WatcherInitialOffset staggers first cycles of same-host mailboxes (MC-33).
func WatcherInitialOffset(i int) time.Duration {
	if i < 0 {
		i = 0
	}
	return time.Duration(i) * watcherCycleInterval
}

// keyFor builds the state-file name for an (agent, workspace) pair.
func keyFor(agentID, workspaceID string) string {
	clean := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return clean(agentID) + "-" + clean(workspaceID)
}

// MailboxStatus is the optional capability the watcher cycle uses for a
// one-command unseen/uidnext/uidvalidity probe (implemented by *Client).
type MailboxStatuser interface {
	MailboxStatus(ctx context.Context) (unseen int, uidnext, uidvalidity uint32, err error)
}

// poolTryStatuser is the watcher's non-blocking STATUS capability (W1
// §4.10.2): the same one-STATUS probe, but the pool acquisition skips
// instead of queueing when the ceilings are full. Same-package capability,
// implemented by *Client when a session source is injected.
type poolTryStatuser interface {
	mailboxStatusTry(ctx context.Context) (unseen int, uidnext, uidvalidity uint32, err error)
}

// Cycle runs one watcher pass for the mailbox: probe unseen/uidnext/
// uidvalidity, update the state file. It never mutates flags, never creates
// tasks, never starts an agent turn (D20/D27/FR-023). When a budget is
// wired, the dial goes through Budget.TryCall (non-blocking): a skip —
// backoff, both slots busy, or the pool ceilings full (ErrPoolSkipped,
// W1 §4.10.2) — is not a cycle failure, so the cycle returns without
// recording anything; only the dial itself is gated, and the watcher's own
// success/failure bookkeeping is unchanged. A skipped cycle leaves the
// last-checked time unchanged: the badge cannot claim a check that did not
// happen.
func (w *Watcher) Cycle(ctx context.Context) error {
	err := w.runCycle(ctx)
	if err == nil {
		return nil
	}
	if w.cfg.Budget != nil && (errors.Is(err, ErrMailSkipped) || errors.Is(err, ErrPoolSkipped)) {
		// The dial never happened — do NOT recordFailure (a skip must not
		// advance backoff); log and let the next tick try again.
		slog.Info("email watcher: cycle skipped",
			"agent_id", w.cfg.AgentID, "workspace_id", w.cfg.WorkspaceID,
			"reason", err.Error())
		return nil
	}
	// Only the closed CLASS is persisted — never the raw provider text
	// (FR-W1-23, §4.12).
	w.recordFailure(classifyMailError(err))
	return err
}

// runCycle is the raw cycle body: probe → recordSuccess, with the probe
// error returned to Cycle for the recordFailure tail. The skip sentinels
// (ErrMailSkipped / ErrPoolSkipped) are detected inside Cycle.
func (w *Watcher) runCycle(ctx context.Context) error {
	w.mu.Lock()
	prev := w.state
	w.mu.Unlock()
	unseen, uidnext, uidvalidity, err := w.probe(ctx)
	if err != nil {
		return err
	}
	w.recordSuccess(unseen, uidnext, uidvalidity)
	w.markPanelDirtyIfNeeded(prev, unseen, uidvalidity)
	return nil
}

// markPanelDirtyIfNeeded is the watcher's ONLY panel-metadata interaction
// (FR-W1-26): when a cycle observes a folder-version change (a UIDVALIDITY
// reset or new unseen mail) while no panel event consumes it, the change is
// signalled through the published dirty-mark interface — never a cache
// write, never a refresh trigger, no mail data carried.
func (w *Watcher) markPanelDirtyIfNeeded(prev WatcherState, unseen int, uidvalidity uint32) {
	if prev.LastSuccessAt == "" {
		// First run establishes the baseline; a baseline is not a change.
		return
	}
	if prev.UIDValidity != uidvalidity || unseen > prev.UnseenTotal {
		if dm, ok := w.cfg.Transport.(interface {
			markPanelMetadataDirty(pair, folder string)
		}); ok {
			dm.markPanelMetadataDirty(w.cfg.AgentID+"/"+w.cfg.WorkspaceID, "INBOX")
		}
	}
}

// probe reads the mailbox's live counters without touching any flag. The
// STATUS-based path is used whenever the transport exposes it (*Client) —
// preferring the pool-aware non-blocking variant when a session source is
// injected (§4.10.2). With a budget wired (A8), the dial itself is gated
// through Budget.TryCall (non-blocking): a refusal — account in backoff or
// both slots busy — surfaces as the ErrMailSkipped sentinel (Cycle's skip
// branch handles it; the dial function never runs). A nil budget dials
// directly, unchanged — nil = ungated, which the unit tests driving a bare
// Watcher rely on.
func (w *Watcher) probe(ctx context.Context) (int, uint32, uint32, error) {
	var unseen int
	var uidnext, uidvalidity uint32
	dial := func(ctx context.Context) error {
		if sp, ok := w.cfg.Transport.(poolTryStatuser); ok {
			u, n, v, err := sp.mailboxStatusTry(ctx)
			if err != nil {
				return err
			}
			unseen, uidnext, uidvalidity = u, n, v
			return nil
		}
		if sp, ok := w.cfg.Transport.(MailboxStatuser); ok {
			u, n, v, err := sp.MailboxStatus(ctx)
			if err != nil {
				return err
			}
			unseen, uidnext, uidvalidity = u, n, v
			return nil
		}
		msgs, err := w.cfg.Transport.ReadInbox(ctx, InboxOptions{Limit: 25, UnseenOnly: true})
		if err != nil {
			return err
		}
		var maxUID uint32
		for i := range msgs {
			if msgs[i].UID > maxUID {
				maxUID = msgs[i].UID
			}
		}
		unseen = len(msgs)
		uidnext = maxUID + 1
		return nil
	}
	if w.cfg.Budget == nil {
		// Ungated (unit tests driving a bare Watcher): dial directly.
		if err := dial(ctx); err != nil {
			return 0, 0, 0, err
		}
		return unseen, uidnext, uidvalidity, nil
	}
	err := w.cfg.Budget.TryCall(ctx, MailBudgetRequest{
		Account:     accountKeyOf(w.cfg.Transport, w.cfg.AgentID, w.cfg.WorkspaceID),
		AgentID:     w.cfg.AgentID,
		WorkspaceID: w.cfg.WorkspaceID,
		Operation:   "watcher_cycle",
		Purpose:     "watcher",
	}, dial)
	if err != nil {
		return 0, 0, 0, err
	}
	return unseen, uidnext, uidvalidity, nil
}

// recordSuccess updates the in-memory and persisted state after a clean cycle.
func (w *Watcher) recordSuccess(unseen int, uidnext, uidvalidity uint32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.applyState(func() {
		w.state.UnseenTotal = unseen
		w.state.UIDValidity = uidvalidity
		w.state.LastSeenUID = w.nextLastSeenUID(uidnext, uidvalidity)
		w.state.State = "ok"
		w.state.LastErrorClass = ""
		w.state.LastErrorText = ""
		w.state.LastSuccessAt = w.now().UTC().Format(time.RFC3339)
		w.state.NextAttemptAt = ""
		w.state.Attempt = 0
	})
}

// recordFailure stores the classified failure and the next attempt time.
// Only the SAFE classified representation is persisted (FR-W1-23, §4.12,
// grill-1 C-1): the closed class from classifyMailError plus safe metadata
// (next-attempt time, consecutive-failure count). The raw provider error
// string is NEVER written to email-watch/<pair>.json — raw provider text can
// embed folder names, server hostnames and account prefixes, and persisting
// it would turn a transient failure into a durable disclosure.
func (w *Watcher) recordFailure(errClass string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.applyState(func() {
		w.state.State = "error"
		w.state.LastErrorClass = errClass
		w.state.LastErrorText = "" // raw provider text never persists (FR-W1-23)
		w.state.Attempt++
		// Real jitter (MC-33): draw the unit from the package-level
		// concurrency-safe source (math/rand/v2) instead of the hardcoded
		// midpoint — a literal 0.5 yields factor exactly 1.0, i.e. zero
		// jitter, so every failure of an attempt count backed off identically.
		w.state.NextAttemptAt = w.now().Add(WatcherBackoff(w.state.Attempt, errClass, rand.Float64())).UTC().Format(time.RFC3339)
	})
}

// nextLastSeenUID applies the MC-31 baseline rule: on a UIDVALIDITY change or
// first run, baseline to UIDNEXT-1 without flagging the backlog as new.
func (w *Watcher) nextLastSeenUID(uidnext, uidvalidity uint32) uint32 {
	if w.state.UIDValidity != uidvalidity || w.state.LastSeenUID == 0 {
		return uidnext - 1
	}
	if uidnext-1 > w.state.LastSeenUID {
		return uidnext - 1
	}
	return w.state.LastSeenUID
}

// applyState mutates state under lock and persists it. A persistence failure
// is logged and reflected in the state's error fields, never fatal for the
// cycle itself.
func (w *Watcher) applyState(fn func()) {
	fn()
	w.state.AgentID = w.cfg.AgentID
	w.state.WorkspaceID = w.cfg.WorkspaceID
	if err := w.save(); err != nil {
		slog.Error("email watcher: persist state failed", "agent_id", w.cfg.AgentID, "error", err)
	}
}

// now returns the (test-overridable) clock.
func (w *Watcher) now() time.Time {
	if w.cfg.Now != nil {
		return w.cfg.Now()
	}
	return time.Now()
}

// load reads the persisted state if present. A missing state file is the
// normal never-ran case (silent); ANY other read failure (permission, EIO,
// is-a-directory) or an unmarshal failure (corrupt/truncated file) is logged
// before the fresh-state fallback so the reset is never silent — the save
// path's slog.Error has a visible mirror here now.
func (w *Watcher) load() {
	b, err := os.ReadFile(w.statePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("email watcher: state load failed; resetting",
				"path", w.statePath, "error", err)
		}
		return
	}
	var st WatcherState
	if err := json.Unmarshal(b, &st); err != nil {
		slog.Warn("email watcher: state load failed; resetting",
			"path", w.statePath, "error", err)
		return
	}
	w.state = st
}

// save persists the state atomically, creating the directory on first write.
func (w *Watcher) save() error {
	b, err := json.Marshal(w.state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(w.statePath), 0o700); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(w.statePath, b, 0o600)
}

// classifyMailError maps a mail transport error to the spec's closed error
// class enum (MC-8): timeout | dns | connect_refused | auth_failed | tls |
// folder_missing | server_error. Structural detection runs first (errors.Is /
// errors.As on the sentinel error types), before the substring sweep: the
// dial wrapper's "dial TLS" text must not turn a refused connection into the
// tls class, and a *net.DNSError is the resolver's own verdict — no substring
// guess needed. The dns TEXT patterns are deliberately name-resolution-only:
// the old bare "resolve" pattern matched the pool's "resolve credentials"
// wrapper and reported a local credential failure as dns — the wrong lead
// for an operator. A credential-resolution failure never reaches the mail
// server, so neither auth_failed nor dns is truthful; MC-8 has no local
// class, and the contract enum is closed, so it takes the honest
// server_error fallback with a distinguishing warning (never the raw text —
// FR-W1-23 redaction discipline).
func classifyMailError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return "timeout"
	}
	var neterr net.Error
	if errors.As(err, &neterr) && neterr.Timeout() {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connect_refused"
	}
	var dnserr *net.DNSError
	if errors.As(err, &dnserr) {
		return "dns"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "authentication"), strings.Contains(msg, "invalid credentials"), strings.Contains(msg, "login failed"):
		// The server rejected presented credentials. Must stay ahead of the
		// generic credential case below: "invalid credentials" is auth.
		return "auth_failed"
	case strings.Contains(msg, "credential"):
		// Local credential-resolution failure (the pool's "resolve
		// credentials" wrapper; the resolver's "credential store locked",
		// "credentials not resolvable"): no dial ever happened. server_error
		// is the closest truthful class in the closed MC-8 enum; this warn
		// keeps the cause distinguishable from a server fault. The raw error
		// text is deliberately NOT logged here (FR-W1-23).
		slog.Warn("email error classified: local credential-resolution failure, stored as server_error")
		return "server_error"
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "lookup"), strings.Contains(msg, "dns error"), strings.Contains(msg, "no addresses for"):
		// Name-resolution failures only. "no addresses for" is
		// resolveHostBounded's empty-answer failure — a genuine resolution
		// failure that carries no *net.DNSError for the structural check.
		return "dns"
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "connect:"):
		return "connect_refused"
	case strings.Contains(msg, "tls"), strings.Contains(msg, "x509"), strings.Contains(msg, "certificate"):
		return "tls"
	case strings.Contains(msg, "folder"), strings.Contains(msg, "mailbox"):
		return "folder_missing"
	default:
		return "server_error"
	}
}

// Mailbox describes one configured, enabled mailbox the watcher polls: its
// owning agent, surfacing workspace, and a Transport to read mail with.
type Mailbox struct {
	AgentID     string
	WorkspaceID string
	Transport   Transport
}

// MailboxProvider supplies the set of mailboxes to poll on each tick. It is a
// function-shaped interface so the caller (gateway) can build live transports
// from current config + resolved credentials without this package importing
// config or credentials.
type MailboxProvider interface {
	Mailboxes() []Mailbox
}

// MailboxProviderFunc adapts a plain func to MailboxProvider.
type MailboxProviderFunc func() []Mailbox

// Mailboxes implements MailboxProvider.
func (f MailboxProviderFunc) Mailboxes() []Mailbox { return f() }

// cycleIfDue runs a cycle unless the mailbox is backing off (next_attempt_at
// in the future). MC-33: no automatic dial while backing off.
func (w *Watcher) cycleIfDue(ctx context.Context, now time.Time) error {
	w.mu.Lock()
	w.load()
	due := true
	if w.state.NextAttemptAt != "" {
		if next, err := time.Parse(time.RFC3339, w.state.NextAttemptAt); err == nil && now.Before(next) {
			due = false
		}
	}
	w.mu.Unlock()
	if !due {
		return nil
	}
	return w.Cycle(ctx)
}

// ErrNoWatcherState is returned by LoadWatcherState when no state has ever
// been saved for the mailbox pair (no state file on disk). Callers check it
// with errors.Is; the returned state is nil in that case.
var ErrNoWatcherState = errors.New("email watcher: no state saved for mailbox pair")

// LoadWatcherState reads one mailbox's saved watcher state. A nil state with
// ErrNoWatcherState means no state has ever been saved for the pair — the
// summary endpoint renders the never-checked shape (round-2 MAJ-019: ok
// never lies about blindness).
func LoadWatcherState(stateDir, agentID, workspaceID string) (*WatcherState, error) {
	p := filepath.Join(stateDir, "email-watch", keyFor(agentID, workspaceID)+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoWatcherState
		}
		return nil, err
	}
	var st WatcherState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return &st, nil
}
