package gateway

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// browser_panel_tab_set_test.go — issue #671's gateway half.
//
// The manager-level rule (which tab set a panel should drive) is tested in
// pkg/tools/browser/panel_tab_set_test.go. What is tested HERE is that the
// gateway actually asks it: that the live-view call sites address the id the
// connection resolved at attach, and not the operator's workspace-owned set
// they were all hardwired to before.

// TestBrowserPanel_LiveViewCallSitesNeverHardwireTheOperatorSet is the
// structural guard, in the same spirit (and for the same reason) as
// pkg/tools/browser/no_residual_test.go's two guards.
//
// A behavioural test cannot cover this: reproducing the divergent state needs
// two REAL tab sets, which needs a real Chromium, so every gateway unit test
// here runs against a manager with no tabs at all — a state where the resolved
// id and the operator's id are the same string, and a regression is therefore
// invisible. The way the bug comes back is also mechanical rather than clever:
// a merge from a branch cut before this fix re-adds `mgr.OperatorSessionID()`
// at one of ~19 call sites as an ordinary, conflict-free addition, and nothing
// fails. One panel call site left behind is enough to put the viewer's clicks,
// or their video, on a different tab from everything else.
//
// Deliberately scoped to the two live-panel files. Legitimate operator-scoped
// callers exist elsewhere and are NOT in scope: the boot-time warm-up
// (gateway.go) has no viewer and no chat to resolve against, and
// pkg/tools/browser's own resolveSessionID/panelTabSet fallbacks are the
// documented "no panel context" default.
func TestBrowserPanel_LiveViewCallSitesNeverHardwireTheOperatorSet(t *testing.T) {
	files := []string{"browser_ws.go", "browser_webrtc.go"}
	pattern := regexp.MustCompile(`\.OperatorSessionID\(\)`)

	var offenders []string
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(".", name))
		require.NoError(t, err, "the guard must fail loudly if it cannot read a file it guards")
		for i, line := range strings.Split(string(body), "\n") {
			if pattern.MatchString(line) {
				offenders = append(offenders, name+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
	}

	require.Empty(t, offenders,
		"the live panel must drive the tab set it resolved at attach (state.attachment()'s "+
			"panelSessionID, from BrowserManager.PanelTabSetID), never the operator's "+
			"workspace-owned set unconditionally — that is issue #671, where an operator "+
			"watched a blank /browser-start tab while the agent browsed elsewhere and "+
			"reported success.\n%s",
		strings.Join(offenders, "\n"))
}

// TestResolvePanelTabSet_PrefersTheIdPinnedAtAttach: the WebRTC offer arrives
// on its own goroutine, after the attach. It must bind the video to the SAME
// tab set the control plane resolved — re-deriving it independently is how the
// video and the clicks end up on different tabs.
func TestResolvePanelTabSet_PrefersTheIdPinnedAtAttach(t *testing.T) {
	mgr := newPanelTestManager(t)
	state := &browserConnState{}
	epoch := state.beginAttach()
	require.True(t, state.bindAttachment(epoch, mgr, "chat-1", "pinned/tab-set"))

	require.Equal(t, "pinned/tab-set", resolvePanelTabSet(state, mgr, "chat-1"),
		"the offer must reuse what the attach resolved, not resolve again")
}

// TestResolvePanelTabSet_FallsBackWhenNothingIsPinned covers the real timing
// case — an offer whose attach has not committed yet — and the safety case: a
// pin belonging to a DIFFERENT manager must never be handed to this one, since
// a session key minted for another browser names a tab set this browser does
// not have.
func TestResolvePanelTabSet_FallsBackWhenNothingIsPinned(t *testing.T) {
	mgr := newPanelTestManager(t)
	other := newPanelTestManager(t)

	require.Equal(t, mgr.PanelTabSetID("chat-1"), resolvePanelTabSet(&browserConnState{}, mgr, "chat-1"),
		"an offer that beat its attach must resolve the same way the attach will")
	require.Equal(t, mgr.PanelTabSetID("chat-1"), resolvePanelTabSet(nil, mgr, "chat-1"),
		"no connection state at all must still resolve, never return empty")

	state := &browserConnState{}
	epoch := state.beginAttach()
	require.True(t, state.bindAttachment(epoch, other, "chat-1", "other-browser/tab-set"))
	require.Equal(t, mgr.PanelTabSetID("chat-1"), resolvePanelTabSet(state, mgr, "chat-1"),
		"a pin from another manager must be ignored, not borrowed across browsers")
}

// TestBrowserConnState_ClearAttachmentClearsTheResolvedTabSet: the pinned tab
// set must die with the attachment. A stale id surviving a detach would let
// the next handler act on a tab set this connection no longer watches.
func TestBrowserConnState_ClearAttachmentClearsTheResolvedTabSet(t *testing.T) {
	mgr := newPanelTestManager(t)
	state := &browserConnState{}
	epoch := state.beginAttach()
	require.True(t, state.bindAttachment(epoch, mgr, "chat-1", "pinned/tab-set"))

	gotMgr, gotSession, gotPanel := state.clearAttachment()
	require.Equal(t, mgr, gotMgr)
	require.Equal(t, "chat-1", gotSession)
	require.Equal(t, "pinned/tab-set", gotPanel,
		"teardown must be handed the tab set the attach ran on, or the control lock is released on the wrong one")

	_, _, panelAfter := state.attachment()
	require.Empty(t, panelAfter, "a cleared attachment must not leave its tab set behind")
}

// newPanelTestManager builds a never-started BrowserManager — enough for tab
// set RESOLUTION, which is a map lookup and touches no Chromium.
func newPanelTestManager(t *testing.T) *browser.BrowserManager {
	t.Helper()
	cfg, err := browser.DefaultConfig()
	require.NoError(t, err)
	cfg.ProfileDir = filepath.Join(t.TempDir(), "profile")
	mgr, err := browser.NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	return mgr
}
