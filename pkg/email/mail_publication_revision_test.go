package email

// W1 §4.9, US-6/AS-4/5, MC-W1-17/18, CX-6; landing register row 10/R-4.
// W1 owns capture/compare/publication suppression, not W2's caches or W5's
// revision counter. This test-owned counter implements that external seam.
// A real IMAP mutation supplies Seen=true; the observable published flags and
// validation time must survive the late pre-mutation response unchanged.
//
// RevisionRef and MailBudgetRequest.Revision/Publish are indicative binding
// assumptions inherited from the unfinished pack; §3.1 freezes semantics,
// not these exact Go signatures. They are not implemented yet. A compiler
// error is a BLOCKER, never a claim of behavioral RED or a mutation kill.

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type countingRevisionSource struct {
	mu     sync.Mutex
	rev    uint64
	events []string
}

func (r *countingRevisionSource) Capture(context.Context, RevisionRef) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "capture")
	return strconv.FormatUint(r.rev, 10), nil
}

func (r *countingRevisionSource) IsCurrent(_ context.Context, _ RevisionRef, captured string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return captured == strconv.FormatUint(r.rev, 10)
}

func (r *countingRevisionSource) advance() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rev++ // external mutation/invalidation event, not a timer/config generation change
}

func (r *countingRevisionSource) noteFn() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "server_work")
}

func (r *countingRevisionSource) order() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

type mailRevisionValue struct {
	UID       uint32
	Seen      bool
	Validated time.Time
}

type mailRevisionPublication struct {
	mu     sync.Mutex
	state  mailRevisionValue
	writes int
}

func (p *mailRevisionPublication) publish(value any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = value.(mailRevisionValue)
	p.writes++
}

func (p *mailRevisionPublication) snapshot() (mailRevisionValue, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state, p.writes
}

type mailRevisionResult struct {
	value any
	err   error
}

func mailRevisionRequest(client *Client, revision *countingRevisionSource, published *mailRevisionPublication) MailBudgetRequest {
	return MailBudgetRequest{
		Account: client.AccountKey(), AgentID: "agent-a", WorkspaceID: "ws-a",
		Operation: "listMailMessages", Params: map[string]any{"folder": "INBOX", "limit": float64(25)},
		Generation: "same-config-generation", Purpose: "read_live",
		Revision: revision, Publish: published.publish,
	}
}

func mailRevisionRead(ctx context.Context, client *Client, rev *countingRevisionSource, at time.Time) (any, error) {
	rev.noteFn()
	rows, _, _, err := client.ReadFolderPage(ctx, FolderInbox, 25, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("revision fixture: expected one seeded row, got %d", len(rows))
	}
	return mailRevisionValue{UID: rows[0].UID, Seen: rows[0].Seen, Validated: at}, nil
}

func mailRevisionCtx(t *testing.T, bound time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	t.Cleanup(cancel)
	return ctx
}

func TestBudget_RevisionSupersededReadPublishesNothing(t *testing.T) {
	t.Run("fresh_capture_precedes_server_work_and_publishes", func(t *testing.T) {
		client := startMemIMAP(t, [][]byte{mkMsg("Revision", "a@x.test", "body")}, nil)
		budget, rev := NewMailBudget(t.TempDir()), &countingRevisionSource{}
		pub := &mailRevisionPublication{}
		at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) // synthetic successful validation time
		value, err := CallValue(budget, mailRevisionCtx(t, 3*time.Second), mailRevisionRequest(client, rev, pub),
			func(ctx context.Context) (any, error) { return mailRevisionRead(ctx, client, rev, at) })
		require.NoError(t, err)
		want := mailRevisionValue{UID: 1, Seen: false, Validated: at} // seeded UID 1, initially unread
		require.Equal(t, want, value)
		state, writes := pub.snapshot()
		require.Equal(t, want, state, "fresh publication reached the actual seam's state")
		require.Equal(t, 1, writes)
		require.Equal(t, []string{"capture", "server_work"}, rev.order(), "§4.9.1: capture BEFORE server I/O")
	})

	t.Run("late_old_flags_cannot_overwrite_the_post_mutation_refresh", func(t *testing.T) {
		client := startMemIMAP(t, [][]byte{mkMsg("Revision", "a@x.test", "body")}, nil)
		budget, rev := NewMailBudget(t.TempDir()), &countingRevisionSource{}
		oldAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
		freshAt := oldAt.Add(time.Minute)
		initial := mailRevisionValue{UID: 1, Seen: false, Validated: oldAt.Add(-time.Minute)}
		pub := &mailRevisionPublication{state: initial}
		// Request is IDENTICAL before and after mark-read: configuration does
		// not change. Only publication revision advances (ADR P1.3, MC-W1-18).
		req := mailRevisionRequest(client, rev, pub)
		gate := newIdentityBarrier(t)
		fetched, oldOut := make(chan struct{}, 1), make(chan mailRevisionResult, 1)
		go func() {
			value, err := CallValue(budget, mailRevisionCtx(t, 5*time.Second), req, func(ctx context.Context) (any, error) {
				value, err := mailRevisionRead(ctx, client, rev, oldAt)
				if err != nil {
					return nil, err
				}
				fetched <- struct{}{}
				select {
				case <-gate.ch:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return value, nil // stale server snapshot: Seen=false
			})
			oldOut <- mailRevisionResult{value, err}
		}()
		identityReceive(t, fetched, "pre-mutation unread snapshot was collected")
		state, writes := pub.snapshot()
		require.Equal(t, initial, state, "paused read has not published")
		require.Zero(t, writes)
		// Real server mutation; advancing the external counter only happens
		// after the IMAP acknowledgment, not by time or test-only pool state.
		require.NoError(t, client.MarkSeenIn(mailRevisionCtx(t, 3*time.Second), FolderInbox, 1))
		rev.advance()
		freshOut := make(chan mailRevisionResult, 1)
		go func() {
			value, err := CallValue(budget, mailRevisionCtx(t, 3*time.Second), req,
				func(ctx context.Context) (any, error) { return mailRevisionRead(ctx, client, rev, freshAt) })
			freshOut <- mailRevisionResult{value, err}
		}()
		fresh := identityReceive(t, freshOut, "new revision refresh completes while old read remains paused (never joins)")
		require.NoError(t, fresh.err)
		want := mailRevisionValue{UID: 1, Seen: true, Validated: freshAt}
		require.Equal(t, want, fresh.value, "real server acknowledged mark-read")
		state, writes = pub.snapshot()
		require.Equal(t, want, state)
		require.Equal(t, 1, writes, "only fresh response published")
		gate.open()
		old := identityReceive(t, oldOut, "superseded pre-mutation read ends")
		require.ErrorIs(t, old.err, ErrRevisionSuperseded, "W1 seam's superseded determination")
		require.Nil(t, old.value, "stale rows are not handed back as a successful response")
		state, writes = pub.snapshot()
		require.Equal(t, want, state, "CX-6: Seen=true and fresh validation time survive the late old read")
		require.Equal(t, 1, writes, "superseded read's publication/commit step was suppressed")
		require.Equal(t, []string{"capture", "server_work", "capture", "server_work"}, rev.order())
	})
}
