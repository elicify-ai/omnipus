package systools_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Chief Q4-A: the conditional credential-setup explanation belongs to an
// uncredentialled CREATE, never to an existing server's bounded attachment.
// Exact failure reasons come from the prior WU-3 outcome contract/Q4 ruling,
// not from running the new implementation.
func TestWU4B_MCPAttachmentNonworkingReasonNeverUsesCreateTimePrefix(t *testing.T) {
	cases := []struct {
		name, status, errMsg, reason string
		reconcileErr                 error
		noStatus, noReconcile        bool
	}{
		{name: "error", status: "error", errMsg: "connection refused", reason: "connection failed: connection refused"},
		{name: "disconnected", status: "disconnected", reason: "the server is not connected"},
		{name: "reconcile-failed", noStatus: true, reconcileErr: errors.New("boom"), reason: "live reconciliation failed: boom"},
		{name: "unchecked", noStatus: true, noReconcile: true, reason: "not checked: live connection status is unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, ref := wu4bMatchingSelection(t, f, "add_mcp_server")
			f.cfg.Tools.MCP.Enabled = true
			f.persist(t)
			if tc.noReconcile {
				f.d.ReconcileMCP = nil
			} else {
				f.d.ReconcileMCP = func(context.Context) error { f.unlocked(t); f.reconciles++; return tc.reconcileErr }
			}
			if tc.noStatus {
				f.d.MCPStatus = nil
			} else {
				f.d.MCPStatus = func(string) (string, int, string) { return tc.status, 2, tc.errMsg }
			}
			before := f.snapshot(t)
			result := tool.Execute(context.Background(), args)
			wu3Success(t, result)
			body := wu3Outcome(t, result, "saved but not working")
			assert.Equal(t, tc.reason, body["reason"], "attachment must retain the actual reason, without the create-time setup prefix")
			assert.Equal(t, ref, f.cfg.Tools.MCP.Servers["fixture"].EnvRefs["TOKEN"])
			assert.Equal(t, before.credentials, f.snapshot(t).credentials)
			assert.Equal(t, before.saves+1, f.saves)
		})
	}
}
