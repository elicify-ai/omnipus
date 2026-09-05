// acquire_provenance_test.go — ADR-072 D1 test 34 (FR-035, SC-003).
//
// THE REQUIREMENT IS BEHAVIOURAL, and that word is the whole point. An earlier
// form of this check was STRUCTURAL: it asserted that BrowsingKey's field is
// unexported, so a key cannot be minted outside the package. That is true and
// it proves nothing about a RUN — a key resolved for turn A and then reused to
// acquire a browser for turn B is perfectly well-typed, and is exactly the
// identity confusion ADR-072 §1.1 records.
//
// So this test observes an actual run: several turns, in several workspaces,
// each making several browser tool calls, and asserts the invariant over
// everything that happened.
//
//	Every browser acquisition in the run carried a key that ResolveBrowsingKey
//	returned IN THE SAME TURN.
//
// NOTE ON SCOPE, stated rather than implied. The spec writes this requirement
// against `pool.Acquire`. There is no pool at this HEAD — it lands in a later
// wave. The acquisition point today is the ManagerResolver's ManagerFor, which
// is the single seam every browser tool call passes through to obtain its
// browser, and that is where the invariant is asserted. When the pool arrives,
// its Acquire sits behind this same seam and the invariant is unchanged.

package browser

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// turnIDKey tags a context with the turn it belongs to, so a resolution and an
// acquisition can be attributed to the same turn rather than merely to the
// same process.
type turnIDKeyType struct{}

var turnIDKey = turnIDKeyType{}

func withTurnID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, turnIDKey, id)
}

func turnIDOf(ctx context.Context) string {
	id, _ := ctx.Value(turnIDKey).(string)
	return id
}

// provenanceLedger records what a run actually did.
type provenanceLedger struct {
	mu sync.Mutex
	// resolved[turnID] is every key ResolveBrowsingKey returned during that turn.
	resolved map[string][]string
	// acquired is every (turnID, key) pair that reached a browser acquisition.
	acquired []acquisition
}

type acquisition struct{ turnID, key string }

func newProvenanceLedger() *provenanceLedger {
	return &provenanceLedger{resolved: map[string][]string{}}
}

func (l *provenanceLedger) noteResolved(turnID, key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.resolved[turnID] = append(l.resolved[turnID], key)
}

func (l *provenanceLedger) noteAcquired(turnID, key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.acquired = append(l.acquired, acquisition{turnID: turnID, key: key})
}

// untraceable returns every acquisition whose key was NOT returned by a
// ResolveBrowsingKey call in that same turn. SC-003 requires this to be empty.
func (l *provenanceLedger) untraceable() []acquisition {
	l.mu.Lock()
	defer l.mu.Unlock()
	var bad []acquisition
	for _, a := range l.acquired {
		found := false
		for _, k := range l.resolved[a.turnID] {
			if k == a.key {
				found = true
				break
			}
		}
		if !found {
			bad = append(bad, a)
		}
	}
	return bad
}

// provenanceResolver is the real acquisition path with a ledger wrapped round
// it. It calls the REAL ResolveBrowsingKey — not a stub returning a canned key
// — because a stub would make the test assert its own fixture.
type provenanceResolver struct {
	t      *testing.T
	ledger *provenanceLedger
	// forgeKey, when non-nil, supplies the acquisition key INSTEAD of the
	// resolved one. It is the test's mutation seam: it is what a build that
	// acquired under a key nobody resolved would look like.
	forgeKey func(ctx context.Context, resolved BrowsingKey) BrowsingKey

	// home is ONE $OMNIPUS_HOME for the whole workload, carrying a workspace
	// file per id with "jim" on its core team. It must be stable across calls:
	// this used to be a fresh t.TempDir() per resolution, which was invisible
	// until 9bfe7e38b made ResolveBrowsingKey check team membership. An empty
	// home means every workspace refuses, so all three tests here failed with
	// "does not have this agent on its team" — the security fix working, and
	// the fixture never having been a member of anything.
	home string

	mu   sync.Mutex
	mgrs map[string]*BrowserManager
}

func (r *provenanceResolver) ManagerFor(
	ctx context.Context,
) (*BrowserManager, BrowsingKey, TabOwner, error) {
	key, err := ResolveBrowsingKey(ctx, r.home)
	if err != nil {
		return nil, BrowsingKey{}, TabOwner{}, err
	}
	r.ledger.noteResolved(turnIDOf(ctx), key.String())

	acquireKey := key
	if r.forgeKey != nil {
		acquireKey = r.forgeKey(ctx, key)
	}

	owner, err := TabOwnerSession(tools.ToolTranscriptSessionID(ctx))
	if err != nil {
		return nil, BrowsingKey{}, TabOwner{}, err
	}

	r.mu.Lock()
	mgr, ok := r.mgrs[acquireKey.String()]
	if !ok {
		mgr = newTestManagerWithFakeTabs(r.t)
		mgr.key = acquireKey
		r.mgrs[acquireKey.String()] = mgr
	}
	r.mu.Unlock()

	r.ledger.noteAcquired(turnIDOf(ctx), acquireKey.String())
	return mgr, acquireKey, owner, nil
}

// runProvenanceWorkload drives a realistic run: three workspaces, two turns
// each, several browser tool calls per turn, mixing read-only and write-class
// tools. Returns the ledger of what happened.
// provenanceHome writes one $OMNIPUS_HOME carrying the three workspaces the
// workload drives, each with "jim" on its core team.
//
// Membership is not decoration here. Since 9bfe7e38b, ResolveBrowsingKey checks
// that the agent is actually on the named workspace's team before handing over
// that workspace's browser — and therefore its live logins. A fixture that
// names a workspace it is not a member of is refused, which is the control
// working, not a test problem.
func provenanceHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, ws := range provenanceWorkspaces {
		writeWorkspace(t, home, ws, "jim")
	}
	return home
}

// provenanceWorkspaces is the closed set the workload drives. Declared once so
// the fixture's membership and the workload's loop cannot drift apart — a
// workspace present in one and absent from the other resolves to a refusal.
var provenanceWorkspaces = []string{"01WSALPHA", "01WSBETA", "01WSGAMMA"}

func runProvenanceWorkload(t *testing.T, res *provenanceResolver) {
	t.Helper()
	list := &ListTabsTool{res: res}
	open := &OpenTabTool{res: res}
	sw := &SwitchTabTool{res: res}

	for _, ws := range provenanceWorkspaces {
		for turn := 0; turn < 2; turn++ {
			turnID := fmt.Sprintf("%s#%d", ws, turn)
			ctx := withTurnID(context.Background(), turnID)
			ctx = tools.WithWorkspaceID(ctx, ws)
			ctx = tools.WithAgentID(ctx, "jim")
			ctx = tools.WithTranscriptSessionID(ctx, "session-"+turnID)

			require.False(t, list.Execute(ctx, map[string]any{}).IsError, turnID)
			require.False(t, open.Execute(ctx, map[string]any{}).IsError, turnID)
			require.False(t, open.Execute(ctx, map[string]any{}).IsError, turnID)
			require.False(t, sw.Execute(ctx, map[string]any{"index": float64(0)}).IsError, turnID)
			require.False(t, list.Execute(ctx, map[string]any{}).IsError, turnID)
		}
	}
}

// TestAcquire_KeyProvenance — SC-003: zero acquisitions in a full run carried a
// key that did not come from a ResolveBrowsingKey return in the same turn.
func TestAcquire_KeyProvenance(t *testing.T) {
	ledger := newProvenanceLedger()
	res := &provenanceResolver{t: t, ledger: ledger, home: provenanceHome(t), mgrs: map[string]*BrowserManager{}}
	runProvenanceWorkload(t, res)

	// Non-vacuity first. An invariant over an empty set is not an invariant,
	// and this test's whole value is that the run really happened.
	require.GreaterOrEqual(t, len(ledger.acquired), 30,
		"the workload must have made real acquisitions; %d is too few for 3 workspaces x 2 turns x 5 calls",
		len(ledger.acquired))
	require.Len(t, ledger.resolved, 6, "six distinct turns must have resolved a key")

	assert.Empty(t, ledger.untraceable(),
		"SC-003: every browser acquisition must carry a key that ResolveBrowsingKey returned in the "+
			"SAME turn. An untraceable key means a turn acted on a browser chosen by something other "+
			"than its own workspace resolution — which is ADR-072 §1.1's defect with a different cause")

	// And the keys are the ones the turns were actually rooted in — the
	// invariant above would also hold if every turn resolved and acquired the
	// same wrong key.
	for _, a := range ledger.acquired {
		wantWS := a.turnID[:len(a.turnID)-2] // strip the "#N" turn suffix
		assert.Equal(t, "ws:"+wantWS, a.key,
			"turn %s acquired %s — a turn must act with the workspace it is rooted in", a.turnID, a.key)
	}
}

// TestAcquire_KeyProvenance_DetectsAForgedKey is the falsification, kept as a
// permanent test rather than a one-off mutation: it drives the SAME workload
// through a resolver that acquires under a key it minted itself, and asserts
// the provenance check CATCHES it.
//
// Without this, TestAcquire_KeyProvenance could be green because the predicate
// is trivially satisfiable rather than because the invariant holds.
func TestAcquire_KeyProvenance_DetectsAForgedKey(t *testing.T) {
	ledger := newProvenanceLedger()
	res := &provenanceResolver{
		t:      t,
		home:   provenanceHome(t),
		ledger: ledger,
		mgrs:   map[string]*BrowserManager{},
		// A structurally VALID key — same type, same package-private
		// constructor — that no ResolveBrowsingKey call in this turn returned.
		// This is precisely what the structural version of this requirement
		// could not see.
		forgeKey: func(_ context.Context, _ BrowsingKey) BrowsingKey {
			return mustBrowsingKey("01WSFORGED")
		},
	}
	runProvenanceWorkload(t, res)

	assert.Len(t, ledger.untraceable(), len(ledger.acquired),
		"a key minted outside ResolveBrowsingKey must be reported untraceable by the same check that "+
			"passes in TestAcquire_KeyProvenance; if it is not, that test proves nothing")
}

// TestAcquire_KeyProvenance_DetectsCrossTurnReuse is the second falsification,
// and the more realistic failure: a key that WAS resolved — just not by this
// turn. A resolver caching "the" browsing key across turns produces exactly
// this, and every key involved is legitimate in isolation.
func TestAcquire_KeyProvenance_DetectsCrossTurnReuse(t *testing.T) {
	ledger := newProvenanceLedger()
	var first BrowsingKey
	var once sync.Once
	res := &provenanceResolver{
		t:      t,
		home:   provenanceHome(t),
		ledger: ledger,
		mgrs:   map[string]*BrowserManager{},
		forgeKey: func(_ context.Context, resolved BrowsingKey) BrowsingKey {
			once.Do(func() { first = resolved })
			return first // every later turn acquires the FIRST turn's browser
		},
	}
	runProvenanceWorkload(t, res)

	bad := ledger.untraceable()
	assert.NotEmpty(t, bad,
		"reusing an earlier turn's browsing key must be caught. Every key here was returned by a real "+
			"ResolveBrowsingKey call, so a check that only asks 'was this key ever resolved?' passes "+
			"while five of six turns drive the wrong workspace's logins")
	for _, a := range bad {
		assert.Equal(t, first.String(), a.key)
	}
}
