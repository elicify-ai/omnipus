package email

import (
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
	mu             sync.Mutex
	cfg            WatcherConfig
	statePath      string
	state          WatcherState
	backoffAttempt int
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

// WatcherBackoff is the per-mailbox reconnect backoff (MC-33/B-43).
// auth_failed goes straight to the 15 min cap.
func WatcherBackoff(attempt int, errClass string, randUnit float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	d := watcherBackoffBase << shift
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

// Cycle runs one watcher pass for the mailbox: probe unseen/uidnext/
// uidvalidity, update the state file. It never mutates flags, never creates
// tasks, never starts an agent turn (D20/D27/FR-023).
func (w *Watcher) Cycle(ctx context.Context) error {
	unseen, uidnext, uidvalidity, err := w.probe(ctx)
	if err != nil {
		w.recordFailure(classifyMailError(err), err.Error())
		return err
	}
	w.recordSuccess(unseen, uidnext, uidvalidity)
	return nil
}

// probe reads the mailbox's live counters without touching any flag. The
// STATUS-based path is used whenever the transport exposes it (*Client).
func (w *Watcher) probe(ctx context.Context) (int, uint32, uint32, error) {
	if sp, ok := w.cfg.Transport.(MailboxStatuser); ok {
		return sp.MailboxStatus(ctx)
	}
	msgs, err := w.cfg.Transport.ReadInbox(ctx, InboxOptions{Limit: 25, UnseenOnly: true})
	if err != nil {
		return 0, 0, 0, err
	}

	var maxUID uint32
	for i := range msgs {
		if msgs[i].UID > maxUID {
			maxUID = msgs[i].UID
		}
	}
	return len(msgs), maxUID + 1, 0, nil
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
func (w *Watcher) recordFailure(errClass, errText string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.applyState(func() {
		w.state.State = "error"
		w.state.LastErrorClass = errClass
		w.state.LastErrorText = errText
		w.state.Attempt++
		w.state.NextAttemptAt = w.now().Add(WatcherBackoff(w.state.Attempt, errClass, 0.5)).UTC().Format(time.RFC3339)
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

// load reads the persisted state if present.
func (w *Watcher) load() {
	b, err := os.ReadFile(w.statePath)
	if err != nil {
		return
	}
	var st WatcherState
	if json.Unmarshal(b, &st) != nil {
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
// folder_missing | server_error.
func classifyMailError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "dns"), strings.Contains(msg, "resolve"), strings.Contains(msg, "no such host"):
		return "dns"
	case strings.Contains(msg, "auth"), strings.Contains(msg, "invalid credentials"), strings.Contains(msg, "login failed"):
		return "auth_failed"
	case strings.Contains(msg, "tls"), strings.Contains(msg, "x509"), strings.Contains(msg, "certificate"):
		return "tls"
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "connect:"):
		return "connect_refused"
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
