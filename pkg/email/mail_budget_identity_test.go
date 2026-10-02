package email

// W1 §4.8, US-6/AS-1/2/3, MC-W1-15/16; DS-3. Key comparisons are relational
// spec invariants, not copied implementation strings. Concurrent results come
// from real startViewIMAP folder reads, not mocked final values.
//
// Pair-only tests intentionally use the already-existing pair fields so the
// original cross-pair defect can be observed independently before W1 adds its
// generation/purpose fields. Tests requiring a missing extension fail loudly
// with BLOCKED (never skip or synthesize a production interface).

import (
	"context"
	"crypto/tls"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/require"
)

type identityResult struct {
	rows []string
	err  error
}

type identityReadRig struct {
	a, b   *Client
	budget *MailBudget
	dials  atomic.Int32
	work   atomic.Int32
}

func newIdentityReadRig(t *testing.T) *identityReadRig {
	t.Helper()
	// The fixture seed order specifies the oracle: Sent contains Subject 1,
	// INBOX contains Subject 2. Both clients share endpoint+login but resolve
	// their logical Sent role differently, exactly US-6/AS-1's wrong-data case.
	base, _ := startViewIMAP(t, "sent", "inbox")
	a, err := NewClient(base.acct)
	require.NoError(t, err)
	other := base.acct
	other.SentFolder = "INBOX"
	b, err := NewClient(other)
	require.NoError(t, err)
	rig := &identityReadRig{a: a, b: b, budget: NewMailBudget(t.TempDir())}
	require.Equal(t, a.AccountKey(), b.AccountKey(), "instrument: same actual server account")
	prev := imapDial
	imapDial = func(ctx context.Context, addr string, cfg *tls.Config) (*imapclient.Client, error) {
		rig.dials.Add(1)
		return prev(ctx, addr, cfg) // real protocol edge; no replacement budget/client
	}
	t.Cleanup(func() { imapDial = prev })
	return rig
}

func (r *identityReadRig) request(agent, workspace string) MailBudgetRequest {
	req := MailBudgetRequest{Account: r.a.AccountKey(), AgentID: agent, WorkspaceID: workspace,
		Operation: "listMailMessages", Params: map[string]any{"folder": "Sent", "limit": float64(25)}}
	// Input compatibility only: pair regressions run on the old interface,
	// and supply the full nominal identity when the extensions arrive. The
	// generation/purpose-specific tests still BLOCK loudly if either is absent.
	for name, value := range map[string]string{"Generation": "gen-1", "Purpose": "read_live"} {
		field := reflect.ValueOf(&req).Elem().FieldByName(name)
		if field.IsValid() && field.Kind() == reflect.String && field.CanSet() {
			field.SetString(value)
		}
	}
	return req
}

// Setting fields on a REQUEST is not a production test hook. Reflection keeps
// the pair regression runnable before these frozen request extensions exist.
// A missing required extension is explicitly BLOCKED, never a skipped case.
func identityRequireStringField(t *testing.T, req *MailBudgetRequest, name, value string) {
	t.Helper()
	field := reflect.ValueOf(req).Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("BLOCKED: MailBudgetRequest.%s not implemented — required by W1 §3.1/§4.8/FR-W1-17", name)
	}
	require.Equal(t, reflect.String, field.Kind(), "W1's %s extension is string-shaped", name)
	field.SetString(value)
}

func identityFullRequest(t *testing.T, rig *identityReadRig, generation, purpose string) MailBudgetRequest {
	t.Helper()
	req := rig.request("agent-a", "ws-a")
	identityRequireStringField(t, &req, "Generation", generation)
	identityRequireStringField(t, &req, "Purpose", purpose)
	return req
}

// Done is an observable caller wait boundary. The registered caller's
// context is inspected after flight admission, so release the server-data
// barrier only when the second caller has entered its wait. No arbitrary
// sleep is used to 'hope' it joined. Its real cancellation channel is preserved.
type identityWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func newIdentityWaitContext(t *testing.T) *identityWaitContext {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // harness safety, not a product threshold
	t.Cleanup(cancel)
	return &identityWaitContext{Context: ctx, entered: make(chan struct{})}
}

func (c *identityWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

type identityBarrier struct {
	ch   chan struct{}
	once sync.Once
}

func newIdentityBarrier(t *testing.T) *identityBarrier {
	t.Helper()
	b := &identityBarrier{ch: make(chan struct{})}
	t.Cleanup(b.open)
	return b
}
func (b *identityBarrier) open() { b.once.Do(func() { close(b.ch) }) }

func identityReceive[T any](t *testing.T, out <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-out:
		return value
	case <-time.After(3 * time.Second):
		t.Fatalf("expected %s; actual: no completion within harness safety bound", what)
		var zero T
		return zero
	}
}

func (r *identityReadRig) run(req MailBudgetRequest, ctx context.Context, client *Client, pause *identityBarrier, fetched chan<- struct{}) <-chan identityResult {
	out := make(chan identityResult, 1)
	go func() {
		rows, err := CallValue(r.budget, ctx, req, func(workCtx context.Context) ([]string, error) {
			r.work.Add(1)
			page, _, _, err := client.ReadFolderPage(workCtx, FolderSent, 25, 0)
			if err != nil {
				return nil, err
			}
			subjects := make([]string, 0, len(page))
			for _, row := range page {
				subjects = append(subjects, row.Subject)
			}
			if fetched != nil {
				fetched <- struct{}{}
			}
			if pause != nil {
				select {
				case <-pause.ch:
				case <-workCtx.Done():
					return nil, workCtx.Err()
				}
			}
			return subjects, nil
		})
		out <- identityResult{rows, err}
	}()
	return out
}

func identityAssertDifferentReads(t *testing.T, rig *identityReadRig, oldReq, newReq MailBudgetRequest) {
	t.Helper()
	// Detect key collisions explicitly, but still exercise results/counters:
	// Errorf does not prevent the wrong-sharing scenario from completing.
	if oldReq.flightKey() == newReq.flightKey() {
		t.Errorf("expected distinct read-coalescing identities; actual: both requests have the same key %q", oldReq.flightKey())
	}
	gate := newIdentityBarrier(t)
	fetched := make(chan struct{}, 1)
	oldCtx, newCtx := newIdentityWaitContext(t), newIdentityWaitContext(t)
	oldResult := rig.run(oldReq, oldCtx, rig.a, gate, fetched)
	identityReceive(t, fetched, "old real Sent read reached its publication barrier")
	newResult := rig.run(newReq, newCtx, rig.b, nil, nil)
	identityReceive(t, newCtx.entered, "new caller entered the concurrent read wait")
	gate.open()
	old, fresh := identityReceive(t, oldResult, "old caller result"), identityReceive(t, newResult, "new caller result")
	require.NoError(t, old.err)
	require.NoError(t, fresh.err)
	require.Equal(t, []string{"Subject 1"}, old.rows, "old mapping returns its server folder")
	require.Equal(t, []string{"Subject 2"}, fresh.rows, "new identity must receive its own folder, never old flight's rows")
	require.Equal(t, int32(2), rig.work.Load(), "different identities execute two real operations")
	require.Equal(t, int32(2), rig.dials.Load(), "unpooled baseline fixture: two independent protocol reads")
}

func TestBudget_CoalescingIdentity_SeparatesPairs(t *testing.T) {
	for _, component := range []string{"agent", "workspace"} {
		t.Run(component, func(t *testing.T) {
			rig := newIdentityReadRig(t)
			oldReq, newReq := rig.request("agent-a", "ws-a"), rig.request("agent-a", "ws-a")
			if component == "agent" {
				newReq.AgentID = "agent-b"
			} else {
				newReq.WorkspaceID = "ws-b"
			}
			identityAssertDifferentReads(t, rig, oldReq, newReq)
		})
	}
}

// Same-key positive control. A conflicting second mapping deliberately makes
// it obvious whether it joined: both get Subject 1 and only ONE real dial runs.
func TestBudget_CoalescingIdentity_SameIdentitySharesOneFlight(t *testing.T) {
	rig := newIdentityReadRig(t)
	req := rig.request("agent-a", "ws-a")
	gate, fetched := newIdentityBarrier(t), make(chan struct{}, 1)
	aCtx, bCtx := newIdentityWaitContext(t), newIdentityWaitContext(t)
	a := rig.run(req, aCtx, rig.a, gate, fetched)
	identityReceive(t, fetched, "first real read reached barrier")
	b := rig.run(req, bCtx, rig.b, nil, nil)
	identityReceive(t, bCtx.entered, "identical joiner registered")
	gate.open()
	for _, ch := range []<-chan identityResult{a, b} {
		got := identityReceive(t, ch, "joined read result")
		require.NoError(t, got.err)
		require.Equal(t, []string{"Subject 1"}, got.rows)
	}
	require.Equal(t, int32(1), rig.work.Load())
	require.Equal(t, int32(1), rig.dials.Load(), "control proves the harness can see real sharing")
}

func TestBudget_CoalescingIdentity_SeparatesGenerations(t *testing.T) {
	rig := newIdentityReadRig(t)
	oldReq := identityFullRequest(t, rig, "gen-old", "read_live")
	newReq := identityFullRequest(t, rig, "gen-new", "read_live")
	identityAssertDifferentReads(t, rig, oldReq, newReq)
}

func TestBudget_CoalescingIdentity_SeparatesPurposes(t *testing.T) {
	rig := newIdentityReadRig(t)
	cache := identityFullRequest(t, rig, "gen-1", "read_cache")
	live := identityFullRequest(t, rig, "gen-1", "read_live")
	identityAssertDifferentReads(t, rig, cache, live)
}

// Both existing key dimensions remain meaningful; normalization is independent
// of map insertion order. This never pins a private delimiter/encoding string.
func TestBudget_CoalescingIdentity_SeparatesOperationAndArguments(t *testing.T) {
	for _, dimension := range []string{"operation", "folder", "limit"} {
		t.Run(dimension, func(t *testing.T) {
			rig := newIdentityReadRig(t)
			a, b := rig.request("agent-a", "ws-a"), rig.request("agent-a", "ws-a")
			switch dimension {
			case "operation":
				b.Operation = "read_inbox"
			case "folder":
				b.Params["folder"] = "INBOX"
			case "limit":
				b.Params["limit"] = float64(50)
			}
			identityAssertDifferentReads(t, rig, a, b)
		})
	}
	// Exact invariant: reordered normalized arguments describe the same read.
	rig := newIdentityReadRig(t)
	a, b := rig.request("agent-a", "ws-a"), rig.request("agent-a", "ws-a")
	b.Params = map[string]any{"limit": float64(25), "folder": "Sent"}
	require.Equal(t, a.flightKey(), b.flightKey(), "JSON object insertion order does not change identity")
}

func TestBudget_ParamlessAndMutationsNeverCoalesce(t *testing.T) {
	for _, kind := range []string{"paramless", "mutation"} {
		t.Run(kind, func(t *testing.T) {
			rig := newIdentityReadRig(t)
			a, b := rig.request("agent-a", "ws-a"), rig.request("agent-a", "ws-a")
			if kind == "paramless" {
				a.Params, b.Params = nil, map[string]any{}
				require.Empty(t, a.flightKey(), "W1 §4.8 preserves empty-params opt-out")
				require.Empty(t, b.flightKey())
			} else {
				a = identityFullRequest(t, rig, "gen-1", "mutation")
				b = a
				require.Empty(t, a.flightKey(), "W1 §4.8: identical mutations never share a flight")
			}
			gate, fetched := newIdentityBarrier(t), make(chan struct{}, 2)
			aOut := rig.run(a, newIdentityWaitContext(t), rig.a, gate, fetched)
			identityReceive(t, fetched, "first independent operation reached barrier")
			bOut := rig.run(b, newIdentityWaitContext(t), rig.b, gate, fetched)
			identityReceive(t, fetched, "second independent operation did NOT coalesce")
			gate.open()
			gotA, gotB := identityReceive(t, aOut, "first operation"), identityReceive(t, bOut, "second operation")
			require.NoError(t, gotA.err)
			require.NoError(t, gotB.err)
			require.Equal(t, []string{"Subject 1"}, gotA.rows)
			require.Equal(t, []string{"Subject 2"}, gotB.rows)
			require.Equal(t, int32(2), rig.dials.Load())
		})
	}
}

// FR-W1-17: account stays the contention key, not the result-sharing key.
// Hold two distinct operations at their completion barrier; a third queues,
// receives account-busy on its caller deadline, and never opens a socket.
func TestBudget_DifferentPairsStillShareAccountCap(t *testing.T) {
	rig := newIdentityReadRig(t)
	a, b := rig.request("a", "wa"), rig.request("b", "wb")
	// Different operation also keeps this account-cap instrument usable on the
	// pre-W1 key. Cross-pair sharing itself is tested above without that change.
	b.Operation = "read_message"
	gate, fetched := newIdentityBarrier(t), make(chan struct{}, 2)
	first := rig.run(a, newIdentityWaitContext(t), rig.a, gate, fetched)
	second := rig.run(b, newIdentityWaitContext(t), rig.b, gate, fetched)
	identityReceive(t, fetched, "first held account operation")
	identityReceive(t, fetched, "second held account operation")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	thirdReq := rig.request("c", "wc")
	thirdReq.Operation = "third_distinct_read"
	third := identityReceive(t, rig.run(thirdReq, ctx, rig.a, nil, nil), "queued third account request")
	require.ErrorIs(t, third.err, ErrMailBusy)
	require.ErrorIs(t, third.err, context.DeadlineExceeded)
	require.Nil(t, third.rows)
	require.Equal(t, int32(2), rig.work.Load())
	require.Equal(t, int32(2), rig.dials.Load(), "queued overflow never dialed")
	gate.open()
	for _, ch := range []<-chan identityResult{first, second} {
		require.NoError(t, identityReceive(t, ch, "holder completed").err)
	}
}

// US-5/AS-5, MC-W1-16. Cancel one of THREE registered owners while their
// real read is held; it cannot cancel the flight or corrupt either survivor.
func TestBudget_JoinerCancellationIsolated(t *testing.T) {
	rig := newIdentityReadRig(t)
	req := rig.request("agent-a", "ws-a")
	gate, fetched := newIdentityBarrier(t), make(chan struct{}, 1)
	first := rig.run(req, newIdentityWaitContext(t), rig.a, gate, fetched)
	identityReceive(t, fetched, "shared server data fetched")
	ctx, cancel := context.WithCancel(context.Background())
	cancelCtx := &identityWaitContext{Context: ctx, entered: make(chan struct{})}
	cancelled := rig.run(req, cancelCtx, rig.b, nil, nil)
	identityReceive(t, cancelCtx.entered, "cancelled owner registered before cancellation")
	thirdCtx := newIdentityWaitContext(t)
	third := rig.run(req, thirdCtx, rig.b, nil, nil)
	identityReceive(t, thirdCtx.entered, "third owner registered")
	cancel()
	got := identityReceive(t, cancelled, "cancelled waiter exits")
	require.ErrorIs(t, got.err, context.Canceled)
	require.Nil(t, got.rows)
	gate.open()
	for _, ch := range []<-chan identityResult{first, third} {
		got := identityReceive(t, ch, "surviving shared-read owner")
		require.NoError(t, got.err)
		require.Equal(t, []string{"Subject 1"}, got.rows)
	}
	require.Equal(t, int32(1), rig.work.Load())
	require.Equal(t, int32(1), rig.dials.Load())
}
