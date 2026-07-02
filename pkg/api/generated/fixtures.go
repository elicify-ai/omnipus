package generated

import "time"

// ── WebSocket frame fixtures (AsyncAPI) ─────────────────────────────────────

// ToolApprovalRequiredFrame — the Ava-chat bug type. args MUST be a non-nil map.

func FixtureToolApprovalRequiredFrame_Populated() ToolApprovalRequiredFrame {
	return ToolApprovalRequiredFrame{
		Type:        "tool_approval_required",
		ApprovalId:  "ap-550e8400-e29b-41d4-a716-446655440001",
		ToolCallId:  "tc-550e8400-e29b-41d4-a716-446655440002",
		ToolName:    "workspace.shell",
		Args:        map[string]any{"command": "ls -la", "working_dir": "/tmp"},
		AgentId:     "jim",
		SessionId:   "sess-550e8400-e29b-41d4-a716-446655440003",
		TurnId:      "turn-550e8400-e29b-41d4-a716-446655440004",
		ExpiresInMs: 30000,
	}
}

// FixtureToolApprovalRequiredFrame_ZeroValue — Go zero values.
// Expected behavior: should FAIL JSON schema validation because:
//   - type is "" (schema requires const "tool_approval_required")
//   - approval_id is "" (minLength: 1)
//   - args is nil (marshals to null, schema requires type: object)
//   - other minLength:1 fields are ""
func FixtureToolApprovalRequiredFrame_ZeroValue() ToolApprovalRequiredFrame {
	return ToolApprovalRequiredFrame{}
}

// FixtureToolApprovalRequiredFrame_NilArgs — the exact state that caused the Ava-chat crash.
// args is nil → marshals to "args":null → schema rejects because type: object, not nullable.
func FixtureToolApprovalRequiredFrame_NilArgs() ToolApprovalRequiredFrame {
	return ToolApprovalRequiredFrame{
		Type:        "tool_approval_required",
		ApprovalId:  "ap-1",
		ToolCallId:  "tc-1",
		ToolName:    "workspace.shell",
		Args:        nil, // THE BUG — must be caught by the contract test
		AgentId:     "jim",
		SessionId:   "sess-1",
		TurnId:      "turn-1",
		ExpiresInMs: 30000,
	}
}

// FixtureToolApprovalRequiredFrame_Edge — unicode tool name, empty args object (valid), large expires_in_ms.
func FixtureToolApprovalRequiredFrame_Edge() ToolApprovalRequiredFrame {
	return ToolApprovalRequiredFrame{
		Type:        "tool_approval_required",
		ApprovalId:  "ap-edge-" + repeatStr("x", 100),
		ToolCallId:  "tc-edge-1",
		ToolName:    "system.spawn_subagent",
		Args:        map[string]any{}, // empty object is valid — not null
		AgentId:     "ava-🐙",
		SessionId:   "sess-edge-1",
		TurnId:      "turn-edge-1",
		ExpiresInMs: 86400000, // 24 hours
	}
}

// SessionStateFrame — pending_approvals MUST be a non-nil slice.

func FixtureSessionStateFrame_Populated() SessionStateFrame {
	return SessionStateFrame{
		Type:      "session_state",
		UserId:    "user-admin-1",
		EmittedAt: time.Now().UTC().Format(time.RFC3339),
		PendingApprovals: []SessionStatePendingApproval{
			{
				ApprovalId:  "ap-1",
				SessionId:   "sess-1",
				ToolName:    "workspace.shell",
				AgentId:     "jim",
				ExpiresInMs: 30000,
			},
		},
	}
}

// FixtureSessionStateFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="", user_id="", pending_approvals=nil (marshals to null),
// emitted_at="" (not a valid date-time).
func FixtureSessionStateFrame_ZeroValue() SessionStateFrame {
	return SessionStateFrame{}
}

// FixtureSessionStateFrame_EmptyApprovals — valid: empty but non-nil slice.
// This is the common case when no approvals are pending.
func FixtureSessionStateFrame_EmptyApprovals() SessionStateFrame {
	return SessionStateFrame{
		Type:             "session_state",
		UserId:           "user-admin-1",
		EmittedAt:        "2026-05-17T10:00:00Z",
		PendingApprovals: []SessionStatePendingApproval{}, // empty slice, not nil
	}
}

// FixtureSessionStateFrame_Edge — multiple approvals, unicode user ID.
func FixtureSessionStateFrame_Edge() SessionStateFrame {
	return SessionStateFrame{
		Type:      "session_state",
		UserId:    "user-unicode-🔑",
		EmittedAt: "2026-05-17T00:00:01Z",
		PendingApprovals: []SessionStatePendingApproval{
			{ApprovalId: "ap-1", SessionId: "sess-1", ToolName: "tool.a", AgentId: "jim", ExpiresInMs: 1},
			{ApprovalId: "ap-2", SessionId: "sess-2", ToolName: "tool.b", AgentId: "ava", ExpiresInMs: 2},
			{ApprovalId: "ap-3", SessionId: "sess-3", ToolName: "tool.c", AgentId: "rex", ExpiresInMs: 3},
		},
	}
}

// MediaFrame — parts MUST be a non-nil, non-empty slice.

func FixtureMediaFrame_Populated() MediaFrame {
	return MediaFrame{
		Type:      "media",
		SessionId: "sess-1",
		Parts: []MediaPart{
			{
				Type:        "image",
				Url:         "/api/v1/media/screenshot-abc123.png",
				Filename:    "screenshot.png",
				ContentType: "image/png",
				Caption:     strPtr("Browser screenshot"),
			},
		},
	}
}

// FixtureMediaFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="", session_id="", parts=nil (marshals to null).
func FixtureMediaFrame_ZeroValue() MediaFrame {
	return MediaFrame{}
}

// FixtureMediaFrame_NilParts — parts is nil — this must FAIL validation.
func FixtureMediaFrame_NilParts() MediaFrame {
	return MediaFrame{
		Type:      "media",
		SessionId: "sess-1",
		Parts:     nil, // must fail: schema requires array with minItems: 1
	}
}

// FixtureMediaFrame_Edge — multiple parts with various filenames.
func FixtureMediaFrame_Edge() MediaFrame {
	return MediaFrame{
		Type:      "media",
		SessionId: "sess-edge-1",
		Parts: []MediaPart{
			{Type: "image", Url: "/api/v1/media/img1.png", Filename: "screenshot.png", ContentType: "image/png"},
			{Type: "file", Url: "/api/v1/media/doc1.pdf", Filename: "report_2026.pdf", ContentType: "application/pdf"},
			{
				Type:        "audio",
				Url:         "/api/v1/media/clip1.mp3",
				Filename:    "recording.mp3",
				ContentType: "audio/mpeg",
				Caption:     strPtr("Voice note"),
			},
		},
	}
}

// ToolCallStartFrame — params MUST be a non-nil map.

func FixtureToolCallStartFrame_Populated() ToolCallStartFrame {
	parentCallId := "parent-call-abc"
	agentId := "jim"
	return ToolCallStartFrame{
		Type:         "tool_call_start",
		SessionId:    "sess-1",
		Tool:         "workspace.shell",
		CallId:       "call-xyz-1",
		Params:       map[string]any{"command": "echo hello", "working_dir": "/workspace"},
		ParentCallId: &parentCallId,
		AgentId:      &agentId,
	}
}

// FixtureToolCallStartFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="", session_id="", tool="", call_id="", params=nil.
func FixtureToolCallStartFrame_ZeroValue() ToolCallStartFrame {
	return ToolCallStartFrame{}
}

// FixtureToolCallStartFrame_NilParams — params is nil — this must FAIL validation.
func FixtureToolCallStartFrame_NilParams() ToolCallStartFrame {
	return ToolCallStartFrame{
		Type:      "tool_call_start",
		SessionId: "sess-1",
		Tool:      "workspace.shell",
		CallId:    "call-1",
		Params:    nil, // must fail: schema requires type: object
	}
}

// FixtureToolCallStartFrame_Edge — no-parameter tool, no parent call.
func FixtureToolCallStartFrame_Edge() ToolCallStartFrame {
	return ToolCallStartFrame{
		Type:      "tool_call_start",
		SessionId: "sess-edge-1",
		Tool:      "system.ping",
		CallId:    "call-edge-" + repeatStr("a", 50),
		Params:    map[string]any{}, // valid empty object (no-arg tool)
	}
}

// DoneFrame

func FixtureDoneFrame_Populated() DoneFrame {
	tokens := float64(1234)
	cost := float64(0.00412)
	durationMs := float64(3720)
	tokensDropped := float64(2)
	framesEmitted := float64(47)
	orphanCount := float64(0)
	dupCount := float64(0)
	truncCount := float64(1)
	replayErr := false
	return DoneFrame{
		Type:      "done",
		SessionId: "sess-1",
		Stats: &DoneStats{
			Tokens:                   &tokens,
			Cost:                     &cost,
			DurationMs:               &durationMs,
			TokensDropped:            &tokensDropped,
			FramesEmitted:            &framesEmitted,
			OrphanCount:              &orphanCount,
			DuplicateToolCallIdCount: &dupCount,
			TruncatedResultCount:     &truncCount,
			ReplayError:              &replayErr,
		},
	}
}

func FixtureDoneFrame_ZeroValue() DoneFrame {
	return DoneFrame{}
}

func FixtureDoneFrame_NoStats() DoneFrame {
	return DoneFrame{
		Type:      "done",
		SessionId: "sess-1",
		Stats:     nil, // stats is optional per schema
	}
}

func FixtureDoneFrame_Edge() DoneFrame {
	cost := float64(0)
	return DoneFrame{
		Type:      "done",
		SessionId: "sess-unicode-🏁",
		Stats:     &DoneStats{Cost: &cost},
	}
}

// ErrorFrame

func FixtureErrorFrame_Populated() ErrorFrame {
	sessId := "sess-1"
	return ErrorFrame{
		Type:      "error",
		Message:   "LLM rate limit exceeded — retry after 60 seconds",
		SessionId: &sessId,
	}
}

func FixtureErrorFrame_ZeroValue() ErrorFrame {
	return ErrorFrame{}
}

func FixtureErrorFrame_Edge() ErrorFrame {
	return ErrorFrame{
		Type:    "error",
		Message: repeatStr("x", 4096), // long error message
	}
}

// TokenFrame

func FixtureTokenFrame_Populated() TokenFrame {
	return TokenFrame{
		Type:      "token",
		SessionId: "sess-1",
		Content:   "Hello, world!",
	}
}

func FixtureTokenFrame_ZeroValue() TokenFrame {
	return TokenFrame{}
}

func FixtureTokenFrame_Edge() TokenFrame {
	return TokenFrame{
		Type:      "token",
		SessionId: "sess-edge-1",
		Content:   "streaming token with special chars: -- hello world 123",
	}
}

// ToolCallResultFrame

func FixtureToolCallResultFrame_Populated() ToolCallResultFrame {
	durationMs := 128
	agentId := "jim"
	parentCallId := "parent-1"
	return ToolCallResultFrame{
		Type:         "tool_call_result",
		SessionId:    "sess-1",
		Tool:         "workspace.shell",
		CallId:       "call-1",
		Result:       map[string]any{"stdout": "hello\n", "exit_code": float64(0)},
		Status:       "success",
		DurationMs:   &durationMs,
		AgentId:      &agentId,
		ParentCallId: &parentCallId,
	}
}

func FixtureToolCallResultFrame_ZeroValue() ToolCallResultFrame {
	return ToolCallResultFrame{}
}

func FixtureToolCallResultFrame_Error() ToolCallResultFrame {
	errMsg := "command not found: foobar"
	return ToolCallResultFrame{
		Type:      "tool_call_result",
		SessionId: "sess-1",
		Tool:      "workspace.shell",
		CallId:    "call-err-1",
		Result:    nil,
		Status:    "error",
		Error:     &errMsg,
	}
}

func FixtureToolCallResultFrame_Edge() ToolCallResultFrame {
	return ToolCallResultFrame{
		Type:      "tool_call_result",
		SessionId: "sess-edge-1",
		Tool:      "system.spawn_subagent",
		CallId:    "call-edge-1",
		Result:    "plain string result", // result is oneOf: any value is valid
		Status:    "success",
	}
}

// SessionStartedFrame

func FixtureSessionStartedFrame_Populated() SessionStartedFrame {
	agentId := "jim"
	return SessionStartedFrame{
		Type:      "session_started",
		SessionId: "sess-new-1",
		AgentId:   &agentId,
	}
}

func FixtureSessionStartedFrame_ZeroValue() SessionStartedFrame {
	return SessionStartedFrame{}
}

// SubagentStartFrame

func FixtureSubagentStartFrame_Populated() SubagentStartFrame {
	agentId := "ava"
	return SubagentStartFrame{
		Type:         "subagent_start",
		SessionId:    "sess-1",
		SpanId:       "span-abc-123",
		ParentCallId: "parent-call-1",
		TaskLabel:    "Research latest AI papers",
		AgentId:      &agentId,
	}
}

func FixtureSubagentStartFrame_ZeroValue() SubagentStartFrame {
	return SubagentStartFrame{}
}

// SubagentEndFrame

func FixtureSubagentEndFrame_Populated() SubagentEndFrame {
	agentId := "ava"
	durationMs := 4500
	finalResult := "Found 3 relevant papers"
	msg := "Subagent completed successfully"
	parentCallId := "parent-call-1"
	reason := "parent_done_early"
	return SubagentEndFrame{
		Type:         "subagent_end",
		SessionId:    "sess-1",
		SpanId:       "span-abc-123",
		Status:       "success",
		AgentId:      &agentId,
		DurationMs:   &durationMs,
		FinalResult:  &finalResult,
		Message:      &msg,
		ParentCallId: &parentCallId,
		Reason:       &reason,
	}
}

func FixtureSubagentEndFrame_ZeroValue() SubagentEndFrame {
	return SubagentEndFrame{}
}

// ExecApprovalRequestFrame

func FixtureExecApprovalRequestFrame_Populated() ExecApprovalRequestFrame {
	matchedPolicy := "allow_list"
	workingDir := "/workspace"
	return ExecApprovalRequestFrame{
		Type:          "exec_approval_request",
		SessionId:     "sess-1",
		Id:            "exec-req-1",
		Command:       "rm -rf /tmp/build",
		MatchedPolicy: &matchedPolicy,
		WorkingDir:    &workingDir,
	}
}

func FixtureExecApprovalRequestFrame_ZeroValue() ExecApprovalRequestFrame {
	return ExecApprovalRequestFrame{}
}

// ReplayMessageFrame

func FixtureReplayMessageFrame_Populated() ReplayMessageFrame {
	agentId := "jim"
	msgId := "msg-uuid-1"
	ts := "2026-05-17T10:00:00Z"
	return ReplayMessageFrame{
		Type:      "replay_message",
		SessionId: "sess-1",
		Role:      "assistant",
		Content:   "Hello! How can I help you today?",
		AgentId:   &agentId,
		Id:        &msgId,
		Timestamp: &ts,
	}
}

func FixtureReplayMessageFrame_ZeroValue() ReplayMessageFrame {
	return ReplayMessageFrame{}
}

// RateLimitFrame

func FixtureRateLimitFrame_Populated() RateLimitFrame {
	agentId := "jim"
	tool := "workspace.shell"
	return RateLimitFrame{
		Type:              "rate_limit",
		SessionId:         "sess-1",
		PolicyRule:        "100req/min",
		Resource:          "workspace.shell",
		RetryAfterSeconds: 60.0,
		Scope:             "agent",
		AgentId:           &agentId,
		Tool:              &tool,
	}
}

func FixtureRateLimitFrame_ZeroValue() RateLimitFrame {
	return RateLimitFrame{}
}

// AgentSwitchedFrame

func FixtureAgentSwitchedFrame_Populated() AgentSwitchedFrame {
	agentId := "ava"
	msg := "Switched to Ava for research task"
	return AgentSwitchedFrame{
		Type:      "agent_switched",
		SessionId: "sess-1",
		AgentId:   &agentId,
		Message:   &msg,
	}
}

func FixtureAgentSwitchedFrame_ZeroValue() AgentSwitchedFrame {
	return AgentSwitchedFrame{}
}

// TaskStatusChangedFrame

func FixtureTaskStatusChangedFrame_Populated() TaskStatusChangedFrame {
	agentId := "jim"
	return TaskStatusChangedFrame{
		Type:      "task_status_changed",
		SessionId: "sess-1",
		TaskId:    "task-uuid-1",
		Status:    "done",
		AgentId:   &agentId,
	}
}

func FixtureTaskStatusChangedFrame_ZeroValue() TaskStatusChangedFrame {
	return TaskStatusChangedFrame{}
}

// SystemOverloadFrame

func FixtureSystemOverloadFrame_Populated() SystemOverloadFrame {
	msg := "System at capacity — please retry in 30 seconds"
	return SystemOverloadFrame{
		Type:      "system_overload",
		SessionId: "sess-1",
		Message:   &msg,
	}
}

func FixtureSystemOverloadFrame_ZeroValue() SystemOverloadFrame {
	return SystemOverloadFrame{}
}

// CancelStageFrame

func FixtureCancelStageFrame_Populated() CancelStageFrame {
	return CancelStageFrame{
		Type:      "cancel_stage",
		SessionId: "sess-1",
		Stage:     "graceful",
	}
}

func FixtureCancelStageFrame_ZeroValue() CancelStageFrame {
	return CancelStageFrame{}
}

// ReplayWarningFrame

func FixtureReplayWarningFrame_Populated() ReplayWarningFrame {
	dupCount := 3
	return ReplayWarningFrame{
		Type:      "replay_warning",
		SessionId: "sess-1",
		Message:   "Duplicate tool_call_ids detected during replay",
		Stats:     &ReplayWarningStats{DuplicateToolCallIdCount: &dupCount},
	}
}

func FixtureReplayWarningFrame_ZeroValue() ReplayWarningFrame {
	return ReplayWarningFrame{}
}

// SessionCloseAckFrame

func FixtureSessionCloseAckFrame_Populated() SessionCloseAckFrame {
	id := "close-ack-1"
	return SessionCloseAckFrame{
		Type:      "session_close_ack",
		SessionId: "sess-1",
		Id:        &id,
	}
}

func FixtureSessionCloseAckFrame_ZeroValue() SessionCloseAckFrame {
	return SessionCloseAckFrame{}
}

// DevicePairingRequestFrame

func FixtureDevicePairingRequestFrame_Populated() DevicePairingRequestFrame {
	name := "iPhone 15"
	fp := "SHA256:abc123"
	code := "XK7P-9QR2"
	sessId := "sess-1"
	return DevicePairingRequestFrame{
		Type:        "device_pairing_request",
		DeviceId:    "device-uuid-1",
		DeviceName:  &name,
		Fingerprint: &fp,
		PairingCode: &code,
		SessionId:   &sessId,
	}
}

func FixtureDevicePairingRequestFrame_ZeroValue() DevicePairingRequestFrame {
	return DevicePairingRequestFrame{}
}

// ExecApprovalResponseAckFrame

func FixtureExecApprovalResponseAckFrame_Populated() ExecApprovalResponseAckFrame {
	id := "exec-req-1"
	sessId := "sess-1"
	return ExecApprovalResponseAckFrame{
		Type:      "exec_approval_response_ack",
		Id:        &id,
		SessionId: &sessId,
	}
}

func FixtureExecApprovalResponseAckFrame_ZeroValue() ExecApprovalResponseAckFrame {
	return ExecApprovalResponseAckFrame{}
}

// ── REST response type fixtures (OpenAPI) ────────────────────────────────────

// LoginResponse

func FixtureLoginResponse_Populated() LoginResponse {
	warning := strPtr("API key stored in plaintext")
	return LoginResponse{
		Token:    "omnipus_" + repeatStr("a", 64),
		Role:     "admin",
		Username: "admin",
		Warning:  warning,
	}
}

func FixtureLoginResponse_ZeroValue() LoginResponse {
	return LoginResponse{}
}

func FixtureLoginResponse_Edge() LoginResponse {
	return LoginResponse{
		Token:    "omnipus_" + repeatStr("f", 64),
		Role:     "user",
		Username: "unicode-user-🔑",
	}
}

// Session

func FixtureSession_Populated() Session {
	agentId := "jim"
	activeAgentId := "jim"
	model := "claude-sonnet-4-6"
	sessionType := SessionType("chat")
	partitions := []string{"2026-05-16.jsonl", "2026-05-17.jsonl"}
	return Session{
		Id:            "550e8400-e29b-41d4-a716-446655440000",
		AgentId:       "jim",
		ActiveAgentId: &activeAgentId,
		AgentIds:      &[]string{agentId},
		Title:         "My test session",
		Status:        "active",
		CreatedAt:     time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC),
		Channel:       "webchat",
		Partitions:    partitions,
		Model:         &model,
		Type:          &sessionType,
		Stats: struct {
			ByModel *map[string]struct {
				CacheRead  *int `json:"cache_read,omitempty"`
				CacheWrite *int `json:"cache_write,omitempty"`
				In         *int `json:"in,omitempty"`
				Out        *int `json:"out,omitempty"`
				Total      int  `json:"total"`
			} `json:"by_model,omitempty"`
			Cost             float64 `json:"cost"`
			MessageCount     int     `json:"message_count"`
			TokensCacheRead  *int    `json:"tokens_cache_read,omitempty"`
			TokensCacheWrite *int    `json:"tokens_cache_write,omitempty"`
			TokensIn         int     `json:"tokens_in"`
			TokensOut        int     `json:"tokens_out"`
			TokensTotal      int     `json:"tokens_total"`
			ToolCalls        int     `json:"tool_calls"`
		}{
			Cost:         0.0412,
			MessageCount: 10,
			TokensIn:     1200,
			TokensOut:    800,
			TokensTotal:  2000,
			ToolCalls:    3,
		},
	}
}

// FixtureSession_ZeroValue — Go zero values.
// Expected: FAIL because required fields (id, agent_id, title, status, etc.) are "".
// Also: created_at/updated_at are time.Time{} which marshals to "0001-01-01T00:00:00Z"
// (technically valid RFC3339 but wrong per the "reasonable year" assertion).
func FixtureSession_ZeroValue() Session {
	return Session{}
}

func FixtureSession_Edge() Session {
	sessionType := SessionType("task")
	return Session{
		Id:         "00000000-0000-0000-0000-000000000001",
		AgentId:    "custom-agent-" + repeatStr("x", 36),
		Title:      "Edge case session title with special chars",
		Status:     "archived",
		CreatedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
		Channel:    "telegram",
		Partitions: []string{},
		Type:       &sessionType,
		Stats: struct {
			ByModel *map[string]struct {
				CacheRead  *int `json:"cache_read,omitempty"`
				CacheWrite *int `json:"cache_write,omitempty"`
				In         *int `json:"in,omitempty"`
				Out        *int `json:"out,omitempty"`
				Total      int  `json:"total"`
			} `json:"by_model,omitempty"`
			Cost             float64 `json:"cost"`
			MessageCount     int     `json:"message_count"`
			TokensCacheRead  *int    `json:"tokens_cache_read,omitempty"`
			TokensCacheWrite *int    `json:"tokens_cache_write,omitempty"`
			TokensIn         int     `json:"tokens_in"`
			TokensOut        int     `json:"tokens_out"`
			TokensTotal      int     `json:"tokens_total"`
			ToolCalls        int     `json:"tool_calls"`
		}{},
	}
}

// Agent

func FixtureAgent_Populated() Agent {
	color := "#D4AF37"
	icon := "Robot"
	model := "claude-sonnet-4-6"
	sandboxProfile := AgentSandboxProfile("workspace")
	warning := strPtr("Config reload failed after update")
	return Agent{
		Id:                "jim",
		Name:              "Jim",
		Type:              AgentTypeCore,
		Locked:            true,
		Status:            AgentStatusIdle,
		Soul:              "You are Jim, a helpful assistant.",
		TimeoutSeconds:    300,
		MaxToolIterations: 50,
		SteeringMode:      "one-at-a-time",
		Color:             &color,
		Icon:              &icon,
		Model:             &model,
		SandboxProfile:    &sandboxProfile,
		Warning:           warning,
	}
}

func FixtureAgent_ZeroValue() Agent {
	return Agent{}
}

func FixtureAgent_Edge() Agent {
	return Agent{
		Id:                "custom-" + repeatStr("y", 36),
		Name:              "Unicode Agent 🤖",
		Type:              AgentTypeMain,
		Locked:            false,
		Status:            AgentStatusDraft,
		Soul:              "",
		TimeoutSeconds:    0,
		MaxToolIterations: 0,
		SteeringMode:      "one-at-a-time",
	}
}

// LoginResponse — User response

func FixtureUser_Populated() User {
	return User{
		Username:       "alice",
		Role:           "admin",
		HasPassword:    true,
		HasActiveToken: true,
	}
}

func FixtureUser_ZeroValue() User {
	return User{}
}

func FixtureUser_Edge() User {
	return User{
		Username:       "bob-" + repeatStr("z", 55), // long but valid
		Role:           "user",
		HasPassword:    false,
		HasActiveToken: false,
	}
}

// HealthResponse

func FixtureHealthResponse_Populated() HealthResponse {
	return HealthResponse{
		Status: "ok",
	}
}

func FixtureHealthResponse_ZeroValue() HealthResponse {
	return HealthResponse{}
}

// ── Client → server frame fixtures (AsyncAPI) ────────────────────────────────

// AttachSessionFrame — client → server request transcript replay.
// Traces to: contracts/asyncapi.yaml AttachSessionFrame schema.

func FixtureAttachSessionFrame_Populated() AttachSessionFrame {
	return AttachSessionFrame{
		Type:      "attach_session",
		SessionId: "sess-550e8400-e29b-41d4-a716-446655440000",
	}
}

// FixtureAttachSessionFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="" and session_id="" (both required, minLength:1).
func FixtureAttachSessionFrame_ZeroValue() AttachSessionFrame {
	return AttachSessionFrame{}
}

// FixtureAttachSessionFrame_Edge — unicode session ID at a reasonable length.
func FixtureAttachSessionFrame_Edge() AttachSessionFrame {
	return AttachSessionFrame{
		Type:      "attach_session",
		SessionId: "sess-" + repeatStr("a", 60),
	}
}

// AuthFrame — client → server authentication frame.
// Traces to: contracts/asyncapi.yaml AuthFrame schema.

func FixtureAuthFrame_Populated() AuthFrame {
	return AuthFrame{
		Type:  "auth",
		Token: "omnipus_" + repeatStr("a", 64), // 'a' is valid hex; total 72 chars matches pattern
	}
}

// FixtureAuthFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="" and token="" (both required; fails minLength and pattern).
func FixtureAuthFrame_ZeroValue() AuthFrame {
	return AuthFrame{}
}

// FixtureAuthFrame_Edge — valid token using all-f hex digits.
// Updated from "x" (single char, no longer valid) because pattern now requires
// "^omnipus_[a-f0-9]{64}$" — token must be exactly the omnipus_ prefix + 64 hex chars.
func FixtureAuthFrame_Edge() AuthFrame {
	return AuthFrame{
		Type:  "auth",
		Token: "omnipus_" + repeatStr("f", 64), // 'f' is valid hex
	}
}

// CancelFrame — client → server cancel in-progress turn.
// Traces to: contracts/asyncapi.yaml CancelFrame schema.

func FixtureCancelFrame_Populated() CancelFrame {
	return CancelFrame{
		Type:      "cancel",
		SessionId: "sess-550e8400-e29b-41d4-a716-446655440001",
	}
}

// FixtureCancelFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="" and session_id="" (both required, minLength:1).
func FixtureCancelFrame_ZeroValue() CancelFrame {
	return CancelFrame{}
}

// FixtureCancelFrame_Edge — session_id with special chars (valid).
func FixtureCancelFrame_Edge() CancelFrame {
	return CancelFrame{
		Type:      "cancel",
		SessionId: "sess-" + repeatStr("b", 60),
	}
}

// DevicePairingResponseFrame — client → server device pairing decision.
// Traces to: contracts/asyncapi.yaml DevicePairingResponseFrame schema.

func FixtureDevicePairingResponseFrame_Populated() DevicePairingResponseFrame {
	return DevicePairingResponseFrame{
		Type:     "device_pairing_response",
		DeviceId: "device-uuid-550e8400-e29b-41d4-a716-446655440002",
		Decision: "approve",
	}
}

// FixtureDevicePairingResponseFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="", device_id="", decision="" (none match enum).
func FixtureDevicePairingResponseFrame_ZeroValue() DevicePairingResponseFrame {
	return DevicePairingResponseFrame{}
}

// FixtureDevicePairingResponseFrame_Edge — reject decision (other valid enum value).
func FixtureDevicePairingResponseFrame_Edge() DevicePairingResponseFrame {
	return DevicePairingResponseFrame{
		Type:     "device_pairing_response",
		DeviceId: "device-" + repeatStr("d", 36),
		Decision: "reject",
	}
}

// ExecApprovalResponseFrame — client → server exec approval decision.
// Traces to: contracts/asyncapi.yaml ExecApprovalResponseFrame schema.

func FixtureExecApprovalResponseFrame_Populated() ExecApprovalResponseFrame {
	return ExecApprovalResponseFrame{
		Type:     "exec_approval_response",
		Id:       "exec-req-550e8400-e29b-41d4-a716-446655440003",
		Decision: "allow",
	}
}

// FixtureExecApprovalResponseFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="", id="" (minLength:1), decision="" (not in enum).
func FixtureExecApprovalResponseFrame_ZeroValue() ExecApprovalResponseFrame {
	return ExecApprovalResponseFrame{}
}

// FixtureExecApprovalResponseFrame_Edge — deny decision (other valid enum value).
func FixtureExecApprovalResponseFrame_Edge() ExecApprovalResponseFrame {
	return ExecApprovalResponseFrame{
		Type:     "exec_approval_response",
		Id:       "exec-req-" + repeatStr("e", 36),
		Decision: "deny",
	}
}

// MessageFrame — client → server user chat message.
// Traces to: contracts/asyncapi.yaml MessageFrame schema.

func FixtureMessageFrame_Populated() MessageFrame {
	sessId := "sess-1"
	agentId := "jim"
	return MessageFrame{
		Type:      "message",
		Content:   "Hello, what can you help me with today?",
		SessionId: &sessId,
		AgentId:   &agentId,
	}
}

// FixtureMessageFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="" and content="" (content has minLength:1).
func FixtureMessageFrame_ZeroValue() MessageFrame {
	return MessageFrame{}
}

// FixtureMessageFrame_Edge — new session (no session_id), max-length-ish content.
func FixtureMessageFrame_Edge() MessageFrame {
	return MessageFrame{
		Type:    "message",
		Content: repeatStr("x", 5000), // well under maxLength:5242880
		// no session_id — starts a new session
	}
}

// PingFrame — client → server heartbeat.
// Traces to: contracts/asyncapi.yaml PingFrame schema.

func FixturePingFrame_Populated() PingFrame {
	return PingFrame{
		Type: "ping",
	}
}

// FixturePingFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="" (const: "ping" requires the literal value "ping").
func FixturePingFrame_ZeroValue() PingFrame {
	return PingFrame{}
}

// FixturePingFrame_Edge is the same as Populated — PingFrame has only one field.
// The edge case is that even a frame with no payload beyond type is valid.
func FixturePingFrame_Edge() PingFrame {
	return PingFrame{
		Type: "ping",
	}
}

// ── REST response type fixtures ─────────────────────────────────────────────
//
// Covers Task, ExecApprovalExpiredFrame, SessionCloseFrame, and related types.

// ── Task ─────────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/Task.yaml

func FixtureTask_Populated() Task {
	agentId := "jim"
	agentName := "Jim"
	parentTaskId := "parent-task-00000000-0000-0000-0000-000000000001"
	prompt := "Summarize the last 7 days of gateway logs."
	description := "Look for anomalies in the last 7 days of gateway logs."
	milestoneId := "m-1234"
	result := "Found 3 anomalies in the log."
	sessionId := "sess-00000000-0000-0000-0000-000000000002"
	sourceChannel := "telegram"
	sourceChatId := "chat-12345"
	priority := 3
	blockedBy := []string{"550e8400-e29b-41d4-a716-446655440001"}
	artifacts := []string{"/workspace/report.pdf", "/workspace/chart.png"}
	due := time.Date(2026, 7, 31, 17, 0, 0, 0, time.UTC)
	createdAt := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	startedAt := time.Date(2026, 5, 16, 10, 1, 0, 0, time.UTC)
	completedAt := time.Date(2026, 5, 16, 10, 5, 30, 0, time.UTC)
	surface := TaskSurface("user")
	t := Task{
		Id:            "550e8400-e29b-41d4-a716-446655440000",
		Title:         "Analyze logs",
		Description:   &description,
		Prompt:        &prompt,
		Action:        TaskAction("llm"),
		Status:        TaskStatus("done"),
		AgentId:       &agentId,
		AgentName:     &agentName,
		Priority:      &priority,
		BlockedBy:     &blockedBy,
		ParentTaskId:  &parentTaskId,
		WorkspaceId:   "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		MilestoneId:   &milestoneId,
		Due:           &due,
		Surface:       &surface,
		SourceChannel: &sourceChannel,
		SourceChatId:  &sourceChatId,
		SessionId:     &sessionId,
		Result:        &result,
		Artifacts:     &artifacts,
		Owner:         "alice",
		CreatedBy:     "admin",
		CreatedAt:     createdAt,
		UpdatedAt:     completedAt,
		StartedAt:     &startedAt,
		CompletedAt:   &completedAt,
	}
	t.Todos = &[]struct {
		Status TaskTodosStatus `json:"status"`
		Text   string          `json:"text"`
	}{{Text: "Draft the summary section", Status: TaskTodosStatusPending}}
	t.Trigger = &struct {
		Config Task_Trigger_Config `json:"config"`
		Type   TaskTriggerType     `json:"type"`
	}{Type: TaskTriggerType("manual")}
	return t
}

// FixtureTask_ZeroValue — Go zero values.
// Expected: FAIL because id="", title="", action="" (not in enum),
// status="" (not in enum), workspace_id="" (required), owner/created_by empty.
func FixtureTask_ZeroValue() Task {
	return Task{}
}

// FixtureTask_Edge — inbox task, no agent, unicode title, max priority,
// recurring trigger.
func FixtureTask_Edge() Task {
	prompt := repeatStr("task prompt content ", 20)
	priority := 5
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t := Task{
		Id:          "00000000-0000-0000-0000-000000000001",
		Title:       "unicode-task-title-タスク-rocket",
		Prompt:      &prompt,
		Action:      TaskAction("llm"),
		Status:      TaskStatus("inbox"),
		Priority:    &priority,
		WorkspaceId: "00000000-0000-0000-0000-0000000000ff",
		Owner:       "alice",
		CreatedBy:   "alice",
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}
	cron := "0 9 * * MON"
	t.Trigger = &struct {
		Config Task_Trigger_Config `json:"config"`
		Type   TaskTriggerType     `json:"type"`
	}{Type: TaskTriggerType("recurring")}
	t.Trigger.Config.CronExpr = &cron
	return t
}

// FixtureTaskTrigger_Populated — a recurring time trigger.
func FixtureTaskTrigger_Populated() TaskTrigger {
	cron := "0 9 * * MON"
	tr := TaskTrigger{Type: TaskTriggerType("recurring")}
	tr.Config.CronExpr = &cron
	return tr
}

// FixtureTaskTrigger_ZeroValue — Go zero values. Expected: FAIL (type="" not in enum).
func FixtureTaskTrigger_ZeroValue() TaskTrigger {
	return TaskTrigger{}
}

// FixtureTaskTrigger_Edge — a once trigger at an absolute instant.
func FixtureTaskTrigger_Edge() TaskTrigger {
	at := int64(1781000000000)
	tr := TaskTrigger{Type: TaskTriggerType("once")}
	tr.Config.AtMs = &at
	return tr
}

// FixtureTodo_Populated — a checklist item.
func FixtureTodo_Populated() Todo {
	return Todo{Text: "Draft the summary section", Status: TodoStatusPending}
}

// FixtureTodo_ZeroValue — Go zero values. Expected: FAIL (text="" minLength 1).
func FixtureTodo_ZeroValue() Todo {
	return Todo{}
}

// FixtureTodo_Edge — a completed checklist item with unicode text.
func FixtureTodo_Edge() Todo {
	return Todo{Text: "完了-done-✓", Status: TodoStatusCompleted} //nolint:gosmopolitan // intentional non-ASCII test fixture
}

// ── McpServer ─────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/McpServer.yaml

func FixtureMcpServer_Populated() McpServer {
	tools := []string{"search", "fetch", "index"}
	return McpServer{
		Id:        "my-mcp-server",
		Name:      "My MCP Server",
		Transport: McpServerTransport("stdio"),
		Status:    McpServerStatus("connected"),
		ToolCount: 3,
		Tools:     &tools,
	}
}

// FixtureMcpServer_ZeroValue — Go zero values.
// Expected: FAIL because id="", name="", transport="" (not in enum),
// status="" (not in enum), tool_count=0 (valid — minimum: 0).
func FixtureMcpServer_ZeroValue() McpServer {
	return McpServer{}
}

// FixtureMcpServer_Edge — disconnected server, empty tools list, SSE transport.
func FixtureMcpServer_Edge() McpServer {
	return McpServer{
		Id:        "sse-server-" + repeatStr("x", 20),
		Name:      "SSE Server 🔌",
		Transport: McpServerTransport("sse"),
		Status:    McpServerStatus("disconnected"),
		ToolCount: 0,
		// Tools omitted (nil) — allowed per schema (optional)
	}
}

// FixtureMcpServer_NilToolsAllowed — tools is optional; omitting it is valid.
// This is NOT the bug pattern (nil tools is optional per schema, not required).
func FixtureMcpServer_NilToolsAllowed() McpServer {
	return McpServer{
		Id:        "mcp-no-tools",
		Name:      "Pending Enumeration",
		Transport: McpServerTransport("http"),
		Status:    McpServerStatus("connected"),
		ToolCount: 0,
		Tools:     nil, // valid: tools is optional
	}
}

// ── McpServerCreate ───────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/McpServerCreate.yaml

func FixtureMcpServerCreate_Populated() McpServerCreate {
	args := []string{"--port", "3000"}
	cmd := "npx @modelcontextprotocol/server-everything"
	return McpServerCreate{
		Name:      "My MCP Server",
		Command:   &cmd,
		Args:      &args,
		Transport: McpServerCreateTransport("stdio"),
	}
}

// FixtureMcpServerCreate_ZeroValue — Go zero values.
// Expected: FAIL because name="", command="", transport="" (not in enum).
func FixtureMcpServerCreate_ZeroValue() McpServerCreate {
	return McpServerCreate{}
}

// FixtureMcpServerCreate_Edge — no args (optional), SSE transport with URL, unicode name.
func FixtureMcpServerCreate_Edge() McpServerCreate {
	sseURL := "https://mcp.example.com/sse"
	return McpServerCreate{
		Name:      "remote-server-sse-world",
		Url:       &sseURL,
		Args:      nil, // args is optional per schema
		Transport: McpServerCreateTransport("sse"),
	}
}

// ── AppState ─────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AppState.yaml

func FixtureAppState_Populated() AppState {
	lastRun := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	score := 85
	godModeAvail := false
	godModeOptedIn := false
	devModeBypass := false
	return AppState{
		OnboardingComplete: true,
		LastDoctorRun:      &lastRun,
		LastDoctorScore:    &score,
		GodModeAvailable:   &godModeAvail,
		GodModeOptedIn:     &godModeOptedIn,
		DevModeBypass:      &devModeBypass,
	}
}

// FixtureAppState_ZeroValue — Go zero value.
// Expected: PASS — onboarding_complete is the only required field,
// and bool zero value (false) is a valid boolean (not an absent value).
// This is one of the few types where ZeroValue passes.
func FixtureAppState_ZeroValue() AppState {
	return AppState{}
}

// FixtureAppState_Edge — onboarding not complete, god mode available and opted in.
func FixtureAppState_Edge() AppState {
	godModeAvail := true
	godModeOptedIn := true
	devModeBypass := true
	return AppState{
		OnboardingComplete: false,
		GodModeAvailable:   &godModeAvail,
		GodModeOptedIn:     &godModeOptedIn,
		DevModeBypass:      &devModeBypass,
	}
}

// ── ValidateTokenResponse ─────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ValidateTokenResponse.yaml

func FixtureValidateTokenResponse_Populated() ValidateTokenResponse {
	return ValidateTokenResponse{
		Username: "admin",
		Role:     ValidateTokenResponseRole("admin"),
	}
}

// FixtureValidateTokenResponse_ZeroValue — Go zero values.
// Expected: FAIL because username="", role="" (not in enum [admin, user]).
func FixtureValidateTokenResponse_ZeroValue() ValidateTokenResponse {
	return ValidateTokenResponse{}
}

// FixtureValidateTokenResponse_Edge — user role, unicode username.
func FixtureValidateTokenResponse_Edge() ValidateTokenResponse {
	return ValidateTokenResponse{
		Username: "unicode-user-🔑-" + repeatStr("a", 20),
		Role:     ValidateTokenResponseRole("user"),
	}
}

// ── DoctorIssue (via DoctorResult.Issues inline struct) ───────────────────────
// Note: oapi-codegen inlined DoctorIssue as an anonymous struct inside DoctorResult.
// Tests are written against DoctorResult (which includes issues) and the
// component schema DoctorIssue.yaml directly via raw JSON.
// Traces to: contracts/components/schemas/DoctorIssue.yaml

// FixtureDoctorIssueJSON_Populated — returns a raw JSON map matching DoctorIssue schema.
// Used in raw-JSON contract tests since there is no named Go type for DoctorIssue.
func FixtureDoctorIssueJSON_Populated() map[string]any {
	return map[string]any{
		"id":             "no-provider-configured",
		"severity":       "high",
		"title":          "No LLM provider configured",
		"description":    "No API provider has been configured. The agent cannot generate responses.",
		"recommendation": "Go to Settings > Providers and add an API key.",
		"action_link":    "/settings/providers",
		"action_label":   "Configure Provider",
	}
}

// FixtureDoctorIssueJSON_ZeroValue — empty map → missing all required fields.
func FixtureDoctorIssueJSON_ZeroValue() map[string]any {
	return map[string]any{}
}

// FixtureDoctorIssueJSON_Edge — low severity, no optional fields.
func FixtureDoctorIssueJSON_Edge() map[string]any {
	return map[string]any{
		"id":             "master-key-permissions",
		"severity":       "low",
		"title":          "Master key file has lax permissions",
		"description":    "The master.key file should be mode 0600 to prevent unauthorized reads.",
		"recommendation": "Run: chmod 600 ~/.omnipus/master.key",
	}
}

// ── DoctorResult ─────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/DoctorResult.yaml

func FixtureDoctorResult_Populated() DoctorResult {
	actionLink := "/settings/providers"
	actionLabel := "Configure Provider"
	return DoctorResult{
		Score:     85,
		CheckedAt: time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC),
		Issues: []struct {
			ActionLabel    *string                    `json:"action_label,omitempty"`
			ActionLink     *string                    `json:"action_link,omitempty"`
			Description    string                     `json:"description"`
			Id             string                     `json:"id"`
			Recommendation string                     `json:"recommendation"`
			Severity       DoctorResultIssuesSeverity `json:"severity"`
			Title          string                     `json:"title"`
		}{
			{
				Id:             "no-provider-configured",
				Severity:       DoctorResultIssuesSeverity("high"),
				Title:          "No LLM provider configured",
				Description:    "No API provider has been configured.",
				Recommendation: "Go to Settings > Providers and add an API key.",
				ActionLink:     &actionLink,
				ActionLabel:    &actionLabel,
			},
		},
	}
}

// FixtureDoctorResult_ZeroValue — Go zero values.
// Expected: FAIL because score=0 (valid; minimum:0), but checked_at=time.Time{} marshals
// to "0001-01-01T00:00:00Z" (valid RFC3339). The issues slice nil → JSON null → FAIL
// because issues is required type:array.
func FixtureDoctorResult_ZeroValue() DoctorResult {
	return DoctorResult{}
}

// FixtureDoctorResult_Edge — perfect score, empty issues (valid), unicode checked_at.
func FixtureDoctorResult_Edge() DoctorResult {
	return DoctorResult{
		Score:     100,
		CheckedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Issues: []struct {
			ActionLabel    *string                    `json:"action_label,omitempty"`
			ActionLink     *string                    `json:"action_link,omitempty"`
			Description    string                     `json:"description"`
			Id             string                     `json:"id"`
			Recommendation string                     `json:"recommendation"`
			Severity       DoctorResultIssuesSeverity `json:"severity"`
			Title          string                     `json:"title"`
		}{}, // empty array is valid — score 100 means no issues
	}
}

// FixtureDoctorResult_NilIssues — issues nil → JSON null → schema violation.
func FixtureDoctorResult_NilIssues() DoctorResult {
	return DoctorResult{
		Score:     75,
		CheckedAt: time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC),
		Issues:    nil, // THE BUG: nil slice → JSON null → schema requires type: array
	}
}

// ── DevicePending (via DevicesResponse.Pending inline struct) ─────────────────
// Note: oapi-codegen inlined DevicePending as an anonymous struct inside DevicesResponse.
// Tests use raw JSON maps for DoctorIssue-style direct schema validation.
// Traces to: contracts/components/schemas/DevicePending.yaml

func FixtureDevicePendingJSON_Populated() map[string]any {
	return map[string]any{
		"device_id":    "dev_01HXYZ",
		"fingerprint":  "SHA256:abc123...",
		"pairing_code": "BLUE-TIGER-42",
		"device_name":  "Alice's MacBook",
		"created_at":   "2026-05-16T10:00:00Z",
		"expires_at":   "2026-05-16T10:10:00Z",
	}
}

func FixtureDevicePendingJSON_ZeroValue() map[string]any {
	return map[string]any{}
}

func FixtureDevicePendingJSON_Edge() map[string]any {
	return map[string]any{
		"device_id":    "dev-" + repeatStr("f", 36),
		"fingerprint":  "SHA256:" + repeatStr("a", 43) + "=",
		"pairing_code": "ORANGE-FALCON-99",
		"device_name":  "device-name-unicode-key",
		"created_at":   "2026-01-01T00:00:00Z",
		"expires_at":   "2026-01-01T00:10:00Z",
	}
}

// ── DevicePaired (via DevicesResponse.Paired inline struct) ───────────────────
// Traces to: contracts/components/schemas/DevicePaired.yaml

func FixtureDevicePairedJSON_Populated() map[string]any {
	return map[string]any{
		"device_id":    "dev_01HXYZ",
		"fingerprint":  "SHA256:abc123...",
		"device_name":  "Alice's MacBook",
		"paired_at":    "2026-05-16T10:00:00Z",
		"last_seen_at": "2026-05-16T11:30:00Z",
		"status":       "active",
	}
}

func FixtureDevicePairedJSON_ZeroValue() map[string]any {
	return map[string]any{}
}

func FixtureDevicePairedJSON_Edge() map[string]any {
	return map[string]any{
		"device_id":    "dev-" + repeatStr("b", 36),
		"fingerprint":  "SHA256:" + repeatStr("b", 43) + "=",
		"device_name":  "Revoked Laptop 💻",
		"paired_at":    "2026-01-01T00:00:00Z",
		"last_seen_at": "2026-01-01T12:00:00Z",
		"status":       "revoked",
	}
}

// ── DevicesResponse ───────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/DevicesResponse.yaml

func FixtureDevicesResponse_Populated() DevicesResponse {
	return DevicesResponse{
		Pending: []struct {
			CreatedAt   time.Time `json:"created_at"`
			DeviceId    string    `json:"device_id"`
			DeviceName  string    `json:"device_name"`
			ExpiresAt   time.Time `json:"expires_at"`
			Fingerprint string    `json:"fingerprint"`
			PairingCode string    `json:"pairing_code"`
		}{
			{
				DeviceId:    "dev_pending_01",
				Fingerprint: "SHA256:pending123...",
				PairingCode: "BLUE-TIGER-42",
				DeviceName:  "Alice's MacBook",
				CreatedAt:   time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC),
				ExpiresAt:   time.Date(2026, 5, 16, 10, 10, 0, 0, time.UTC),
			},
		},
		Paired: []struct {
			DeviceId    string                      `json:"device_id"`
			DeviceName  string                      `json:"device_name"`
			Fingerprint string                      `json:"fingerprint"`
			LastSeenAt  time.Time                   `json:"last_seen_at"`
			PairedAt    time.Time                   `json:"paired_at"`
			Status      DevicesResponsePairedStatus `json:"status"`
		}{
			{
				DeviceId:    "dev_paired_01",
				Fingerprint: "SHA256:paired456...",
				DeviceName:  "Bob's iPhone",
				PairedAt:    time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC),
				LastSeenAt:  time.Date(2026, 5, 16, 11, 30, 0, 0, time.UTC),
				Status:      DevicesResponsePairedStatusActive,
			},
		},
	}
}

// FixtureDevicesResponse_ZeroValue — Go zero values.
// Expected: FAIL because pending=nil and paired=nil (both marshal to null,
// schema requires type: array for both fields).
func FixtureDevicesResponse_ZeroValue() DevicesResponse {
	return DevicesResponse{}
}

// FixtureDevicesResponse_Edge — empty arrays (no devices) — valid, common state.
func FixtureDevicesResponse_Edge() DevicesResponse {
	return DevicesResponse{
		Pending: []struct {
			CreatedAt   time.Time `json:"created_at"`
			DeviceId    string    `json:"device_id"`
			DeviceName  string    `json:"device_name"`
			ExpiresAt   time.Time `json:"expires_at"`
			Fingerprint string    `json:"fingerprint"`
			PairingCode string    `json:"pairing_code"`
		}{},
		Paired: []struct {
			DeviceId    string                      `json:"device_id"`
			DeviceName  string                      `json:"device_name"`
			Fingerprint string                      `json:"fingerprint"`
			LastSeenAt  time.Time                   `json:"last_seen_at"`
			PairedAt    time.Time                   `json:"paired_at"`
			Status      DevicesResponsePairedStatus `json:"status"`
		}{},
	}
}

// ── BackupEntry (inlined in listBackups response, tested via raw JSON) ─────────
// Note: oapi-codegen inlined BackupEntry as an anonymous object in the listBackups
// response. Tests validate against the component schema BackupEntry.yaml directly.
// Traces to: contracts/components/schemas/BackupEntry.yaml

func FixtureBackupEntryJSON_Populated() map[string]any {
	return map[string]any{
		"filename":   "omnipus-backup-2026-05-16T10-00-00Z.tar.gz",
		"size_bytes": int64(1048576),
		"created_at": "2026-05-16T10:00:00Z",
	}
}

func FixtureBackupEntryJSON_ZeroValue() map[string]any {
	return map[string]any{}
}

func FixtureBackupEntryJSON_Edge() map[string]any {
	return map[string]any{
		"filename":   "omnipus-backup-" + repeatStr("x", 30) + ".tar.gz",
		"size_bytes": int64(0), // minimum: 0 — empty archive is valid
		"created_at": "2026-01-01T00:00:00Z",
	}
}

// ── StorageStats ──────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/StorageStats.yaml

func FixtureStorageStats_Populated() StorageStats {
	oldest := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	warnings := []string{"workspace size unavailable: permission denied"}
	return StorageStats{
		WorkspaceSizeBytes: 52428800,
		SessionCount:       42,
		MemoryEntryCount:   7,
		OldestSessionDate:  &oldest,
		Warnings:           &warnings,
	}
}

// FixtureStorageStats_ZeroValue — Go zero values.
// Expected: PASS — all required fields (workspace_size_bytes, session_count,
// memory_entry_count) are integers with zero as a valid value (minimum: 0).
// Zero value is a legitimate "empty system" state.
func FixtureStorageStats_ZeroValue() StorageStats {
	return StorageStats{}
}

// FixtureStorageStats_Edge — no sessions, no memory, multiple warnings.
func FixtureStorageStats_Edge() StorageStats {
	warnings := []string{
		"agent store 'custom-agent-1' unreadable: permission denied",
		"agent store 'custom-agent-2' unreadable: file not found",
	}
	return StorageStats{
		WorkspaceSizeBytes: 0,
		SessionCount:       0,
		MemoryEntryCount:   0,
		Warnings:           &warnings,
	}
}

// FixtureStorageStats_NilWarningsAllowed — warnings is optional; nil is valid.
func FixtureStorageStats_NilWarningsAllowed() StorageStats {
	oldest := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	return StorageStats{
		WorkspaceSizeBytes: 1024,
		SessionCount:       3,
		MemoryEntryCount:   0,
		OldestSessionDate:  &oldest,
		Warnings:           nil, // optional — nil is valid
	}
}

// ── MeInfo ────────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/MeInfo.yaml

func FixtureMeInfo_Populated() MeInfo {
	return MeInfo{
		Role: MeInfoRole("admin"),
	}
}

// FixtureMeInfo_ZeroValue — Go zero value.
// Expected: FAIL because role="" (not in enum [admin, user]).
func FixtureMeInfo_ZeroValue() MeInfo {
	return MeInfo{}
}

// FixtureMeInfo_Edge — user role (the other enum value).
func FixtureMeInfo_Edge() MeInfo {
	return MeInfo{
		Role: MeInfoRole("user"),
	}
}

// ── ExecApprovalExpiredFrame ──────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ExecApprovalExpiredFrame.yaml

func FixtureExecApprovalExpiredFrame_Populated() ExecApprovalExpiredFrame {
	msg := "Exec approval for 'rm -rf /tmp/build' expired after 30s — no admin response"
	return ExecApprovalExpiredFrame{
		Type:      "exec_approval_expired",
		Id:        "exec-req-550e8400-e29b-41d4-a716-446655440001",
		SessionId: "sess-550e8400-e29b-41d4-a716-446655440002",
		Message:   &msg,
	}
}

// FixtureExecApprovalExpiredFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="" (const: exec_approval_expired), id="", session_id="".
func FixtureExecApprovalExpiredFrame_ZeroValue() ExecApprovalExpiredFrame {
	return ExecApprovalExpiredFrame{}
}

// FixtureExecApprovalExpiredFrame_Edge — no message (optional), short IDs.
func FixtureExecApprovalExpiredFrame_Edge() ExecApprovalExpiredFrame {
	return ExecApprovalExpiredFrame{
		Type:      "exec_approval_expired",
		Id:        "exec-req-" + repeatStr("e", 36),
		SessionId: "sess-" + repeatStr("s", 36),
		// Message omitted — optional per schema
	}
}

// ── SessionCloseFrame ─────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/SessionCloseFrame.yaml

func FixtureSessionCloseFrame_Populated() SessionCloseFrame {
	return SessionCloseFrame{
		Type:      "session_close",
		SessionId: "sess-550e8400-e29b-41d4-a716-446655440003",
	}
}

// FixtureSessionCloseFrame_ZeroValue — Go zero values.
// Expected: FAIL because type="" (const: session_close), session_id="" (minLength: 1).
func FixtureSessionCloseFrame_ZeroValue() SessionCloseFrame {
	return SessionCloseFrame{}
}

// FixtureSessionCloseFrame_Edge — long session_id (valid).
func FixtureSessionCloseFrame_Edge() SessionCloseFrame {
	return SessionCloseFrame{
		Type:      "session_close",
		SessionId: "sess-" + repeatStr("c", 60),
	}
}

// ── helper functions ─────────────────────────────────────────────────────────

func strPtr(s string) *string { return &s }

func intPtr(i int) *int { return &i }

func boolPtr(b bool) *bool { return &b }

func float32Ptr(f float32) *float32 { return &f }

func repeatStr(s string, n int) string {
	result := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		result = append(result, s...)
	}
	return string(result)
}

// ── AuditLogToggleRequest ─────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AuditLogToggleRequest.yaml

func FixtureAuditLogToggleRequest_Populated() AuditLogToggleRequest {
	return AuditLogToggleRequest{Enabled: true}
}

func FixtureAuditLogToggleRequest_ZeroValue() AuditLogToggleRequest {
	return AuditLogToggleRequest{}
}

func FixtureAuditLogToggleRequest_Edge() AuditLogToggleRequest {
	return AuditLogToggleRequest{Enabled: false}
}

// ── AuditLogUpdateResponse ────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AuditLogUpdateResponse.yaml

func FixtureAuditLogUpdateResponse_Populated() AuditLogUpdateResponse {
	return AuditLogUpdateResponse{
		Saved:           true,
		RequiresRestart: true,
		AppliedEnabled:  false, // old value before restart
	}
}

func FixtureAuditLogUpdateResponse_ZeroValue() AuditLogUpdateResponse {
	return AuditLogUpdateResponse{}
}

func FixtureAuditLogUpdateResponse_Edge() AuditLogUpdateResponse {
	return AuditLogUpdateResponse{
		Saved:           false,
		RequiresRestart: true,
		AppliedEnabled:  true,
	}
}

// ── SkillTrustUpdateRequest ───────────────────────────────────────────────────
// Traces to: contracts/components/schemas/SkillTrustUpdateRequest.yaml

func FixtureSkillTrustUpdateRequest_Populated() SkillTrustUpdateRequest {
	return SkillTrustUpdateRequest{Level: SkillTrustUpdateRequestLevel("block_unverified")}
}

func FixtureSkillTrustUpdateRequest_ZeroValue() SkillTrustUpdateRequest {
	return SkillTrustUpdateRequest{}
}

func FixtureSkillTrustUpdateRequest_Edge() SkillTrustUpdateRequest {
	return SkillTrustUpdateRequest{Level: SkillTrustUpdateRequestLevel("allow_all")}
}

// ── SkillTrustUpdateResponse ──────────────────────────────────────────────────
// Traces to: contracts/components/schemas/SkillTrustUpdateResponse.yaml

func FixtureSkillTrustUpdateResponse_Populated() SkillTrustUpdateResponse {
	warning := strPtr("allow_all disables hash verification — community skills run without integrity checks")
	return SkillTrustUpdateResponse{
		Saved:           true,
		RequiresRestart: false,
		AppliedLevel:    SkillTrustUpdateResponseAppliedLevel("allow_all"),
		Warning:         warning,
	}
}

func FixtureSkillTrustUpdateResponse_ZeroValue() SkillTrustUpdateResponse {
	return SkillTrustUpdateResponse{}
}

func FixtureSkillTrustUpdateResponse_Edge() SkillTrustUpdateResponse {
	return SkillTrustUpdateResponse{
		Saved:           true,
		RequiresRestart: false,
		AppliedLevel:    SkillTrustUpdateResponseAppliedLevel("block_unverified"),
	}
}

// ── PromptGuardUpdateRequest ──────────────────────────────────────────────────
// Traces to: contracts/components/schemas/PromptGuardUpdateRequest.yaml

func FixturePromptGuardUpdateRequest_Populated() PromptGuardUpdateRequest {
	return PromptGuardUpdateRequest{Level: PromptGuardUpdateRequestLevel("high")}
}

func FixturePromptGuardUpdateRequest_ZeroValue() PromptGuardUpdateRequest {
	return PromptGuardUpdateRequest{}
}

func FixturePromptGuardUpdateRequest_Edge() PromptGuardUpdateRequest {
	return PromptGuardUpdateRequest{Level: PromptGuardUpdateRequestLevel("low")}
}

// ── PromptGuardUpdateResponse ─────────────────────────────────────────────────
// Traces to: contracts/components/schemas/PromptGuardUpdateResponse.yaml

func FixturePromptGuardUpdateResponse_Populated() PromptGuardUpdateResponse {
	return PromptGuardUpdateResponse{
		Saved:           true,
		RequiresRestart: false,
		AppliedLevel:    PromptGuardUpdateResponseAppliedLevel("high"),
	}
}

func FixturePromptGuardUpdateResponse_ZeroValue() PromptGuardUpdateResponse {
	return PromptGuardUpdateResponse{}
}

func FixturePromptGuardUpdateResponse_Edge() PromptGuardUpdateResponse {
	warning := strPtr("hot-reload failed — restart required")
	return PromptGuardUpdateResponse{
		Saved:           true,
		RequiresRestart: true,
		AppliedLevel:    PromptGuardUpdateResponseAppliedLevel("medium"),
		Warning:         warning,
	}
}

// ── RateLimitsResponse ────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RateLimitsResponse.yaml

func FixtureRateLimitsResponse_Populated() RateLimitsResponse {
	return RateLimitsResponse{
		Enabled:                    true,
		DailyCostCap:               5.0,
		DailyCostUsd:               1.23,
		MaxAgentLlmCallsPerHour:    100,
		MaxAgentToolCallsPerMinute: 60,
	}
}

func FixtureRateLimitsResponse_ZeroValue() RateLimitsResponse {
	return RateLimitsResponse{}
}

func FixtureRateLimitsResponse_Edge() RateLimitsResponse {
	return RateLimitsResponse{
		Enabled:                    false,
		DailyCostCap:               0, // unlimited
		DailyCostUsd:               0,
		MaxAgentLlmCallsPerHour:    0, // unlimited
		MaxAgentToolCallsPerMinute: 0, // unlimited
	}
}

// ── RateLimitsUpdateRequest ───────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RateLimitsUpdateRequest.yaml

func FixtureRateLimitsUpdateRequest_Populated() RateLimitsUpdateRequest {
	costCap := float64(10.0)
	llm := int64(200)
	tool := int64(120)
	return RateLimitsUpdateRequest{
		DailyCostCapUsd:            &costCap,
		MaxAgentLlmCallsPerHour:    &llm,
		MaxAgentToolCallsPerMinute: &tool,
	}
}

func FixtureRateLimitsUpdateRequest_ZeroValue() RateLimitsUpdateRequest {
	return RateLimitsUpdateRequest{}
}

func FixtureRateLimitsUpdateRequest_Edge() RateLimitsUpdateRequest {
	costCap := float64(0)
	return RateLimitsUpdateRequest{DailyCostCapUsd: &costCap}
}

// ── RateLimitsUpdateResponse ──────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RateLimitsUpdateResponse.yaml

func FixtureRateLimitsUpdateResponse_Populated() RateLimitsUpdateResponse {
	costCap := float64(10.0)
	llm := int64(200)
	tool := int64(120)
	return RateLimitsUpdateResponse{
		Saved:           true,
		RequiresRestart: false,
		Applied: &struct {
			DailyCostCapUsd            *float64 `json:"daily_cost_cap_usd,omitempty"`
			MaxAgentLlmCallsPerHour    *int64   `json:"max_agent_llm_calls_per_hour,omitempty"`
			MaxAgentToolCallsPerMinute *int64   `json:"max_agent_tool_calls_per_minute,omitempty"`
		}{
			DailyCostCapUsd:            &costCap,
			MaxAgentLlmCallsPerHour:    &llm,
			MaxAgentToolCallsPerMinute: &tool,
		},
	}
}

func FixtureRateLimitsUpdateResponse_ZeroValue() RateLimitsUpdateResponse {
	return RateLimitsUpdateResponse{}
}

func FixtureRateLimitsUpdateResponse_Edge() RateLimitsUpdateResponse {
	warning := strPtr("hot-reload failed — restart required")
	return RateLimitsUpdateResponse{
		Saved:           true,
		RequiresRestart: true,
		Warning:         warning,
	}
}

// ── SessionScopeUpdateResponse ────────────────────────────────────────────────
// Traces to: contracts/components/schemas/SessionScopeUpdateResponse.yaml

func FixtureSessionScopeUpdateResponse_Populated() SessionScopeUpdateResponse {
	return SessionScopeUpdateResponse{
		Saved:           true,
		RequiresRestart: true,
		AppliedDmScope:  "agent",
	}
}

func FixtureSessionScopeUpdateResponse_ZeroValue() SessionScopeUpdateResponse {
	return SessionScopeUpdateResponse{}
}

func FixtureSessionScopeUpdateResponse_Edge() SessionScopeUpdateResponse {
	warning := strPtr("session routing restart required")
	return SessionScopeUpdateResponse{
		Saved:           true,
		RequiresRestart: true,
		AppliedDmScope:  "shared",
		Warning:         warning,
	}
}

// ── RetentionUpdateResponse ───────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RetentionUpdateResponse.yaml

func FixtureRetentionUpdateResponse_Populated() RetentionUpdateResponse {
	return RetentionUpdateResponse{
		Saved:           true,
		RequiresRestart: false,
		Disabled:        false,
		SessionDays:     90,
	}
}

func FixtureRetentionUpdateResponse_ZeroValue() RetentionUpdateResponse {
	return RetentionUpdateResponse{}
}

func FixtureRetentionUpdateResponse_Edge() RetentionUpdateResponse {
	return RetentionUpdateResponse{
		Saved:           true,
		RequiresRestart: false,
		Disabled:        true, // retention disabled — keep forever
		SessionDays:     0,
	}
}

// ── AgentToolsResponse ────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AgentToolsResponse.yaml

func FixtureAgentToolsResponse_Populated() AgentToolsResponse {
	agentType := AgentToolsResponseAgentTypeCore
	allow := AgentToolsResponseConfigBuiltinDefaultPolicyAllow
	toolCfgAllow := AgentToolsResponseToolsConfiguredPolicyAllow
	toolEffAllow := AgentToolsResponseToolsEffectivePolicyAllow
	return AgentToolsResponse{
		AgentType: &agentType,
		Config: struct {
			Builtin *struct {
				DefaultPolicy *AgentToolsResponseConfigBuiltinDefaultPolicy       `json:"default_policy,omitempty"`
				Policies      *map[string]AgentToolsResponseConfigBuiltinPolicies `json:"policies,omitempty"`
			} `json:"builtin,omitempty"`
			Mcp *struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			} `json:"mcp,omitempty"`
		}{
			Builtin: &struct {
				DefaultPolicy *AgentToolsResponseConfigBuiltinDefaultPolicy       `json:"default_policy,omitempty"`
				Policies      *map[string]AgentToolsResponseConfigBuiltinPolicies `json:"policies,omitempty"`
			}{DefaultPolicy: &allow},
		},
		Tools: []struct {
			ConfiguredPolicy AgentToolsResponseToolsConfiguredPolicy `json:"configured_policy"`
			EffectivePolicy  AgentToolsResponseToolsEffectivePolicy  `json:"effective_policy"`
			ManifestTier     AgentToolsResponseToolsManifestTier     `json:"manifest_tier"`
			Name             string                                  `json:"name"`
		}{
			{
				Name:             "workspace.shell",
				ConfiguredPolicy: toolCfgAllow,
				EffectivePolicy:  toolEffAllow,
				ManifestTier:     AgentToolsResponseToolsManifestTierCompressed,
			},
		},
	}
}

func FixtureAgentToolsResponse_ZeroValue() AgentToolsResponse {
	return AgentToolsResponse{}
}

func FixtureAgentToolsResponse_Edge() AgentToolsResponse {
	agentType := AgentToolsResponseAgentTypeMain
	_ = agentType
	deny := AgentToolsResponseConfigBuiltinDefaultPolicyDeny
	toolCfgDeny := AgentToolsResponseToolsConfiguredPolicyDeny
	toolEffAsk := AgentToolsResponseToolsEffectivePolicyAsk
	return AgentToolsResponse{
		AgentType: &agentType,
		Config: struct {
			Builtin *struct {
				DefaultPolicy *AgentToolsResponseConfigBuiltinDefaultPolicy       `json:"default_policy,omitempty"`
				Policies      *map[string]AgentToolsResponseConfigBuiltinPolicies `json:"policies,omitempty"`
			} `json:"builtin,omitempty"`
			Mcp *struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			} `json:"mcp,omitempty"`
		}{
			Builtin: &struct {
				DefaultPolicy *AgentToolsResponseConfigBuiltinDefaultPolicy       `json:"default_policy,omitempty"`
				Policies      *map[string]AgentToolsResponseConfigBuiltinPolicies `json:"policies,omitempty"`
			}{DefaultPolicy: &deny},
		},
		Tools: []struct {
			ConfiguredPolicy AgentToolsResponseToolsConfiguredPolicy `json:"configured_policy"`
			EffectivePolicy  AgentToolsResponseToolsEffectivePolicy  `json:"effective_policy"`
			ManifestTier     AgentToolsResponseToolsManifestTier     `json:"manifest_tier"`
			Name             string                                  `json:"name"`
		}{
			{
				Name:             "system.spawn_subagent",
				ConfiguredPolicy: toolCfgDeny,
				EffectivePolicy:  toolEffAsk,
				ManifestTier:     AgentToolsResponseToolsManifestTierCompressed,
			},
		},
	}
}

// ── ChannelEnabledResponse ────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ChannelEnabledResponse.yaml

func FixtureChannelEnabledResponse_Populated() ChannelEnabledResponse {
	return ChannelEnabledResponse{Id: "telegram", Enabled: true}
}

func FixtureChannelEnabledResponse_ZeroValue() ChannelEnabledResponse {
	return ChannelEnabledResponse{}
}

func FixtureChannelEnabledResponse_Edge() ChannelEnabledResponse {
	return ChannelEnabledResponse{Id: "discord", Enabled: false}
}

// ── ChannelTestResponse ───────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ChannelTestResponse.yaml

func FixtureChannelTestResponse_Populated() ChannelTestResponse {
	return ChannelTestResponse{Success: true, Message: "all required credentials are configured"}
}

func FixtureChannelTestResponse_ZeroValue() ChannelTestResponse {
	return ChannelTestResponse{}
}

func FixtureChannelTestResponse_Edge() ChannelTestResponse {
	return ChannelTestResponse{
		Success: false,
		Message: "missing required credential: telegram_bot_token",
	}
}

// ── BackupCreateResponse ──────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/BackupCreateResponse.yaml

func FixtureBackupCreateResponse_Populated() BackupCreateResponse {
	return BackupCreateResponse{
		Path:      "/home/user/.omnipus/backups/backup-20260516T103000Z.tar.gz",
		SizeBytes: 1048576,
		CreatedAt: time.Date(2026, 5, 16, 10, 30, 0, 0, time.UTC),
	}
}

func FixtureBackupCreateResponse_ZeroValue() BackupCreateResponse {
	return BackupCreateResponse{}
}

func FixtureBackupCreateResponse_Edge() BackupCreateResponse {
	return BackupCreateResponse{
		Path:      "/home/user/.omnipus/backups/backup-" + repeatStr("x", 30) + ".tar.gz",
		SizeBytes: 0, // minimum: 0 — empty archive valid
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// ── OperationResult ───────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/OperationResult.yaml

func FixtureOperationResult_Populated() OperationResult {
	return OperationResult{Success: true}
}

func FixtureOperationResult_ZeroValue() OperationResult {
	return OperationResult{}
}

func FixtureOperationResult_Edge() OperationResult {
	errMsg := strPtr("operation failed: permission denied")
	return OperationResult{Success: false, Error: errMsg}
}

// ── ToolApprovalResponse ──────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ToolApprovalResponse.yaml

func FixtureToolApprovalResponse_Populated() ToolApprovalResponse {
	return ToolApprovalResponse{
		ApprovalId: "ap-550e8400-e29b-41d4-a716-446655440001",
		Action:     ToolApprovalResponseAction("approve"),
		Status:     ToolApprovalResponseStatus("ok"),
	}
}

func FixtureToolApprovalResponse_ZeroValue() ToolApprovalResponse {
	return ToolApprovalResponse{}
}

func FixtureToolApprovalResponse_Edge() ToolApprovalResponse {
	return ToolApprovalResponse{
		ApprovalId: "ap-" + repeatStr("e", 36),
		Action:     ToolApprovalResponseAction("deny"),
		Status:     ToolApprovalResponseStatus("ok"),
	}
}

// ── UploadFilesResponse ───────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/UploadFilesResponse.yaml

func FixtureUploadFilesResponse_Populated() UploadFilesResponse {
	return UploadFilesResponse{
		Files: []UploadedFile{
			{
				Name:        "report.pdf",
				Path:        "uploads/sess-1/report.pdf",
				ContentType: "application/pdf",
				Ref:         strPtr("media://550e8400-e29b-41d4-a716-446655440000"),
				Size:        204800,
			},
		},
	}
}

func FixtureUploadFilesResponse_ZeroValue() UploadFilesResponse {
	return UploadFilesResponse{}
}

func FixtureUploadFilesResponse_Edge() UploadFilesResponse {
	return UploadFilesResponse{
		Files: []UploadedFile{
			{Name: "a.png", Path: "uploads/s/a.png", ContentType: "image/png", Size: 0},
			{Name: "b.txt", Path: "uploads/s/b.txt", ContentType: "text/plain", Size: 1},
		},
	}
}

// ── AgentOwnerUpdateResponse ──────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AgentOwnerUpdateResponse.yaml

func FixtureAgentOwnerUpdateResponse_Populated() AgentOwnerUpdateResponse {
	return AgentOwnerUpdateResponse{
		AgentId:       "custom-agent-abc123",
		OwnerUsername: "alice",
		Success:       true,
	}
}

func FixtureAgentOwnerUpdateResponse_ZeroValue() AgentOwnerUpdateResponse {
	return AgentOwnerUpdateResponse{}
}

func FixtureAgentOwnerUpdateResponse_Edge() AgentOwnerUpdateResponse {
	return AgentOwnerUpdateResponse{
		AgentId:       "custom-agent-" + repeatStr("x", 36),
		OwnerUsername: "unicode-owner-🔑",
		Success:       false,
	}
}

// ── Level-1 / Spec-3 / Spec-6 REST response fixtures ─────────────────────────
// Marshal-validate roundtrip coverage for the REST response types served by the
// gateway, so a Go struct producing schema-invalid JSON is caught by CI.

// ── Milestone ────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/Milestone.yaml

func FixtureMilestone_Populated() Milestone {
	return Milestone{
		Id:          "c3d4e5f6-a7b8-9012-cdef-123456789012",
		WorkspaceId: "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		Name:        "v1.0 Launch",
		Description: strPtr("Ship the first public release."),
		DueDate:     strPtr("2026-12-31"),
		Owner:       strPtr("alice"),
		Progress:    float32Ptr(0.5),
		CreatedAt:   time.Date(2026, 6, 8, 14, 22, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 6, 8, 15, 0, 0, 0, time.UTC),
	}
}

// FixtureMilestone_ZeroValue — Go zero values. Expected to FAIL validation:
// name is "" (minLength: 1) and workspace_id/id are required-but-empty
// (id has no minLength, but name does, so the object is invalid).
func FixtureMilestone_ZeroValue() Milestone {
	return Milestone{}
}

// ── Workspace ────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/Workspace.yaml

func FixtureWorkspace_Populated() Workspace {
	return Workspace{
		Id:          "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		Name:        "website-api",
		Description: strPtr("Main REST API service"),
		Status:      WorkspaceStatusActive,
		Pinned:      true,
		PinOrder:    1,
		CoreTeam:    &[]string{"mia", "jim"},
		Repository:  strPtr("https://github.com/org/repo"),
		TaskCount:   3,
		IsDefault:   boolPtr(false),
		Owner:       strPtr("alice"),
		CreatedAt:   time.Date(2026, 6, 8, 14, 22, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 6, 8, 15, 0, 0, 0, time.UTC),
	}
}

// FixtureWorkspace_ZeroValue — Go zero values. Expected to FAIL validation:
// status is "" (not in [active, archived]) and name is "" (minLength: 1).
func FixtureWorkspace_ZeroValue() Workspace {
	return Workspace{}
}

// ── DelegationPolicy ─────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/DelegationPolicy.yaml
//
// DelegationPolicy is not promoted to a named top-level component (oapi-codegen
// inlines it as an anonymous struct inside Agent / AgentCreateRequest /
// AgentUpdateRequest). The wire shape served by the gateway is the Agent's
// `delegation_policy` sub-object, so these fixtures return that exact serialized
// shape as raw JSON for validation against the schema file. Note: the schema has
// no required fields, so the empty object ({}) is VALID (deny-by-default).

// FixtureDelegationPolicy_PopulatedJSON returns the serialized shape the gateway
// emits for a fully-specified delegation policy.
func FixtureDelegationPolicy_PopulatedJSON() []byte {
	return []byte(`{
		"to": [{"kind": "local", "id": "ray"}],
		"accept_from": [{"kind": "local", "id": "jim"}],
		"modes": ["await", "background", "task"],
		"depth": 3,
		"budget": {"max_cost_usd": 1.0, "max_tokens": 100000}
	}`)
}

// FixtureDelegationPolicy_ZeroValueJSON is the empty object the gateway emits
// when no delegation is configured. Expected to PASS: no required fields.
func FixtureDelegationPolicy_ZeroValueJSON() []byte {
	return []byte(`{}`)
}

// FixtureDelegationPolicy_InvalidJSON has an out-of-enum reference kind.
// Expected to FAIL: to[].kind must be one of [local, remote-a2a].
func FixtureDelegationPolicy_InvalidJSON() []byte {
	return []byte(`{"to": [{"kind": "telepathy", "id": "ray"}]}`)
}

// ── ExecutorConfig ───────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ExecutorConfig.yaml

func FixtureExecutorConfig_Populated() ExecutorConfig {
	kind := ExternalCli
	cli := ClaudeCode
	return ExecutorConfig{
		Kind: &kind,
		Cli:  &cli,
	}
}

// FixtureExecutorConfig_ZeroValue — Go zero values. Expected to FAIL validation:
// kind is "" (not in [native, external-cli, remote-a2a]).
func FixtureExecutorConfig_ZeroValue() ExecutorConfig {
	return ExecutorConfig{}
}

// ── IntegrationProvider ──────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/IntegrationProvider.yaml

func FixtureIntegrationProvider_Populated() IntegrationProvider {
	return IntegrationProvider{
		Id:          "brave",
		Kind:        IntegrationProviderKindSearch,
		DisplayName: "Brave Search",
		Configured:  true,
		RequiresKey: true,
		Active:      boolPtr(true),
	}
}

// FixtureIntegrationProvider_ZeroValue — Go zero values. Expected to FAIL
// validation: kind is "" (not in [search, voice]).
func FixtureIntegrationProvider_ZeroValue() IntegrationProvider {
	return IntegrationProvider{}
}

// ── ReAuthResponse ───────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ReAuthResponse.yaml
// Note: all required fields are scalar with no value constraints, so the Go
// zero value ({verified:false, token:"", expires_in:0}) is a VALID object —
// there is no ZeroValue-fails case for this type.

func FixtureReAuthResponse_Populated() ReAuthResponse {
	return ReAuthResponse{
		Verified:  true,
		Token:     "reauth_2f1a9c0b8d7e6f5a",
		ExpiresIn: 300,
	}
}

// FixtureReAuthResponse_ZeroValue — Go zero values. Expected to PASS: all
// required fields are present (false/""/0 are valid for their types).
func FixtureReAuthResponse_ZeroValue() ReAuthResponse {
	return ReAuthResponse{}
}

// ── PerformanceSettings ──────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/PerformanceSettings.yaml
// Note: PerformanceSettings has no required fields and both properties are
// optional pointers, so the zero value ({}) is a VALID empty object.

func FixturePerformanceSettings_Populated() PerformanceSettings {
	return PerformanceSettings{
		MaxParallelAgents:          intPtr(4),
		EffectiveMaxParallelAgents: intPtr(4),
	}
}

// FixturePerformanceSettings_ZeroValue — nil pointers marshal to {}. Expected
// to PASS: no required fields.
func FixturePerformanceSettings_ZeroValue() PerformanceSettings {
	return PerformanceSettings{}
}

// ── AgentCreateRequest ────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AgentCreateRequest.yaml

func FixtureAgentCreateRequest_Populated() AgentCreateRequest {
	t := AgentCreateRequestTypeMain
	color := "#D4AF37"
	icon := "Robot"
	model := "claude-sonnet-4-6"
	enabled := true
	defaultPolicy := AgentCreateRequestToolsCfgBuiltinDefaultPolicyAllow
	deny := AgentCreateRequestToolsCfgBuiltinPoliciesDeny
	sandbox := AgentCreateRequestSandboxProfileWorkspace
	steering := AgentCreateRequestSteeringModeOneAtATime
	mode := AgentCreateRequestDelegationPolicyModesAwait
	toKind := AgentCreateRequestDelegationPolicyToKindLocal
	description := "Focused research assistant"
	temperature := 0.7
	maxTokens := 4096
	topP := 1.0
	maxCost := 5.0
	maxCalls := 100
	maxTools := 60

	return AgentCreateRequest{
		Name:        "Research Bot",
		Type:        &t,
		Description: &description,
		Model:       &model,
		Color:       &color,
		Icon:        &icon,
		Soul:        "You are a focused research assistant.",
		Skills:      &[]string{"web-research"},
		FallbackModels: &[]FallbackModel{
			{Model: "claude-sonnet-4-6", Provider: strPtr("anthropic")},
		},
		ModelParams: &struct {
			MaxTokens   *int     `json:"max_tokens,omitempty"`
			Temperature *float64 `json:"temperature,omitempty"`
			TopP        *float64 `json:"top_p,omitempty"`
		}{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			TopP:        &topP,
		},
		RateLimits: &struct {
			MaxCostPerDay         *float64 `json:"max_cost_per_day,omitempty"`
			MaxLlmCallsPerHour    *int     `json:"max_llm_calls_per_hour,omitempty"`
			MaxToolCallsPerMinute *int     `json:"max_tool_calls_per_minute,omitempty"`
			UseGlobalDefaults     *bool    `json:"use_global_defaults,omitempty"`
		}{
			UseGlobalDefaults:     &enabled,
			MaxCostPerDay:         &maxCost,
			MaxLlmCallsPerHour:    &maxCalls,
			MaxToolCallsPerMinute: &maxTools,
		},
		SandboxProfile: &sandbox,
		ShellPolicy: &struct {
			CustomDenyPatterns *[]string `json:"custom_deny_patterns,omitempty"`
			EnableDenyPatterns *bool     `json:"enable_deny_patterns,omitempty"`
		}{
			EnableDenyPatterns: &enabled,
			CustomDenyPatterns: &[]string{"rm -rf /"},
		},
		DelegationPolicy: &struct {
			AcceptFrom *[]struct {
				Id   string                                           `json:"id"`
				Kind AgentCreateRequestDelegationPolicyAcceptFromKind `json:"kind"`
			} `json:"accept_from,omitempty"`
			Budget *struct {
				MaxCostUsd *float64 `json:"max_cost_usd,omitempty"`
				MaxTokens  *int     `json:"max_tokens,omitempty"`
			} `json:"budget,omitempty"`
			Depth *int                                       `json:"depth,omitempty"`
			Modes *[]AgentCreateRequestDelegationPolicyModes `json:"modes,omitempty"`
			To    *[]struct {
				Id   string                                   `json:"id"`
				Kind AgentCreateRequestDelegationPolicyToKind `json:"kind"`
			} `json:"to,omitempty"`
		}{
			Depth: intPtr(3),
			Modes: &[]AgentCreateRequestDelegationPolicyModes{mode},
			To: &[]struct {
				Id   string                                   `json:"id"`
				Kind AgentCreateRequestDelegationPolicyToKind `json:"kind"`
			}{{Id: "ray", Kind: toKind}},
		},
		ToolsCfg: &struct {
			Builtin *struct {
				DefaultPolicy *AgentCreateRequestToolsCfgBuiltinDefaultPolicy       `json:"default_policy,omitempty"`
				Policies      *map[string]AgentCreateRequestToolsCfgBuiltinPolicies `json:"policies,omitempty"`
			} `json:"builtin,omitempty"`
			Mcp *struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			} `json:"mcp,omitempty"`
		}{
			Builtin: &struct {
				DefaultPolicy *AgentCreateRequestToolsCfgBuiltinDefaultPolicy       `json:"default_policy,omitempty"`
				Policies      *map[string]AgentCreateRequestToolsCfgBuiltinPolicies `json:"policies,omitempty"`
			}{
				DefaultPolicy: &defaultPolicy,
				Policies: &map[string]AgentCreateRequestToolsCfgBuiltinPolicies{
					"system.*": deny,
				},
			},
			Mcp: &struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			}{
				Servers: &[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				}{{Id: "my-mcp"}},
			},
		},
		SteeringMode: &steering,
	}
}

// FixtureAgentCreateRequest_InvalidType returns a request whose type value is
// not in the enum. JSON Schema validation must reject it.
func FixtureAgentCreateRequest_InvalidType() AgentCreateRequest {
	t := AgentCreateRequestType("not-a-valid-type")
	return AgentCreateRequest{
		Name: "Bad Type",
		Type: &t,
		Soul: "Valid soul content.",
	}
}

// ── AgentUpdateRequest ───────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AgentUpdateRequest.yaml

func FixtureAgentUpdateRequest_Populated() AgentUpdateRequest {
	color := "#D4AF37"
	icon := "Robot"
	model := "gpt-4o"
	name := "Renamed Agent"
	description := "Updated description"
	temperature := 0.5
	maxTokens := 2048
	topP := 0.9
	defaultPolicy := AgentUpdateRequestToolsCfgBuiltinDefaultPolicyAsk
	allow := AgentUpdateRequestToolsCfgBuiltinPoliciesAllow
	sandbox := AgentUpdateRequestSandboxProfileHost
	steering := QueueAndProcess
	mode := AgentUpdateRequestDelegationPolicyModesBackground
	toKind := AgentUpdateRequestDelegationPolicyToKindLocal
	heartbeat := "Check queue every hour."
	soul := "You are a helpful assistant."
	voice := "alloy"
	updatedAt := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)

	vDefault := true
	return AgentUpdateRequest{
		Name:           &name,
		Description:    &description,
		Model:          &model,
		Color:          &color,
		Icon:           &icon,
		Default:        &vDefault,
		Soul:           &soul,
		Heartbeat:      &heartbeat,
		Voice:          &voice,
		SandboxProfile: &sandbox,
		SteeringMode:   &steering,
		ModelParams: &struct {
			MaxTokens   *int     `json:"max_tokens,omitempty"`
			Temperature *float64 `json:"temperature,omitempty"`
			TopP        *float64 `json:"top_p,omitempty"`
		}{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			TopP:        &topP,
		},
		ToolsCfg: &struct {
			Builtin *struct {
				DefaultPolicy *AgentUpdateRequestToolsCfgBuiltinDefaultPolicy       `json:"default_policy,omitempty"`
				Policies      *map[string]AgentUpdateRequestToolsCfgBuiltinPolicies `json:"policies,omitempty"`
			} `json:"builtin,omitempty"`
			Mcp *struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			} `json:"mcp,omitempty"`
		}{
			Builtin: &struct {
				DefaultPolicy *AgentUpdateRequestToolsCfgBuiltinDefaultPolicy       `json:"default_policy,omitempty"`
				Policies      *map[string]AgentUpdateRequestToolsCfgBuiltinPolicies `json:"policies,omitempty"`
			}{
				DefaultPolicy: &defaultPolicy,
				Policies: &map[string]AgentUpdateRequestToolsCfgBuiltinPolicies{
					"workspace.shell": allow,
				},
			},
		},
		DelegationPolicy: &struct {
			AcceptFrom *[]struct {
				Id   string                                           `json:"id"`
				Kind AgentUpdateRequestDelegationPolicyAcceptFromKind `json:"kind"`
			} `json:"accept_from,omitempty"`
			Budget *struct {
				MaxCostUsd *float64 `json:"max_cost_usd,omitempty"`
				MaxTokens  *int     `json:"max_tokens,omitempty"`
			} `json:"budget,omitempty"`
			Depth *int                                       `json:"depth,omitempty"`
			Modes *[]AgentUpdateRequestDelegationPolicyModes `json:"modes,omitempty"`
			To    *[]struct {
				Id   string                                   `json:"id"`
				Kind AgentUpdateRequestDelegationPolicyToKind `json:"kind"`
			} `json:"to,omitempty"`
		}{
			Depth: intPtr(2),
			Modes: &[]AgentUpdateRequestDelegationPolicyModes{mode},
			To: &[]struct {
				Id   string                                   `json:"id"`
				Kind AgentUpdateRequestDelegationPolicyToKind `json:"kind"`
			}{{Id: "m", Kind: toKind}},
		},
		UpdatedAt: &updatedAt,
	}
}

// FixtureAgentUpdateRequest_UpdatedAt returns a minimal patch whose only field
// is a valid RFC3339 updated_at. JSON Schema validation must accept it.
func FixtureAgentUpdateRequest_UpdatedAt() AgentUpdateRequest {
	updatedAt := time.Date(2026, 6, 19, 12, 34, 56, 0, time.UTC)
	return AgentUpdateRequest{
		UpdatedAt: &updatedAt,
	}
}

// ── ChannelRouting ────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ChannelRouting.yaml

func FixtureChannelRouting_Populated() ChannelRouting {
	id := "agent-a"
	return ChannelRouting{DefaultAgentId: &id}
}

// ── CliDetect ─────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/CliDetect.yaml
// Restructured from three booleans (hasClaude/hasCodex/hasOpencode) to
// per-CLI {installed, path, source} objects (External-Executor CLI Path
// Detection feature, ADR-030).

func FixtureCliDetect_Populated() CliDetect {
	claudePath := "/usr/local/bin/claude"
	claudeSource := CliDetectClaudeSourcePath
	opencodePath := "/home/dev/.local/bin/opencode"
	opencodeSource := CliDetectOpencodeSourceWellKnown
	return CliDetect{
		Claude: struct {
			Installed bool                   `json:"installed"`
			Path      *string                `json:"path,omitempty"`
			Source    *CliDetectClaudeSource `json:"source,omitempty"`
		}{
			Installed: true,
			Path:      &claudePath,
			Source:    &claudeSource,
		},
		Codex: struct {
			Installed bool                  `json:"installed"`
			Path      *string               `json:"path,omitempty"`
			Source    *CliDetectCodexSource `json:"source,omitempty"`
		}{
			Installed: false,
		},
		Opencode: struct {
			Installed bool                     `json:"installed"`
			Path      *string                  `json:"path,omitempty"`
			Source    *CliDetectOpencodeSource `json:"source,omitempty"`
		}{
			Installed: true,
			Path:      &opencodePath,
			Source:    &opencodeSource,
		},
	}
}

// FixtureCliDetect_ZeroValue — Go zero values. Expected to PASS: claude/codex/
// opencode are present (required) and each satisfies its own required
// "installed" field via its Go zero value (false); path/source are optional.
func FixtureCliDetect_ZeroValue() CliDetect {
	return CliDetect{}
}

// ── CliDetectEntry ────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/CliDetectEntry.yaml

func FixtureCliDetectEntry_Populated() CliDetectEntry {
	path := "/usr/local/bin/codex"
	source := CliDetectEntrySourcePath
	return CliDetectEntry{
		Installed: true,
		Path:      &path,
		Source:    &source,
	}
}

// FixtureCliDetectEntry_NotInstalled — installed:false with path/source omitted,
// the shape detection returns when a CLI cannot be located anywhere.
func FixtureCliDetectEntry_NotInstalled() CliDetectEntry {
	return CliDetectEntry{Installed: false}
}

// ── CliValidateRequest ───────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/CliValidateRequest.yaml

func FixtureCliValidateRequest_Populated() CliValidateRequest {
	return CliValidateRequest{
		Cli:     CliValidateRequestCliClaudeCode,
		CliPath: "/usr/local/bin/claude",
	}
}

// FixtureCliValidateRequest_ZeroValue — Go zero values. Expected to FAIL
// validation: cli is "" (not in [claude-code, codex, opencode]).
func FixtureCliValidateRequest_ZeroValue() CliValidateRequest {
	return CliValidateRequest{}
}

// ── CliValidateResponse ──────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/CliValidateResponse.yaml

func FixtureCliValidateResponse_Populated() CliValidateResponse {
	resolvedPath := "/usr/local/bin/claude"
	version := "1.2.3"
	detail := "OK"
	return CliValidateResponse{
		Ok:           true,
		Reason:       CliValidateResponseReasonOk,
		ResolvedPath: &resolvedPath,
		Version:      &version,
		Detail:       &detail,
	}
}

// FixtureCliValidateResponse_MissingBinary — resolved_path/version absent, a
// classified (never raw-stderr) detail. reason=missing-binary blocks Create/Save.
func FixtureCliValidateResponse_MissingBinary() CliValidateResponse {
	detail := "not found"
	return CliValidateResponse{
		Ok:     false,
		Reason: CliValidateResponseReasonMissingBinary,
		Detail: &detail,
	}
}

// FixtureCliValidateResponse_ZeroValue — Go zero values. Expected to FAIL
// validation: reason is "" (not in [ok, missing-binary, handshake-failed,
// unauthenticated, unknown-cli]).
func FixtureCliValidateResponse_ZeroValue() CliValidateResponse {
	return CliValidateResponse{}
}

// ── SlashCommand ──────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/SlashCommand.yaml
// Regression guard: delivery is a required enum [client, agent]. Non-web
// commands (agents/tasks/skills/channels/status/config) must emit "agent",
// not an empty string, which would fail schema validation.

// FixtureSlashCommand_ClientDelivery returns a web command (clear) with delivery=client.
func FixtureSlashCommand_ClientDelivery() SlashCommand {
	usage := "/clear"
	return SlashCommand{
		Name:        "clear",
		Label:       "/clear",
		Description: "Start a new chat",
		Usage:       &usage,
		Delivery:    SlashCommandDeliveryClient,
	}
}

// FixtureSlashCommand_AgentDelivery returns a non-web command (agents) with delivery=agent.
// This is the regression fixture for the Constraint #8 bug: non-web commands
// previously emitted delivery="" which fails the enum constraint in SlashCommand.yaml.
func FixtureSlashCommand_AgentDelivery() SlashCommand {
	usage := "/agents"
	return SlashCommand{
		Name:        "agents",
		Label:       "/agents",
		Description: "List registered agents",
		Usage:       &usage,
		Delivery:    SlashCommandDeliveryAgent,
	}
}

// FixtureSlashCommand_WithAliasesAndStreaming returns /cancel with all optional fields set.
func FixtureSlashCommand_WithAliasesAndStreaming() SlashCommand {
	usage := "/cancel"
	availWhileStreaming := true
	return SlashCommand{
		Name:                    "cancel",
		Label:                   "/cancel",
		Description:             "Cancel the current turn",
		Usage:                   &usage,
		Delivery:                SlashCommandDeliveryClient,
		AvailableWhileStreaming: &availWhileStreaming,
	}
}

// FixtureSlashCommand_ZeroValue returns a zero-value SlashCommand.
// Expected to FAIL schema validation: name/label/description/delivery are required;
// delivery="" is not in enum [client, agent].
func FixtureSlashCommand_ZeroValue() SlashCommand {
	return SlashCommand{}
}
