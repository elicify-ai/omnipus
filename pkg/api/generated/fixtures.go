package generated

import "time"

// ── WebSocket frame fixtures (AsyncAPI) ─────────────────────────────────────

// ToolApprovalRequiredFrame — the Ava-chat bug type. args MUST be a non-nil map.

func FixtureToolApprovalRequiredFrame_Populated() ToolApprovalRequiredFrame {
	return ToolApprovalRequiredFrame{
		Type:        "tool_approval_required",
		ApprovalId:  "ap-550e8400-e29b-41d4-a716-446655440001",
		ToolCallId:  "tc-550e8400-e29b-41d4-a716-446655440002",
		ToolName:    "bash",
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
		ToolName:    "bash",
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
		ToolName:    "delegate",
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
				ToolName:    "bash",
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
		Tool:         "bash",
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
		Tool:      "bash",
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
	detail := "structured detail for the live error"
	return ErrorFrame{
		Type:      "error",
		Message:   "LLM rate limit exceeded — retry after 60 seconds",
		SessionId: &sessId,
		Payload: &ErrorPayload{
			LlmError: LLMError{
				Code: "rate_limited",
				// Read from the generated catalogue rather than pasted: this
				// fixture used to carry a hardcoded copy of the message, which
				// went stale the moment the copy changed and left the retired
				// "From the model:" prefix sitting in the repo.
				Message:   LLMErrorUserMessages["rate_limited"],
				Retryable: true,
				Detail:    &detail,
			},
		},
	}
}

func FixtureErrorFrame_ZeroValue() ErrorFrame {
	return ErrorFrame{}
}

func FixtureErrorFrame_Edge() ErrorFrame {
	return ErrorFrame{
		Type:    "error",
		Message: repeatStr("x", 4096), // long error message
		Payload: &ErrorPayload{
			LlmError: LLMError{
				Code:      "unknown",
				Message:   repeatStr("x", 4096),
				Retryable: false,
			},
		},
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
		Tool:         "bash",
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
		Tool:      "bash",
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
		Tool:      "delegate",
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

// FixtureSubagentEndFrame_Parked — ADR-057 UAT defect C2 fix: the child
// sub-turn stopped because a message_parent(kind="question", wait=true)
// call parked it awaiting the parent's answer, not because it succeeded,
// errored, was cancelled, or timed out. No `reason` (that field is
// documented as populated only when status is "interrupted").
func FixtureSubagentEndFrame_Parked() SubagentEndFrame {
	agentId := "ava"
	durationMs := 1200
	parentCallId := "parent-call-2"
	return SubagentEndFrame{
		Type:         "subagent_end",
		SessionId:    "sess-1",
		SpanId:       "span-def-456",
		Status:       "parked",
		AgentId:      &agentId,
		DurationMs:   &durationMs,
		ParentCallId: &parentCallId,
	}
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
	tool := "bash"
	return RateLimitFrame{
		Type:              "rate_limit",
		SessionId:         "sess-1",
		PolicyRule:        "100req/min",
		Resource:          "bash",
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

// ── REST response type fixtures (OpenAPI) ────────────────────────────────────

// LoginResponse

func FixtureLoginResponse_Populated() LoginResponse {
	warning := strPtr("API key stored in plaintext")
	return LoginResponse{
		Token:    "omnipus_" + repeatStr("a", 64),
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
		Color:             &color,
		Icon:              &icon,
		Model:             &model,
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
// Expected: FAIL — type="" fails the const:"message" check, and content=""
// with no media satisfies neither anyOf branch (content minLength:1 OR
// media minItems:1).
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

// FixtureMessageFrame_EmptyContentWithMedia — UAT Issue 5: an
// attachment-only send legitimately has no caption. content="" is valid
// ONLY because media is present and non-empty (the anyOf's second branch).
func FixtureMessageFrame_EmptyContentWithMedia() MessageFrame {
	return MessageFrame{
		Type:    "message",
		Content: "",
		Media:   []string{"media://workspace/ws1/att1"},
	}
}

// FixtureMessageFrame_EmptyContentNoMedia — content="" and no media at all
// must still be rejected: the content-or-media invariant must not degrade
// into "content is always optional".
func FixtureMessageFrame_EmptyContentNoMedia() MessageFrame {
	return MessageFrame{
		Type:    "message",
		Content: "",
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
// Covers Task, SessionCloseFrame, and related types.

// ── Task ─────────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/Task.yaml

func FixtureTask_Populated() Task {
	agentId := "jim"
	agentName := "Jim"
	parentTaskId := "parent-task-00000000-0000-0000-0000-000000000001"
	prompt := "Summarize the last 7 days of gateway logs."
	description := "Look for anomalies in the last 7 days of gateway logs."
	planId := "01J3ZQK8N2H8VXNRP5T7C9M4WE"
	attemptCount := 1
	maxAttempts := 5
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
		Tags:          &[]string{"milestone:v1.0-launch"},
		PlanId:        &planId,
		AttemptCount:  &attemptCount,
		MaxAttempts:   &maxAttempts,
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
	t.Criteria = &[]struct {
		Author struct {
			Id   string                 `json:"id"`
			Kind TaskCriteriaAuthorKind `json:"kind"`
		} `json:"author"`
		Behavior *struct {
			MaxCount *int                       `json:"max_count,omitempty"`
			MinCount *int                       `json:"min_count,omitempty"`
			Scope    *TaskCriteriaBehaviorScope `json:"scope,omitempty"`
			Tool     string                     `json:"tool"`
		} `json:"behavior,omitempty"`
		Check *struct {
			Command          string `json:"command"`
			ExpectedExitCode int    `json:"expected_exit_code"`
		} `json:"check,omitempty"`
		Id     *string            `json:"id,omitempty"`
		Kind   TaskCriteriaKind   `json:"kind"`
		Status TaskCriteriaStatus `json:"status"`
		Text   string             `json:"text"`
	}{
		{
			Kind:   TaskCriteriaKind("check"),
			Text:   "go test ./pkg/plan/... passes",
			Status: TaskCriteriaStatus("pending"),
			Check: &struct {
				Command          string `json:"command"`
				ExpectedExitCode int    `json:"expected_exit_code"`
			}{Command: "go test ./pkg/plan/...", ExpectedExitCode: 0},
			Author: struct {
				Id   string                 `json:"id"`
				Kind TaskCriteriaAuthorKind `json:"kind"`
			}{Id: "jim", Kind: TaskCriteriaAuthorKind("agent")},
		},
	}
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

// FixtureTaskTrigger_Rrule — a recurring RFC 5545 RRULE trigger, exercising
// the rrule/dtstart_ms/tz wire keys (Calendar Recurrence Redesign,
// contracts/components/schemas/TaskTrigger.yaml's `recurring` variant with
// `rrule` rather than the legacy `cron_expr`).
func FixtureTaskTrigger_Rrule() TaskTrigger {
	rrule := "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=10"
	dtstartMs := int64(1784624400000)
	tz := "Europe/Berlin"
	tr := TaskTrigger{Type: TaskTriggerType("recurring")}
	tr.Config.Rrule = &rrule
	tr.Config.DtstartMs = &dtstartMs
	tr.Config.Tz = &tz
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
	return Todo{
		Text:   "完了-done-✓", //nolint:gosmopolitan // intentional CJK text in a unicode edge-case fixture
		Status: TodoStatusCompleted,
	}
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
	}
}

// FixtureValidateTokenResponse_ZeroValue — Go zero values.
// Expected: PASS — username has no minLength, so "" still satisfies the schema.
func FixtureValidateTokenResponse_ZeroValue() ValidateTokenResponse {
	return ValidateTokenResponse{}
}

// FixtureValidateTokenResponse_Edge — unicode username.
func FixtureValidateTokenResponse_Edge() ValidateTokenResponse {
	return ValidateTokenResponse{
		Username: "unicode-user-🔑-" + repeatStr("a", 20),
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

// TokenBudget is the sole app-level spend brake; see pkg/agent/budget.go (D12 / R§8.3).

func FixtureRateLimitsResponse_Populated() RateLimitsResponse {
	return RateLimitsResponse{
		Enabled:                    true,
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
		MaxAgentLlmCallsPerHour:    0, // unlimited
		MaxAgentToolCallsPerMinute: 0, // unlimited
	}
}

// ── RateLimitsUpdateRequest ───────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RateLimitsUpdateRequest.yaml

// TokenBudget is the sole app-level spend brake; see pkg/agent/budget.go (D12 / R§8.3).

func FixtureRateLimitsUpdateRequest_Populated() RateLimitsUpdateRequest {
	llm := int64(200)
	tool := int64(120)
	return RateLimitsUpdateRequest{
		MaxAgentLlmCallsPerHour:    &llm,
		MaxAgentToolCallsPerMinute: &tool,
	}
}

func FixtureRateLimitsUpdateRequest_ZeroValue() RateLimitsUpdateRequest {
	return RateLimitsUpdateRequest{}
}

func FixtureRateLimitsUpdateRequest_Edge() RateLimitsUpdateRequest {
	llm := int64(0)
	return RateLimitsUpdateRequest{MaxAgentLlmCallsPerHour: &llm}
}

// ── RateLimitsUpdateResponse ──────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RateLimitsUpdateResponse.yaml

// TokenBudget is the sole app-level spend brake; see pkg/agent/budget.go (D12 / R§8.3).

func FixtureRateLimitsUpdateResponse_Populated() RateLimitsUpdateResponse {
	llm := int64(200)
	tool := int64(120)
	return RateLimitsUpdateResponse{
		Saved:           true,
		RequiresRestart: false,
		Applied: &struct {
			MaxAgentLlmCallsPerHour    *int64 `json:"max_agent_llm_calls_per_hour,omitempty"`
			MaxAgentToolCallsPerMinute *int64 `json:"max_agent_tool_calls_per_minute,omitempty"`
		}{
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
	toolCfgAllow := AgentToolsResponseToolsConfiguredPolicyAllow
	toolEffAllow := AgentToolsResponseToolsEffectivePolicyAllow
	return AgentToolsResponse{
		AgentType: &agentType,
		Config: struct {
			Builtin *struct {
				Policies map[string]AgentToolsResponseConfigBuiltinPolicies `json:"policies"`
			} `json:"builtin,omitempty"`
			Mcp *struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			} `json:"mcp,omitempty"`
		}{
			Builtin: &struct {
				Policies map[string]AgentToolsResponseConfigBuiltinPolicies `json:"policies"`
			}{Policies: map[string]AgentToolsResponseConfigBuiltinPolicies{
				"bash": AgentToolsResponseConfigBuiltinPoliciesAllow,
			}},
		},
		Tools: []struct {
			ConfiguredPolicy AgentToolsResponseToolsConfiguredPolicy `json:"configured_policy"`
			EffectivePolicy  AgentToolsResponseToolsEffectivePolicy  `json:"effective_policy"`
			ManifestTier     AgentToolsResponseToolsManifestTier     `json:"manifest_tier"`
			Name             string                                  `json:"name"`
		}{
			{
				Name:             "bash",
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
	toolCfgDeny := AgentToolsResponseToolsConfiguredPolicyDeny
	toolEffAsk := AgentToolsResponseToolsEffectivePolicyAsk
	return AgentToolsResponse{
		AgentType: &agentType,
		Config: struct {
			Builtin *struct {
				Policies map[string]AgentToolsResponseConfigBuiltinPolicies `json:"policies"`
			} `json:"builtin,omitempty"`
			Mcp *struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			} `json:"mcp,omitempty"`
		}{
			Builtin: &struct {
				Policies map[string]AgentToolsResponseConfigBuiltinPolicies `json:"policies"`
			}{Policies: map[string]AgentToolsResponseConfigBuiltinPolicies{
				"delete_agent": AgentToolsResponseConfigBuiltinPoliciesDeny,
			}},
		},
		Tools: []struct {
			ConfiguredPolicy AgentToolsResponseToolsConfiguredPolicy `json:"configured_policy"`
			EffectivePolicy  AgentToolsResponseToolsEffectivePolicy  `json:"effective_policy"`
			ManifestTier     AgentToolsResponseToolsManifestTier     `json:"manifest_tier"`
			Name             string                                  `json:"name"`
		}{
			{
				Name:             "delete_agent",
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

// ── Level-1 / Spec-3 / Spec-6 REST response fixtures ─────────────────────────
// Marshal-validate roundtrip coverage for the REST response types served by the
// gateway, so a Go struct producing schema-invalid JSON is caught by CI.

// ── AcceptanceCriterion ──────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AcceptanceCriterion.yaml

func FixtureAcceptanceCriterion_Populated() AcceptanceCriterion {
	return AcceptanceCriterion{
		Id:   strPtr("550e8400-e29b-41d4-a716-446655440010"),
		Kind: AcceptanceCriterionKind("check"),
		Text: "All new pkg/plan tests pass",
		Check: &struct {
			Command          string `json:"command"`
			ExpectedExitCode int    `json:"expected_exit_code"`
		}{Command: "go test ./pkg/plan/...", ExpectedExitCode: 0},
		Author: struct {
			Id   string                        `json:"id"`
			Kind AcceptanceCriterionAuthorKind `json:"kind"`
		}{Id: "jim", Kind: AcceptanceCriterionAuthorKind("agent")},
		Status: AcceptanceCriterionStatus("pending"),
	}
}

// FixtureAcceptanceCriterion_ZeroValue — Go zero values. Expected to FAIL
// validation: kind="" (not in enum), author.kind="" (not in enum),
// author.id="" (minLength: 1), status="" (not in enum).
func FixtureAcceptanceCriterion_ZeroValue() AcceptanceCriterion {
	return AcceptanceCriterion{}
}

// FixtureAcceptanceCriterion_Prose — a prose (LLM-judged) criterion with no check.
func FixtureAcceptanceCriterion_Prose() AcceptanceCriterion {
	return AcceptanceCriterion{
		Kind: AcceptanceCriterionKind("prose"),
		Text: "The release notes clearly explain the migration path",
		Author: struct {
			Id   string                        `json:"id"`
			Kind AcceptanceCriterionAuthorKind `json:"kind"`
		}{Id: "alice", Kind: AcceptanceCriterionAuthorKind("user")},
		Status: AcceptanceCriterionStatus("pending"),
	}
}

// ── EvidenceRecord ───────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/EvidenceRecord.yaml

func FixtureEvidenceRecord_Populated() EvidenceRecord {
	return EvidenceRecord{
		Id:           "550e8400-e29b-41d4-a716-446655440030",
		TaskId:       "550e8400-e29b-41d4-a716-446655440000",
		CriterionId:  "550e8400-e29b-41d4-a716-446655440010",
		Attempt:      1,
		Command:      "go test ./pkg/plan/... [REDACTED]",
		ExitCode:     0,
		Output:       "ok  \tpkg/plan\t0.412s",
		Truncated:    false,
		TimedOut:     false,
		PolicyDenied: false,
		RecordedAt:   time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
	}
}

// FixtureEvidenceRecord_ZeroValue — Go zero values. Expected to FAIL
// validation: id/task_id/criterion_id/command/output/recorded_at are all "".
func FixtureEvidenceRecord_ZeroValue() EvidenceRecord {
	return EvidenceRecord{}
}

// ── CriterionVerdict ─────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/CriterionVerdict.yaml

func FixtureCriterionVerdict_Populated() CriterionVerdict {
	return CriterionVerdict{
		CriterionId: "550e8400-e29b-41d4-a716-446655440010",
		Met:         true,
		Reason:      "go test output shows all tests passing.",
	}
}

func FixtureCriterionVerdict_ZeroValue() CriterionVerdict {
	return CriterionVerdict{}
}

// ── JudgeVerdict ─────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/JudgeVerdict.yaml

func FixtureJudgeVerdict_Populated() JudgeVerdict {
	taskId := "550e8400-e29b-41d4-a716-446655440000"
	return JudgeVerdict{
		Id:     "550e8400-e29b-41d4-a716-446655440020",
		Scope:  JudgeVerdictScope("task"),
		TaskId: &taskId,
		Round:  1,
		Met:    false,
		PerCriterion: []struct {
			CriterionId string `json:"criterion_id"`
			Met         bool   `json:"met"`
			Reason      string `json:"reason"`
		}{
			{CriterionId: "550e8400-e29b-41d4-a716-446655440010", Met: false, Reason: "3 tests still failing"},
		},
		Model:        "z-ai/glm-5-turbo",
		JudgedAt:     time.Date(2026, 7, 19, 12, 5, 0, 0, time.UTC),
		JudgeAgentId: "judge",
	}
}

// FixtureJudgeVerdict_ZeroValue — Go zero values. Expected to FAIL validation:
// id="", scope="" (not in enum), round=0 (minimum:1), model/judged_at/
// judge_agent_id are all "".
func FixtureJudgeVerdict_ZeroValue() JudgeVerdict {
	return JudgeVerdict{}
}

// ── Plan ─────────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/Plan.yaml

func FixturePlan_Populated() Plan {
	progress := float32(0.5)
	return Plan{
		Id:           "01J3ZQK8N2H8VXNRP5T7C9M4WE",
		WorkspaceId:  "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		Title:        "v1.0 Launch",
		Goal:         strPtr("Ship the v1.0 release with all P0 issues closed and CI green."),
		Description:  strPtr("Coordinates the v1.0 release train across backend and SPA."),
		State:        PlanState("running"),
		PlanPhase:    (*PlanPlanPhase)(strPtr("dispatching")),
		OwnerAgentId: "jim",
		JudgeRounds:  intPtr(2),
		ActiveLoop:   boolPtr(true),
		Progress:     &progress,
		Owner:        strPtr("alice"),
		CreatedBy:    strPtr("alice"),
		CreatedAt:    time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 7, 19, 10, 5, 0, 0, time.UTC),
	}
}

// FixturePlan_ZeroValue — Go zero values. Expected to FAIL validation:
// id/workspace_id/title/owner_agent_id/owner/created_by are all "", and
// state="" (not in the 5-value enum).
func FixturePlan_ZeroValue() Plan {
	return Plan{}
}

// ── PlanCreateRequest ────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/PlanCreateRequest.yaml

func FixturePlanCreateRequest_Populated() PlanCreateRequest {
	return PlanCreateRequest{
		WorkspaceId:  "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		Title:        "v1.0 Launch",
		Goal:         strPtr("Ship the v1.0 release with all P0 issues closed and CI green."),
		OwnerAgentId: "jim",
	}
}

func FixturePlanCreateRequest_ZeroValue() PlanCreateRequest {
	return PlanCreateRequest{}
}

// ── PlanUpdateRequest ────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/PlanUpdateRequest.yaml

func FixturePlanUpdateRequest_Populated() PlanUpdateRequest {
	state := PlanUpdateRequestState("approved")
	return PlanUpdateRequest{
		Title: strPtr("v1.0 Launch (delayed)"),
		State: &state,
	}
}

// FixturePlanUpdateRequest_Empty — no fields set. Expected to FAIL validation:
// minProperties:1.
func FixturePlanUpdateRequest_Empty() PlanUpdateRequest {
	return PlanUpdateRequest{}
}

// ── PlanListResponse ─────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/PlanListResponse.yaml

func FixturePlanListResponse_Populated() PlanListResponse {
	progress := float32(0.5)
	return PlanListResponse{
		Plans: []struct {
			ActiveLoop *bool      `json:"active_loop,omitempty"`
			ApprovedAt *time.Time `json:"approved_at,omitempty"`
			Bounds     *struct {
				IdleExpiryDays                *int `json:"idle_expiry_days,omitempty"`
				PlanJudgeMaxRounds            *int `json:"plan_judge_max_rounds,omitempty"`
				SupervisionMaxAttempts        *int `json:"supervision_max_attempts,omitempty"`
				SupervisionTurnTimeoutSeconds *int `json:"supervision_turn_timeout_seconds,omitempty"`
			} `json:"bounds,omitempty"`
			CompletedAt *time.Time `json:"completed_at,omitempty"`
			CreatedAt   time.Time  `json:"created_at"`
			CreatedBy   *string    `json:"created_by,omitempty"`
			Description *string    `json:"description,omitempty"`
			Dod         *[]struct {
				Author struct {
					Id   string                             `json:"id"`
					Kind PlanListResponsePlansDodAuthorKind `json:"kind"`
				} `json:"author"`
				Behavior *struct {
					MaxCount *int                                   `json:"max_count,omitempty"`
					MinCount *int                                   `json:"min_count,omitempty"`
					Scope    *PlanListResponsePlansDodBehaviorScope `json:"scope,omitempty"`
					Tool     string                                 `json:"tool"`
				} `json:"behavior,omitempty"`
				Check *struct {
					Command          string `json:"command"`
					ExpectedExitCode int    `json:"expected_exit_code"`
				} `json:"check,omitempty"`
				Id     *string                        `json:"id,omitempty"`
				Kind   PlanListResponsePlansDodKind   `json:"kind"`
				Status PlanListResponsePlansDodStatus `json:"status"`
				Text   string                         `json:"text"`
			} `json:"dod,omitempty"`
			FailedReason               *PlanListResponsePlansFailedReason `json:"failed_reason,omitempty"`
			Goal                       *string                            `json:"goal,omitempty"`
			Id                         string                             `json:"id"`
			JudgeRounds                *int                               `json:"judge_rounds,omitempty"`
			LastActivityAt             *time.Time                         `json:"last_activity_at,omitempty"`
			LastUnmetTerminalSignature *string                            `json:"last_unmet_terminal_signature,omitempty"`
			Owner                      *string                            `json:"owner,omitempty"`
			OwnerAgentId               string                             `json:"owner_agent_id"`
			OwnerSessionId             *string                            `json:"owner_session_id,omitempty"`
			PausedReason               *string                            `json:"paused_reason,omitempty"`
			PlanPhase                  *PlanListResponsePlansPlanPhase    `json:"plan_phase,omitempty"`
			Progress                   *float32                           `json:"progress,omitempty"`
			Rationale                  *string                            `json:"rationale,omitempty"`
			SourceChannel              *string                            `json:"source_channel,omitempty"`
			SourceChatId               *string                            `json:"source_chat_id,omitempty"`
			StartedAt                  *time.Time                         `json:"started_at,omitempty"`
			State                      PlanListResponsePlansState         `json:"state"`
			Supervision                *struct {
				Attempts         *int       `json:"attempts,omitempty"`
				CorrectionRounds *int       `json:"correction_rounds,omitempty"`
				SessionId        *string    `json:"session_id,omitempty"`
				WakeAt           *time.Time `json:"wake_at,omitempty"`
				WakeError        *string    `json:"wake_error,omitempty"`
			} `json:"supervision,omitempty"`
			Title       string    `json:"title"`
			UpdatedAt   time.Time `json:"updated_at"`
			WorkspaceId string    `json:"workspace_id"`
		}{
			{
				Id:           "01J3ZQK8N2H8VXNRP5T7C9M4WE",
				WorkspaceId:  "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
				Title:        "v1.0 Launch",
				State:        PlanListResponsePlansState("running"),
				OwnerAgentId: "jim",
				Owner:        strPtr("alice"),
				CreatedBy:    strPtr("alice"),
				Progress:     &progress,
				CreatedAt:    time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC),
				UpdatedAt:    time.Date(2026, 7, 19, 10, 5, 0, 0, time.UTC),
			},
		},
		Total: 1,
	}
}

func FixturePlanListResponse_ZeroValue() PlanListResponse {
	return PlanListResponse{}
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

// ── ExecutorConfig ───────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/ExecutorConfig.yaml

func FixtureExecutorConfig_Populated() ExecutorConfig {
	kind := ExternalCli
	cli := ExternalCliToolClaudeCode
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

// ── AgentCreateRequestMain / AgentCreateRequestSubagent / AgentCreateRequestSubagent3p ──
// Traces to: contracts/components/schemas/AgentCreateRequest{Main,Subagent,Subagent3p}.yaml
//
// W1 turned the single flat AgentCreateRequest into a discriminated union —
// one named Go type per agent type, each with additionalProperties: false.
// Field allocation per docs/internal/architecture/agent-types-field-matrix.md:
//   - Main: full field set (voice included), no executor.
//   - Subagent: like Main minus voice, no executor (server derives native).
//   - Subagent3p: ONLY type/name/description/model/provider/color/icon/
//     rate_limits/soul/executor/timeout_seconds — executor
//     is REQUIRED. All Main/Subagent-only fields (tools_cfg, skills,
//     fallback_models, model_params, shell_policy, voice,
//     max_tool_iterations) do not exist as properties on this
//     variant at all.

func FixtureAgentCreateRequestMain_Populated() AgentCreateRequestMain {
	color := "#D4AF37"
	icon := "Robot"
	model := "claude-sonnet-4-6"
	enabled := true
	deny := AgentCreateRequestMainToolsCfgBuiltinPoliciesDeny
	description := "Focused research assistant"
	temperature := 0.7
	maxTokens := 4096
	topP := 1.0
	maxCost := 5.0
	maxCalls := 100
	maxTools := 60
	maxToolIterations := 60
	voice := "alloy"

	return AgentCreateRequestMain{
		Name:              "Research Bot",
		Type:              AgentCreateRequestMainTypeMain,
		Description:       &description,
		Model:             &model,
		Color:             &color,
		Icon:              &icon,
		Soul:              "You are a focused research assistant.",
		Skills:            &[]string{"web-research"},
		MaxToolIterations: &maxToolIterations,
		Voice:             &voice,
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
		ShellPolicy: &struct {
			CustomDenyPatterns *[]string `json:"custom_deny_patterns,omitempty"`
			EnableDenyPatterns *bool     `json:"enable_deny_patterns,omitempty"`
		}{
			EnableDenyPatterns: &enabled,
			CustomDenyPatterns: &[]string{"rm -rf /"},
		},
		ToolsCfg: &struct {
			Builtin *struct {
				Policies map[string]AgentCreateRequestMainToolsCfgBuiltinPolicies `json:"policies"`
			} `json:"builtin,omitempty"`
			Mcp *struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			} `json:"mcp,omitempty"`
		}{
			Builtin: &struct {
				Policies map[string]AgentCreateRequestMainToolsCfgBuiltinPolicies `json:"policies"`
			}{
				Policies: map[string]AgentCreateRequestMainToolsCfgBuiltinPolicies{
					"bash": deny,
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
	}
}

// FixtureAgentCreateRequestMain_InvalidType returns a Main request whose type
// value is not "Main". JSON Schema validation must reject it (enum: [Main]).
func FixtureAgentCreateRequestMain_InvalidType() AgentCreateRequestMain {
	return AgentCreateRequestMain{
		Name: "Bad Type",
		Type: AgentCreateRequestMainType("not-a-valid-type"),
		Soul: "Valid soul content.",
	}
}

func FixtureAgentCreateRequestSubagent_Populated() AgentCreateRequestSubagent {
	color := "#4287f5"
	icon := "Robot"
	model := "claude-sonnet-4-6"
	description := "Native delegation-only research worker"
	deny := AgentCreateRequestSubagentToolsCfgBuiltinPoliciesDeny
	maxToolIterations := 40

	return AgentCreateRequestSubagent{
		Name:              "Research Worker",
		Type:              AgentCreateRequestSubagentTypeSubagent,
		Description:       &description,
		Model:             &model,
		Color:             &color,
		Icon:              &icon,
		Soul:              "You are a focused research worker invoked only via delegation.",
		Skills:            &[]string{"web-research"},
		MaxToolIterations: &maxToolIterations,
		FallbackModels: &[]FallbackModel{
			{Model: "claude-sonnet-4-6", Provider: strPtr("anthropic")},
		},
		ToolsCfg: &struct {
			Builtin *struct {
				Policies map[string]AgentCreateRequestSubagentToolsCfgBuiltinPolicies `json:"policies"`
			} `json:"builtin,omitempty"`
			Mcp *struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			} `json:"mcp,omitempty"`
		}{
			Builtin: &struct {
				Policies map[string]AgentCreateRequestSubagentToolsCfgBuiltinPolicies `json:"policies"`
			}{
				Policies: map[string]AgentCreateRequestSubagentToolsCfgBuiltinPolicies{
					"bash": deny,
				},
			},
		},
	}
}

// FixtureAgentCreateRequestSubagent_InvalidType returns a Subagent request
// whose type value is not "Subagent". JSON Schema validation must reject it.
func FixtureAgentCreateRequestSubagent_InvalidType() AgentCreateRequestSubagent {
	return AgentCreateRequestSubagent{
		Name: "Bad Type",
		Type: AgentCreateRequestSubagentType("not-a-valid-type"),
		Soul: "Valid soul content.",
	}
}

// FixtureAgentCreateRequestSubagent3p_Populated — every field this variant
// allows: type/name/description/model/provider/color/icon/rate_limits/soul/
// executor/timeout_seconds. executor is REQUIRED.
func FixtureAgentCreateRequestSubagent3p_Populated() AgentCreateRequestSubagent3p {
	color := "#f542a7"
	icon := "Terminal"
	model := "claude-sonnet-4-6"
	provider := "anthropic"
	description := "External-CLI delegation-only worker"
	enabled := true
	maxCost := 5.0
	maxCalls := 100
	maxTools := 60
	timeoutSeconds := 300
	cli := ExternalCliToolClaudeCode
	cliPath := "/usr/local/bin/claude"

	return AgentCreateRequestSubagent3p{
		Name:           "Claude Code Worker",
		Type:           Subagent3p,
		Description:    &description,
		Model:          &model,
		Provider:       &provider,
		Color:          &color,
		Icon:           &icon,
		Soul:           "You are a focused worker running on the claude-code CLI.",
		TimeoutSeconds: &timeoutSeconds,
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
		Executor: struct {
			Cli          *ExternalCliTool                          `json:"cli,omitempty"`
			CliArgs      *string                                   `json:"cli_args,omitempty"`
			CliPath      *string                                   `json:"cli_path,omitempty"`
			EnvOverrides *map[string]string                        `json:"env_overrides,omitempty"`
			Kind         *AgentCreateRequestSubagent3pExecutorKind `json:"kind,omitempty"`
		}{
			Cli:     &cli,
			CliPath: &cliPath,
		},
	}
}

// FixtureAgentCreateRequestSubagent3p_InvalidType returns a subagent_3p
// request whose type value is not "subagent_3p". JSON Schema validation must
// reject it.
func FixtureAgentCreateRequestSubagent3p_InvalidType() AgentCreateRequestSubagent3p {
	cli := ExternalCliToolCodex
	cliPath := "/usr/local/bin/codex"
	return AgentCreateRequestSubagent3p{
		Name: "Bad Type",
		Type: AgentCreateRequestSubagent3pType("not-a-valid-type"),
		Soul: "Valid soul content.",
		Executor: struct {
			Cli          *ExternalCliTool                          `json:"cli,omitempty"`
			CliArgs      *string                                   `json:"cli_args,omitempty"`
			CliPath      *string                                   `json:"cli_path,omitempty"`
			EnvOverrides *map[string]string                        `json:"env_overrides,omitempty"`
			Kind         *AgentCreateRequestSubagent3pExecutorKind `json:"kind,omitempty"`
		}{
			Cli:     &cli,
			CliPath: &cliPath,
		},
	}
}

// FixtureAgentCreateRequestSubagent3p_ForbiddenFieldJSON proves the
// discriminated union's additionalProperties:false rejects a field this
// variant structurally does not carry. Built from raw JSON (not the Go
// struct — the field does not exist on AgentCreateRequestSubagent3p, so
// there is no way to set it via the typed struct at all) to exercise the
// schema's additionalProperties gate directly.
func FixtureAgentCreateRequestSubagent3p_ForbiddenFieldJSON() []byte {
	return []byte(`{
		"type": "subagent_3p",
		"name": "Bad 3p",
		"soul": "Valid soul content.",
		"executor": {"cli": "codex", "cli_path": "/usr/local/bin/codex"},
		"tools_cfg": {"builtin": {"policies": {}}}
	}`)
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
	allow := AgentUpdateRequestToolsCfgBuiltinPoliciesAllow
	heartbeat := "Check queue every hour."
	soul := "You are a helpful assistant."
	voice := "alloy"
	updatedAt := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)

	vDefault := true
	return AgentUpdateRequest{
		Name:        &name,
		Description: &description,
		Model:       &model,
		Color:       &color,
		Icon:        &icon,
		Default:     &vDefault,
		Soul:        &soul,
		Heartbeat:   &heartbeat,
		Voice:       &voice,
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
				Policies map[string]AgentUpdateRequestToolsCfgBuiltinPolicies `json:"policies"`
			} `json:"builtin,omitempty"`
			Mcp *struct {
				Servers *[]struct {
					Id    string    `json:"id"`
					Tools *[]string `json:"tools,omitempty"`
				} `json:"servers,omitempty"`
			} `json:"mcp,omitempty"`
		}{
			Builtin: &struct {
				Policies map[string]AgentUpdateRequestToolsCfgBuiltinPolicies `json:"policies"`
			}{
				Policies: map[string]AgentUpdateRequestToolsCfgBuiltinPolicies{
					"bash": allow,
				},
			},
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

// FixtureChannelRouting_Bound returns a fully-bound ChannelRouting (F-12/FR-026):
// both workspace_id and default_agent_id are set, as returned by getChannelRouting
// for a bound instance (ADR-029 MAJ-004 / FR-029).
func FixtureChannelRouting_Bound() ChannelRouting {
	id := "agent-a"
	wsID := "my-workspace"
	return ChannelRouting{
		WorkspaceId:    &wsID,
		DefaultAgentId: &id,
	}
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
		Cli:     ExternalCliToolClaudeCode,
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

// ── TaskOccurrenceSet ─────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/TaskOccurrenceSet.yaml,
// contracts/components/schemas/DayBucket.yaml
// Regression guard: occurrences_ms/day_buckets are both `required`,
// non-nullable array fields (TaskOccurrenceSet.yaml) with no `omitempty` on
// the generated struct — a bare nil Go slice marshals to JSON null, which
// fails schema validation (see
// TestContract_TaskOccurrenceSet_NilOccurrencesMsRejected /
// TestContract_TaskOccurrenceSet_NilDayBucketsRejected below). Every fixture
// here is deliberately built with a literal `[]int64{...}` / composite
// literal rather than a nil var, so each constructor is itself schema-valid
// — mirroring the fix in pkg/gateway/task_occurrences.go's
// buildOneOccurrenceSet, which normalizes both fields to non-nil before
// constructing the real response.

// FixtureTaskOccurrenceSet_Populated — both occurrences_ms and day_buckets
// non-empty: a legal wire shape per the schema (occurrences_ms carries the
// <=3/day raw days, day_buckets the >3/day aggregated days of the SAME
// task's response).
func FixtureTaskOccurrenceSet_Populated() TaskOccurrenceSet {
	interval := int64(1800000)
	return TaskOccurrenceSet{
		TaskId:        "550e8400-e29b-41d4-a716-446655440000",
		OccurrencesMs: []int64{1784620800000, 1785225600000},
		DayBuckets: []struct {
			Count      int32  `json:"count"`
			DayEndMs   int64  `json:"day_end_ms"`
			DayStartMs int64  `json:"day_start_ms"`
			FirstMs    int64  `json:"first_ms"`
			IntervalMs *int64 `json:"interval_ms"`
			RunCounts  *struct {
				Done       int32 `json:"done"`
				Failed     int32 `json:"failed"`
				InProgress int32 `json:"in_progress"`
				Scheduled  int32 `json:"scheduled"`
				Skipped    int32 `json:"skipped"`
			} `json:"run_counts,omitempty"`
		}{
			{Count: 48, DayEndMs: 1784678400000, DayStartMs: 1784592000000, FirstMs: 1784620800000, IntervalMs: &interval},
		},
		Truncated: false,
	}
}

// FixtureTaskOccurrenceSet_BucketsOnly — the "dense overview" edge shape:
// day_buckets populated, occurrences_ms empty-but-NON-NIL (the
// buildOverview "every day is dense enough to bucket" case — was the
// occurrences_ms:null bug's exact shape before the fix).
func FixtureTaskOccurrenceSet_BucketsOnly() TaskOccurrenceSet {
	interval := int64(60000)
	return TaskOccurrenceSet{
		TaskId:        "550e8400-e29b-41d4-a716-446655440001",
		OccurrencesMs: []int64{},
		DayBuckets: []struct {
			Count      int32  `json:"count"`
			DayEndMs   int64  `json:"day_end_ms"`
			DayStartMs int64  `json:"day_start_ms"`
			FirstMs    int64  `json:"first_ms"`
			IntervalMs *int64 `json:"interval_ms"`
			RunCounts  *struct {
				Done       int32 `json:"done"`
				Failed     int32 `json:"failed"`
				InProgress int32 `json:"in_progress"`
				Scheduled  int32 `json:"scheduled"`
				Skipped    int32 `json:"skipped"`
			} `json:"run_counts,omitempty"`
		}{
			{Count: 1440, DayEndMs: 1784678400000, DayStartMs: 1784592000000, FirstMs: 1784592000000, IntervalMs: &interval},
		},
		Truncated: false,
	}
}

// FixtureTaskOccurrenceSet_OccurrencesOnly — the "detail mode" edge shape:
// occurrences_ms populated, day_buckets empty-but-NON-NIL (detail mode never
// buckets — was the day_buckets:null bug's exact shape before the fix).
func FixtureTaskOccurrenceSet_OccurrencesOnly() TaskOccurrenceSet {
	return TaskOccurrenceSet{
		TaskId:        "550e8400-e29b-41d4-a716-446655440002",
		OccurrencesMs: []int64{1784620800000, 1784624400000, 1784628000000},
		DayBuckets: []struct {
			Count      int32  `json:"count"`
			DayEndMs   int64  `json:"day_end_ms"`
			DayStartMs int64  `json:"day_start_ms"`
			FirstMs    int64  `json:"first_ms"`
			IntervalMs *int64 `json:"interval_ms"`
			RunCounts  *struct {
				Done       int32 `json:"done"`
				Failed     int32 `json:"failed"`
				InProgress int32 `json:"in_progress"`
				Scheduled  int32 `json:"scheduled"`
				Skipped    int32 `json:"skipped"`
			} `json:"run_counts,omitempty"`
		}{},
		Truncated: false,
	}
}

// FixtureTaskOccurrenceSet_WithRunOverlay — the ADR-050 run-overlay shape
// (task-run-history-spec.md §3.7): occurrence_runs populated for the
// individual instants AND day_buckets[].run_counts populated for the
// aggregated day, exercising both additive overlay fields in the SAME
// response (previously ZERO coverage — M2/H5).
func FixtureTaskOccurrenceSet_WithRunOverlay() TaskOccurrenceSet {
	interval := int64(1800000)
	return TaskOccurrenceSet{
		TaskId:        "550e8400-e29b-41d4-a716-446655440003",
		OccurrencesMs: []int64{1784620800000, 1784624400000},
		DayBuckets: []struct {
			Count      int32  `json:"count"`
			DayEndMs   int64  `json:"day_end_ms"`
			DayStartMs int64  `json:"day_start_ms"`
			FirstMs    int64  `json:"first_ms"`
			IntervalMs *int64 `json:"interval_ms"`
			RunCounts  *struct {
				Done       int32 `json:"done"`
				Failed     int32 `json:"failed"`
				InProgress int32 `json:"in_progress"`
				Scheduled  int32 `json:"scheduled"`
				Skipped    int32 `json:"skipped"`
			} `json:"run_counts,omitempty"`
		}{
			{
				Count: 48, DayEndMs: 1784678400000, DayStartMs: 1784592000000, FirstMs: 1784620800000, IntervalMs: &interval,
				RunCounts: &struct {
					Done       int32 `json:"done"`
					Failed     int32 `json:"failed"`
					InProgress int32 `json:"in_progress"`
					Scheduled  int32 `json:"scheduled"`
					Skipped    int32 `json:"skipped"`
				}{Done: 12, Failed: 2, InProgress: 0, Scheduled: 25, Skipped: 1},
			},
		},
		OccurrenceRuns: &[]struct {
			HasResult    bool                                  `json:"has_result"`
			OccurrenceMs int64                                 `json:"occurrence_ms"`
			RunId        string                                `json:"run_id"`
			SessionId    string                                `json:"session_id"`
			Status       TaskOccurrenceSetOccurrenceRunsStatus `json:"status"`
		}{
			{HasResult: true, OccurrenceMs: 1784620800000, RunId: "01J8Z3K2R9G4S6M0N1P2Q3R4S5", SessionId: "session-uuid-1", Status: "done"},
			{HasResult: false, OccurrenceMs: 1784624400000, RunId: "01J8Z3K2R9G4S6M0N1P2Q3R4S6", SessionId: "session-uuid-2", Status: "failed"},
		},
		Truncated: false,
	}
}

// ── TaskRun ──────────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/TaskRun.yaml (ADR-050 / task-run-
// history-spec.md §2.1). Previously ZERO contract coverage (M2/H5).

// FixtureTaskRun_Populated — a closed (done) scheduled recurring-occurrence
// run: every field set, including the terminal result and ended_at.
func FixtureTaskRun_Populated() TaskRun {
	occMs := int64(1784620800000)
	result := "Found 3 anomalies in the gateway logs."
	endedAt := time.Date(2026, 7, 20, 9, 5, 30, 0, time.UTC)
	return TaskRun{
		RunId:        "01J8Z3K2R9G4S6M0N1P2Q3R4S5",
		TaskId:       "550e8400-e29b-41d4-a716-446655440000",
		OccurrenceMs: &occMs,
		Status:       TaskRunStatus("done"),
		Result:       &result,
		SessionId:    "session-uuid-1",
		Kind:         TaskRunKind("scheduled"),
		StartedAt:    time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
		EndedAt:      &endedAt,
	}
}

// FixtureTaskRun_ZeroValue — Go zero values. Expected to FAIL schema
// validation: kind="" and status="" are not members of their respective
// enums ([scheduled, manual] / [in_progress, done, failed]).
func FixtureTaskRun_ZeroValue() TaskRun {
	return TaskRun{}
}

// FixtureTaskRun_Edge — a closed (failed) MANUAL ad-hoc run at the
// epoch-ms boundary: occurrence_ms=0 (Unix epoch, a legal minimal int64
// value — distinct from Populated's large value), ended_at equal to
// started_at (zero-duration run), unicode result text.
//
// NOTE: this fixture deliberately does NOT set occurrence_ms/ended_at to an
// actual nil (JSON null), even though TaskRun.yaml declares both
// `required` + `nullable: true` and Go's *int64/*time.Time nil is the
// documented "ad-hoc run" / "still in_progress" wire shape (spec §2.1). The
// standalone-file jsonschema/v6 compiler wired up in contract_test.go's
// initSchemas() (plain jsonschema.NewCompiler(), no OpenAPI dialect) does
// not implement the OpenAPI `nullable` extension keyword, so a literal null
// on a `required` field currently fails validation here even though it is
// spec-legal — a pre-existing gap in the shared test harness (also latent,
// unexercised, on DayBucket.interval_ms), outside this fixture's/this
// task's file scope (contracts/components/schemas/*.yaml, the jsonschema
// loader setup) to fix. Flagged for follow-up rather than silently masked.
func FixtureTaskRun_Edge() TaskRun {
	occMs := int64(0)
	result := "失败：远程服务在 30 秒后超时 (⚠ retry exhausted)" //nolint:gosmopolitan // intentional non-ASCII result text — this fixture deliberately exercises unicode rendering (see doc comment above)
	startedAt := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	endedAt := startedAt
	return TaskRun{
		RunId:        "01J8Z3K2R9G4S6M0N1P2Q3R4S6",
		TaskId:       "550e8400-e29b-41d4-a716-446655440001",
		OccurrenceMs: &occMs,
		Status:       TaskRunStatus("failed"),
		Result:       &result,
		SessionId:    "session-uuid-2",
		Kind:         TaskRunKind("manual"),
		StartedAt:    startedAt,
		EndedAt:      &endedAt,
	}
}

// FixtureTaskRun_Skipped — a closed (skipped) scheduled recurring-occurrence
// run: the overlap guard found the previous occurrence still in_progress, so
// this occurrence never actually ran. Recorded as a zero-duration bookkeeping
// record (started_at == ended_at) so the calendar overlay has something to
// join against instead of silently omitting the occurrence. Matches what
// Store.RecordSkippedOccurrence (pkg/task/run_store.go) actually writes: no
// session ever ran for a skip, so session_id is always "" (never a synthetic
// value), and no result is ever set (a skip is a pure bookkeeping close, not
// an execution outcome) — this fixture deliberately does NOT set Result,
// unlike its sibling Populated/Edge builders which represent real executions.
func FixtureTaskRun_Skipped() TaskRun {
	occMs := int64(1784624400000)
	skippedAt := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	return TaskRun{
		RunId:        "01J8Z3K2R9G4S6M0N1P2Q3R4S7",
		TaskId:       "550e8400-e29b-41d4-a716-446655440000",
		OccurrenceMs: &occMs,
		Status:       TaskRunStatus("skipped"),
		SessionId:    "",
		Kind:         TaskRunKind("scheduled"),
		StartedAt:    skippedAt,
		EndedAt:      &skippedAt,
	}
}

// ── RunNowRequest ────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/RunNowRequest.yaml (ADR-050 RD7).
// Previously ZERO contract coverage (M2/H5).

// FixtureRunNowRequest_Populated — Run-now for a specific recurring
// occurrence (materialize-on-demand).
func FixtureRunNowRequest_Populated() RunNowRequest {
	occMs := int64(1784620800000)
	return RunNowRequest{OccurrenceMs: &occMs}
}

// FixtureRunNowRequest_Empty — the empty body: occurrence_ms omitted. This is
// NOT a failure case (unlike other _ZeroValue fixtures) — the whole request
// body is optional and an empty body legitimately means "re-run a
// normal/once task as a fresh run" per RunNowRequest.yaml.
func FixtureRunNowRequest_Empty() RunNowRequest {
	return RunNowRequest{}
}

// ── TaskRunStatusFrame ───────────────────────────────────────────────────────
// Traces to: contracts/asyncapi.yaml components.schemas.TaskRunStatusFrame
// (ADR-050 §3.8). Previously ZERO contract coverage (M2/H5). Also the
// regression-fixture pair for the AsyncAPI int64 codegen drift fix
// (scripts/gen-asyncapi-go/main.go): OccurrenceMs is now *int64, not *int —
// see TestContract_TaskRunStatusFrame_OccurrenceMsIsInt64Type in
// contract_test.go.

// FixtureTaskRunStatusFrame_Populated — a closed scheduled-occurrence run.
func FixtureTaskRunStatusFrame_Populated() TaskRunStatusFrame {
	occMs := int64(1784620800000)
	return TaskRunStatusFrame{
		Type:         "task_run_status",
		TaskId:       "550e8400-e29b-41d4-a716-446655440000",
		RunId:        "01J8Z3K2R9G4S6M0N1P2Q3R4S5",
		OccurrenceMs: &occMs,
		Status:       "done",
	}
}

// FixtureTaskRunStatusFrame_ZeroValue — Go zero values.
func FixtureTaskRunStatusFrame_ZeroValue() TaskRunStatusFrame {
	return TaskRunStatusFrame{}
}

// FixtureTaskRunStatusFrame_ManualRun — occurrence_ms omitted: the legal
// ad-hoc/once/manual-run shape (occurrence_ms is optional in this frame,
// unlike TaskRun.occurrence_ms which is required-but-nullable).
func FixtureTaskRunStatusFrame_ManualRun() TaskRunStatusFrame {
	return TaskRunStatusFrame{
		Type:   "task_run_status",
		TaskId: "550e8400-e29b-41d4-a716-446655440001",
		RunId:  "01J8Z3K2R9G4S6M0N1P2Q3R4S6",
		Status: "in_progress",
	}
}

// ── AuditEntry ───────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/AuditEntry.yaml. Previously ZERO
// contract coverage (test-coverage gate on fix/uat-v0.1.1-defects, GAP 2).

// FixtureAuditEntry_Populated — a tool_call event with every optional field
// set (the "full shape" a security_setting_change-adjacent record can carry:
// actor/resource/old_value/new_value alongside the tool-call fields).
func FixtureAuditEntry_Populated() AuditEntry {
	actor := "admin"
	agentID := "jim"
	command := "ls -la /tmp"
	decision := AuditEntryDecision("allow")
	sessionID := "sess_abc123"
	tool := "bash"
	policyRule := "exec: allow"
	resource := "gateway.god_mode"
	userID := "cli"
	return AuditEntry{
		Timestamp:  time.Date(2026, 5, 16, 10, 30, 0, 0, time.UTC),
		Event:      "tool_call",
		Decision:   &decision,
		Actor:      &actor,
		AgentId:    &agentID,
		Command:    &command,
		SessionId:  &sessionID,
		Tool:       &tool,
		PolicyRule: &policyRule,
		Resource:   &resource,
		User:       &userID,
		Parameters: &map[string]any{"command": "ls -la /tmp"},
		Details:    &map[string]any{"exit_code": float64(0)},
		OldValue:   &map[string]any{"sandbox.god_mode": false},
		NewValue:   &map[string]any{"sandbox.god_mode": true},
	}
}

// FixtureAuditEntry_ZeroValue — Go zero values. Expected to FAIL: event=""
// does not match the required pattern ^[a-z_]+$ (one-or-more lowercase
// letters/underscores — the empty string has zero characters).
func FixtureAuditEntry_ZeroValue() AuditEntry {
	return AuditEntry{}
}

// FixtureAuditEntry_Edge — the minimal legal shape: only the two required
// fields (timestamp, event) set, every optional field absent. A routine
// startup/shutdown record looks exactly like this.
func FixtureAuditEntry_Edge() AuditEntry {
	return AuditEntry{
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Event:     "startup",
	}
}

// ── GodModeStatus ────────────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/GodModeStatus.yaml. Previously
// ZERO contract coverage (test-coverage gate on fix/uat-v0.1.1-defects, GAP 2).

// FixtureGodModeStatus_Populated — god mode fully armed and active: build
// supports it, this boot authorized it, and it is currently switched on.
func FixtureGodModeStatus_Populated() GodModeStatus {
	return GodModeStatus{
		Enabled:   true,
		Available: true,
		Supported: true,
		Persisted: true,
	}
}

// FixtureGodModeStatus_ZeroValue — Go zero values (all four fields false).
// UNLIKE most other _ZeroValue fixtures in this file, this is a LEGAL,
// schema-VALID state, not a failure case: all four fields are plain
// required booleans with no additional constraint, and false/false/false/
// false is exactly the real "never armed, build supports it but this
// install never opted in" fresh-install state GodModeStatus.yaml's own
// field docs describe. Do not mistake omission-testing convention for a
// universal "zero value must fail" rule — mustPassComponent is correct here.
func FixtureGodModeStatus_ZeroValue() GodModeStatus {
	return GodModeStatus{}
}

// FixtureGodModeStatus_Edge — the "armed via the UI, pending restart" state
// called out explicitly in persisted's field doc: persisted=true (operator
// just enabled it) but available=false (authorization is boot-frozen, so it
// hasn't taken effect yet) and enabled=false (never active without
// availability). Distinguishes this fixture's JSON from Populated's — every
// field flips relative to Populated except Supported.
func FixtureGodModeStatus_Edge() GodModeStatus {
	return GodModeStatus{
		Enabled:   false,
		Available: false,
		Supported: true,
		Persisted: true,
	}
}
