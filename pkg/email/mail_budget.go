package email

// mail_budget.go — the A8 mail-operation budget (spec §2.3/A8, MC-33, MIN-003,
// edge 16): ONE per-account shared gate for the REST panel path, the agent-tool
// path and the watcher cycles, keyed by account = "host|username". Three
// layers, in order:
//
//  1. backoff-no-dial: reads the SAME persisted watcher state both paths
//     already read (LoadWatcherState + EffectiveState — the ONE derivation
//     point), so the watcher's own backoff bookkeeping and this check can
//     never disagree (founder agreement requirement). retry=true bypasses
//     ONLY this check (the human Retry click); the tool path structurally
//     cannot set it (pkg/tools guard).
//  2. singleflight coalescing keyed by (account, operation, normalized
//     params): identical concurrent refreshes share ONE dial (N tabs = 1
//     login, D29/R2-9). Coalescing requires a NON-EMPTY params map — the RED
//     oracle pins that two identical PARAMLESS calls dial independently
//     (TestMailBudget_WatcherTryAcquireSkipsNonBlocking's holders and
//     TestMailBudget_WatcherBackoffAndCallersAgree's ErrMailBusy subtest both
//     need inFlight==2 for identical paramless ops), so a paramless op cannot
//     coalesce by construction. Production callers always pass a params map.
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
	// Account is the per-account key: "host|username" (AccountKey on
	// *Client). REST/tool/watcher callers all use the same derivation, so
	// the cap and the coalescing are shared across the paths.
	Account string
	// AgentID and WorkspaceID select the watcher state file for the backoff
	// check (the same file the watcher itself reads and writes).
	AgentID     string
	WorkspaceID string
	// Operation is a stable op name (e.g. listMailFolders, read_inbox,
	// watcher_cycle); part of the coalescing key.
	Operation string
	// Params are the operation's normalized arguments; part of the
	// coalescing key. An EMPTY map opts the call out of coalescing (see the
	// file header) — pass a real params map for refresh-shaped ops.
	Params map[string]any
	// Retry is the REST-only human bypass: it skips the backoff check and
	// nothing else. The tool path never sets it.
	Retry bool
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

	mu       sync.Mutex
	accounts map[string]*mailAccountGate
}

// mailAccountGate is one account's slice of the budget: the 2-slot semaphore
// and the singleflight group.
type mailAccountGate struct {
	slots   chan struct{}
	flights singleflight.Group
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
		g = &mailAccountGate{slots: make(chan struct{}, mailBudgetSlotsPerAccount)}
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

// call is the shared core of Call and both CallValue forms.
func call[T any](b *MailBudget, ctx context.Context, req MailBudgetRequest, fn func(context.Context) (T, error)) (T, error) {
	if bf := b.backoffRefusal(req); bf != nil {
		var zero T
		return zero, bf
	}
	gate := b.gateFor(req.Account)
	key := req.flightKey()
	if key == "" {
		// Paramless op: coalescing opt-out (file header). Straight to the
		// semaphore — unshared, so the caller's OWN context is the correct
		// bound.
		return runDialValue(gate, ctx, fn)
	}
	resCh := gate.flights.DoChan(key, func() (any, error) {
		// The flight is SHARED: it must never be hostage to the first
		// caller's cancellation — a tab close or an aborted fetch would
		// spuriously fail every coalesced joiner whose own context is
		// perfectly live. The dial runs on a detached context bounded by the
		// package's existing no-caller-deadline fallback
		// (transport.go::ctxOrCommandDeadline's rule): the first caller's
		// deadline when it set one, else commandTimeout. Each caller keeps
		// its own independent bail-out in the select below.
		flightCtx, cancel := flightContext(ctx)
		defer cancel()
		return runDialValue(gate, flightCtx, fn)
	})
	select {
	case res := <-resCh:
		// Joiners receive the executor's value AND error (standard
		// singleflight semantics; no Forget — a caller whose deadline
		// expires simply stops waiting and leaves the flight to finish for
		// the others).
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
		var zero T
		return zero, fmt.Errorf("%w: %w", ErrMailBusy, ctx.Err())
	}
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

// flightKey builds the coalescing key: (account, operation, normalized
// params). Empty params → "" — the coalescing opt-out.
func (r MailBudgetRequest) flightKey() string {
	if len(r.Params) == 0 {
		return ""
	}
	raw, err := json.Marshal(r.Params)
	if err != nil {
		return ""
	}
	return r.Account + "\x00" + r.Operation + "\x00" + string(raw)
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
