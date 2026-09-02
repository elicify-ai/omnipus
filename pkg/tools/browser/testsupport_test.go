package browser

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// testsupport_test.go — the ONE seam package tests use to mint a browsing key
// and a manager-level session id.
//
// Why this file exists at all. Before ADR-072 every test addressed the browser
// through the exported DefaultSessionID constant, which is exactly the shared
// identity FR-002b deletes. There is deliberately no exported literal
// constructor for a BrowsingKey (see key.go), so test code needs one seam
// rather than 26 files each inventing their own string — and it must be a
// _test.go file so nothing in the shipped binary can reach it.
//
// It is NOT a replacement constant. newTestBrowsingKey goes through
// newBrowsingKey, the same private constructor ResolveBrowsingKey uses, so a
// key a test mints is subject to FR-037's path-segment validation exactly like
// a real one.

// newTestBrowsingKey mints the browsing key for workspaceID. It fails the test
// rather than returning an error, because every caller wants a key and a test
// that could not mint one has nothing left to assert.
func newTestBrowsingKey(t *testing.T, workspaceID string) BrowsingKey {
	t.Helper()
	k, err := newBrowsingKey(workspaceID)
	if err != nil {
		t.Fatalf("newTestBrowsingKey(%q): %v", workspaceID, err)
	}
	return k
}

// testWorkspaceID is the workspace every package test browses in unless it
// deliberately needs a second one.
const testWorkspaceID = "01TESTWORKSPACE"

// testTranscriptSessionID is the chat session every package test's tabs belong
// to unless it deliberately needs a second one. FR-080: the id is a TRANSCRIPT
// session id, never a routing session id.
const testTranscriptSessionID = "01TESTSESSION"

// mustBrowsingKey / mustTabOwner are the package-level, no-*testing.T forms the
// var block below needs. They panic, which is the correct behaviour for a
// package-level test fixture that cannot be constructed: the whole test binary
// is meaningless without it.
func mustBrowsingKey(workspaceID string) BrowsingKey {
	k, err := newBrowsingKey(workspaceID)
	if err != nil {
		panic("browser test support: " + err.Error())
	}
	return k
}

func mustTabOwner(transcriptSessionID string) TabOwner {
	o, err := TabOwnerSession(transcriptSessionID)
	if err != nil {
		panic("browser test support: " + err.Error())
	}
	return o
}

var (
	// testKey is the browsing key package tests address.
	testKey = mustBrowsingKey(testWorkspaceID)
	// testOwner is the tab owner package tests address — a SESSION's own tab
	// set, which is what an ordinary tool call resolves to.
	testOwner = mustTabOwner(testTranscriptSessionID)
	// testSessionID is the manager-level session key, i.e. what every call site
	// that used to pass DefaultSessionID now passes. Mechanically substituted
	// across the package's tests by the FR-002e migration.
	testSessionID = sessionKey(testKey, testOwner)
	// testOperatorSessionID is the WORKSPACE-OWNED set inside the same browser
	// — the operator's own tabs. Both gates (the human-control lock and the
	// write lease) are live on it, which is why the lease-membership and
	// implicit-acquisition suites set up against this one.
	testOperatorSessionID = sessionKey(testKey, TabOwnerWorkspace())
)

// fixedResolver is the ManagerResolver test doubles use: it hands every
// Execute the manager, key and owner the test already built, with no workspace
// files on disk and no agent registry.
type fixedResolver struct {
	mgr   *BrowserManager
	key   BrowsingKey
	owner TabOwner
	err   error
	// calls counts ManagerFor invocations, so a test can assert that a tool
	// resolves its manager PER EXECUTE rather than capturing one (FR-002a).
	calls int
}

func (f *fixedResolver) ManagerFor(_ context.Context) (*BrowserManager, BrowsingKey, TabOwner, error) {
	f.calls++
	if f.err != nil {
		return nil, BrowsingKey{}, TabOwner{}, f.err
	}
	return f.mgr, f.key, f.owner, nil
}

// newFixedResolver builds a resolver over mgr addressing the package's standard
// (testKey, testOwner) pair.
func newFixedResolver(mgr *BrowserManager) *fixedResolver {
	return &fixedResolver{mgr: mgr, key: testKey, owner: testOwner}
}

// newOperatorResolver builds a resolver over mgr addressing the WORKSPACE-OWNED
// tab set — the operator's tabs, where both gates are live.
func newOperatorResolver(mgr *BrowserManager) *fixedResolver {
	return &fixedResolver{mgr: mgr, key: testKey, owner: TabOwnerWorkspace()}
}

// registerToolsForTest is the migration shim for the tests that used to call
// RegisterTools' pre-ADR-072 signature (which built the manager itself and
// returned it). It builds the manager, binds it to the package's standard
// browsing key, and registers the eleven tools against a resolver that hands
// every Execute that same (manager, key, owner) triple.
//
// It is a TEST helper and not a second production entry point: production wires
// the real AgentLoop resolver, which resolves per turn. What this preserves is
// the tests' ability to drive a tool against a manager they built, which is
// orthogonal to which browser the turn addresses.
func registerToolsForTest(
	t *testing.T,
	registry *tools.ToolRegistry,
	cfg BrowserConfig,
	ssrf *security.SSRFChecker,
	evaluateEnabled bool,
	agentHome string,
	restrict bool,
) (*BrowserManager, error) {
	t.Helper()
	mgr, err := NewBrowserManager(cfg, ssrf)
	if err != nil {
		return nil, err
	}
	mgr.key = testKey
	if err := RegisterTools(registry, newFixedResolver(mgr), evaluateEnabled, agentHome, restrict); err != nil {
		return nil, err
	}
	return mgr, nil
}

// registerToolsForTestAs is registerToolsForTest with a caller-chosen resolver,
// for the suites that must drive the tools against the OPERATOR's
// workspace-owned tab set — the one set on which BOTH gates (the human-control
// lock and the write lease) are live, and therefore the only one on which their
// classification is actually visible.
func registerToolsForTestAs(
	t *testing.T,
	registry *tools.ToolRegistry,
	cfg BrowserConfig,
	ssrf *security.SSRFChecker,
	makeResolver func(*BrowserManager) ManagerResolver,
) *BrowserManager {
	t.Helper()
	mgr, err := NewBrowserManager(cfg, ssrf)
	if err != nil {
		t.Fatalf("registerToolsForTestAs: %v", err)
	}
	mgr.key = testKey
	if err := RegisterTools(registry, makeResolver(mgr), true, t.TempDir(), true); err != nil {
		t.Fatalf("registerToolsForTestAs: RegisterTools: %v", err)
	}
	return mgr
}

// browserTestKey is the migration shim for the tests that passed an agent id to
// AttachSharedChrome. Under FR-001 the coordinator's bookkeeping is keyed by the
// BROWSING KEY, so each of those distinct ids becomes a distinct workspace —
// which is what those tests were actually exercising (two managers, two
// isolated browser contexts, one coordinator).
func browserTestKey(workspaceID string) BrowsingKey { return mustBrowsingKey(workspaceID) }

// refuseTabsAtOrAbove drives the FR-060 memory gate to refuse once the browser
// already has n tabs open, and to admit below that.
//
// It is the direct successor to the deleted per-agent tab cap in the tests that used
// to set one: the oracle "the third tab is refused" is unchanged, only the
// reason for the refusal moved from a counter to live memory. Reported as
// MEASURED (ok=true) so it exercises the pressure branch and not FR-082's
// unmeasurable-host floor, which is a different requirement with a different
// test.
func refuseTabsAtOrAbove(n int) func(openTabs int) (bool, bool) {
	return func(openTabs int) (bool, bool) { return openTabs >= n, true }
}

// unmeasurableHost drives the gate's FR-065/FR-082 branch: this host's memory
// cannot be read at all.
func unmeasurableHost() func(openTabs int) (bool, bool) {
	return func(int) (bool, bool) { return false, false }
}

// deletedTabCapConfigKey is the config key ADR-072 D1.5a DELETED, assembled at
// runtime rather than written as a literal.
//
// The assembly is not cosmetic. Several tests assert that a memory refusal does
// NOT name this key — an operator told to raise a limit would go looking for a
// setting this build does not have — and the repo-wide TestNoResidualTabCap
// guard forbids the literal appearing in any .go file, precisely so a merge
// cannot quietly restore it. A test asserting the key's absence must therefore
// not spell it out.
var deletedTabCapConfigKey = "max_" + "tabs"
