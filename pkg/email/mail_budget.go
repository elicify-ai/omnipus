package email

// mail_budget.go — the A8 mail-operation budget (spec §2.3/A8, MC-33, MIN-003,
// edge 16): ONE per-account shared gate for the REST panel path, the agent-tool
// path and the watcher cycles, keyed by account = "host:port|username"
// (Client.AccountKey — the port is part of the key; the account's role after
// the W1 identity correction is contention only). Three layers, in order:
//
//  1. backoff-no-dial: reads the SAME persisted watcher state both paths
//     already read (LoadWatcherState + EffectiveState — the ONE derivation
//     point), so the watcher's own backoff bookkeeping and this check can
//     never disagree (founder agreement requirement). retry=true bypasses
//     ONLY this check (the human Retry click); the tool path structurally
//     cannot set it (pkg/tools guard).
//  2. singleflight coalescing keyed by the FULL read-coalescing identity
//     (W1 §4.8, grill I-01): pair + generation + operation + normalized
//     params + purpose. Identical concurrent refreshes share ONE dial
//     (N tabs = 1 login, D29/R2-9); different pairs, generations, purposes
//     or arguments never share. Coalescing requires a NON-EMPTY params map —
//     the RED oracle pins that two identical PARAMLESS calls dial
//     independently — and Purpose "mutation" opts out entirely (identical
//     mutations are two operations). Production callers always pass a params
//     map.
//  3. a 2-per-account semaphore: overflow queues under the caller's context
//     deadline, then fails with ErrMailBusy — never dials.
//
// The gate is shared process-wide through SharedMailBudget (keyed by the
// watcher state dir — every caller that resolves with the same data dir gets
// the SAME budget instance, so the panel, the tools and the watcher contend
// on one 2-per-account cap).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// MailBudgetRequest describes one gated mail operation.
type MailBudgetRequest struct {
	// Account is the per-account contention key: "host:port|username"
	// (Client.AccountKey). REST/tool/watcher callers all use the same
	// derivation, so the 2-per-account cap is shared across the paths. After
	// the W1 identity correction (§4.8, grill I-01) the account key has ONLY
	// this contention role — it never decides result sharing.
	Account string
	// AgentID and WorkspaceID select the watcher state file for the backoff
	// check (the same file the watcher itself reads and writes) and form the
	// PAIR component of the read-coalescing identity: results never share
	// across pairs.
	AgentID     string
	WorkspaceID string
	// Operation is a stable op name (e.g. listMailFolders, read_inbox,
	// watcher_cycle); part of the coalescing identity.
	Operation string
	// Params are the operation's normalized arguments; part of the
	// coalescing identity. An EMPTY map opts the call out of coalescing (see
	// the file header) — pass a real params map for refresh-shaped ops.
	Params map[string]any
	// Retry is the REST-only human bypass: it skips the backoff check and
	// nothing else (never the semaphore, never the pool ceilings). The tool
	// path never sets it.
	Retry bool
	// Generation is the non-secret configuration/folder-mapping generation
	// (W1 §4.8 identity component). w5-integration implements the source
	// (register row 12); W1 treats it as opaque. An old generation never
	// joins or receives a new generation's flight.
	Generation string
	// Purpose is the live-versus-cache purpose of the operation: "read_live",
	// "read_cache", "mutation" or "watcher". A cache-shaped read and a live
	// refresh for the same folder are DIFFERENT flights — a live refresh can
	// never be answered by joining a cache-shaped flight (grill I-01).
	// Purpose "mutation" opts the call out of coalescing entirely.
	Purpose string
	// TotalReadDeadline bounds the whole pooled read — account-slot queue +
	// pool wait + establish + commands (§4.4; the founder-accepted 45 s is
	// the ceiling). Zero keeps today's bounds; a shorter caller deadline
	// always wins.
	TotalReadDeadline time.Duration
	// Revision, when set, captures the folder publication revision BEFORE
	// any server work and gates the result's publication on freshness
	// (§4.9): a read whose captured revision is no longer current at
	// completion fails with ErrRevisionSuperseded and publishes nothing.
	// The captured revision also rides the flight identity, so a refresh
	// issued after a revision-advancing event never joins a superseded
	// flight (§4.9.4).
	Revision RevisionSource
	// Publish, when set (with Revision), receives the flight's value exactly
	// once — only when the captured revision is still current at completion.
	// It is the publication seam the superseded determination guards.
	Publish func(value any)
}

// ErrMailBusy is the semaphore-deadline refusal: the operation queued past
// the caller's context deadline and never dialed.
var ErrMailBusy = errors.New("mail budget: operation queued past its deadline")

// ErrMailSkipped is the watcher's countable non-blocking skip: the cycle did
// not run (and did NOT count as a failure — it must not advance backoff).
var ErrMailSkipped = errors.New("mail budget: watcher cycle skipped")

// MailBackoffError is the typed backoff refusal. NextAttemptAt is the raw
// RFC3339 string from the persisted watcher state, echoed verbatim.
type MailBackoffError struct {
	LastErrorClass string
	NextAttemptAt  string
}

func (e *MailBackoffError) Error() string {
	return fmt.Sprintf("mail budget: account in backoff until %s (last error: %s)",
		e.NextAttemptAt, e.LastErrorClass)
}

// MailBudget is the per-account gate. One instance is shared process-wide
// (SharedMailBudget); the per-account state is created lazily per account.
type MailBudget struct {
	stateDir string

	// Instrument, when set, receives the coalesced joiner's W1 pool
	// sub-fields of the instrument record (MC-W1-28, register row 17 — the
	// mechanical joiner rule W1 §3.1 assigns to THIS layer): a joiner never
	// reaches the pool, so its record carries socket_count=0 plus the
	// w6-proof §6.1 frozen shared_flight marker, keeping MC-P4's socket sum
	// comparable to the server's connection counter. It delivers the same
	// PoolInstrumentSample the pool seam uses — one record shape, one
	// publisher (FR-W1-25). Set before the first gated call; never
	// reassigned afterwards (the emit path reads it unlocked).
	Instrument func(PoolInstrumentSample)

	mu       sync.Mutex
	accounts map[string]*mailAccountGate
}

// mailAccountGate is one account's slice of the budget: the 2-slot semaphore,
// the singleflight group, and the flight-claim registry the joiner rule's
// detection half reads.
type mailAccountGate struct {
	slots   chan struct{}
	flights singleflight.Group

	// joinMu guards joinFlight. Claim and DoChan happen under ONE hold of it,
	// so claim order is flight-creation order: the first claimant is always
	// the caller whose fn singleflight runs.
	joinMu     sync.Mutex
	joinFlight map[string]struct{}
}

const mailBudgetSlotsPerAccount = 2

// NewMailBudget builds a budget reading watcher state under stateDir (the
// data dir: <stateDir>/email-watch/<pair>.json).
func NewMailBudget(stateDir string) *MailBudget {
	return &MailBudget{stateDir: stateDir, accounts: map[string]*mailAccountGate{}}
}

// sharedBudgets caches one budget per state dir so the gateway REST handlers,
// the agent tools and the watcher set resolve to the SAME instance without
// deep constructor plumbing.
var (
	sharedBudgetsMu sync.Mutex
	sharedBudgets   = map[string]*MailBudget{}
)

// SharedMailBudget returns the process-wide budget for one state dir,
// creating it on first use. Boot, reload, tool registration and the REST
// handlers all resolve through this, so all three dialing paths share one
// 2-per-account cap and one coalescing map.
func SharedMailBudget(stateDir string) *MailBudget {
	sharedBudgetsMu.Lock()
	defer sharedBudgetsMu.Unlock()
	b, ok := sharedBudgets[stateDir]
	if !ok {
		b = NewMailBudget(stateDir)
		sharedBudgets[stateDir] = b
	}
	return b
}

// gateFor returns (creating on first use) the gate for one account.
func (b *MailBudget) gateFor(account string) *mailAccountGate {
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.accounts[account]
	if !ok {
		g = &mailAccountGate{slots: make(chan struct{}, mailBudgetSlotsPerAccount), joinFlight: map[string]struct{}{}}
		b.accounts[account] = g
	}
	return g
}

// backoffRefusal returns the typed refusal when the account pair is inside
// its backoff window and the request does not carry the human Retry bypass.
// The check reads the SAME persisted state the watcher writes, and derives
// "in backoff" through the SAME EffectiveState rule the summary endpoint
// renders — the two can never disagree.
func (b *MailBudget) backoffRefusal(req MailBudgetRequest) *MailBackoffError {
	if req.Retry {
		return nil
	}
	if req.AgentID == "" || req.WorkspaceID == "" {
		// Fail-open is the accepted stance for an absent/unreadable state
		// file — but an empty pair never matches any state file the watcher
		// writes, so the check would no-op invisibly. Make the
		// misconfiguration observable; the fail-open behavior is unchanged.
		slog.Warn("mail budget: empty agent_id or workspace_id — backoff check fail-open (no state file exists for the empty pair)",
			"operation", req.Operation)
	}
	st, err := LoadWatcherState(b.stateDir, req.AgentID, req.WorkspaceID)
	if err != nil {
		// No state yet (ErrNoWatcherState — the normal never-ran case) stays
		// silent. Any OTHER failure (corrupt JSON, permission) still fails
		// open — the accepted stance, the gate's job is politeness — but the
		// fail-open must be observable (round-8 F4, FR-018/FR-036): a corrupt
		// email-watch/<pair>.json silently removes ALL MC-33 politeness
		// gating while every surface renders normal. One WARN naming the
		// unreadable state, mirroring the summary endpoint's state_unreadable
		// family; the returned nil is unchanged.
		if !errors.Is(err, ErrNoWatcherState) {
			slog.Warn("mail budget: watcher state unreadable — backoff check fail-open",
				"error", err, "operation", req.Operation, "state", "unreadable")
		}
		return nil
	}
	if st.EffectiveState(time.Now()) == "backoff" {
		return &MailBackoffError{
			LastErrorClass: st.LastErrorClass,
			NextAttemptAt:  st.NextAttemptAt,
		}
	}
	return nil
}

// Call runs fn through all three layers, blocking: backoff check (skipped
// when req.Retry), singleflight coalescing for params-carrying ops, then the
// 2-per-account semaphore, queueing under ctx and failing ErrMailBusy on
// deadline.
func (b *MailBudget) Call(ctx context.Context, req MailBudgetRequest, fn func(context.Context) error) error {
	_, err := call(b, ctx, req, func(c context.Context) (any, error) {
		return nil, fn(c)
	})
	return err
}

// CallValue is the data-bearing sibling of Call for ops whose dial produces a
// result the caller needs (the RED pack drives it as the any-typed entry).
// The TYPE-SAFE form for production call sites is the package-level generic
// CallValue[T] below: a Go method cannot carry type parameters, so the
// any-typed method is retained only because TestMailBudget_SingleflightKey
// IncludesParams pins the method shape — typed callers should migrate to the
// function.
func (b *MailBudget) CallValue(ctx context.Context, req MailBudgetRequest, fn func(context.Context) (any, error)) (any, error) {
	return CallValue(b, ctx, req, fn)
}

// CallValue is the generic, type-safe form of the data-bearing budget entry:
// on success it returns the dial's value AS T, so a result-shape drift is a
// compile error at the call site, not a silently empty agent response. Go
// methods cannot carry type parameters, hence the explicit budget argument.
// It behaves identically to Call except the flight's fn returns a value, and
// COALESCED JOINERS RECEIVE THE EXECUTOR'S VALUE — with an error-only fn the
// joiner could never see the dial's data and would serve an empty response,
// the silent-failure shape this entry exists to prevent. Identical keys
// (account+operation+params) dial the identical read, so sharing the value is
// semantically exact.
func CallValue[T any](b *MailBudget, ctx context.Context, req MailBudgetRequest, fn func(context.Context) (T, error)) (T, error) {
	return call(b, ctx, req, fn)
}

// call is the shared core of Call and both CallValue forms. Order (FR-W1-9):
// backoff gate → total-read-deadline bound → revision capture (before any
// server work, §4.9.1) → coalescing join under the FULL identity → account
// slot.
func call[T any](b *MailBudget, ctx context.Context, req MailBudgetRequest, fn func(context.Context) (T, error)) (T, error) {
	if bf := b.backoffRefusal(req); bf != nil {
		var zero T
		return zero, bf
	}
	ctx, cancelTotal := boundTotalReadDeadline(ctx, req)
	defer cancelTotal()

	// Capture the publication revision BEFORE any server work (§4.9.1). The
	// captured value rides the flight identity, so a refresh issued after a
	// revision-advancing event cannot join a flight captured earlier
	// (§4.9.4) — I-01 and I-02 compose structurally.
	var (
		ref      RevisionRef
		captured string
	)
	if req.Revision != nil {
		ref = revisionRefFor(req)
		c, cerr := req.Revision.Capture(ctx, ref)
		if cerr != nil {
			var zero T
			return zero, fmt.Errorf("mail budget: capture publication revision: %w", cerr)
		}
		captured = c
	}

	// exec wraps the executor with the publication gate: the value is
	// published (once) only when the captured revision is still current at
	// completion; a superseded read publishes nothing anywhere and returns
	// the superseded determination instead of its stale rows (§4.9.3).
	exec := func(c context.Context) (T, error) {
		v, err := fn(c)
		if err != nil {
			var zero T
			return zero, err
		}
		if req.Revision != nil {
			if !req.Revision.IsCurrent(c, ref, captured) {
				var zero T
				return zero, fmt.Errorf("mail budget: %w", ErrRevisionSuperseded)
			}
			if req.Publish != nil {
				req.Publish(v)
			}
		}
		return v, nil
	}

	gate := b.gateFor(req.Account)
	key := req.flightKey()
	if key == "" {
		// Paramless op or mutation: coalescing opt-out (file header). Straight
		// to the semaphore — unshared, so the caller's OWN context is the
		// correct bound.
		return runDialValue(gate, ctx, exec)
	}
	if req.Revision != nil {
		key = key + "\x00" + captured
	}
	// Coalescing join. The joiner rule (W1 §3.1, MC-W1-28, register row 17)
	// is applied HERE — the joiner never reaches the pool, so the pool seam
	// cannot see it. The claim and the DoChan run under ONE hold of joinMu,
	// so claim order equals flight-creation order: the first claimant is
	// always the caller whose fn singleflight executes (the flight owner,
	// whose dial the pool seam records); every later arrival while the key
	// stays claimed is a joiner and records its own
	// socket_count=0+shared_flight marker (emitJoiner below). The owner's fn
	// retires the claim when the dial truly ends, so a later caller starts a
	// fresh flight instead of being counted as a joiner.
	started := time.Now()
	var (
		joiner bool
		resCh  <-chan singleflight.Result
	)
	gate.joinMu.Lock()
	if _, ok := gate.joinFlight[key]; ok {
		joiner = true
	} else {
		gate.joinFlight[key] = struct{}{}
	}
	resCh = gate.flights.DoChan(key, func() (any, error) {
		// The flight is SHARED: it must never be hostage to the first
		// caller's cancellation — a tab close or an aborted fetch would
		// spuriously fail every coalesced joiner whose own context is
		// perfectly live. The dial runs on a detached context bounded by the
		// package's existing no-caller-deadline fallback
		// (transport.go::ctxOrCommandDeadline's rule): the first caller's
		// deadline when it set one, else commandTimeout. Each caller keeps
		// its own independent bail-out in the select below.
		//
		// Runs at most once per flight — in the claimant's DoChan (claim
		// order == creation order under joinMu) — so this unconditional
		// release always retires THIS flight's own claim. The singleflight
		// map's own delete lags this release by its post-fn epilogue: a
		// caller claiming inside that lag joins the just-finished call's
		// result while labeled owner and emits no joiner record — a
		// nanoseconds-wide undercount of one operation's record, never a
		// socket-sum error (the dial is still counted exactly once).
		defer gate.releaseFlightKey(key)
		flightCtx, cancel := flightContext(ctx)
		defer cancel()
		return runDialValue(gate, flightCtx, exec)
	})
	gate.joinMu.Unlock()
	select {
	case res := <-resCh:
		// Joiners receive the executor's value AND error (standard
		// singleflight semantics; no Forget — a caller whose deadline
		// expires simply stops waiting and leaves the flight to finish for
		// the others).
		if joiner {
			outcome := "ok"
			if res.Err != nil {
				outcome = classifyMailError(res.Err)
			}
			b.emitJoiner(started, outcome)
		}
		if res.Err != nil {
			var zero T
			return zero, res.Err
		}
		if res.Val == nil {
			var zero T
			return zero, nil
		}
		v, ok := res.Val.(T)
		if !ok {
			// The same flight key means the same operation, so a wrong
			// result type is a caller bug — fail loudly here rather than
			// serve a silently empty result downstream.
			var zero T
			return zero, fmt.Errorf("mail budget: coalesced flight returned %T, want %T", res.Val, zero)
		}
		return v, nil
	case <-ctx.Done():
		// Per-caller bail-out: ONLY this caller's own context ends its wait;
		// the shared flight itself continues for the others.
		if joiner {
			// The joiner's operation still ended — record it (w6 §6.1:
			// emitted on success and failure alike) with the seam's
			// bounded-refusal class, the same condition the pool seam maps
			// to pool_busy for a never-dialed operation.
			b.emitJoiner(started, "pool_busy")
		}
		var zero T
		return zero, fmt.Errorf("%w: %w", ErrMailBusy, ctx.Err())
	}
}

// releaseFlightKey retires one flight claim (the owner's fn defer; see the
// claim site in call for the ordering that makes the claimant always the
// flight's executor).
func (g *mailAccountGate) releaseFlightKey(key string) {
	g.joinMu.Lock()
	delete(g.joinFlight, key)
	g.joinMu.Unlock()
}

// emitJoiner delivers the coalesced joiner's W1 pool sub-fields of the
// instrument record (MC-W1-28, register row 17) to MailBudget.Instrument:
// socket_count=0 (a joiner never dials — the flight owner's pool record
// carries the socket) plus the w6-proof §6.1 shared_flight marker, with the
// joiner's own measured queue time inside the budget (§6.1: acquire_wait_ms
// is measured, never inferred). A nil sink stays silent, exactly like the
// pool seam. Outcome is the caller's pre-mapped safe class.
func (b *MailBudget) emitJoiner(started time.Time, outcome string) {
	if b.Instrument == nil {
		return
	}
	waitMs := time.Since(started).Milliseconds()
	if waitMs < 0 {
		waitMs = 0
	}
	b.Instrument(PoolInstrumentSample{
		AcquireWaitMs: waitMs,
		SocketCount:   0,
		Outcome:       outcome,
		SharedFlight:  true,
	})
}

// totalReadDeadline is the founder-accepted total read-work ceiling (§4.4):
// the whole pooled read — account-slot queue, pool wait, establish, and all
// commands — finishes inside it. Numerically it coincides with the existing
// per-command commandTimeout; the two are distinct clocks and keep distinct
// names.
const totalReadDeadline = 45 * time.Second

// boundTotalReadDeadline applies the request's total read deadline as a
// ceiling: a caller-supplied shorter deadline always wins, and the founder
// bound is never an extension. Zero keeps today's bounds (watcher cycles and
// callers that do not stamp it are unchanged).
func boundTotalReadDeadline(ctx context.Context, req MailBudgetRequest) (context.Context, context.CancelFunc) {
	if req.TotalReadDeadline <= 0 {
		return ctx, func() {}
	}
	bound := req.TotalReadDeadline
	if bound > totalReadDeadline {
		bound = totalReadDeadline
	}
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= bound {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, bound)
}

// revisionRefFor derives the seam's reference from the request: pair plus
// folder (the folder parameter when the operation carries one).
func revisionRefFor(req MailBudgetRequest) RevisionRef {
	folder := ""
	if f, ok := req.Params["folder"].(string); ok {
		folder = f
	}
	return RevisionRef{PairKey: req.AgentID + "/" + req.WorkspaceID, Folder: folder}
}

// flightContext detaches the shared flight from the first caller's
// cancellation and bounds it by the package's existing no-caller-deadline
// fallback (transport.go::ctxOrCommandDeadline): carry the first caller's
// deadline when it set one, else commandTimeout — no new timeout value is
// invented.
func flightContext(ctx context.Context) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(ctx)
	if dl, ok := ctx.Deadline(); ok {
		return context.WithDeadline(detached, dl)
	}
	return context.WithTimeout(detached, commandTimeout)
}

// TryCall is the watcher's non-blocking entry: it refuses like Call during
// backoff (surfaced as ErrMailSkipped — a skip is not a cycle failure), and
// with both slots held it returns ErrMailSkipped immediately instead of
// queueing. It never coalesces (a cycle is not a refresh).
func (b *MailBudget) TryCall(ctx context.Context, req MailBudgetRequest, fn func(context.Context) error) error {
	if bf := b.backoffRefusal(req); bf != nil {
		// Multi-%w wrap: both stay extractable — ErrMailSkipped via errors.Is
		// (the watcher's countable skip) and the typed *MailBackoffError via
		// errors.As (LastErrorClass / NextAttemptAt preserved for a future
		// TryCall caller).
		return fmt.Errorf("mail budget: %s skipped while account in backoff: %w: %w", req.Operation, ErrMailSkipped, bf)
	}
	gate := b.gateFor(req.Account)
	select {
	case gate.slots <- struct{}{}:
		defer func() { <-gate.slots }()
		return fn(ctx)
	default:
		return fmt.Errorf("mail budget: %s skipped, no free slot: %w", req.Operation, ErrMailSkipped)
	}
}

// runDialValue acquires one of the account's two slots, queueing under ctx,
// and runs the data-bearing dial fn. Deadline exceeded while queued →
// ErrMailBusy, never a dial.
func runDialValue[T any](g *mailAccountGate, ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
		return fn(ctx)
	case <-ctx.Done():
		return zero, fmt.Errorf("%w: %w", ErrMailBusy, ctx.Err())
	}
}

// ErrRevisionSuperseded is the superseded-read determination (§4.9.3): the
// read's captured publication revision was no longer current at completion,
// so its result is discarded — no rows into any memory cache, no disk
// snapshot write, no timestamp advance, no frontend state update.
var ErrRevisionSuperseded = errors.New("mail budget: read superseded by a newer publication revision")

// flightKey builds the read-coalescing identity (W1 §4.8, grill correction
// I-01): the agent/workspace PAIR, the configuration generation, the
// operation, the normalized (JSON-marshaled) arguments, and the live-versus-
// cache purpose. The account key ("host:port|username") is deliberately
// ABSENT: it keys the two-slot semaphore — its contention role — and never
// decides result sharing, so two pairs on one account can never share one
// flight. Empty params → "" (the coalescing opt-out, preserved); Purpose
// "mutation" → "" (identical mutations are two operations, never one shared
// flight — §4.8). JSON object marshaling sorts map keys, so argument
// insertion order does not change identity.
func (r MailBudgetRequest) flightKey() string {
	if len(r.Params) == 0 || r.Purpose == "mutation" {
		return ""
	}
	raw, err := json.Marshal(r.Params)
	if err != nil {
		return ""
	}
	return r.AgentID + "\x00" + r.WorkspaceID + "\x00" + r.Generation + "\x00" + r.Operation + "\x00" + r.Purpose + "\x00" + string(raw)
}

// AccountKeyer is the optional capability exposing an account's per-account
// budget key ("host|username"). Implemented by *Client.
type AccountKeyer interface {
	AccountKey() string
}

// accountKeyOf derives the budget key from a transport: real *Client
// transports carry their account key; test stubs fall back to the pair key so
// gating still applies (under a synthetic account), never disappears.
func accountKeyOf(transport Transport, agentID, workspaceID string) string {
	if ak, ok := transport.(AccountKeyer); ok {
		return ak.AccountKey()
	}
	return "agent:" + agentID + "|ws:" + workspaceID
}
