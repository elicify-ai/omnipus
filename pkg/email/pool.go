// pool.go — the W1 read runtime (ADR-20261001 "Mail live access: pooled
// connections, folder discovery and a bounded cache"; spec
// mail-live-access-w1-read-runtime-spec.md §4): ONE application-owned IMAP
// connection manager below the Client facade. It caps the process at
// maxSocketsPerMailbox (2) and maxSocketsGlobal (8) concurrent sockets,
// counting connecting reservations; gives every operation an exclusive lease
// with its requested folder (re-)selected before the first borrower command;
// bounds the acquisition wait at poolAcquireWait (5 s) ending in the typed
// pool-busy outcome; retires poisoned sockets (timeout, cancellation, server
// BYE, protocol failure) with the command reader's termination observed via
// the client library's own Close acknowledgment; releases idle sockets ~2 min
// after their last COMPLETED use and evicts the least-recently-completed
// idle socket — never active work — when a new establishment needs room.
//
// Lock order (FR-W1-9): no manager lock is ever held during network I/O —
// the reservation is taken under mu, released before dial, and the session
// is registered after it. The only waits (reservation polling) happen while
// holding nothing but the caller's context.
//
// Release never issues a folder-CLOSE (the client library's Close is a pure
// transport close), so pending \Deleted flags can never be expunged by a
// pool teardown (MC-W1-14). The only timer this file introduces is the idle
// sweep; there is no mail-data polling of any kind (FR-W1-22).

package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

const (
	// maxSocketsPerMailbox / maxSocketsGlobal are the founder-set socket
	// ceilings (spec §4.2). They count connecting reservations AND
	// established sockets, idle or active.
	maxSocketsPerMailbox = 2
	maxSocketsGlobal     = 8

	// poolAcquireWait bounds the wait for a reservation inside the ceilings
	// (spec §4.4). Exhaustion is the typed pool-busy outcome, never a
	// timeout-class error.
	poolAcquireWait = 5 * time.Second

	// poolReservePoll is the reservation retry cadence while waiting. The
	// wait is a bounded poll (no condition-variable/context coupling), and
	// each retry re-checks both ceilings and the LRU eviction path.
	poolReservePoll = 2 * time.Millisecond

	// sessionIdleTTL is the idle-release window measured from the last
	// COMPLETED use (spec §4.6.1), so a long operation cannot lose its
	// socket mid-flight.
	sessionIdleTTL = 2 * time.Minute

	// defaultIdleSweepInterval is the sweep cadence when SessionsConfig
	// leaves it unset. The sweep is the only timer the pool introduces.
	defaultIdleSweepInterval = 30 * time.Second
)

// ErrPoolBusy is the typed pool-capacity outcome: the demand waited out the
// bounded acquisition window and no reservation became available. It is
// distinct from ErrMailBusy (the account work-slot refusal) and never wraps
// context.DeadlineExceeded (capacity exhaustion is not a command timeout).
var ErrPoolBusy = errors.New("email pool: no free socket within the acquisition window")

// ErrPoolSkipped is the watcher's non-blocking pool skip (spec §4.10.2): the
// reservation attempt found no free capacity and the cycle skipped instead
// of queueing. Like ErrMailSkipped, a skip is not a failure.
var ErrPoolSkipped = errors.New("email pool: no free socket for a non-blocking acquisition")

// ErrFolderAbsent is the structural folder-absence outcome (spec §4.3.3): a
// SELECT answered with the server's [NONEXISTENT]-class response. It rides
// Lease.SelectError; the session stays healthy.
var ErrFolderAbsent = errors.New("email pool: folder structurally absent on the server")

// ErrSessionSourceMissing is the typed missing-wiring error (FR-W1-2): a
// production dial through a client with no injected session source in a
// process where the shared manager has been wired. It never falls back to an
// uncounted per-call dial — the private-dial path this design forbids. The
// nil-source legacy dial remains only for processes where no manager exists
// (pre-injection window) and for tests.
var ErrSessionSourceMissing = errors.New("email transport: no mail session source injected while the shared session manager is wired — inject it via Client.SetSessionSource at the client construction site (w5-integration wave)")

// RevisionRef names what a publication revision is about (spec §4.9). It
// carries identity only — no mail data.
type RevisionRef struct {
	PairKey string
	Folder  string
}

// RevisionSource is the publication-revision seam (spec §3.1, register row
// 10 — W1 is its single publisher). A read captures the revision BEFORE any
// server work and tests freshness BEFORE publishing; w5-integration owns the
// counter and the events that advance it; W1 never stores or advances the
// revision itself.
type RevisionSource interface {
	// Capture returns the revision current at operation start.
	Capture(ctx context.Context, ref RevisionRef) (string, error)
	// IsCurrent reports whether a captured revision is still the current one.
	IsCurrent(ctx context.Context, ref RevisionRef, captured string) bool
}

// SessionsConfig configures the shared session manager.
type SessionsConfig struct {
	// StateDir is the data dir the manager is resolved from (the pool
	// analogue of SharedMailBudget's key).
	StateDir string
	// Now, when set, overrides the clock (idle expiry, wait measurement).
	Now func() time.Time
	// IdleSweepInterval is the idle-expiry sweep cadence; zero uses
	// defaultIdleSweepInterval. This is the pool's only timer.
	IdleSweepInterval time.Duration
	// Credentials resolves a pair's IMAP login at establishment time
	// (username, password). Password text is resolved per establishment and
	// is never part of any pool identity (FR-W1-4).
	Credentials func(pairKey string) (username, password string, err error)
	// Instrument, when set, receives W1's sub-fields of the per-operation
	// instrument record whose full shape w6-proof §6.1 freezes (register
	// row 17): the acquisition wait, the socket delta this operation caused
	// (1 when it established a connection, 0 when it reused an existing
	// one), and the pool outcome in the frozen outcome domain. A coalesced
	// joiner never reaches the pool, so THIS seam never carries its record —
	// the coalescing layer emits it (socket_count=0 plus the frozen
	// shared-flight marker, MC-W1-28's joiner rule) through
	// MailBudget.Instrument, keeping the record sum comparable to the
	// server's connection counter. SharedFlight on the sample is w6 §6.1's
	// frozen member (the joiner marker), not a W1-invented field; the frozen
	// shape still has no established/reused discriminator and W1 adds none
	// (R2-I-2).
	Instrument func(PoolInstrumentSample)
}

// PoolInstrumentSample carries W1's pool sub-fields of the instrument record
// (register row 17). The record shape itself has exactly one publisher —
// w6-proof; this is not a second definition. SharedFlight is w6 §6.1's frozen
// shared_flight member (MC-W1-28's joiner rule binds W1 to carry it): true
// only on a coalesced joiner's record, which the coalescing layer emits with
// socket_count=0 through MailBudget.Instrument — the flight owner's pool
// record carries the socket, keeping MC-P4's socket sum comparable to the
// server's connection counter.
type PoolInstrumentSample struct {
	AcquireWaitMs int64
	SocketCount   int
	Outcome       string // "ok" | "pool_busy" | a classified failure class (w6-proof §6.1 outcome domain)
	SharedFlight  bool   // w6-proof §6.1 shared_flight: true only on a coalesced joiner's record
}

// mailboxIdentity is the pool key (spec §4.2): the agent/workspace pair, the
// configured endpoint, and the non-secret configuration generation. It never
// contains password text. Two pairs that share an endpoint therefore hold
// separate identities and never share a socket.
type mailboxIdentity struct {
	pair       string
	endpoint   string
	generation string
}

func (m mailboxIdentity) key() string {
	return m.pair + "\x00" + m.endpoint + "\x00" + m.generation
}

// poolSession is one established IMAP connection and its pool state. All
// fields are guarded by MailSessions.mu.
type poolSession struct {
	identity      mailboxIdentity
	client        *imapclient.Client
	active        bool // leased to a borrower
	lastCompleted time.Time
}

// mailboxState is one identity's slice of the pool.
type mailboxState struct {
	identity mailboxIdentity
	// count is reservations (dialing) + established sockets, per §4.2.
	count int
	// sockets is established sockets only (OpenSockets' basis).
	sockets int
	// idle holds released, healthy, retained-eligible sessions.
	idle []*poolSession
}

// LeaseRequest describes one pooled operation (spec §3.1).
type LeaseRequest struct {
	// PairKey is the agent/workspace pair (pool identity component).
	PairKey string
	// Endpoint is the IMAP host:port (pool identity component).
	Endpoint string
	// Generation is the non-secret configuration generation (pool identity
	// component; opaque to the pool).
	Generation string
	// Folder is the folder to select for the lease, or "" for a no-folder
	// lease (STATUS-style probes need no selected state — spec §4.3.1).
	Folder string
	// Retain marks the caller as retention-eligible (panel-observer work).
	// Tool and watcher work never retains (spec §4.10.3).
	Retain bool
	// Mutation marks a flag-writing operation: mutations are never
	// coalesced and never automatically replayed (spec §4.5.4).
	Mutation bool
}

// Lease is the exclusive borrower handle (spec §3.1): one socket, owned by
// one operation until Release. The zero Lease is returned alongside errors.
type Lease struct {
	s    *MailSessions
	sess *poolSession
	ctx  context.Context // the acquiring operation's context

	// Generation is the identity generation this lease was acquired under.
	Generation string
	// UIDValidity and NumMessages are the requested folder's SELECT data,
	// available to the borrower before its first command (FR-W1-24 — the
	// same-lease epoch check W2's validation rides).
	UIDValidity uint32
	NumMessages uint32
	// SelectError, when non-nil, is the lease's select outcome: structural
	// folder absence (ErrFolderAbsent) keeps the session healthy; the
	// caller got a usable lease on a socket with no folder selected.
	SelectError error

	pair     string
	folder   string
	mutation bool
	retain   bool

	// abortStop ends the lease's abort watch (armed in finishAcquire). A
	// plain channel — no lock — because Lease is a value type and release is
	// serial by contract.
	abortStop chan struct{}

	// poisoned records a §4.5.1 poison cause releaseLease cannot otherwise
	// observe: the borrower command's own bound (the per-command timeout
	// inside runIMAP) or a cancellation firing while the lease's operation
	// context is still live. A timed-out command may still be in flight on
	// the socket — the command reader survives one slow command, so
	// readerClosed sees nothing — and such a socket is retired at Release,
	// never returned to the idle pool. Set only through noteOperationError.
	poisoned bool

	released bool
}

// Client returns the leased IMAP client. The borrower owns it exclusively
// until Release.
func (l Lease) Client() *imapclient.Client {
	if l.sess == nil {
		return nil
	}
	return l.sess.client
}

// Release ends the lease. A healthy, retention-eligible socket returns to
// the idle pool; anything else (poisoned operation context, terminated
// command reader, retention disabled, no eligible observer, manager closed)
// closes it. Release never issues a folder-CLOSE, so pending \Deleted flags
// survive every teardown path (MC-W1-14).
func (l Lease) Release() {
	if l.s == nil || l.sess == nil || l.released {
		return
	}
	l.released = true
	l.stopAbortWatch()
	l.s.releaseLease(&l)
}

// armAbortWatch poisons the socket the moment the operation's context fires
// (§4.5.1: timeout and cancellation are poison causes): closing the
// transport terminates any parked command and the command reader, so a
// borrower not routing its commands through a context-bound wrapper is still
// bounded by its own deadline, and the release path can never return a
// cancelled socket to the pool.
func (l *Lease) armAbortWatch() {
	done := l.ctx.Done()
	if done == nil {
		return
	}
	stop := make(chan struct{})
	l.abortStop = stop
	go func() {
		select {
		case <-done:
			_ = l.sess.client.Close()
		case <-stop:
		}
	}()
}

// stopAbortWatch disarms the abort watch (healthy release before the
// deadline). If the watch already fired, the context was done and the
// release path retires — the two can never disagree.
func (l *Lease) stopAbortWatch() {
	if l.abortStop == nil {
		return
	}
	select {
	case <-l.abortStop:
	default:
		close(l.abortStop)
	}
}

// noteOperationError records the borrower operation's outcome on the lease
// before Release (§4.5.1): a timeout- or cancellation-class operation error
// poisons the socket even when the lease's own context is still live — the
// per-command bound fired inside runIMAP, which returns its error without
// closing anything, so neither l.ctx.Err() nor readerClosed can see it at
// release. Release consults this marker: a marked lease is retired, never
// returned to the idle pool with the timed-out command still in flight.
func (l *Lease) noteOperationError(err error) {
	if isPoisonOperationError(err) {
		l.poisoned = true
	}
}

// isPoisonOperationError reports whether err is one of §4.5.1's poison
// causes that releaseLease cannot observe through the lease context or the
// command reader: a timeout (a context deadline, or a deadline-flavored
// transport failure) or a cancellation. Server BYE and protocol failures
// terminate the command reader and are caught by readerClosed at release;
// the internal command deadline leaves the reader alive.
func isPoisonOperationError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	return isDeadlineFlavored(err)
}

// PanelPresence is the transport-agnostic observer registry (spec §3.1,
// register row 11 — W1 is its single publisher). Observers bind to the
// authenticated gateway connection's opaque ID — never a caller-supplied
// identity. Retention is decided by the presence count alone; the registry
// carries no mail data and no cache coupling.
type PanelPresence struct {
	mu sync.Mutex
	// observers: connID → observerID → workspaceID. One observer per panel
	// tab; a connection's observers unbind together on teardown.
	observers map[string]map[string]string
}

// NewPanelPresence returns an empty registry.
func NewPanelPresence() *PanelPresence {
	return &PanelPresence{observers: map[string]map[string]string{}}
}

// Bind registers one observer on an authenticated connection.
func (p *PanelPresence) Bind(connID, observerID, workspaceID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.observers[connID] == nil {
		p.observers[connID] = map[string]string{}
	}
	p.observers[connID][observerID] = workspaceID
}

// Unbind removes one observer (a tab close).
func (p *PanelPresence) Unbind(connID, observerID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.observers[connID], observerID)
	if len(p.observers[connID]) == 0 {
		delete(p.observers, connID)
	}
}

// UnbindAll removes every observer of a connection (disconnect, logout,
// workspace exit — the gateway's teardown reaps lost connections).
func (p *PanelPresence) UnbindAll(connID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.observers, connID)
}

// Count reports the observers currently bound for a workspace.
func (p *PanelPresence) Count(workspaceID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.countLocked(workspaceID)
}

func (p *PanelPresence) countLocked(workspaceID string) int {
	n := 0
	for _, byObserver := range p.observers {
		for _, ws := range byObserver {
			if ws == workspaceID {
				n++
			}
		}
	}
	return n
}

// MailSessions is the application-owned connection manager (spec §4.1): one
// process-wide instance per data dir, injected into every client-producing
// path. It is never constructed by a Client.
type MailSessions struct {
	cfg       SessionsConfig
	done      chan struct{}
	closeOnce sync.Once

	mu        sync.Mutex
	mailboxes map[string]*mailboxState
	global    int
	closed    bool

	presence         *PanelPresence
	retentionEnabled bool

	dirtyMu      sync.Mutex
	dirtySink    func(pair, folder string)
	dirtyPending map[string]string // identity key → folder, until a sink is wired
}

// NewMailSessions builds a manager. Production resolves it through
// SharedMailSessions so every injection site shares one instance.
func NewMailSessions(cfg SessionsConfig) *MailSessions {
	if cfg.IdleSweepInterval <= 0 {
		cfg.IdleSweepInterval = defaultIdleSweepInterval
	}
	s := &MailSessions{
		cfg:          cfg,
		done:         make(chan struct{}),
		mailboxes:    map[string]*mailboxState{},
		presence:     NewPanelPresence(),
		dirtyPending: map[string]string{},
	}
	go s.sweepLoop()
	return s
}

// sharedSessions mirrors SharedMailBudget's process-wide resolution: one
// manager per state dir. Creation also marks the process manager-wired — the
// flag FR-W1-2's qualified nil-source rule keys on.
var (
	sharedSessionsMu    sync.Mutex
	sharedSessions      = map[string]*MailSessions{}
	sharedSessionsWired atomic.Bool
)

// SharedMailSessions returns the process-wide manager for one state dir,
// creating it on first use (the pool analogue of SharedMailBudget). The
// first call marks the process as manager-wired: from that moment a
// production dial through a client without an injected source is the typed
// ErrSessionSourceMissing wiring error, never a silent uncounted dial.
func SharedMailSessions(stateDir string, cfg SessionsConfig) *MailSessions {
	sharedSessionsMu.Lock()
	defer sharedSessionsMu.Unlock()
	s, ok := sharedSessions[stateDir]
	if !ok {
		cfg.StateDir = stateDir
		s = NewMailSessions(cfg)
		sharedSessions[stateDir] = s
	}
	sharedSessionsWired.Store(true)
	return s
}

// sessionManagerWired reports whether a shared manager exists in this
// process (FR-W1-2's qualified condition).
func sessionManagerWired() bool { return sharedSessionsWired.Load() }

// Presence returns the observer registry (w5-integration binds the gateway's
// connection teardown and presence frames to it).
func (s *MailSessions) Presence() *PanelPresence { return s.presence }

// EnableRetention turns the retention gate on. It is the production
// integration activation: until w5-integration registers the presence frame
// handlers this stays off and every socket closes after its operation
// (FR-W1-16, the conservative request-scoped default).
func (s *MailSessions) EnableRetention() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retentionEnabled = true
}

// RetentionEnabled reports the conservative default (false until wired).
func (s *MailSessions) RetentionEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retentionEnabled
}

// OpenSockets reports established sockets currently tracked (leased + idle).
// Connecting reservations are not sockets yet and are not counted.
func (s *MailSessions) OpenSockets() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, mb := range s.mailboxes {
		n += mb.sockets
	}
	return n
}

// Close stops the sweep and tears down every idle session. Leases still
// held retire on their Release (the closed manager retains nothing).
func (s *MailSessions) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.mu.Lock()
		s.closed = true
		var clients []*imapclient.Client
		for _, mb := range s.mailboxes {
			for _, sess := range mb.idle {
				clients = append(clients, sess.client)
				mb.count--
				mb.sockets--
				s.global--
			}
			mb.idle = nil
		}
		s.mu.Unlock()
		for _, c := range clients {
			_ = c.Close() // waits for the command reader's termination
		}
	})
}

func (s *MailSessions) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

// Acquire leases one socket for the operation (spec §4.2–4.4): reuse a
// healthy idle session or establish inside the ceilings, with the requested
// folder (re-)selected before the first borrower command. The wait for a
// reservation is bounded by poolAcquireWait and ends in ErrPoolBusy; the
// establishment is bounded by the caller's context and the dial ceiling.
func (s *MailSessions) Acquire(ctx context.Context, req LeaseRequest) (Lease, error) {
	return s.acquire(ctx, req, false)
}

// TryAcquire is the non-blocking acquisition (spec §4.10.2): it takes a
// free idle session or a free reservation immediately — including via LRU
// eviction of an idle socket — and returns ErrPoolSkipped instead of
// waiting. Watcher work never displaces foreground reads.
func (s *MailSessions) TryAcquire(ctx context.Context, req LeaseRequest) (Lease, error) {
	return s.acquire(ctx, req, true)
}

func (s *MailSessions) acquire(ctx context.Context, req LeaseRequest, tryOnly bool) (Lease, error) {
	id := mailboxIdentity{pair: req.PairKey, endpoint: req.Endpoint, generation: req.Generation}
	started := s.now()
	sess, fresh, err := s.takeSession(ctx, id, tryOnly)
	if err != nil {
		if errors.Is(err, ErrPoolBusy) {
			s.emitInstrument(started, 0, "pool_busy")
		}
		return Lease{}, err
	}
	lease, err := s.finishAcquire(ctx, sess, !fresh, req, id)
	if err != nil {
		return Lease{}, err
	}
	sockets := 0
	if fresh {
		sockets = 1 // a reuse adds no connection; the record sum stays comparable to the server's counter
	}
	s.emitInstrument(started, sockets, "ok")
	return lease, nil
}

// takeSession hands out an idle session or establishes a new one under a
// reservation. The bool reports whether the session was freshly established.
func (s *MailSessions) takeSession(ctx context.Context, id mailboxIdentity, tryOnly bool) (*poolSession, bool, error) {
	s.mu.Lock()
	if sess := s.takeIdleLocked(id); sess != nil {
		sess.active = true
		s.mu.Unlock()
		return sess, false, nil
	}
	s.mu.Unlock()

	if tryOnly {
		granted, victim := s.tryReserve(id)
		if victim != nil {
			_ = victim.client.Close()
		}
		if !granted {
			return nil, false, ErrPoolSkipped
		}
	} else {
		if err := s.reserveWait(ctx, id); err != nil {
			return nil, false, err
		}
	}

	sess, err := s.establish(ctx, id)
	if err != nil {
		// A failed dial releases every reservation it held before the
		// error propagates (FR-W1-5).
		s.releaseReservation(id)
		return nil, false, err
	}
	return sess, true, nil
}

// mailboxLocked returns (creating on first use) the identity's state.
func (s *MailSessions) mailboxLocked(id mailboxIdentity) *mailboxState {
	mb, ok := s.mailboxes[id.key()]
	if !ok {
		mb = &mailboxState{identity: id}
		s.mailboxes[id.key()] = mb
	}
	return mb
}

// takeIdleLocked moves the identity's least-recently-completed idle session
// to active, or returns nil. The session keeps its reservation-count slot
// (idle→active is not a count change).
func (s *MailSessions) takeIdleLocked(id mailboxIdentity) *poolSession {
	mb, ok := s.mailboxes[id.key()]
	if !ok || len(mb.idle) == 0 {
		return nil
	}
	oldest := 0
	for i, sess := range mb.idle {
		if sess.lastCompleted.Before(mb.idle[oldest].lastCompleted) {
			oldest = i
		}
	}
	sess := mb.idle[oldest]
	mb.idle = append(mb.idle[:oldest], mb.idle[oldest+1:]...)
	return sess
}

// tryReserve takes one reservation against both ceilings, atomically
// (FR-W1-3). When the global ceiling is full it evicts the globally
// least-recently-completed IDLE socket — never active work (FR-W1-13) —
// and hands the victim back for closing outside the lock (close before
// replacement: no transient over-cap socket).
func (s *MailSessions) tryReserve(id mailboxIdentity) (granted bool, victim *poolSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, nil
	}
	mb := s.mailboxLocked(id)
	if mb.count >= maxSocketsPerMailbox {
		return false, nil
	}
	if s.global < maxSocketsGlobal {
		mb.count++
		s.global++
		return true, nil
	}
	if v := s.evictOldestIdleLocked(); v != nil {
		mb.count++
		s.global++ // the eviction already decremented; the net is unchanged
		return true, v
	}
	return false, nil
}

// evictOldestIdleLocked removes and returns the globally least-recently-
// completed idle socket, decrementing its mailbox and the global count.
func (s *MailSessions) evictOldestIdleLocked() *poolSession {
	var (
		victim    *poolSession
		victimKey string
	)
	for key, mb := range s.mailboxes {
		for _, sess := range mb.idle {
			if victim == nil || sess.lastCompleted.Before(victim.lastCompleted) {
				victim, victimKey = sess, key
			}
		}
	}
	if victim == nil {
		return nil
	}
	mb := s.mailboxes[victimKey]
	for i, sess := range mb.idle {
		if sess == victim {
			mb.idle = append(mb.idle[:i], mb.idle[i+1:]...)
			break
		}
	}
	mb.count--
	mb.sockets--
	s.global--
	return victim
}

// reserveWait waits up to poolAcquireWait for a reservation. Exhaustion is
// the plain typed ErrPoolBusy — never a wrapped context deadline (MC-W1-3).
// The caller's own cancellation still ends the wait with its context error.
func (s *MailSessions) reserveWait(ctx context.Context, id mailboxIdentity) error {
	deadline := time.NewTimer(poolAcquireWait)
	defer deadline.Stop()
	tick := time.NewTicker(poolReservePoll)
	defer tick.Stop()
	for {
		granted, victim := s.tryReserve(id)
		if victim != nil {
			_ = victim.client.Close()
		}
		if granted {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return ErrPoolBusy
		case <-tick.C:
		}
	}
}

// releaseReservation returns one reservation (a failed establishment).
func (s *MailSessions) releaseReservation(id mailboxIdentity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mb, ok := s.mailboxes[id.key()]
	if !ok {
		return
	}
	mb.count--
	s.global--
}

// establish dials + logs in (no folder SELECT — establishment is session
// setup only, spec §4.3.1) with the dial ceiling as a subordinate bound.
// The caller's context can abort the step even while the connection is
// parked on a stalled greeting: the client is registered in a shared slot
// the moment the dial returns, and the cancelled waiter closes it to
// release both the reader goroutine and the TCP socket.
func (s *MailSessions) establish(ctx context.Context, id mailboxIdentity) (*poolSession, error) {
	if s.cfg.Credentials == nil {
		return nil, fmt.Errorf("email pool: no credential resolver configured for session establishment")
	}
	user, pass, err := s.cfg.Credentials(id.pair)
	if err != nil {
		return nil, fmt.Errorf("email pool: resolve credentials for %s: %w", id.pair, err)
	}
	host, _, err := net.SplitHostPort(id.endpoint)
	if err != nil {
		return nil, fmt.Errorf("email pool: endpoint %q: %w", id.endpoint, err)
	}
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}

	dialCtx, cancelDial := context.WithTimeout(ctx, dialTimeout)
	defer cancelDial()

	slot := &establishSlot{}
	type outcome struct {
		client *imapclient.Client
		err    error
	}
	ch := make(chan outcome, 1)
	go func() {
		client, derr := dialIMAPCandidates(dialCtx, id.endpoint, tlsCfg)
		if derr == nil {
			slot.set(client)
			_, lerr := runIMAP(dialCtx, "login", func() (struct{}, error) {
				return struct{}{}, client.Login(user, pass).Wait()
			})
			if lerr != nil {
				// The label is deliberately classifier-neutral ("login failed"
				// would read as auth_failed even when the root cause is a
				// server BYE or a dead connection); the root error in the
				// chain carries the true class — including a real
				// AUTHENTICATIONFAILED rejection (classifyMailError).
				derr = fmt.Errorf("email pool: login: %w", lerr)
			}
		}
		ch <- outcome{client, derr}
	}()

	var o outcome
	select {
	case o = <-ch:
	case <-dialCtx.Done():
		// Cancelled (or the dial ceiling fired): close the registered
		// client so a greeting-parked reader terminates, then collect the
		// goroutine so the socket is closed exactly once and nothing leaks.
		slot.close()
		cancelDial()
		o = <-ch
		if o.client != nil {
			_ = o.client.Close()
		}
		return nil, dialCtx.Err()
	}
	if o.err != nil {
		if o.client != nil {
			_ = o.client.Close()
		}
		return nil, o.err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = o.client.Close()
		return nil, errors.New("email pool: manager closed during establishment")
	}
	mb := s.mailboxLocked(id)
	mb.sockets++ // the reservation converts into the established socket's slot (count is unchanged)
	sess := &poolSession{identity: id, client: o.client, active: true}
	s.mu.Unlock()
	return sess, nil
}

// establishSlot lets a cancelled waiter close the client the establish
// goroutine may still be blocked on.
type establishSlot struct {
	mu     sync.Mutex
	client *imapclient.Client
}

func (sl *establishSlot) set(c *imapclient.Client) {
	sl.mu.Lock()
	sl.client = c
	sl.mu.Unlock()
}

func (sl *establishSlot) close() {
	sl.mu.Lock()
	c := sl.client
	sl.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// finishAcquire performs the lease's select work (spec §4.3): the requested
// folder is (re-)selected and validated before the borrower's first command,
// on a reused OR a freshly established socket. A structural absence is a
// select outcome on a healthy lease; a transport-class select failure on a
// reused idle socket is the dead-idle case — retired and replaced exactly
// once under the same reservation and the caller's remaining deadline; any
// other select failure poisons the socket.
func (s *MailSessions) finishAcquire(ctx context.Context, sess *poolSession, replaceable bool, req LeaseRequest, id mailboxIdentity) (Lease, error) {
	lease := Lease{
		s:          s,
		sess:       sess,
		ctx:        ctx,
		Generation: req.Generation,
		pair:       id.pair,
		folder:     req.Folder,
		mutation:   req.Mutation,
		retain:     req.Retain,
	}
	lease.armAbortWatch()
	if req.Folder == "" {
		return lease, nil
	}
	if err := s.selectLeaseFolder(ctx, &lease, sess.client, req.Folder); err == nil {
		return lease, nil
	} else if isNonexistentFolder(err) {
		lease.SelectError = fmt.Errorf("email pool: select %q: %w (server: %w)", req.Folder, ErrFolderAbsent, err)
		return lease, nil
	} else if replaceable && isConnectionClassError(err) {
		// Dead idle socket: one in-bounds replacement (FR-W1-12). The
		// retirement and the re-reservation happen under ONE lock section so
		// no waiter can steal the freed slot between them; the caller's
		// original deadline still bounds everything.
		s.swapReservation(sess, id)
		sess2, eerr := s.establish(ctx, id)
		if eerr != nil {
			s.releaseReservation(id)
			return Lease{}, eerr
		}
		lease.sess = sess2
		if err2 := s.selectLeaseFolder(ctx, &lease, sess2.client, req.Folder); err2 != nil {
			if isNonexistentFolder(err2) {
				lease.SelectError = fmt.Errorf("email pool: select %q: %w (server: %w)", req.Folder, ErrFolderAbsent, err2)
				return lease, nil
			}
			s.retireSession(sess2)
			return Lease{}, err2
		}
		return lease, nil
	} else {
		// Timeout-class, protocol-class, or a fresh socket that failed its
		// first select: the socket is poisoned (FR-W1-11) — closed and
		// retired, never returned to the idle pool.
		s.retireSession(sess)
		return Lease{}, err
	}
}

// selectLeaseFolder runs the lease's SELECT and records the SELECT data on
// the lease for the borrower's epoch check. The command is bounded by the
// caller's context (and, subordinate to it, the per-command bound).
func (s *MailSessions) selectLeaseFolder(ctx context.Context, lease *Lease, client *imapclient.Client, folder string) error {
	data, err := runIMAP(ctx, "select "+folder, func() (*imap.SelectData, error) {
		return client.Select(folder, nil).Wait()
	})
	if err != nil {
		return err
	}
	lease.UIDValidity = data.UIDValidity
	lease.NumMessages = data.NumMessages
	return nil
}

// retireSession removes an established session from the pool and closes it.
// Close waits for the command reader's termination before returning, so the
// socket is counted gone only after the reader acknowledged (FR-W1-11).
func (s *MailSessions) retireSession(sess *poolSession) {
	s.mu.Lock()
	mb, ok := s.mailboxes[sess.identity.key()]
	if ok {
		mb.count--
		mb.sockets--
		s.global--
	}
	s.mu.Unlock()
	_ = sess.client.Close()
}

// swapReservation retires a dead session and immediately re-takes its slot
// as a fresh reservation, atomically — the dead-idle replacement's establish
// therefore never waits and never blinks the ceilings.
func (s *MailSessions) swapReservation(sess *poolSession, id mailboxIdentity) {
	s.mu.Lock()
	mb, ok := s.mailboxes[sess.identity.key()]
	if ok {
		mb.count--
		mb.sockets--
		s.global--
	}
	nb := s.mailboxLocked(id)
	nb.count++
	s.global++
	s.mu.Unlock()
	_ = sess.client.Close()
}

// releaseLease implements Lease.Release's decision (spec §4.6.4/4.6.5): a
// healthy socket returns to the idle pool only when retention is enabled
// AND at least one eligible panel observer remains for the socket's
// workspace; everything else closes. A poisoned operation (a borrower
// command timeout or cancellation recorded by noteOperationError, a
// cancelled or timed-out operation context, a terminated command reader)
// never returns to the pool.
func (s *MailSessions) releaseLease(l *Lease) {
	sess := l.sess
	poison := l.poisoned || (l.ctx != nil && l.ctx.Err() != nil) || readerClosed(sess.client)

	s.mu.Lock()
	now := s.now()
	keep := !poison && !s.closed && s.retentionEnabled && l.retain &&
		s.presence.countLocked(workspaceFromPair(l.pair)) > 0
	if keep {
		sess.active = false
		sess.lastCompleted = now
		mb := s.mailboxLocked(sess.identity)
		mb.idle = append(mb.idle, sess)
		s.mu.Unlock()
		return
	}
	mb, ok := s.mailboxes[sess.identity.key()]
	if ok {
		mb.count--
		mb.sockets--
	}
	s.global--
	s.mu.Unlock()
	_ = sess.client.Close()
}

// sweepLoop is the pool's only timer (FR-W1-22): it closes sockets idle
// past sessionIdleTTL since their last COMPLETED use. Active work is never
// a sweep candidate — the idle clock cannot expire a socket mid-operation.
func (s *MailSessions) sweepLoop() {
	ticker := time.NewTicker(s.cfg.IdleSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.sweepIdle()
		}
	}
}

func (s *MailSessions) sweepIdle() {
	now := s.now()
	s.mu.Lock()
	var victims []*poolSession
	for _, mb := range s.mailboxes {
		kept := mb.idle[:0]
		for _, sess := range mb.idle {
			if now.Sub(sess.lastCompleted) > sessionIdleTTL {
				victims = append(victims, sess)
				mb.count--
				mb.sockets--
			} else {
				kept = append(kept, sess)
			}
		}
		mb.idle = kept
	}
	s.mu.Unlock()
	for _, v := range victims {
		_ = v.client.Close()
	}
}

// readerClosed reports whether the client's command reader has terminated —
// the observable signature of a server BYE, a protocol failure, or a
// server-side close (spec §4.5).
func readerClosed(c *imapclient.Client) bool {
	if c == nil {
		return true
	}
	select {
	case <-c.Closed():
		return true
	default:
		return false
	}
}

// isConnectionClassError reports whether err is the signature of a dead or
// broken connection — the dead-idle replacement trigger (spec §4.5.3).
// Timeout-class failures are deliberately excluded: they poison without a
// replacement.
func isConnectionClassError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	var neterr net.Error
	return errors.As(err, &neterr) && !neterr.Timeout()
}

// workspaceFromPair derives the presence workspace from the pair key
// ("agentID/workspaceID" — the WorkspaceID suffix). A pair without a
// separator is its own workspace.
func workspaceFromPair(pair string) string {
	if i := lastSlashIndex(pair); i >= 0 {
		return pair[i+1:]
	}
	return pair
}

func lastSlashIndex(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

// MarkPanelMetadataDirty is the watcher dirty-mark signal (spec §3.1,
// register row 13): "folder version changed while closed". It is
// transport-agnostic, carries no mail data, and is never a cache write or a
// refresh trigger by itself — W2/w5-integration consume it at the next
// eligible panel event.
func (s *MailSessions) MarkPanelMetadataDirty(pair, folder string) {
	s.dirtyMu.Lock()
	sink := s.dirtySink
	if sink != nil {
		s.dirtyMu.Unlock()
		sink(pair, folder)
		return
	}
	s.dirtyPending[pair+"\x00"+folder] = folder
	s.dirtyMu.Unlock()
}

// SetPanelDirtySink wires the consumer (w5-integration). Pending marks
// recorded before a sink existed are delivered once, preserving
// consumed-once semantics. A nil sink unwires WITHOUT discarding: buffered
// marks stay parked for the next sink — dropping them here would silently
// lose every change a closed panel never learned of.
func (s *MailSessions) SetPanelDirtySink(sink func(pair, folder string)) {
	s.dirtyMu.Lock()
	s.dirtySink = sink
	if sink == nil {
		s.dirtyMu.Unlock()
		return
	}
	pending := make([][2]string, 0, len(s.dirtyPending))
	seen := map[string]bool{}
	for key, folder := range s.dirtyPending {
		pair := key[:len(key)-len(folder)-1]
		if !seen[key] {
			pending = append(pending, [2]string{pair, folder})
			seen[key] = true
		}
	}
	s.dirtyPending = map[string]string{}
	s.dirtyMu.Unlock()
	for _, p := range pending {
		sink(p[0], p[1])
	}
}

// emitInstrument reports W1's pool sub-fields of the instrument record.
func (s *MailSessions) emitInstrument(started time.Time, sockets int, outcome string) {
	if s.cfg.Instrument == nil {
		return
	}
	waitMs := s.now().Sub(started).Milliseconds()
	if waitMs < 0 {
		waitMs = 0
	}
	s.cfg.Instrument(PoolInstrumentSample{AcquireWaitMs: waitMs, SocketCount: sockets, Outcome: outcome})
}

// ---------------------------------------------------------------------------
// Client facade wiring (spec §3.1 session-source injection, §4.11 one budget
// owner). The injected session source performs NO account-slot acquisition —
// Acquire carries no budget parameter, so the wrapper+client double-acquire
// is unwritable, not merely forbidden by comment (MC-W1-24).
// ---------------------------------------------------------------------------

// SetSessionSource injects the shared session manager (W1 §3.1). It must be
// called before the client's first operation. A nil source (the default)
// keeps the legacy per-call dial in processes where no manager is wired, and
// is the tests-only path once a manager exists (FR-W1-2).
func (c *Client) SetSessionSource(sessions *MailSessions) {
	c.sessions = sessions
}

// SetSessionScope sets the pool-identity components the client cannot derive
// from its account: the owning pair key and the configuration generation.
// Unset, the client pools under the endpoint-only identity. Production
// construction sites (w5-integration) set both.
func (c *Client) SetSessionScope(pairKey, generation string) {
	c.sessionPair = pairKey
	c.sessionGeneration = generation
}

// withMailSession runs fn with a mailbox client: a pooled lease when a
// session source is injected, the legacy per-call dial otherwise (nil source
// in a manager-wired process is the typed wiring error, FR-W1-2).
func (c *Client) withMailSession(ctx context.Context, folder string, mutation bool, fn func(ctx context.Context, client *imapclient.Client, numMessages uint32) error) error {
	if c.sessions == nil {
		if sessionManagerWired() {
			return ErrSessionSourceMissing
		}
		client, selData, err := c.dialIMAP(ctx)
		if err != nil {
			return err
		}
		defer client.Close()
		return fn(ctx, client, selData.NumMessages)
	}
	lease, err := c.sessions.Acquire(ctx, LeaseRequest{
		PairKey:    c.sessionPair,
		Endpoint:   c.sessionEndpoint(),
		Generation: c.sessionGeneration,
		Folder:     folder,
		Retain:     false, // facade work is request-scoped; retention eligibility is w5-integration's validated observer association
		Mutation:   mutation,
	})
	if err != nil {
		return err
	}
	// Release runs in a closure over the lease variable: a plain
	// `defer lease.Release()` snapshots the value receiver HERE, before the
	// operation runs, and a poison recorded afterwards would never reach it.
	defer func() { lease.Release() }()
	if lease.SelectError != nil {
		return lease.SelectError
	}
	opErr := fn(ctx, lease.Client(), lease.NumMessages)
	// §4.5.1: a borrower command timeout or cancellation retires the socket
	// — the timed-out command may still be in flight even though the reader
	// and the lease context are both alive.
	lease.noteOperationError(opErr)
	return opErr
}

func (c *Client) sessionEndpoint() string {
	return fmt.Sprintf("%s:%d", c.acct.IMAPHost, c.acct.IMAPPort)
}

// mailboxStatusTry is the watcher's non-blocking STATUS capability: the same
// one-STATUS probe, but the pool acquisition skips instead of queueing when
// the ceilings are full (spec §4.10.2).
func (c *Client) mailboxStatusTry(ctx context.Context) (int, uint32, uint32, error) {
	return c.mailboxStatus(ctx, true)
}

// mailboxStatus returns the INBOX counters with one STATUS command. With a
// session source it rides a no-folder lease (STATUS needs no selected
// state); the watcher's tryOnly path skips rather than queues. tryOnly is
// the watcher's non-blocking intent — never a test hook.
func (c *Client) mailboxStatus(ctx context.Context, tryOnly bool) (int, uint32, uint32, error) {
	if c.sessions == nil {
		if sessionManagerWired() {
			return 0, 0, 0, ErrSessionSourceMissing
		}
		return c.mailboxStatusDirect(ctx)
	}
	lease, err := c.sessions.acquire(ctx, LeaseRequest{
		PairKey:    c.sessionPair,
		Endpoint:   c.sessionEndpoint(),
		Generation: c.sessionGeneration,
		Folder:     "", // STATUS-style probe: no-folder lease, no SELECT (spec §4.3.1)
		Retain:     false,
		Mutation:   false,
	}, tryOnly)
	if err != nil {
		return 0, 0, 0, err
	}
	// Closure, not `defer lease.Release()`: the deferred release must see
	// noteOperationError's poison mark (see withMailSession).
	defer func() { lease.Release() }()
	unseen, uidnext, uidvalidity, statErr := statusOnClient(ctx, lease.Client())
	lease.noteOperationError(statErr)
	return unseen, uidnext, uidvalidity, statErr
}

// markPanelMetadataDirty forwards the watcher's dirty-mark through the
// client's session source. Unexported capability — same-package watcher use
// only.
func (c *Client) markPanelMetadataDirty(pair, folder string) {
	if c.sessions != nil {
		c.sessions.MarkPanelMetadataDirty(pair, folder)
	}
}
