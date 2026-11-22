//go:build goolm && stdjson

package gateway

// Order 64 — TestToolApprovals_ExactActionNoTimeChoice (S-129; Dataset D17
// rows 21–25; C-26 point 9, founder Q5 A 2026-11-22). Oracle:
// skills-tools-redesign-spec.md —
//   C-26 point 9: the exact-action tools environment_setup, request_mount,
//        send_email, browser_evaluate and set_config keep a card for every
//        action. Their cards offer Approve Once and Deny only, with no
//        time choices, so the person always sees the exact action (the
//        ADR-090 promise). A timed allowance can never cover them, and the
//        server refuses allow_for through the offered-actions rule (C-24):
//        409 "This choice is not available for this request.", nothing
//        stored.
//   S-129: with Auto-approve on, Mia's call shows the card; an
//        {action: allow_for, duration} answer gets the 409 and stores
//        nothing; Approve Once releases this one call; the same call again
//        shows a NEW card with its exact action.
//
// Harness: the real handler (rest_tool_registry.go::HandleToolApprovals),
// the real raise path and the real reload snapshot — the same shape as
// order 58's pack. The exact-action list itself is GREEN's new
// pkg/tools/exact_action.go::ExactActionTools (beside AlwaysAskTools) —
// exactActionToolsOf below is that seam; nil keeps the RED run loud
// (BLOCKED), never a skip.
//
// RED today: the handler computes no offered actions (every frame's
// offered_actions is empty) and answers allow_for with the "unknown
// action" 400 — every card leg fails on that; the tool list is BLOCKED at
// its seam.
import (
	"net/http"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"

	"github.com/stretchr/testify/require"
)

// exactActionToolsOf returns the exact-action tool list (C-26 point 9).
// GREEN wires it to pkg/tools/exact_action.go::ExactActionTools; nil keeps
// the RED run loud (BLOCKED), never a skip.
var exactActionToolsOf func(t testing.TB) []string

func TestToolApprovals_ExactActionNoTimeChoice(t *testing.T) {
	t.Run("C-26 point 9: the exact-action list holds exactly the five tools", func(t *testing.T) {
		if exactActionToolsOf == nil {
			t.Fatal("BLOCKED: the exact-action tool list (pkg/tools/exact_action.go::" +
				"ExactActionTools, beside AlwaysAskTools) is not implemented — required by " +
				"C-26 point 9 (founder Q5 A) and Dataset D17 rows 21–25")
		}
		require.Equal(t,
			[]string{"browser_evaluate", "environment_setup", "request_mount", "send_email", "set_config"},
			exactActionToolsOf(t),
			"C-26 point 9: exactly the five exact-action tools (founder Q5 A)")
	})

	rows := []struct {
		name     string
		tool     string
		args     map[string]any
		exactArg string // the D17 row's exact-action fact the repeat card must show
	}{
		{"D17#21 environment_setup", "environment_setup",
			map[string]any{"command": "apt-get install -y poppler-utils"}, "poppler-utils"},
		{"D17#22 request_mount", "request_mount",
			map[string]any{"path": "/home/p/Projects"}, "/home/p/Projects"},
		{"D17#23 send_email", "send_email",
			map[string]any{"to": "person@example.com", "subject": "Quarterly report", "body": "Hello."}, "person@example.com"},
		{"D17#24 browser_evaluate", "browser_evaluate",
			map[string]any{"script": "document.title"}, "document.title"},
		{"D17#25 set_config", "set_config",
			map[string]any{"key": "gateway.preview_enabled", "value": true}, "gateway.preview_enabled"},
	}

	for _, row := range rows {
		t.Run(row.name+": Approve Once and Deny only, allow_for refused, every action asks", func(t *testing.T) {
			msgBus := bus.NewMessageBus()
			t.Cleanup(msgBus.Close)
			handler, _ := newTestWSHandlerForModelName(t, msgBus)
			reg := newApprovalRegistryV2(64, 300*time.Second)

			entry, accepted := reg.requestApproval("tc-exact-1", row.tool, row.args,
				"mia", "sess-exact", "turn-exact-1")
			require.True(t, accepted, "S-129: the card appears even with Auto-approve on")
			t.Cleanup(func() { go func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel, false) }() })
			go func() { <-entry.resultCh }()
			handler.approvalRegV2 = reg

			snap := handler.pendingApprovalSnapshot(entry)
			require.Equal(t, []string{"allow_once", "deny", "cancel"}, snap.OfferedActions,
				"S-129: the card offers Approve Once and Deny only (Cancel where it has it today), got %+v",
				snap.OfferedActions)
			require.Empty(t, snap.AllowDurations,
				"S-129: the frame's allow_durations is empty — no time choice")

			api, reg2 := newTestRestAPIWithApprovalReg(t)
			entry2, accepted2 := reg2.requestApproval("tc-exact-2", row.tool, row.args,
				"mia", "sess-exact", "turn-exact-2")
			require.True(t, accepted2)
			w := postToolApprovalRaw(t, api, entry2.ApprovalID, `{"action":"allow_for","duration":"1d"}`)
			require.Equal(t, http.StatusConflict, w.Code,
				"S-129: the allow_for answer is refused through the offered-actions rule, got %d, body: %s",
				w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), notOfferedText,
				"C-26 point 9: the 409 carries the offered-actions text, body: %s", w.Body.String())
			require.NotContains(t, w.Body.String(), "expires_at",
				"S-129: nothing is stored — no allowance in the answer, body: %s", w.Body.String())

			// Approve Once releases this one call.
			w = postToolApprovalRaw(t, api, entry2.ApprovalID, `{"action":"allow_once"}`)
			require.Equal(t, http.StatusOK, w.Code,
				"S-129: Approve Once answers the card, got %d, body: %s", w.Code, w.Body.String())

			// The same call again raises a NEW card with its exact action.
			again, accepted3 := reg2.requestApproval("tc-exact-3", row.tool, row.args,
				"mia", "sess-exact", "turn-exact-3")
			require.True(t, accepted3, "S-129: the same call asks again — no allowance ever covers it")
			snapAgain := handler.pendingApprovalSnapshot(again)
			require.NotNil(t, snapAgain, "S-129: the new card exists")
			require.Equal(t, row.tool, snapAgain.ToolName,
				"S-129: the new card names the exact-action tool")
			require.Equal(t, []string{"allow_once", "deny", "cancel"}, snapAgain.OfferedActions,
				"S-129: the new card offers the same two choices, got %+v", snapAgain.OfferedActions)
			require.NotEqual(t, entry.ToolCallID, snapAgain.ToolCallId,
				"S-129: the new card is a distinct call, not the answered one")
		})
	}
}
