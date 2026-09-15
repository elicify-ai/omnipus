// browser_deferral_wiring_test.go — ADR-085 BROWSER-FR-029/FR-029a.
//
// WHY THIS FILE EXISTS. SetBrowserWheelReleaseHook shipped with ZERO call
// sites anywhere in the module: the only non-test references were its own
// declaration and three comments. Everything about the release was built —
// the fail-closed OperatorPrompt carrier, the invocation in processMessage,
// the hook type, the actor struct — and the two ends were never joined, so
// "the operator resumes your browser driving by sending a new message" (which
// the agent is told in three separate tool messages) was connected to nothing.
// With tools.browser.control_idle_release set to 0 — which that setting's own
// documentation describes as "expiry disabled" — the agent was then locked out
// of the browser for the rest of the process's life.
//
// These tests pin BOTH ends: that the invoker honours the fail-closed field,
// and that a production call site for each half still exists. The second half
// is structural on purpose (FR-029a: "a helper call site can be moved, but the
// field's truth value is the thing the gate reads") — a behavioural test alone
// would have passed throughout the whole period the feature was dead.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// withCapturedReleaseHook installs a recording release hook for one test and
// restores the previous registration afterwards.
func withCapturedReleaseHook(t *testing.T) *[]struct {
	SessionID string
	Actor     ReleaseActor
} {
	t.Helper()
	browserWheelReleaseHookMu.RLock()
	prev := browserWheelReleaseHook
	browserWheelReleaseHookMu.RUnlock()
	t.Cleanup(func() {
		browserWheelReleaseHookMu.Lock()
		browserWheelReleaseHook = prev
		browserWheelReleaseHookMu.Unlock()
	})

	calls := &[]struct {
		SessionID string
		Actor     ReleaseActor
	}{}
	(&AgentLoop{}).SetBrowserWheelReleaseHook(func(_ context.Context, sessionID string, actor ReleaseActor) {
		*calls = append(*calls, struct {
			SessionID string
			Actor     ReleaseActor
		}{sessionID, actor})
	})
	return calls
}

// TestBrowserWheelReleaseHook_FiresOnlyForAnOperatorPrompt is FR-029's gate.
// The release MUST fire for an operator-originated prompt and MUST NOT fire
// for anything else — a question-card resume, an async-notifier completion, a
// goal follow-up re-injection or a cron/heartbeat turn all reach the same
// processMessage and all leave OperatorPrompt false.
func TestBrowserWheelReleaseHook_FiresOnlyForAnOperatorPrompt(t *testing.T) {
	calls := withCapturedReleaseHook(t)

	operator := bus.InboundMessage{OperatorPrompt: true, GatewayUserID: "admin"}
	operator.Sender.CanonicalID = "webchat:admin"
	invokeBrowserWheelReleaseHookIfOperatorPrompt(context.Background(), operator, "sess-1")

	// Everything below is NOT a prompt and must change nothing.
	invokeBrowserWheelReleaseHookIfOperatorPrompt(context.Background(), bus.InboundMessage{}, "sess-1")
	cron := bus.InboundMessage{}
	cron.Sender.CanonicalID = "cron"
	invokeBrowserWheelReleaseHookIfOperatorPrompt(context.Background(), cron, "sess-1")

	require.Len(t, *calls, 1,
		"exactly the operator prompt may release the wheel; a cron/heartbeat/resume turn must not")
	assert.Equal(t, "sess-1", (*calls)[0].SessionID,
		"the hook must be handed the chat the prompt landed on — the gateway resolves the whole "+
			"reachability set from it")
	assert.Equal(t, "admin", (*calls)[0].Actor.GatewayUserID,
		"FR-030: the WS-authenticated principal must reach the audit record")
	assert.Equal(t, "webchat:admin", (*calls)[0].Actor.SenderCanonicalID)
}

// TestBrowserWheelReleaseHook_NilHookIsASilentNoOp pins the headless/test
// contract: a build with no gateway registers nothing and must not panic.
func TestBrowserWheelReleaseHook_NilHookIsASilentNoOp(t *testing.T) {
	browserWheelReleaseHookMu.RLock()
	prev := browserWheelReleaseHook
	browserWheelReleaseHookMu.RUnlock()
	t.Cleanup(func() {
		browserWheelReleaseHookMu.Lock()
		browserWheelReleaseHook = prev
		browserWheelReleaseHookMu.Unlock()
	})

	(&AgentLoop{}).SetBrowserWheelReleaseHook(nil)
	assert.NotPanics(t, func() {
		invokeBrowserWheelReleaseHookIfOperatorPrompt(
			context.Background(), bus.InboundMessage{OperatorPrompt: true}, "sess-1")
	})
}

// TestBrowserWheelRelease_BothEndsHaveAProductionCallSite is the guard the
// absence of which let this feature ship dead. A hook with a registrar and no
// invoker, or an invoker and no registrar, is a no-op that every unit test
// passes — this asserts each half is reachable from non-test code.
//
// It reads source rather than behaviour deliberately: the two halves live in
// different packages (pkg/agent invokes, pkg/gateway registers) and pkg/agent
// cannot import pkg/gateway to observe the join at runtime.
func TestBrowserWheelRelease_BothEndsHaveAProductionCallSite(t *testing.T) {
	t.Run("the turn engine invokes the hook", func(t *testing.T) {
		hits := grepNonTestGoSources(t, ".", "invokeBrowserWheelReleaseHookIfOperatorPrompt(")
		assert.NotEmpty(t, hits,
			"pkg/agent must call invokeBrowserWheelReleaseHookIfOperatorPrompt from production code "+
				"(processMessage). With no caller, no prompt ever releases the browser wheel")
	})

	t.Run("the gateway registers the hook", func(t *testing.T) {
		hits := grepNonTestGoSources(t, "../gateway", "SetBrowserWheelReleaseHook(")
		assert.NotEmpty(t, hits,
			"pkg/gateway must call AgentLoop.SetBrowserWheelReleaseHook from production code "+
				"(newBrowserWSHandler). This assertion is the one that was missing while the release "+
				"hook had zero call sites in the entire repository")
	})
}

// grepNonTestGoSources returns the .go files under dir (non-recursive,
// excluding _test.go) whose contents contain needle. A plain scan is enough
// here: the assertion is "a production reference exists at all", and both
// needles are unambiguous identifiers.
func grepNonTestGoSources(t *testing.T, dir, needle string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "reading %s", dir)
	var hits []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, rerr := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, rerr, "reading %s", name)
		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // a comment mentioning the symbol is not a call site
			}
			if strings.Contains(trimmed, needle) && !strings.HasPrefix(trimmed, "func ") {
				hits = append(hits, name)
				break
			}
		}
	}
	return hits
}
