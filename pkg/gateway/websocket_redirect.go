package gateway

// websocket_redirect.go — the RedirectFrame WS branch (ADR-20260928 D9 row 2
// via the architect's corrected transport ruling §3.1): the transport for the
// typed /stop-redirect command, mirroring the /cancel CancelFrame pattern.
// The frame NEVER passes through chat intake — mid-stream text would only
// enter the steering queue unparsed — it is handled here directly.
//
// Runtime checks a JSON Schema cannot express (and that still apply when
// gateway.validate_inbound is off), per D9/seam ruling §3.2 — every failure
// is VISIBLE as an error frame, never silent:
//   - the connection is authenticated (non-empty user principal);
//   - the frame names the session THIS connection is bound to: the target's
//     session_id must equal wc.boundHub.id (read under WSHandler.mu) —
//     wh.chatID is the transport's random webchat UUID, never a session id,
//     so it is never compared against;
//   - the target belongs to the signed-in owner: the persisted session meta's
//     Owner must equal wc.userID — a missing record, an unreadable store and
//     an EMPTY owner ("unowned" never means "anyone may redirect") all refuse
//     fail-closed;
//   - the target session positively resolves to a helper (SteeredBy edge);
//     anything else — including an unreadable record — is refused with
//     helper-targeting guidance and nothing is issued (fail-closed);
//   - the instruction is nonblank after the authoritative Unicode-aware
//     trim (schema pattern '\S' is only an ASCII approximation), and within
//     the 16384-UTF-8-byte ceiling (exactly 16384 accepted, over refused).
//
// Raw store errors are logged server-side only; error frames carry fixed
// sentences, never a wrapped filesystem path. The binding is re-validated
// after the store loads so an unbind (or rebind) between authorization and
// the redirect call cannot slip a redirect through (TOCTOU).

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// redirectMaxInstructionBytes is the D2/T22 steer-body ceiling: 16 KiB of
// UTF-8 bytes. Exactly 16384 bytes are accepted, 16385 refused. JSON Schema
// maxLength counts characters, not bytes — this runtime check is the
// enforcement point (RedirectFrame.yaml's own description says so).
const redirectMaxInstructionBytes = 16384

// dispatchFrameOrRedirect routes `redirect` frames to handleRedirectFrame and
// everything else to dispatchFrame unchanged. It exists ONLY so
// wsHandlerReadLoop.dispatchFrame stays byte-for-byte at its ratchet-pinned
// gocyclo complexity (scripts/budgets/gocyclo.txt — new complexity cannot be
// added to an already-complex function), the same reasoning that moved
// handleCancelFrame out of dispatchFrame's body before it.
func (wh *wsHandlerReadLoop) dispatchFrameOrRedirect(data []byte, peek wsTypeOnly) wsHandlerReadLoopFlow {
	if peek.Type == string(generated.WsFrameTypeRedirect) {
		return wh.handleRedirectFrame(data)
	}
	return wh.dispatchFrame(data, peek)
}

// handleRedirectFrame is the frame dispatcher's entry point for a `redirect`
// frame: parse, authenticate, validate the instruction, verify the helper
// target, then call the redirect primitive directly (never chat intake).
func (wh *wsHandlerReadLoop) handleRedirectFrame(data []byte) wsHandlerReadLoopFlow {
	var f generated.RedirectFrame
	if err := json.Unmarshal(data, &f); err != nil {
		wh.wc.inboundDropped.Add(1)
		slog.Warn("ws: malformed redirect frame", "error", err, "chat_id", wh.chatID)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "malformed redirect frame",
		})
		return wsHandlerReadLoopContinue
	}

	// Authenticate the current user: a redirect is an owner action on a named
	// session; the readLoop's authenticated principal is the authority and a
	// connection without one must never move a session.
	if wh.wc.userID == "" {
		wh.wc.inboundDropped.Add(1)
		slog.Warn("ws: redirect frame on unauthenticated connection — refused", "chat_id", wh.chatID)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "redirect requires an authenticated user",
		})
		return wsHandlerReadLoopContinue
	}

	if f.SessionId == "" {
		wh.wc.inboundDropped.Add(1)
		slog.Warn("ws: redirect frame missing required session_id — dropping", "chat_id", wh.chatID)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "redirect requires session_id",
		})
		return wsHandlerReadLoopContinue
	}

	// Authoritative Unicode-aware blank check (NBSP, em space, … are blank);
	// the schema's pattern '\S' is an ASCII-only approximation.
	instruction := strings.TrimFunc(f.Instruction, unicode.IsSpace)
	if instruction == "" {
		wh.wc.inboundDropped.Add(1)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "/stop-redirect <instruction> — give the instruction to continue with; nothing was changed",
		})
		return wsHandlerReadLoopContinue
	}
	// The 16 KiB ceiling counts UTF-8 BYTES (len of a Go string is bytes).
	if len(instruction) > redirectMaxInstructionBytes {
		wh.wc.inboundDropped.Add(1)
		slog.Warn("ws: redirect instruction over the 16384-byte ceiling — refused",
			"chat_id", wh.chatID, "session_id", f.SessionId, "bytes", len(instruction))
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "redirect instruction exceeds the 16384-byte ceiling — shorten it and retry; nothing was changed",
		})
		return wsHandlerReadLoopContinue
	}

	// Conversation scope (D9: chat commands are conversation-scoped, #955):
	// the frame's session_id must name the session hub THIS connection is
	// bound to. wh.chatID is the transport's random webchat UUID, never a
	// session id (websocket.go::ServeHTTP) — the binding, read under
	// WSHandler.mu, is the authority. Re-checked below, after the store
	// loads, to close the bind/unbind race between authorization and the
	// redirect call.
	if !wh.h.connBoundToSession(wh.wc, f.SessionId) {
		wh.wc.inboundDropped.Add(1)
		slog.Warn("ws: redirect frame targets an unbound session — refused",
			"chat_id", wh.chatID, "session_id", f.SessionId)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "redirect works only in the helper session's own chat — open this helper session's chat and retry there; nothing was changed",
		})
		return wsHandlerReadLoopContinue
	}

	// Signed-in-owner gate (D9: authorized for the signed-in owner): the
	// target's persisted session meta must name exactly this connection's
	// authenticated principal. Missing meta, an unreadable store and an
	// EMPTY owner all refuse fail-closed — an "unowned" session never means
	// "anyone may redirect". Raw store errors are logged server-side only:
	// the error frame carries a fixed sentence, never a wrapped filesystem
	// path.
	sessionStore := wh.h.agentLoop.GetSessionStore()
	if sessionStore == nil {
		wh.wc.inboundDropped.Add(1)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "redirect could not verify the target session's ownership — nothing was changed",
		})
		return wsHandlerReadLoopContinue
	}
	meta, metaErr := sessionStore.GetMeta(f.SessionId)
	switch {
	case metaErr != nil:
		wh.wc.inboundDropped.Add(1)
		slog.Warn("ws: redirect target ownership could not be verified — refused (fail-closed)",
			"chat_id", wh.chatID, "session_id", f.SessionId, "error", metaErr.Error())
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "redirect could not verify the target session's ownership — nothing was changed",
		})
		return wsHandlerReadLoopContinue
	case meta == nil || meta.Owner != wh.wc.userID:
		// meta.Owner == "" (unowned) also lands here: it never equals the
		// authenticated principal, and an unowned session is redirectable by
		// nobody.
		wh.wc.inboundDropped.Add(1)
		slog.Warn("ws: redirect target is not owned by the signed-in user — refused",
			"chat_id", wh.chatID, "session_id", f.SessionId)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "redirect works only for the helper session's owner — the signed-in user does not own this session; nothing was changed",
		})
		return wsHandlerReadLoopContinue
	}

	// Helper target, fail-closed (seam ruling §3.2): only a positively
	// verified SteeredBy edge lets a redirect through; a root chat, a
	// missing record, or an unreadable store all refuse with guidance and
	// issue nothing.
	store := wh.h.agentLoop.GetSessionLifecycleStore()
	if store == nil {
		wh.wc.inboundDropped.Add(1)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "redirect could not verify the target session — no lifecycle store is available; nothing was changed",
		})
		return wsHandlerReadLoopContinue
	}
	rec, loadErr := store.Load(f.SessionId)
	switch {
	case loadErr == nil && rec != nil && rec.SteeredBy != nil:
		// Verified helper — proceed below.
	case errors.Is(loadErr, session.ErrLifecycleNotFound), loadErr == nil:
		wh.wc.inboundDropped.Add(1)
		slog.Warn("ws: redirect target is not a helper session — refused",
			"chat_id", wh.chatID, "session_id", f.SessionId)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "redirect works only in a helper session's chat — " + f.SessionId + " is not a steered helper session. Open the helper session and retry there; nothing was changed",
		})
		return wsHandlerReadLoopContinue
	default:
		// The raw error can carry a filesystem path — server log only; the
		// frame gets a fixed sentence.
		wh.wc.inboundDropped.Add(1)
		slog.Warn("ws: redirect target could not be verified — refused (fail-closed)",
			"chat_id", wh.chatID, "session_id", f.SessionId, "error", loadErr.Error())
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "redirect could not verify the target session — nothing was changed",
		})
		return wsHandlerReadLoopContinue
	}

	// TOCTOU close: the binding must still hold once both stores agree — an
	// unbind (or a rebind to a different session) between the checks above
	// and the primitive call refuses instead of redirecting.
	if !wh.h.connBoundToSession(wh.wc, f.SessionId) {
		wh.wc.inboundDropped.Add(1)
		slog.Warn("ws: redirect frame binding lost before dispatch — refused",
			"chat_id", wh.chatID, "session_id", f.SessionId)
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "redirect works only in the helper session's own chat — open this helper session's chat and retry there; nothing was changed",
		})
		return wsHandlerReadLoopContinue
	}

	// The redirect seam itself — the same primitive the /stop-redirect chat
	// command and the channel interception call; the frame text never passes
	// through chat intake. Until the D2 composition lands this returns the
	// visible ErrRedirectNotWired dependency, surfaced below — never a faked
	// success.
	if err := wh.h.agentLoop.RedirectSessionTurn(wh.ctx, f.SessionId, instruction, wh.wc.userID, "web"); err != nil {
		slog.Warn("ws: redirect failed", "chat_id", wh.chatID, "session_id", f.SessionId, "error", err.Error())
		sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "Redirect failed: " + err.Error(),
		})
		return wsHandlerReadLoopContinue
	}
	// Success: the redirect's own lifecycle transitions (stopped → resumed
	// with the instruction) reach the SPA through the existing session-state
	// frames — the same visibility a Stop press gets. No synthetic frame.
	return wsHandlerReadLoopNext
}
