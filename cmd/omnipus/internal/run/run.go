//go:build goolm && stdjson

// Package run is the one-shot execute client for the minimized CLI. It connects
// to a running local Omnipus gateway over WebSocket, sends a user message, streams
// the response to stdout, and resolves tool-approval requests via REST before
// returning when the done frame is received.
//
// Design (from cli-minimization-spec.md, FR-001..005, FR-007, FR-014, FR-019):
//   - WS-to-observe: the client connects to /api/v1/chat/ws, authenticates, and
//     watches the frame stream for session_started, token, tool_call_start/result,
//     tool_approval_required, done, and error frames.
//   - REST-to-resolve: tool_approval_required frames are resolved by posting to
//     POST /api/v1/tool-approvals/{approval_id} — NOT via the legacy
//     exec_approval_response WS frame which goes through a different registry and
//     causes a ~90 s hang.
//
// This package does NOT read config.json, does NOT discover the CLI token — both
// are injected by the caller via Options.
package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// Error sentinels returned by Run so the cmd layer can set a specific exit code
// and print the correct user-facing message without string inspection.
var (
	// ErrRemoteUnsupported is returned when Options.URL is non-empty. Remote
	// gateway support is deferred beyond P0 (FR-007).
	ErrRemoteUnsupported = errors.New("remote gateways are not supported yet")

	// ErrGatewayDown is returned when the TCP dial to the local gateway fails
	// (connection refused). The caller prints the "start it with: omnipus start"
	// hint (FR-014).
	ErrGatewayDown = errors.New("Omnipus isn't running — start it with: omnipus start")

	// ErrKeyInvalid is returned when the WebSocket connection is established but
	// the server closes it with ClosePolicyViolation after the auth frame. The
	// caller prints the "run: omnipus start" rotation hint (FR-019).
	ErrKeyInvalid = errors.New("Your CLI key is invalid or out of date. Run: omnipus start")

	// ErrTimeout is returned when no done frame is received within Options.Timeout.
	ErrTimeout = errors.New("timed out waiting for the agent to finish")
)

const defaultTimeout = 300 * time.Second

// Options configures a single one-shot run against a local gateway.
// The caller is responsible for resolving Addr and Token before calling Run.
type Options struct {
	// Agent is the agent ID to send the message to. Required.
	Agent string

	// Prompt is the user message to send. Required.
	Prompt string

	// Model is an optional per-turn model override. When non-empty it is sent
	// as metadata.model_name in the message frame. When empty metadata is
	// omitted entirely (US-2).
	Model string

	// URL, when non-empty, causes Run to return ErrRemoteUnsupported immediately
	// (FR-007 P0 guard — remote gateways not supported yet).
	URL string

	// Yes, when true, causes tool_approval_required frames to be resolved with
	// action="approve" instead of the default "deny" (US-4/AC-2).
	Yes bool

	// Timeout is the maximum time to wait for the done frame after connecting.
	// Zero means 300 s (A2 default).
	Timeout time.Duration

	// Addr is the host:port of the local gateway (e.g. "127.0.0.1:5000").
	// Injected by the caller; Run constructs the WS and HTTP URLs from it.
	Addr string

	// Token is the CLI bearer token. Injected by the caller from cli.token.
	// Run sends it as the first WS auth frame and as a Bearer header on REST calls.
	Token string

	// Stdout receives the streamed LLM response (token frames + final newline).
	Stdout io.Writer

	// Stderr receives progress, tool activity, and error diagnostics.
	Stderr io.Writer
}

// Run executes a single one-shot chat turn against the local Omnipus gateway.
//
// It returns nil after a clean done frame (process should exit 0).
// It returns a typed sentinel error on any failure so the cmd layer can map to
// the correct exit code and user-facing message without string inspection.
func Run(ctx context.Context, o Options) error {
	// FR-007 P0 guard: remote gateway support is deferred.
	if o.URL != "" {
		return ErrRemoteUnsupported
	}

	timeout := o.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	wsURL := "ws://" + o.Addr + "/api/v1/chat/ws"
	httpBase := "http://" + o.Addr

	// Dial the WebSocket. A connection-refused error maps to ErrGatewayDown;
	// any other dial error is also treated as gateway-down since from the user's
	// perspective the gateway is unreachable.
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, wsResp, err := dialer.DialContext(ctx, wsURL, nil)
	if wsResp != nil {
		defer wsResp.Body.Close()
	}
	if err != nil {
		slog.Debug("run: WS dial failed", "addr", o.Addr, "error", err)
		return ErrGatewayDown
	}
	defer conn.Close()

	// The first frame MUST be the auth frame (FR-019 / authenticateWS contract).
	authFrame := generated.AuthFrame{
		Type:  string(generated.WsFrameTypeAuth),
		Token: o.Token,
	}
	authData, marshalErr := json.Marshal(authFrame)
	if marshalErr != nil {
		return fmt.Errorf("run: marshal auth frame: %w", marshalErr)
	}
	if writeErr := conn.WriteMessage(websocket.TextMessage, authData); writeErr != nil {
		return fmt.Errorf("run: write auth frame: %w", writeErr)
	}

	// Build the message frame. metadata is only included when a model override is
	// requested (US-2 AC-2: omit entirely when no --model).
	msgFrame := generated.MessageFrame{
		Type:    string(generated.WsFrameTypeMessage),
		Content: o.Prompt,
	}
	if o.Agent != "" {
		msgFrame.AgentId = &o.Agent
	}
	if o.Model != "" {
		msgFrame.Metadata = map[string]any{
			"model_name": o.Model,
		}
	}
	msgData, marshalErr := json.Marshal(msgFrame)
	if marshalErr != nil {
		return fmt.Errorf("run: marshal message frame: %w", marshalErr)
	}

	// send is a single-writer helper that serializes frame writes on this
	// goroutine. The gorilla/websocket library requires all writes from the same
	// goroutine. Since approvals are resolved via HTTP (not WS) we never need
	// concurrent writes, so this constraint is trivially satisfied.
	send := func(data []byte) error {
		return conn.WriteMessage(websocket.TextMessage, data)
	}

	// Set a per-read deadline that gets refreshed on each frame so a stalled
	// server is detected. The outer context timeout enforces the overall wall-clock
	// limit.
	refreshRead := func() {
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	}

	// Send the message frame only after auth is accepted (the server will close
	// the connection if auth fails — we detect that in the read loop below).
	// We optimistically send the message here; if auth fails we'll get a close
	// frame before the session_started arrives and the read loop will return
	// ErrKeyInvalid.
	if writeErr := send(msgData); writeErr != nil {
		return fmt.Errorf("run: write message frame: %w", writeErr)
	}

	// Read loop: receive frames until done or error.
	refreshRead()
	for {
		// Check context before each read.
		select {
		case <-ctx.Done():
			return ErrTimeout
		default:
		}

		_, raw, readErr := conn.ReadMessage()
		if readErr != nil {
			// Distinguish auth-rejection close (ClosePolicyViolation = 1008) from
			// other errors. gorilla/websocket wraps close frames as *CloseError.
			var closeErr *websocket.CloseError
			if errors.As(readErr, &closeErr) && closeErr.Code == websocket.ClosePolicyViolation {
				return ErrKeyInvalid
			}
			// Any other read error after we've already sent the message is an
			// unexpected disconnect.
			return fmt.Errorf("run: connection closed unexpectedly: %w", readErr)
		}
		refreshRead()

		// Decode just the type discriminator first, then re-decode into the
		// specific struct. This avoids importing a full JSON schema dispatcher.
		var envelope struct {
			Type string `json:"type"`
		}
		if jsonErr := json.Unmarshal(raw, &envelope); jsonErr != nil {
			slog.Warn("run: ignoring malformed frame", "error", jsonErr)
			continue
		}

		switch generated.WsFrameType(envelope.Type) {
		case generated.WsFrameTypeSessionStarted:
			// The server has minted a session — the turn has started.
			var f generated.SessionStartedFrame
			if jsonErr := json.Unmarshal(raw, &f); jsonErr == nil {
				fmt.Fprintf(o.Stderr, "[session %s]\n", f.SessionId)
			}

		case generated.WsFrameTypeToken:
			// LLM token: stream to stdout (US-3 AC-1 — stdout = result text only).
			var f generated.TokenFrame
			if jsonErr := json.Unmarshal(raw, &f); jsonErr == nil {
				fmt.Fprint(o.Stdout, f.Content)
			}

		case generated.WsFrameTypeToolCallStart:
			// Tool started: report to stderr (US-3 AC-1).
			var f generated.ToolCallStartFrame
			if jsonErr := json.Unmarshal(raw, &f); jsonErr == nil {
				fmt.Fprintf(o.Stderr, "[tool: %s]\n", f.Tool)
			}

		case generated.WsFrameTypeToolCallResult:
			// Tool finished: report status to stderr.
			var f generated.ToolCallResultFrame
			if jsonErr := json.Unmarshal(raw, &f); jsonErr == nil {
				if f.Error != nil {
					fmt.Fprintf(o.Stderr, "[tool: %s error: %s]\n", f.Tool, *f.Error)
				} else {
					fmt.Fprintf(o.Stderr, "[tool: %s done]\n", f.Tool)
				}
			}

		case generated.WsFrameTypeToolApprovalRequired:
			// FR-005 / US-4: resolve via REST, not via the legacy WS frame.
			// The WS exec_approval_response hits a different registry and hangs 90 s.
			var f generated.ToolApprovalRequiredFrame
			if jsonErr := json.Unmarshal(raw, &f); jsonErr != nil {
				slog.Warn("run: malformed tool_approval_required frame", "error", jsonErr)
				continue
			}
			action := generated.ToolApprovalActionRequestActionDeny
			if o.Yes {
				action = generated.ToolApprovalActionRequestActionApprove
			}
			resolveErr := resolveApproval(ctx, httpBase, o.Token, f.ApprovalId, action)
			if resolveErr != nil {
				// Log but do not abort — the engine will time out the approval on its
				// own and the run will still proceed to done.
				fmt.Fprintf(o.Stderr, "[approval: failed to resolve %s: %v]\n", f.ApprovalId, resolveErr)
			} else if action == generated.ToolApprovalActionRequestActionDeny {
				fmt.Fprintf(o.Stderr, "denied tool: %s\n", f.ToolName)
			}

		case generated.WsFrameTypeDone:
			// Terminal frame: the turn is complete. Write a trailing newline to
			// stdout so the shell prompt starts on its own line (A1).
			fmt.Fprintln(o.Stdout)
			return nil

		case generated.WsFrameTypeError:
			// Server-side error: report to stderr and return a non-zero error
			// (US-3 AC-2).
			var f generated.ErrorFrame
			if jsonErr := json.Unmarshal(raw, &f); jsonErr == nil {
				fmt.Fprintf(o.Stderr, "error: %s\n", f.Message)
				return fmt.Errorf("run: agent error: %s", f.Message)
			}
			return fmt.Errorf("run: agent error (malformed error frame)")

		case generated.WsFrameTypeSubagentStart,
			generated.WsFrameTypeSubagentEnd,
			generated.WsFrameTypeAgentSwitched,
			generated.WsFrameTypeTaskStatusChanged,
			generated.WsFrameTypeRateLimit,
			generated.WsFrameTypeSystemOverload,
			generated.WsFrameTypeCancelStage,
			generated.WsFrameTypePong:
			// Progress frames: no stdout output (US-3 AC-1); progress to stderr
			// for the subagent/overload cases.
			fmt.Fprintf(o.Stderr, "[%s]\n", envelope.Type)

		default:
			// Unrecognized frame type (replay, pairing, etc.): silently ignore so
			// future frame additions don't break existing CLI binaries.
			slog.Debug("run: ignoring unrecognized frame", "type", envelope.Type)
		}
	}
}

// resolveApproval posts a tool-approval action to the gateway's REST endpoint.
// This is the correct resolution path for approvalRegistryV2 approvals —
// the WS exec_approval_response frame targets a different (legacy) registry and
// will cause a ~90 s hang if used here.
//
// Route: POST /api/v1/tool-approvals/{approval_id}
// Body:  {"action":"approve"|"deny"|"cancel"}
// Auth:  Bearer <token>
func resolveApproval(
	ctx context.Context,
	httpBase, token, approvalID string,
	action generated.ToolApprovalActionRequestAction,
) error {
	// Construct the URL. Strip any trailing slash from httpBase for cleanliness.
	base := strings.TrimRight(httpBase, "/")
	url := base + "/api/v1/tool-approvals/" + approvalID

	body := generated.ToolApprovalActionRequest{Action: action}
	bodyData, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return fmt.Errorf("marshal approval request: %w", marshalErr)
	}

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyData))
	if reqErr != nil {
		return fmt.Errorf("build request: %w", reqErr)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, doErr := http.DefaultClient.Do(req)
	if doErr != nil {
		return fmt.Errorf("post: %w", doErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}
