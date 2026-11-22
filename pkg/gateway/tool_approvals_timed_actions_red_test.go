//go:build goolm && stdjson

package gateway

// Order 58 — TestToolApprovals_TimedActions (S-119, S-124, S-125; C-26
// audit). Oracle: skills-tools-redesign-spec.md —
//   S-119: `delete_task` at global Ask, Auto-approve off → the card offers
//        Approve Once, Allow for 1 hour / 3 hours / 1 day, Deny and Cancel,
//        and the frame carries `offered_actions`
//        [allow_once, allow_for, deny, cancel] and `allow_durations`
//        [1h, 3h, 1d]. No "Always Allow" anywhere (the shown-text half is
//        order 62's TS pack).
//   S-124: on a pending delete_task card the answers {action: allow},
//        {action: allow_for}, {action: allow_for, duration: 2h} and
//        {action: allow_once, duration: 1h} get 400 "This choice is no
//        longer offered. Choose Approve Once or a time limit.", 400
//        "Choose 1 hour, 3 hours or 1 day.", 400 "Choose 1 hour, 3 hours
//        or 1 day." and 400 "A time limit applies only to Allow for.";
//        nothing is stored and the card can still be answered with
//        {action: allow_for, duration: 1h}, which gets 200 with `expires_at`
//        one hour later (server-derived wall-clock time, C-26 Length).
//   S-125: the always-ask tool `set_channel_outbound` keeps its card
//        (Approve Once, Deny, Cancel, "This approval applies to this one
//        request."), `allow_durations` is empty, and an allow_for answer is
//        refused with 400 and nothing is remembered (checked before the
//        offered-actions rule, C-26).
//   C-26 audit: a non-shell timed allowance writes the existing
//        approval-decision entry with action allow_for, duration,
//        expires_at, the agent and the tool key.
//
// Harness: the real handler (rest_tool_registry.go::HandleToolApprovals)
// through postToolApprovalRaw, the real raise path
// (approvals.go::approvalRegistryV2.requestApproval) and the real reload
// snapshot (ws_tool_approval.go::pendingApprovalSnapshot).
//
// RED today: the handler refuses allow_for/approve_version* with the
// existing "unknown action" 400 (the #1963 TODO), `allow` still resolves
// the card and records a session grant, and neither frame builder computes
// offered_actions/allow_durations. Every leg below fails on that missing
// behaviour.
import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"

	"github.com/stretchr/testify/require"
)

func TestToolApprovals_TimedActions(t *testing.T) {
	const argsA = `{"task_id":"t1"}`
	raiseCard := func(t *testing.T, api *restAPI, reg *approvalRegistryV2, tool string) *approvalEntry {
		t.Helper()
		entry, accepted := reg.requestApproval("tc-timed-1", tool,
			map[string]any{"task_id": "t1"}, "mia", "sess-timed-1", "turn-timed-1")
		require.True(t, accepted, "the raised approval must be admitted as pending")
		require.NotNil(t, entry)
		return entry
	}

	t.Run("S-119: the card frame offers the time choices and no Always Allow", func(t *testing.T) {
		msgBus := bus.NewMessageBus()
		t.Cleanup(msgBus.Close)
		handler, _ := newTestWSHandlerForModelName(t, msgBus)
		reg := newApprovalRegistryV2(64, 300*time.Second)
		entry := raiseCard(t, nil, reg, "delete_task")
		t.Cleanup(func() { go func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel, false) }() })
		go func() { <-entry.resultCh }()
		handler.approvalRegV2 = reg

		snap := handler.pendingApprovalSnapshot(entry)
		require.Equal(t, []string{"allow_once", "allow_for", "deny", "cancel"}, snap.OfferedActions,
			"S-119: the frame carries offered_actions [allow_once, allow_for, deny, cancel], got %+v", snap.OfferedActions)
		require.Equal(t, []string{"1h", "3h", "1d"}, snap.AllowDurations,
			"S-119: the frame carries allow_durations [1h, 3h, 1d], got %+v", snap.AllowDurations)
	})

	t.Run("S-124 final: allow_for 1h is accepted and reports expires_at one hour later", func(t *testing.T) {
		api, reg := newTestRestAPIWithApprovalReg(t)
		entry := raiseCard(t, api, reg, "delete_task")

		w := postToolApprovalRaw(t, api, entry.ApprovalID, `{"action":"allow_for","duration":"1h"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"S-124: the well-formed time answer is accepted, got %d, body: %s", w.Code, w.Body.String())

		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body: %s", w.Body.String())
		require.Equal(t, true, body["grant_recorded"],
			"C-26: grant_recorded now describes the timed allowance, body: %s", w.Body.String())
		expiresAt, _ := body["expires_at"].(string)
		require.NotEmpty(t, expiresAt,
			"C-26: the response carries the server-derived expires_at, body: %s", w.Body.String())
		parsed, err := time.Parse(time.RFC3339, expiresAt)
		require.NoError(t, err, "C-26: expires_at is a date-time, got %q", expiresAt)
		now := time.Now()
		require.WithinDuration(t, now.Add(time.Hour), parsed, 5*time.Minute,
			"S-124: expires_at is one hour later (server wall clock for the 1h duration), got %s", expiresAt)
	})

	t.Run("S-124: the retired and malformed answers are refused and store nothing", func(t *testing.T) {
		api, reg := newTestRestAPIWithApprovalReg(t)
		entry := raiseCard(t, api, reg, "delete_task")
		args := map[string]any{"task_id": "t1"}

		w := postToolApprovalRaw(t, api, entry.ApprovalID, `{"action":"allow"}`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"S-124: the retired allow choice is refused, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "This choice is no longer offered. Choose Approve Once or a time limit.",
			"S-124: the retired-action text, body: %s", w.Body.String())

		w = postToolApprovalRaw(t, api, entry.ApprovalID, `{"action":"allow_for"}`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"S-124: allow_for without a duration is refused, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "Choose 1 hour, 3 hours or 1 day.",
			"S-124: the duration text, body: %s", w.Body.String())

		w = postToolApprovalRaw(t, api, entry.ApprovalID, `{"action":"allow_for","duration":"2h"}`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"S-124: the 2h value is refused, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "Choose 1 hour, 3 hours or 1 day.",
			"S-124: only the three choices are offered, body: %s", w.Body.String())

		w = postToolApprovalRaw(t, api, entry.ApprovalID, `{"action":"allow_once","duration":"1h"}`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"S-124: a duration on allow_once is refused, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "A time limit applies only to Allow for.",
			"S-124: the misplaced-duration text, body: %s", w.Body.String())

		// Nothing was stored by any refused answer, and the card can still be
		// answered with the good one.
		require.False(t, api.agentLoop.ApprovalGrants().IsAllowed(entry.SessionID, entry.AgentID, "delete_task", args),
			"S-124: nothing is stored by a refused answer (no session grant, no allowance)")
		w = postToolApprovalRaw(t, api, entry.ApprovalID, `{"action":"allow_for","duration":"1h"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"S-124: the card survived the refusals and the good answer is 200, got %d, body: %s",
			w.Code, w.Body.String())
	})

	t.Run("S-125: the always-ask tool keeps its one-request card", func(t *testing.T) {
		msgBus := bus.NewMessageBus()
		t.Cleanup(msgBus.Close)
		handler, _ := newTestWSHandlerForModelName(t, msgBus)
		reg := newApprovalRegistryV2(64, 300*time.Second)
		entry := raiseCard(t, nil, reg, "set_channel_outbound")
		t.Cleanup(func() { go func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel, false) }() })
		go func() { <-entry.resultCh }()
		handler.approvalRegV2 = reg

		snap := handler.pendingApprovalSnapshot(entry)
		require.Equal(t, []string{"allow_once", "deny", "cancel"}, snap.OfferedActions,
			"S-125: the always-ask card offers Approve Once, Deny and Cancel, got %+v", snap.OfferedActions)
		require.Empty(t, snap.AllowDurations,
			"S-125: allow_durations is empty — no time choice, got %+v", snap.AllowDurations)

		api, reg2 := newTestRestAPIWithApprovalReg(t)
		entry2 := raiseCard(t, api, reg2, "set_channel_outbound")
		w := postToolApprovalRaw(t, api, entry2.ApprovalID, `{"action":"allow_for","duration":"1h"}`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"S-125: the allow_for answer is refused, got %d, body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "always asks for approval",
			"S-125: today's always-ask refusal is kept for allow_for, body: %s", w.Body.String())
		require.NotContains(t, w.Body.String(), "expires_at",
			"S-125: nothing is remembered — no allowance in the answer, body: %s", w.Body.String())
	})

	t.Run("C-26 audit: the timed allowance writes its decision entry", func(t *testing.T) {
		api, reg := newTestRestAPIWithApprovalReg(t)
		auditDir := attachTestAuditor(t, api)
		entry := raiseCard(t, api, reg, "delete_task")

		w := postToolApprovalRaw(t, api, entry.ApprovalID, `{"action":"allow_for","duration":"1h"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"setup: the timed answer must be accepted, got %d, body: %s", w.Code, w.Body.String())

		matching := 0
		for _, e := range readAllAuditEntries(t, auditDir) {
			agentID, _ := e["agent_id"].(string)
			tool, _ := e["tool"].(string)
			duration, _ := e["duration"].(string)
			expiresAt, _ := e["expires_at"].(string)
			if agentID == "mia" && tool == "delete_task" && duration == "1h" && expiresAt != "" {
				matching++
			}
		}
		require.Equal(t, 1, matching,
			"C-26 audit: exactly one approval-decision entry names the agent, the tool key, the duration and its end time, found %d in %s",
			matching, auditDir)
	})
}

// readAllAuditEntries reads every audit entry in the directory, whatever its
// event — the C-26 audit oracle names fields, not a new event name ("the
// existing approval-decision entry").
func readAllAuditEntries(t *testing.T, auditDir string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "line=%s", line)
		out = append(out, entry)
	}
	return out
}
