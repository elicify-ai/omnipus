package runner

import "testing"

// Team-lead 2026-11-21 08:05Z requires the real factory capability check,
// not a test-side implementation. It is introduced by bd8ded76a but absent
// from this commissioned frozen baseline. Same-author reconciliation must
// replace this blocker with the prepared behavioral test once that exact
// production API is integrated. A missing symbol is not a behavioral RED.
func TestOpenCodeGateBFactory_RequiresNativeConversationReporter(t *testing.T) {
	t.Fatal("BLOCKED: runner.requireNativeConversationReporter not implemented on the frozen production baseline — required by team-lead 2026-11-21 08:05Z factory-capability scope; integrate the approved implementation before same-author test reconciliation")
}
