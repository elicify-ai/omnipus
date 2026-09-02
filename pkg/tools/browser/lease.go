// Omnipus — the browser write lease (ADR-072 D1 §14, NORMATIVE)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// leaseWaitTimeout bounds how long an action tool waits for the browser's write
// lease before reporting contention. Config key: tools.browser.lease_wait,
// default 2s, reload-applied through BrowserConfig.LeaseWait.
//
// Relationship to the action-tool CDP timeout (MIN-007): it MUST be strictly
// less than the shortest action-tool timeout. If it were longer, a waiting tool
// could exhaust its own deadline inside the wait and return a CDP timeout
// instead of a deferral — an error where the contract requires a non-error.
//
// THE RELATIONSHIP IS ENFORCED RATHER THAN COMMENTED (FR-023a). The named
// timeout is BrowserConfig.PageTimeout (tools.browser.page_timeout). BOTH values
// are operator-configurable, so an operator could set lease_wait=45s against
// page_timeout=30s and turn every contended call into a CDP timeout ERROR where
// FR-020 requires a non-error deferral. config.ClampLeaseWait clamps the
// configured value to at most half page_timeout at load AND on reload, with a
// WARN naming both keys and both values — a silent clamp leaves the operator
// believing a setting took effect that did not.
//
// WHAT THE CLAMP IS FOR. The loser RETRIES INSIDE THE TOOL and BOTH WRITERS
// EVENTUALLY COMPLETE; a deferral is the OUTCOME PAST THE BOUND, not the goal.
// The clamp's job is to keep the whole retry window inside the tool's own CDP
// deadline, so a contended call finishes its retries and SUCCEEDS rather than
// being killed mid-wait by PageTimeout. It is a budget for retrying, not a
// guarantee of giving up early.
var leaseWaitTimeout = 2 * time.Second

// leaseRetryInterval is the polling interval inside the bounded wait, and
// leaseRetryMaxInterval caps its exponential growth. The retry is a poll rather
// than a queue deliberately: a queue would need an entry per waiter, reclaimed
// on cancellation, and FR-023 promises only a BOUND — never fairness (§14.2
// rule 7). Growth exists so a long hold does not spin; the cap exists so the
// last retry before the bound is never a long way from the bound.
const (
	leaseRetryInterval    = 10 * time.Millisecond
	leaseRetryMaxInterval = 100 * time.Millisecond
)

// leaseClock is the test seam for the bounded wait. Production is the real
// clock; tests substitute a fake.
type leaseClock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realLeaseClock struct{}

func (realLeaseClock) Now() time.Time                         { return time.Now() }
func (realLeaseClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// leaseClockImpl is the live clock every lease wait reads. Package-level and
// swappable so a test can drive the bound deterministically without sleeping.
// Production never reassigns it.
var leaseClockImpl leaseClock = realLeaseClock{}

// writeLeaseTable is the per-manager lease state: which (BrowsingKey, TabOwner)
// pairs are currently held, and by whom.
//
// It has its OWN mutex and is NEVER m.mu (§14.2 rule 5, lock order
// writeLease → pool.mu → m.mu). An action tool blocks on CDP for seconds while
// holding the lease, and the ADR-038 "no lock across a blocking call"
// discipline forbids holding m.mu for that long. tableMu is held only for the
// map read/write itself — never across a wait, never across CDP.
type writeLeaseTable struct {
	tableMu sync.Mutex
	held    map[string]string // sessionKey(key, owner) -> holder agent id
}

// leaseHolderUnknown is what a deferral reports when the holder registered no
// agent id. It is a real, reachable state — FR-081's own case is two TURNS of
// ONE agent on one session, where the agent id does not distinguish them, so a
// deferral naming "the same agent you are" would be worse than useless.
const leaseHolderUnknown = "another turn"

// acquireWrite is the single lease primitive in pkg/tools/browser. There is
// exactly one acquire symbol in the package; a structural test asserts it
// (SC-029).
//
// It is mutual exclusion per (BrowsingKey, TabOwner) PAIR — not per browser
// and not per manager-mutex — held for the duration of ONE action-tool call.
//
// THE PAIR IS THE WHOLE POINT, and a per-BROWSER lease is a DEFECT (D1.9c,
// FR-081). A BrowsingKey is "ws:<workspaceID>" — ONE key for every session on
// the workspace — so a lease scoped to the key alone makes two turns in two
// unrelated chats block each other on a tab neither can see. That is what
// TestLease_TwoSessionsNeverBlockEachOther fails on, and the mistake is
// invisible under load: it looks like contention, not like a bug.
//
// It is reached whenever the resolved TabOwner is TabOwnerWorkspace() — the
// operator's shared tab — OR a TabOwnerSession() set, i.e. on every leased tool
// call (D1.9c, FR-080, FR-081). FR-021's earlier "TabOwnerWorkspace() only"
// trigger is SUPERSEDED: it rested on "no second writer can reach an agent's
// own tab set", which is true across sessions and false WITHIN one — /loop,
// async system-notify (filed as #505) and cron SessionModeMain each start a
// second turn on an already-live session id.
//
// Returns:
//
//	ok=true                 -> caller holds the lease; MUST defer release()
//	ok=false, holder="jim"  -> the RETRY BOUND was exhausted; the caller
//	                           defers via leaseWrite's non-error result
//
// release is ALWAYS non-nil and always idempotent, including on the ok=false
// path: a nil return plus a caller that defers before checking is an immediate
// panic, and this function is called from seven Execute bodies.
//
// RETRY, NOT IMMEDIATE DEFERRAL. Within the bound it retries with backoff,
// cancellable by ctx so a cancelled turn parks no goroutine. The contract the
// caller relies on is that BOTH writers eventually complete under ordinary
// contention; ok=false is the bounded fallback, not the normal outcome.
func (m *BrowserManager) acquireWrite(
	ctx context.Context, key BrowsingKey, owner TabOwner, holderAgentID string,
) (release func(), ok bool, holder string) {
	lk := sessionKey(key, owner)
	table := m.writeLeases()
	deadline := leaseClockImpl.Now().Add(m.leaseWait())
	interval := leaseRetryInterval

	for {
		gotIt, current := table.tryAcquire(lk, holderAgentID)
		if gotIt {
			// holder is empty on the success path: it names who ELSE is
			// holding the lease, and on success that is nobody. Returning the
			// caller's own id here would read as "you are contending with
			// yourself" at every call site that logs it.
			var once sync.Once
			return func() { once.Do(func() { table.release(lk) }) }, true, ""
		}
		if !leaseClockImpl.Now().Before(deadline) {
			return func() {}, false, current
		}
		select {
		case <-ctx.Done():
			return func() {}, false, current
		case <-leaseClockImpl.After(interval):
		}
		if interval < leaseRetryMaxInterval {
			interval *= 2
			if interval > leaseRetryMaxInterval {
				interval = leaseRetryMaxInterval
			}
		}
	}
}

// tryAcquire takes the lease for lk if it is free. Returns the current holder
// when it is not.
func (t *writeLeaseTable) tryAcquire(lk, holderAgentID string) (bool, string) {
	t.tableMu.Lock()
	defer t.tableMu.Unlock()
	if t.held == nil {
		t.held = make(map[string]string, 2)
	}
	if cur, busy := t.held[lk]; busy {
		if cur == "" {
			cur = leaseHolderUnknown
		}
		return false, cur
	}
	t.held[lk] = holderAgentID
	return true, ""
}

// release drops the lease for lk. Idempotent: releasing a lease nobody holds is
// a no-op, so a double release (a defer plus an explicit call) cannot hand the
// browser to a writer that never acquired.
func (t *writeLeaseTable) release(lk string) {
	t.tableMu.Lock()
	defer t.tableMu.Unlock()
	delete(t.held, lk)
}

// isHeld reports whether lk currently has a holder. Test-facing observability
// only — no production path branches on it, because a lease that is free when
// you ask and taken when you act is exactly the check-then-act race
// acquireWrite exists to remove.
func (t *writeLeaseTable) isHeld(lk string) bool {
	t.tableMu.Lock()
	defer t.tableMu.Unlock()
	_, busy := t.held[lk]
	return busy
}

// leaseWrite is the convenience wrapper every action tool actually calls.
//
//	deferred, release := leaseWrite(ctx, mgr, key, owner, agentID, "browser_click")
//	if deferred != nil { return deferred }
//	defer release()
//
// owner is the TabOwner the call already resolved (§14.2 rule 1 step 1) — the
// session's own set or the workspace's. It is a PARAMETER and not re-derived
// here: the lease must be taken on the same set the tool is about to write, and
// a wrapper that resolved its own owner could disagree with its caller.
//
// deferred is nil iff the lease was acquired. When non-nil it is a NON-error
// result whose body is {"deferred": true, "reason": <text naming the holder>}.
// release is never nil in either case.
//
// The reason text deliberately does NOT match controlledResult's. Both produce
// the same {"deferred": true} shape, but they mean different things: the
// human-control branch means STOP, a person is present; this branch means the
// call ran out of its retry budget behind another turn and retrying is the
// right response. A model that cannot tell them apart takes the wrong action on
// one of them.
func leaseWrite(
	ctx context.Context, m *BrowserManager, key BrowsingKey, owner TabOwner,
	agentID, toolName string,
) (deferred *tools.ToolResult, release func()) {
	rel, ok, holder := m.acquireWrite(ctx, key, owner, agentID)
	if ok {
		return nil, rel
	}
	if holder == "" {
		holder = leaseHolderUnknown
	}
	reason := fmt.Sprintf(
		"another turn (%s) is currently driving this browser tab and did not finish within "+
			"the wait budget — retry this call", holder,
	)
	body, err := json.Marshal(map[string]any{"deferred": true, "reason": reason})
	if err != nil {
		body = []byte(fmt.Sprintf(`{"deferred":true,"reason":%q}`, reason))
	}
	return tools.NewToolResult(fmt.Sprintf("%s: %s", toolName, string(body))), rel
}
