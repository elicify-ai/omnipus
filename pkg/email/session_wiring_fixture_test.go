package email

// Order-independence fixture for FR-W1-2's process-wide wiring gate.
//
// sessionManagerWired() reads sharedSessionsWired, a process-wide atomic that
// only SharedMailSessions ever sets (pool.go). Once set it never clears, so a
// package that did not manage it would be order-dependent by construction:
// every nil-source client driving a withMailSession/mailboxStatus path
// (transport.go::ReadInbox/Search/ReadMessage/MarkSeen, view.go's read family,
// append.go::AppendMessage, agent_read.go::MailboxStatus,
// folder_discovery.go::Discovery.Resolve, attachment_parts.go::withValidatedRef)
// answers ErrSessionSourceMissing after the first SharedMailSessions call and
// legacy-dials before it — the same test passes or fails depending on which
// tests ran earlier, and the default file order (files sort alphabetically,
// tests run in source order) hides the class instead of exposing it.
//
// The fixture makes each test declare the wiring world it needs and restore
// the previous process state at cleanup, so outcomes are order-independent by
// construction — under the default order and under `go test -shuffle` alike.
// It is test-side only: the tests live in this package, so the unexported
// atomic is language-visible and no production seam is needed. The package
// runs no t.Parallel tests, so a cleanup-time restore cannot race a sibling.
//
// Worlds in this package:
//   - legacy-dial world (pinLegacyDialWorld): no manager wired; nil-source
//     clients ride the per-call dial. Declared by the construction harnesses
//     whose clients are deliberately source-less (startMemIMAP, newTestClient,
//     startEnvDropIMAP, and the other source-less client builders).
//   - wired world (pinSessionManagerWired(t, true)): the refusal test's world;
//     declared and restored by TestClient_MissingSourceFailsVisibly.
//   - source-injected world: clients carry a session source, so the gate is
//     never consulted (startViewIMAP/startViewIMAPRaw/poolFacadeForStub
//     inject via SetSessionSource). No pin needed — the flag is irrelevant.

import "testing"

// pinSessionManagerWired pins the process-wide manager-wired flag to wanted
// for the duration of the test and restores the previous value at cleanup.
func pinSessionManagerWired(t *testing.T, wanted bool) {
	t.Helper()
	prev := sessionManagerWired()
	sharedSessionsWired.Store(wanted)
	t.Cleanup(func() { sharedSessionsWired.Store(prev) })
}

// pinLegacyDialWorld declares the test's wiring world to be "no shared
// manager is wired in this process": nil-source clients ride the legacy
// per-call dial, exactly the pre-wiring behavior these tests verify.
func pinLegacyDialWorld(t *testing.T) {
	t.Helper()
	pinSessionManagerWired(t, false)
}
