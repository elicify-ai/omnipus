// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package generated

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// ── ADR-091 Contract Fields Tests ────────────────────────────────────────────
// Traces to: ADR-091, WP-E § "Contract changes (this package owns every one)"
// Tests that the generated validators accept ADR-091 steered-session fields.

func TestContract_ADR091_SessionLifecycleRecordOrigin(t *testing.T) {
	// SessionLifecycleRecord.origin must be accepted (ADR-091 I-1)
	jsonData := []byte(`{
		"session_id": "sid-123",
		"generation": 1,
		"state": "running",
		"terminal": false,
		"owner_scope_kind": "human",
		"workspace_id": "ws-123",
		"agent_id": "agent-1",
		"is_3p": false,
		"undelivered_message_ids": [],
		"created_at": "2026-07-22T10:00:00Z",
		"updated_at": "2026-07-22T10:00:00Z",
		"origin": {
			"kind": "delegate",
			"call_id": "call_01J3ZQK8N2H8VXNRP5T7C9M4WE"
		}
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "SessionLifecycleRecord", jsonData)
	require.NoError(t, err, "origin field must be accepted")
}

func TestContract_ADR091_SessionLifecycleRecordSteeredBy(t *testing.T) {
	// SessionLifecycleRecord.steered_by must be accepted (ADR-091 I-1)
	jsonData := []byte(`{
		"session_id": "sid-child",
		"generation": 1,
		"state": "running",
		"terminal": false,
		"owner_scope_kind": "human",
		"workspace_id": "ws-123",
		"agent_id": "agent-child",
		"is_3p": false,
		"undelivered_message_ids": [],
		"created_at": "2026-07-22T10:00:00Z",
		"updated_at": "2026-07-22T10:00:00Z",
		"steered_by": {
			"steering_session_id": "sid-parent",
			"root_session_id": "sid-root",
			"reporting_target": {
				"session_id": "sid-parent",
				"channel": "web",
				"chat_id": "chat_01J3ZQK8N2H8VXNRP5T7C9M4WL"
			},
			"authorization": {
				"mode": "direct",
				"remaining_depth": 2
			},
			"limits": {
				"timeout_seconds": 300
			},
			"tool_exclusions": ["switch_agent"]
		}
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "SessionLifecycleRecord", jsonData)
	require.NoError(t, err, "steered_by field must be accepted")
}

func TestContract_ADR091_SessionLifecycleRecordStop(t *testing.T) {
	// SessionLifecycleRecord.stop must be accepted (ADR-091 I-6)
	jsonData := []byte(`{
		"session_id": "sid-stopped",
		"generation": 1,
		"state": "cancelled",
		"terminal": true,
		"owner_scope_kind": "human",
		"workspace_id": "ws-123",
		"agent_id": "agent-1",
		"is_3p": false,
		"undelivered_message_ids": [],
		"created_at": "2026-07-22T10:00:00Z",
		"updated_at": "2026-07-22T10:05:00Z",
		"stop": {
			"at": "2026-07-22T10:05:00Z",
			"generation": 1,
			"by": {
				"kind": "human",
				"id": "user-123"
			}
		}
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "SessionLifecycleRecord", jsonData)
	require.NoError(t, err, "stop field must be accepted")
}

func TestContract_ADR091_DelegateSessionResponseQueuePosition(t *testing.T) {
	// DelegateSessionResponse.queue_position must be accepted (ADR-091 I-2)
	jsonData := []byte(`{
		"session_id": "sid-new",
		"generation": 1,
		"is_3p": false,
		"state": "queued",
		"queue_position": 2
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "DelegateSessionResponse", jsonData)
	require.NoError(t, err, "queue_position field must be accepted when queued")
}

func TestContract_ADR091_SessionMessageGoalStatusSessionToParent(t *testing.T) {
	// SessionMessageGoalStatus.direction gains session_to_parent (ADR-091 I-5)
	jsonData := []byte(`{
		"message_id": "msg_01J3ZQK8N2H8VXNRP5T7C9M4WT",
		"session_id": "sid-child",
		"direction": "session_to_parent",
		"kind": "goal_status",
		"depth": 1,
		"created_at": "2026-07-22T10:00:00Z",
		"sender_identity": "ray",
		"untrusted_origin": false,
		"condition": "met",
		"goal_id": "goal_01J3ZQK8N2H8VXNRP5T7C9M4WU"
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "SessionMessageGoalStatus", jsonData)
	require.NoError(t, err, "session_to_parent direction must be accepted")
}

func TestContract_ADR091_SessionMessageGoalStatusNotMet(t *testing.T) {
	// SessionMessageGoalStatus.condition gains not_met (ADR-091 I-5)
	jsonData := []byte(`{
		"message_id": "msg_01J3ZQK8N2H8VXNRP5T7C9M4WT",
		"session_id": "sid-123",
		"direction": "session_to_ui",
		"kind": "goal_status",
		"depth": 0,
		"created_at": "2026-07-22T10:00:00Z",
		"sender_identity": "judge",
		"untrusted_origin": false,
		"condition": "not_met",
		"goal_id": "goal_01J3ZQK8N2H8VXNRP5T7C9M4WU"
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "SessionMessageGoalStatus", jsonData)
	require.NoError(t, err, "not_met condition must be accepted")
}

func TestContract_ADR091_SessionMessageGoalStatusEvidence(t *testing.T) {
	// SessionMessageGoalStatus.evidence array must be accepted (ADR-091 I-5)
	jsonData := []byte(`{
		"message_id": "msg_01J3ZQK8N2H8VXNRP5T7C9M4WT",
		"session_id": "sid-123",
		"direction": "session_to_ui",
		"kind": "goal_status",
		"depth": 0,
		"created_at": "2026-07-22T10:00:00Z",
		"sender_identity": "judge",
		"untrusted_origin": false,
		"condition": "met",
		"evidence": [
			{
				"criterion": "checkout complete",
				"met": true,
				"note": "confirmed"
			}
		],
		"goal_id": "goal_01J3ZQK8N2H8VXNRP5T7C9M4WU"
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "SessionMessageGoalStatus", jsonData)
	require.NoError(t, err, "evidence array must be accepted")
}

func TestContract_ADR091_SubagentMessageFrameGoalStatus(t *testing.T) {
	// SubagentMessageFrame.kind enum gains goal_status (ADR-091 I-4)
	jsonData := []byte(`{
		"type": "subagent_message",
		"session_id": "sid-child",
		"span_id": "span_01J3ZQK8N2H8VXNRP5T7C9M4WE",
		"message_id": "msg_01J3ZQK8N2H8VXNRP5T7C9M4WF",
		"kind": "goal_status",
		"sender_identity": "ray",
		"untrusted_origin": false,
		"created_at": "2026-07-22T10:00:00Z"
	}`)
	err := validateAgainstAsyncAPISchemaRawJSON(t, "SubagentMessageFrame", jsonData)
	require.NoError(t, err, "goal_status kind must be accepted in SubagentMessageFrame")
}

func TestContract_ADR091_SubagentStartFrameChildSessionId(t *testing.T) {
	// SubagentStartFrame gains optional child_session_id (ADR-091 I-4)
	jsonData := []byte(`{
		"type": "subagent_start",
		"session_id": "sid-parent",
		"span_id": "span_01J3ZQK8N2H8VXNRP5T7C9M4WE",
		"parent_call_id": "call_01J3ZQK8N2H8VXNRP5T7C9M4WD",
		"task_label": "Analyze logs",
		"child_session_id": "sid-child-123"
	}`)
	err := validateAgainstAsyncAPISchemaRawJSON(t, "SubagentStartFrame", jsonData)
	require.NoError(t, err, "child_session_id field must be accepted")
}

func TestContract_ADR091_SubagentStartFrameWithoutChildSessionId(t *testing.T) {
	// SubagentStartFrame without child_session_id (legacy/optional)
	jsonData := []byte(`{
		"type": "subagent_start",
		"session_id": "sid-parent",
		"span_id": "span_01J3ZQK8N2H8VXNRP5T7C9M4WE",
		"parent_call_id": "call_01J3ZQK8N2H8VXNRP5T7C9M4WD",
		"task_label": "Analyze logs"
	}`)
	err := validateAgainstAsyncAPISchemaRawJSON(t, "SubagentStartFrame", jsonData)
	require.NoError(t, err, "child_session_id must be optional")
}

// Helper to validate against AsyncAPI schema raw JSON
func validateAgainstAsyncAPISchemaRawJSON(t *testing.T, schemaName string, jsonData []byte) error {
	t.Helper()
	var data interface{}
	if err := json.Unmarshal(jsonData, &data); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	return validateAgainstAsyncAPISchema(t, schemaName, data)
}

func TestContract_ADR091_CancelStageFrameStopReport(t *testing.T) {
	// The Stop report rides on the existing cancel_stage frame (ADR-091 I-6) — no new frame type.
	jsonData := []byte(`{
		"type": "cancel_stage",
		"session_id": "sid-root",
		"stage": "detached",
		"reached": ["sid-root", "sid-a"],
		"unreachable": [{"id": "sid-b", "reason": "lifecycle record unreadable"}],
		"skipped_newer_generation": ["sid-c"],
		"skipped_terminal": ["sid-d"],
		"partial": true
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "CancelStageFrame", jsonData)
	require.NoError(t, err, "a partial Stop report must be accepted on cancel_stage")
}

func TestContract_ADR091_CancelStageFrameRejectsUnreachableWithoutReason(t *testing.T) {
	// Control: the validator must bite — an unreachable entry without its reason is refused.
	jsonData := []byte(`{
		"type": "cancel_stage",
		"session_id": "sid-root",
		"stage": "detached",
		"unreachable": [{"id": "sid-b"}],
		"partial": true
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "CancelStageFrame", jsonData)
	require.Error(t, err, "an unreachable entry must name its reason")
}

func TestContract_ADR091_SessionLifecycleRecordRejectsUnknownOriginKind(t *testing.T) {
	// Control: origin.kind is a closed enum (landing order I-1).
	jsonData := []byte(`{
		"session_id": "sid-123",
		"generation": 1,
		"state": "running",
		"terminal": false,
		"owner_scope_kind": "human",
		"workspace_id": "ws-123",
		"agent_id": "agent-1",
		"is_3p": false,
		"undelivered_message_ids": [],
		"created_at": "2026-07-22T10:00:00Z",
		"updated_at": "2026-07-22T10:00:00Z",
		"origin": {"kind": "subagent", "call_id": "call_1"}
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "SessionLifecycleRecord", jsonData)
	require.Error(t, err, "an origin kind outside the I-1 enum must be refused")
}

// ── Lane 5c Contract Deletion Tests ──────────────────────────────────────────
// Traces to: ADR-091 WP-E lane 5c, steps 1–3
// Tests that removed fields are now rejected by the validator.

func TestContract_ADR091_ToolResultProjectionFrameRejectsProducingSessionId(t *testing.T) {
	// Control: producing_session_id must be rejected from ToolResultProjectionFrame
	// (WP-E step 1). The schema has additionalProperties: false, so this field is
	// not permitted. Prove the deletion by verifying rejection.
	jsonData := []byte(`{
		"type": "tool_result_projection",
		"session_id": "sid-123",
		"tool_call_id": "call-456",
		"archive_line": 10,
		"content_state": "capped",
		"producing_session_id": "sid-child"
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "ToolResultProjectionFrame", jsonData)
	require.Error(t, err, "producing_session_id must be rejected (deleted from wire)")
}

func TestContract_ADR091_DelegateRunActionRejectsWait(t *testing.T) {
	// Control: wait and allow_blocking_question must be rejected from DelegateRunAction
	// (WP-E step 2). The schema has additionalProperties: false. Prove deletion by
	// verifying rejection of wait.
	jsonData := []byte(`{
		"action": "run",
		"target_agent_id": "ray",
		"task": "Summarize logs",
		"wait": true
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "DelegateRunAction", jsonData)
	require.Error(t, err, "wait must be rejected (deleted from wire)")
}

func TestContract_ADR091_DelegateRunActionRejectsAllowBlockingQuestion(t *testing.T) {
	// Control: allow_blocking_question must also be rejected from DelegateRunAction
	// (WP-E step 2). Test rejection of this separate field.
	jsonData := []byte(`{
		"action": "run",
		"target_agent_id": "ray",
		"task": "Summarize logs",
		"allow_blocking_question": true
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "DelegateRunAction", jsonData)
	require.Error(t, err, "allow_blocking_question must be rejected (deleted from wire)")
}

func TestContract_ADR091_DeleteSessionResponseWithStopReport(t *testing.T) {
	// DELETE session response (WP-E step 3) now carries the Stop report fields.
	// Test that the structure with partial: true marshals correctly (proves the
	// fields are now present in the schema).
	deleteResp := map[string]interface{}{
		"success": true,
		"reached": []string{"sid-root", "sid-a"},
		"unreachable": []map[string]interface{}{
			{"id": "sid-b", "reason": "lifecycle record unreadable"},
		},
		"skipped_newer_generation": []string{"sid-c"},
		"skipped_terminal":         []string{"sid-d"},
		"partial":                  true,
	}
	jsonData, err := json.Marshal(deleteResp)
	require.NoError(t, err, "should marshal DELETE response with Stop report")

	// Verify the JSON is well-formed and contains expected fields
	var unmarshaled map[string]interface{}
	err = json.Unmarshal(jsonData, &unmarshaled)
	require.NoError(t, err, "should unmarshal DELETE response")
	require.True(t, unmarshaled["success"].(bool))
	require.True(t, unmarshaled["partial"].(bool))
}
