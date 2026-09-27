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
	st, err := LoadWatcherState(b.stateDir, req.AgentID, req.WorkspaceID)
	if err != nil {
		// No state yet (the normal case) or an unreadable file: not in
		// backoff. The gate's job is politeness, not correctness — the
		// dial itself surfaces any real problem.
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
	_, err := b.call(ctx, req, func(c context.Context) (any, error) {
		return nil, fn(c)
	})
	return err
}

// CallValue is the data-bearing sibling of Call, for ops whose dial produces
// a result the caller needs (the REST panel reads, the agent read tools). It
// behaves identically to Call except the flight's fn returns a value, and
// COALESCED JOINERS RECEIVE THE EXECUTOR'S VALUE — with an error-only fn the
// joiner could never see the dial's data and would serve an empty response,
// the silent-failure shape this method exists to prevent. Identical keys
// (account+operation+params) dial the identical read, so sharing the value is
// semantically exact.
func (b *MailBudget) CallValue(ctx context.Context, req MailBudgetRequest, fn func(context.Context) (any, error)) (any, error) {
	return b.call(ctx, req, fn)
}

// call is the shared core of Call and CallValue.
func (b *MailBudget) call(ctx context.Context, req MailBudgetRequest, fn func(context.Context) (any, error)) (any, error) {
	if bf := b.backoffRefusal(req); bf != nil {
		return nil, bf
	}
	gate := b.gateFor(req.Account)
	key := req.flightKey()
	if key == "" {
		// Paramless op: coalescing opt-out (file header). Straight to the
		// semaphore.
		return gate.runDialValue(ctx, fn)
	}
	resCh := gate.flights.DoChan(key, func() (any, error) {
		return gate.runDialValue(ctx, fn)
	})
	select {
	case res := <-resCh:
		// Joiners receive the executor's value AND error (standard
		// singleflight semantics; no Forget — a caller whose deadline
		// expires simply stops waiting and leaves the flight to finish for
		// the others).
		return res.Val, res.Err
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %v", ErrMailBusy, ctx.Err())
	}
}

// TryCall is the watcher's non-blocking entry: it refuses like Call during
// backoff (surfaced as ErrMailSkipped — a skip is not a cycle failure), and
// with both slots held it returns ErrMailSkipped immediately instead of
// queueing. It never coalesces (a cycle is not a refresh).
func (b *MailBudget) TryCall(ctx context.Context, req MailBudgetRequest, fn func(context.Context) error) error {
	if bf := b.backoffRefusal(req); bf != nil {
		return fmt.Errorf("mail budget: %s skipped while account in backoff: %w", req.Operation, ErrMailSkipped)
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

// runDial acquires one of the account's two slots, queueing under ctx, and
// runs fn. Deadline exceeded while queued → ErrMailBusy, never a dial.
func (g *mailAccountGate) runDial(ctx context.Context, fn func(context.Context) error) error {
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
		return fn(ctx)
	case <-ctx.Done():
		return fmt.Errorf("%w: %v", ErrMailBusy, ctx.Err())
	}
}

// runDialValue is runDial for a data-bearing dial.
func (g *mailAccountGate) runDialValue(ctx context.Context, fn func(context.Context) (any, error)) (any, error) {
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
		return fn(ctx)
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %v", ErrMailBusy, ctx.Err())
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
