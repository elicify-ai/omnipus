// subturn_cancel_browser_test.go — ADR-072 D1 test 36 (FR-036).
//
// TWO PROPERTIES THAT PULL IN OPPOSITE DIRECTIONS, which is why they are
// asserted together:
//
//   - A parent cancel MUST reach a delegated sub-turn's in-flight work. The
//     reachability key is the routingSessionID the child inherits verbatim from
//     its parent (ADR-057 FR-011). Root CLAUDE.md flags that one assignment in
//     spawnSubTurn as REQUIRED, load-bearing inheritance and warns that deleting
//     it produces no error and no obvious test failure — just a chat-wide Stop
//     that silently stops reaching delegated sub-turns.
//   - A parent cancel MUST NOT close the browser. Under ADR-072 the browser
//     belongs to the WORKSPACE, not the turn: it holds the operator's live
//     logins and every agent on that workspace shares it. A cancel that tore it
//     down would log the operator out of everything because one delegated turn
//     was stopped.
//
// The first without the second is a logout; the second without the first is a
// Stop button that does not stop things.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// cancelBrowserFixture registers a parent turn and one delegated child sharing
// the parent's routingSessionID, each with a real cancellable turn context.
//
// childRouting overrides the child's routingSessionID. The test uses it to run
// the SAME scenario with the inheritance in place and with it broken, which is
// the only way to show the inheritance is load-bearing rather than decorative.
func cancelBrowserFixture(
	t *testing.T, al *AgentLoop, tag string, childRouting session.RoutingSessionID,
) (parent, child *turnState, childCtx context.Context, cleanup func()) {
	t.Helper()

	chatSessionID := "chat-session-" + tag

	parentCtx, parentCancel := context.WithCancel(context.Background())
	parent = &turnState{
		sessionKey:          "parent-key-" + tag,
		turnID:              "parent-turn-" + tag,
		transcriptSessionID: chatSessionID,
		routingSessionID:    session.RoutingSessionID(chatSessionID),
		depth:               0,
		ctx:                 parentCtx,
		turnCancel:          parentCancel,
	}

	cctx, childCancel := context.WithCancel(context.Background())
	child = &turnState{
		sessionKey: "child-key-" + tag,
		turnID:     "child-turn-" + tag,
		// ADR-057 D2/FR-011: its OWN transcript session for persistence...
		transcriptSessionID: "child-session-" + tag,
		// ...and the PARENT's routing id as the cancel-reachability key.
		routingSessionID: childRouting,
		depth:            1,
		parentTurnID:     parent.turnID,
		parentTurnState:  parent,
		ctx:              cctx,
		turnCancel:       childCancel,
	}

	al.registerActiveTurn(parent)
	al.registerActiveTurn(child)
	cleanup = func() {
		al.clearActiveTurnStateEntry(parent.sessionKey, parent)
		al.clearActiveTurnStateEntry(child.sessionKey, child)
		parentCancel()
		childCancel()
	}
	return parent, child, cctx, cleanup
}

// TestCancel_CascadesWithoutClosingBrowser is test 36.
func TestCancel_CascadesWithoutClosingBrowser(t *testing.T) {
	al, alCleanup := newAL(t)
	defer alCleanup()

	// --- The workspace's browser, resolved through the real per-key path.
	// NewBrowserManager launches no Chrome — the process starts lazily at the
	// first tool call — so this is the real object with no real browser cost.
	cfg, err := browser.DefaultConfig()
	require.NoError(t, err)
	built := 0
	al.browserMgrs = make(map[string]*browser.BrowserManager)
	al.browserFactory = func(browser.BrowsingKey) (*browser.BrowserManager, error) {
		built++
		return browser.NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	}
	wsKey := browserTestKey(t, "cancel-cascade-ws")
	mgrBefore, err := al.BrowserManagerForKey(context.Background(), wsKey)
	require.NoError(t, err)
	require.NotNil(t, mgrBefore)
	require.Equal(t, 1, built)

	chatSessionID := "chat-session-inherited"
	parent, child, childCtx, cleanup := cancelBrowserFixture(
		t, al, "inherited", session.RoutingSessionID(chatSessionID),
	)
	defer cleanup()

	// --- Reachability. A chat-wide Stop is anchored on the CHAT's session id.
	// The child must be found by it directly, not only by walking down from
	// the parent — that direct reachability is what the inherited
	// routingSessionID buys.
	anchors := al.resolveInterruptAnchors(chatSessionID)
	anchorTurnIDs := turnIDsOf(anchors)
	assert.Contains(t, anchorTurnIDs, parent.turnID)
	assert.Contains(t, anchorTurnIDs, child.turnID,
		"a delegated sub-turn must be directly reachable from the CHAT's own session id. It is only "+
			"reachable because spawnSubTurn copies the parent's routingSessionID onto the child — "+
			"delete that assignment and a chat-wide Stop silently stops reaching delegated work")

	// --- The cascade actually fires, and it reaches the CHILD's turn context.
	// Every in-flight tool call in that sub-turn — a browser call included —
	// runs under this context, so this is the signal that carries the cancel
	// into the delegated work.
	select {
	case <-childCtx.Done():
		t.Fatal("precondition: the child's turn context must be live before the cancel")
	default:
	}

	descendants, err := al.InterruptSessionHard(chatSessionID, ScopeSubtree, "chat-wide Stop")
	require.NoError(t, err)
	assert.Contains(t, turnIDStrings(descendants), child.turnID,
		"the hard cancel must name the delegated child among the turns it stopped")

	select {
	case <-childCtx.Done():
		// good — the sub-turn's in-flight work is cancelled
	case <-time.After(3 * time.Second):
		t.Fatal("the delegated sub-turn's turn context was never cancelled by the parent-scope Stop")
	}
	assert.True(t, child.hardAbortRequested(), "the child must be marked hard-aborted")

	// --- AND THE BROWSER IS STILL THERE. This is the half that makes the
	// cancel safe: the browser belongs to the workspace, so stopping one turn
	// must not log the operator out of every site the workspace is signed
	// into.
	al.mu.Lock()
	liveKeys := len(al.browserMgrs)
	al.mu.Unlock()
	assert.Equal(t, 1, liveKeys,
		"the workspace's browser must still be live after the cancel — no browser is closed by a "+
			"cancel, because the browser belongs to the workspace and not to the turn")

	mgrAfter, err := al.BrowserManagerForKey(context.Background(), wsKey)
	require.NoError(t, err)
	assert.Same(t, mgrBefore, mgrAfter,
		"the SAME manager must still answer for this workspace — a fresh one would mean a fresh "+
			"profile and a signed-out browser")
	assert.Equal(t, 1, built,
		"no second manager may be built: rebuilding is what a silently-closed browser looks like "+
			"from the next turn's point of view")
	assert.NotNil(t, mgrAfter.Live(),
		"and the surviving manager is still usable, not a husk")
}

// TestCancel_ChildWithItsOwnRoutingID_IsNotReachedFromTheChat is the
// falsification of the reachability assertion above, kept as a permanent test.
//
// It runs the identical scenario with ONE difference: the child carries its own
// routingSessionID instead of the parent's. The chat's Stop then does not find
// it directly — which is exactly what deleting spawnSubTurn's
// `childTS.routingSessionID = parentTS.routingSessionID` produces, and it
// produces no error, no log line and no failure anywhere obvious.
func TestCancel_ChildWithItsOwnRoutingID_IsNotReachedFromTheChat(t *testing.T) {
	al, alCleanup := newAL(t)
	defer alCleanup()

	chatSessionID := "chat-session-broken"
	parent, child, _, cleanup := cancelBrowserFixture(
		t, al, "broken", session.RoutingSessionID("child-own-routing-id"),
	)
	defer cleanup()

	anchors := turnIDsOf(al.resolveInterruptAnchors(chatSessionID))
	assert.Contains(t, anchors, parent.turnID,
		"the parent is still reachable — only the child's inheritance was broken")
	assert.NotContains(t, anchors, child.turnID,
		"WITHOUT the inherited routingSessionID the chat's own Stop does not find the delegated "+
			"child at all. This is the regression the assertion in "+
			"TestCancel_CascadesWithoutClosingBrowser exists to catch; if this test ever passes AND "+
			"that one passes, the reachability assertion there is not discriminating")
}

func turnIDsOf(states []*turnState) []string {
	out := make([]string, 0, len(states))
	for _, ts := range states {
		out = append(out, ts.turnID)
	}
	return out
}

func turnIDStrings(ids []string) []string { return ids }
