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
