// websocket_chat.go: Handle an inbound chat message frame (model-name stamping, workspace setup kickoff, transcript attachments).

package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/validation"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/google/uuid"
)

// parseSetupKickoffMetadata decides whether a MessageFrame's
// metadata.workspace_setup_kickoff (contracts/asyncapi.yaml) signals a
// workspace-setup kickoff, based on KEY PRESENCE rather than a loose type
// assertion.
//
// A naive `metadata["workspace_setup_kickoff"].(bool)` type assertion
// silently reads ANY non-bool value — the string "true", a JSON number, an
// object — as ok=false, which used to make setupKickoff=false and silently
// demote a malformed kickoff frame into an ordinary chat message: the
// synthetic interview instruction would then persist as a user-authored
// transcript entry with a content-derived title, instead of ever being
// recognized as an attempted (but malformed) kickoff.
//
// Returns (setupKickoff=false, malformed=false) when the key is absent
// entirely — an ordinary message, handled normally. Returns
// (setupKickoff=true, malformed=false) only when the key is present AND its
// value is the JSON boolean true. Returns (setupKickoff=false,
// malformed=true) for every other case where the key IS present — including
// an explicit `false` — signaling the caller to reject the frame outright
// (error frame, never processed as a normal message) rather than silently
// falling back to non-kickoff handling.
func parseSetupKickoffMetadata(metadata map[string]any) (setupKickoff bool, malformed bool) {
	raw, present := metadata["workspace_setup_kickoff"]
	if !present {
		return false, false
	}
	b, isBool := raw.(bool)
	if !isBool || !b {
		return false, true
	}
	return true, false
}

const (
	// kickoffConsumed means SetupPending was true and has now been cleared and
	// persisted — the caller should proceed with the kickoff turn.
	kickoffConsumed kickoffOutcome = iota
	// kickoffDuplicate means the workspace's SetupPending was already false (a
	// second kickoff raced or replayed) — the caller must reject the frame
	// outright (error frame, no session minted, nothing published to the bus).
	kickoffDuplicate
	// kickoffFailed means the workspace file could not be read, or the cleared
	// flag could not be persisted — the caller must reject the frame outright
	// (this used to "demote" to a normal message instead, silently
	// persisting the kickoff instruction as a fake user-authored transcript
	// entry). SetupPending is left untouched on disk in this case, so a later
	// workspace open can retry the kickoff.
	kickoffFailed
)

// wsHandlerHandleChatMessage carries the shared state of handleChatMessage across its stages.
type wsHandlerHandleChatMessage struct {
	h                   *WSHandler
	chatID              string
	frameSessionID      string
	content             string
	agentID             string
	mediaRefs           []string
	modelName           string
	workspaceID         string
	setupKickoff        bool
	clientMessageID     string
	wc                  *wsConn
	targetAgentID       string
	sessionID           string
	store               *session.UnifiedStore
	kickoffInstruction  string
	acceptedMedia       []string
	msg                 bus.InboundMessage
	transcriptPersisted bool
	admitted            bool
}

type pendingMessageStatus struct {
	clientMessageID string
	wc              *wsConn
}

// handleChatMessage mints a new session when frame.SessionID is empty, records
// every user message to the transcript, and publishes the message to the bus.
//
// modelName, when non-empty and non-whitespace, is forwarded to the agent loop
// as msg.Metadata["model_name"] so the per-turn switch (Phase 1, FR-010) routes
// THIS message to the chosen model instead of the agent's default. Whitespace-only
// or empty values are treated as absent — the agent falls back to its configured model.
//
// setupKickoff is true when the frame carries metadata.workspace_setup_kickoff
// (contracts/asyncapi.yaml) — the SPA sends this on a workspace's first open so
// Ava introduces herself and interviews the user about the workspace's purpose.
// When true (and workspaceID resolves to a real workspace), the trigger is
// recorded as a NEUTRAL system-role transcript entry ("Workspace setup
// started.", never the client-supplied instruction text verbatim — see
// consumeWorkspaceSetupKickoff's caller below) rather than a user bubble, the
// workspace's SetupPending flag is cleared exactly once under the per-workspace
// lock (workspace.LockID, idempotency-guarded), and the minted session is given
// the fixed title "Workspace setup" instead of a content-derived one.
//
// A kickoff frame is MINT-ONLY: any frame carrying setupKickoff=true AND a
// non-empty client-supplied session_id is rejected before the consume step
// (an arbitrary client would otherwise be able to burn the one-time flag
// against an unrelated EXISTING session it doesn't own). This makes the
// "existing session" branch below unreachable for a kickoff — it only ever
// runs for a normal message.
//
// The turn's driving prompt (msg.Content published to the bus) is built
// SERVER-SIDE from the workspace's own Name/Description (read during the
// consume) — the client-supplied `content` is IGNORED entirely for a kickoff
// frame. This closes a forensics gap (an arbitrary client instruction would
// otherwise silently drive Ava's first turn on a new workspace) and a
// stale-SPA/i18n drift risk (an older or translated client sending a
// different template). The persisted/replayed transcript entry stays the
// separate neutral "Workspace setup started." string regardless.
//
// ANY kickoff-flagged frame that cannot complete the kickoff — a
// non-empty session_id, an unresolved workspace_id, a duplicate (SetupPending
// already false), or a consume read/write failure — is REJECTED outright with
// an error frame: no session is minted, no transcript entry is written, and
// nothing is published to the bus. This replaces an earlier "demote to a
// normal message" behavior, which used to persist the synthetic kickoff
// instruction as a fake user-authored transcript entry and session title.
// Flag state is left untouched on a read/write failure (so a later workspace
// open can retry) and cleared only on the success path. Every frame-level
// validation (agent_id / session_id format) runs BEFORE the consume so a
// malformed frame can never burn the one-time flag with no way to recover it.
//
// If a downstream step fails AFTER a successful consume — minting the new
// session, persisting its title/owner/workspace stamp, or publishing to the
// bus — the consumed SetupPending flag is best-effort RESTORED to true (see
// restoreWorkspaceSetupPending) AND the just-minted session is deleted (see
// rollbackKickoffSession) so the one-time setup interview is not permanently
// lost, and a repeated failure does not accumulate orphan empty "Workspace
// setup" sessions. The workspace.setup_consumed audit entry is emitted only
// AFTER a successful publish — never before — so it never records a false
// positive for a turn that was actually rolled back.
//
// Two failure windows are accepted as out of scope for this in-process
// compensation (both are inherent to the file-based, no-distributed-
// transaction design and are not treated as bugs):
//
//  1. A process crash between the consume-persist (SetupPending written to
//     disk) and the bus publish loses the interview permanently — there is no
//     durable outbox to replay from across a restart, only the in-memory
//     restore/rollback path above, which cannot run if the process is gone.
//  2. The commit point for "the kickoff succeeded" is a successful publish,
//     not a successful TURN. If Ava's turn itself errors after the message
//     was accepted by the bus, the flag stays consumed and SetupPending
//     does not revert — recovery at that point is conversational (the
//     operator can just ask her to continue) rather than a retriggerable
//     kickoff.
func (h *WSHandler) handleChatMessage(
	ctx context.Context,
	chatID string,
	frameSessionID string,
	content string,
	agentID string,
	mediaRefs []string,
	modelName string,
	workspaceID string,
	setupKickoff bool,
	wc *wsConn,
) {
	h.handleChatMessageWithClientID(ctx, chatID, frameSessionID, content, agentID, mediaRefs, modelName, workspaceID, setupKickoff, "", wc)
}

// handleChatMessageWithClientID is the acknowledgement-aware message intake.
// The compatibility wrapper above keeps older callers and clients unchanged;
// a client_message_id opts a newer client into received/working/failed frames.
func (h *WSHandler) handleChatMessageWithClientID(
	ctx context.Context,
	chatID string,
	frameSessionID string,
	content string,
	agentID string,
	mediaRefs []string,
	modelName string,
	workspaceID string,
	setupKickoff bool,
	clientMessageID string,
	wc *wsConn,
) {
	hcm := &wsHandlerHandleChatMessage{h: h, chatID: chatID, frameSessionID: frameSessionID, content: content, agentID: agentID, mediaRefs: mediaRefs, modelName: modelName, workspaceID: workspaceID, setupKickoff: setupKickoff, clientMessageID: clientMessageID, wc: wc}
	defer func() {
		if !hcm.admitted {
			hcm.sendMessageStatus("failed")
		}
	}()

	if hcm.resolveTargetAgent() {
		return
	}

	if hcm.validateFrameIDs() {
		return
	}

	if hcm.resolveSessionStore() {
		return
	}

	hcm.collectAcceptedMedia()

	if hcm.recordSessionAndTranscript() {
		return
	}
	if hcm.transcriptPersisted {
		hcm.sendMessageStatus("received")
	}

	hcm.buildInboundMessage()
	hcm.queueWorkingStatus()
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := hcm.h.msgBus.PublishInbound(pubCtx, hcm.msg); err != nil {
		hcm.removeQueuedWorkingStatus()
		slog.Warn("ws: failed to publish message", "error", err)
		// Same compensation as the earlier session-mint/SetMeta failures — a
		// successful kickoff consume must not be silently lost just because
		// the bus publish that was supposed to drive Ava's turn failed. Also
		// deletes the just-minted "Workspace setup" session so a repeated
		// failure does not accumulate orphan empty sessions.
		if hcm.setupKickoff {
			hcm.h.rollbackKickoffSession(hcm.store, hcm.workspaceID, hcm.sessionID, hcm.chatID)
		}
		sidCopy := hcm.sessionID
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			Message:   fmt.Sprintf("failed to deliver message: %v", err),
			SessionId: &sidCopy,
		})
		return
	}
	hcm.admitted = true
	hcm.markWorkingIfTurnAlreadyActive()

	// Audit the kickoff consume only AFTER a successful publish — the turn is
	// now genuinely running (the commit point; see the two accepted-tradeoff
	// notes in the doc comment above). Emitting this before publish (the
	// previous placement) produced a false "consumed" audit entry even on a
	// publish failure that had just restored the flag and rolled back the
	// session. Mirrors the workspace.create/workspace.update audit calls in
	// rest_workspaces.go — best-effort, never blocks the turn.
	if hcm.setupKickoff {
		if auditor := hcm.h.agentLoop.AuditLogger(); auditor != nil {
			if err := auditor.Log(&audit.Entry{
				Event:     "workspace.setup_consumed",
				Decision:  audit.DecisionAllow,
				AgentID:   hcm.targetAgentID,
				SessionID: hcm.sessionID,
				User:      hcm.wc.userID,
				Details: map[string]any{
					"workspace_id": hcm.workspaceID,
				},
			}); err != nil {
				slog.Warn("ws: workspace setup kickoff: audit write failed",
					"workspace_id", hcm.workspaceID, "session_id", hcm.sessionID, "error", err)
			}
		}
	}
}

func (hcm *wsHandlerHandleChatMessage) queueWorkingStatus() {
	if hcm.clientMessageID == "" || hcm.sessionID == "" {
		return
	}
	hcm.h.mu.Lock()
	if hcm.h.pendingMessageStatuses == nil {
		hcm.h.pendingMessageStatuses = make(map[string][]pendingMessageStatus)
	}
	hcm.h.pendingMessageStatuses[hcm.sessionID] = append(
		hcm.h.pendingMessageStatuses[hcm.sessionID],
		pendingMessageStatus{clientMessageID: hcm.clientMessageID, wc: hcm.wc},
	)
	hcm.h.mu.Unlock()
}

func (hcm *wsHandlerHandleChatMessage) removeQueuedWorkingStatus() bool {
	hcm.h.mu.Lock()
	defer hcm.h.mu.Unlock()
	queue := hcm.h.pendingMessageStatuses[hcm.sessionID]
	for index, pending := range queue {
		if pending.clientMessageID != hcm.clientMessageID || pending.wc != hcm.wc {
			continue
		}
		queue = append(queue[:index], queue[index+1:]...)
		if len(queue) == 0 {
			delete(hcm.h.pendingMessageStatuses, hcm.sessionID)
		} else {
			hcm.h.pendingMessageStatuses[hcm.sessionID] = queue
		}
		return true
	}
	return false
}

func (hcm *wsHandlerHandleChatMessage) markWorkingIfTurnAlreadyActive() {
	hcm.h.mu.Lock()
	active := hcm.h.liveStreamers[hcm.sessionID] != nil
	hcm.h.mu.Unlock()
	if !active {
		return
	}
	if hcm.removeQueuedWorkingStatus() {
		hcm.sendMessageStatus("working")
	}
}

func (h *WSHandler) takePendingMessageStatus(sessionID string) (pendingMessageStatus, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	queue := h.pendingMessageStatuses[sessionID]
	if len(queue) == 0 {
		return pendingMessageStatus{}, false
	}
	pending := queue[0]
	if len(queue) == 1 {
		delete(h.pendingMessageStatuses, sessionID)
	} else {
		h.pendingMessageStatuses[sessionID] = queue[1:]
	}
	return pending, true
}

// takeAllPendingMessageStatuses drains and returns every remaining queued
// entry for sessionID, in FIFO order, clearing the map entry. Used at turn
// end (see wsStreamerFinalize.sendDone) so a queued status entry can never
// outlive the turn it was queued for.
func (h *WSHandler) takeAllPendingMessageStatuses(sessionID string) []pendingMessageStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	queue := h.pendingMessageStatuses[sessionID]
	if len(queue) == 0 {
		return nil
	}
	delete(h.pendingMessageStatuses, sessionID)
	return queue
}

// flushPendingMessageStatusesAsWorking is called at turn end (a `done` frame
// is about to be sent). Review finding 12: "treat a done as implying
// working" plus "clear or expire entries at turn end" — a queued status
// entry that never got consumed by a mid-turn GetStreamer call (e.g. a
// round that never opened a streamer, or a non-streaming reply) must not
// survive past the turn it belongs to: left in place, it would either keep
// a dead connection reference alive indefinitely, or get popped by a LATER,
// UNRELATED turn on the same session and mislabel the wrong message as
// "working". Every remaining entry for sessionID is drained and sent
// "working" (fanned out to all bound connections, not just the original
// sender — see sendPendingMessageWorking) before the turn's done frame goes
// out, so the tick always reaches a terminal, non-stuck state.
func (h *WSHandler) flushPendingMessageStatusesAsWorking(sessionID string) {
	for _, pending := range h.takeAllPendingMessageStatuses(sessionID) {
		sendPendingMessageWorking(h, sessionID, pending)
	}
}

// sendPendingMessageWorking delivers the "working" status to EVERY connection
// currently bound to sessionID — not just pending.wc (the connection that
// originally sent the message). Review finding 12: on a kept-chat reconnect,
// pending.wc is the ORIGINAL (often now-dead) connection; a tab that
// reconnected under a NEW chatID is bound to the same session via
// h.sessionIDs but would otherwise never see the tick flip past "Received".
// h (not just the session id) is threaded through so this can resolve the
// current connection set under h.mu, matching every other webchat delivery
// path (see resolveSessionConnsLocked's doc comment).
func sendPendingMessageWorking(h *WSHandler, sessionID string, pending pendingMessageStatus) {
	frame := generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       sessionID,
		ClientMessageId: pending.clientMessageID,
		State:           "working",
	}
	if h == nil {
		// No handler to resolve the session's connection set — fall back to
		// the originating connection only (defensive; not reached in
		// production, where GetStreamer/sendDone always pass a real h).
		sendConnGenFrame(pending.wc, string(generated.WsFrameTypeMessageStatus), frame)
		return
	}
	h.mu.Lock()
	targets := h.resolveSessionConnsLocked("", sessionID)
	h.mu.Unlock()
	if len(targets) == 0 && pending.wc != nil {
		// No connection resolved via h.sessionIDs yet (e.g. a race right at
		// mint time) — still deliver to the connection that sent the
		// message rather than silently dropping the tick.
		targets = []*wsConn{pending.wc}
	}
	for _, conn := range targets {
		sendConnGenFrame(conn, string(generated.WsFrameTypeMessageStatus), frame)
	}
}

func (hcm *wsHandlerHandleChatMessage) sendMessageStatus(state string) {
	if hcm.clientMessageID == "" || hcm.sessionID == "" {
		return
	}
	sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeMessageStatus), generated.MessageStatusFrame{
		Type:            string(generated.WsFrameTypeMessageStatus),
		SessionId:       hcm.sessionID,
		ClientMessageId: hcm.clientMessageID,
		State:           state,
	})
}

// resolveTargetAgent resolves the target agent from the frame's agent_id (default-agent fallbacks included) and rejects worker agents and targets that resolve to nothing.
func (hcm *wsHandlerHandleChatMessage) resolveTargetAgent() bool {
	hcm.targetAgentID = hcm.agentID
	if hcm.targetAgentID == "" {
		if reg := hcm.h.agentLoop.GetRegistry(); reg != nil {
			if def := reg.GetDefaultAgent(); def != nil {
				hcm.targetAgentID = def.ID
			}
		}
		if hcm.targetAgentID == "" {
			// Fall back to the first chat-target agent (mirrors handleBoardTaskStart /
			// resolveDefaultAgentID in pkg/routing/route.go). firstChatTargetAgentID
			// already skips workers, so this fallback never lands on one.
			hcm.targetAgentID = firstChatTargetAgentID(hcm.h.agentLoop.GetConfig())
		}
	} else if isWorkerAgentID(hcm.h.agentLoop.GetConfig(), hcm.targetAgentID) {
		// An explicit agent_id that resolves to a worker is illegitimate: a worker
		// is a delegation-only labor tier, never a chat target. Refuse to mint a
		// live chat session for it. Mirror the error-frame pattern used for an
		// unknown/invalid session below.
		slog.Warn("ws: rejecting chat frame addressed to a worker agent",
			"agent_id", hcm.targetAgentID, "chat_id", hcm.chatID,
			"reason", "worker is invoked via delegation, not as a chat target")
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "this agent is a worker and cannot be a chat target — workers are invoked via delegation",
		})
		return true
	}

	// No caller-supplied agent_id AND neither fallback resolved one (no
	// seeded default agent, no chat-target agent in the roster at all): reject
	// explicitly rather than letting an empty targetAgentID flow into
	// store.NewSession/transcript writes below. The retired "main" sentinel
	// used to silently absorb this case; there is no substitute default to
	// fall back to now — an empty owner on a persisted session is exactly the
	// unpoliced-shadow-agent bug removing the sentinel was meant to close.
	if hcm.targetAgentID == "" {
		slog.Warn("ws: rejecting chat frame — no agent_id supplied and no default agent could be resolved",
			"chat_id", hcm.chatID, "workspace_id", hcm.workspaceID)
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "no agent available to handle this message: no default agent is configured",
		})
		return true
	}
	return false
}

// validateFrameIDs validates the client-supplied agent_id/session_id formats up front and rejects kickoff frames that carry a session_id or have no resolved workspace, dropping an unknown workspace binding.
func (hcm *wsHandlerHandleChatMessage) validateFrameIDs() bool {
	// Validate the raw client-supplied agent_id format HERE, before any of the
	// workspace-kickoff consume/mint/audit work below. The previous placement
	// of this check (immediately before the bus publish, at the very end of
	// the function) ran AFTER the kickoff flag had already been consumed —
	// a malformed agent_id would permanently burn the one-time setup
	// interview and leave behind an orphan minted session plus a false
	// "consumed" audit entry, with no compensation path. Every frame-level
	// validation must complete before the consume step is ever reached.
	if hcm.agentID != "" {
		if err := validateEntityID(hcm.agentID); err != nil {
			slog.Warn("ws: invalid agent_id in message frame; rejecting", "agent_id", hcm.agentID, "error", err)
			var sidPtr *string
			if hcm.frameSessionID != "" {
				sidCopy := hcm.frameSessionID
				sidPtr = &sidCopy
			}
			sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				Message:   "invalid agent_id",
				SessionId: sidPtr,
			})
			return true
		}
	}

	hcm.sessionID = hcm.frameSessionID

	// Validate the client-supplied session_id format up front too — same
	// rationale as the agent_id hoist above: this used to run only inside the
	// "existing session" branch further down, well after the kickoff consume.
	// A malformed session_id therefore no longer has any path to burning the
	// flag before being rejected.
	if hcm.sessionID != "" {
		if err := validation.EntityID(hcm.sessionID); err != nil {
			slog.Warn("ws: invalid session_id in message frame", "session_id", hcm.sessionID, "error", err)
			sidCopy := hcm.sessionID
			sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				Message:   "invalid session_id format",
				SessionId: &sidCopy,
			})
			return true
		}
	}

	// A workspace-setup kickoff is MINT-ONLY: a frame that carries BOTH
	// setupKickoff and a non-empty client-supplied session_id is rejected
	// before the consume step. Without this guard an arbitrary client could
	// attach workspace_setup_kickoff=true to a message addressed at an
	// unrelated EXISTING session (untested, wrong owner, wrong workspace) and
	// burn the one-time flag against it. Rejecting this combination removes
	// the "existing session" branch as a reachable path for a kickoff — see
	// the (sessionID == "") mint branch below, which is the only path a
	// kickoff can now take.
	if hcm.setupKickoff && hcm.sessionID != "" {
		slog.Warn("ws: workspace setup kickoff with a client-supplied session_id — rejecting",
			"chat_id", hcm.chatID, "session_id", hcm.sessionID)
		sidCopy := hcm.sessionID
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			Message:   "workspace setup could not be started",
			SessionId: &sidCopy,
		})
		return true
	}

	// M4: validate the client-supplied workspace_id at the binding boundary
	// before it is ever stamped onto session meta. A bad/stale/typo'd id would
	// otherwise persist tasks to a workspace no board renders while the agent
	// reports success. On a miss, drop the binding and fall back to the default
	// (resolveWorkspaceID/ResolveDefaultID picks up the real default) rather
	// than stamping the bogus id.
	if hcm.workspaceID != "" && hcm.h.home != "" && !workspace.Exists(hcm.h.home, hcm.workspaceID) {
		slog.Warn("ws: dropping unknown workspace_id binding — falling back to default",
			"workspace_id", hcm.workspaceID, "chat_id", hcm.chatID)
		hcm.workspaceID = ""
	}

	// A workspace-setup kickoff is only meaningful against a real, resolved
	// workspace — either the id was absent to begin with, or the M4 check
	// above just blanked it as unknown. Reject outright (this used to "demote
	// to a normal message", which persisted the kickoff instruction as a
	// fake user-authored transcript entry and session title) — no session is
	// minted, nothing is published.
	if hcm.setupKickoff && hcm.workspaceID == "" {
		slog.Warn("ws: workspace setup kickoff with no resolved workspace_id — rejecting",
			"chat_id", hcm.chatID)
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "workspace setup could not be started: unknown workspace",
		})
		return true
	}
	return false
}

// resolveSessionStore picks the store that owns the session (the existing session's owner store, else shared, else the agent store) and rejects a kickoff with no usable store.
func (hcm *wsHandlerHandleChatMessage) resolveSessionStore() bool {
	// Pick the right store for the operation:
	//
	//   * Resuming an existing session: ask ResolveSessionStore to find the
	//     owning store. New chat sessions live in sharedSessionStore, but
	//     sessions created by the task scheduler, by per-agent tools, or by
	//     custom agents (e.g. Hans completing a task) live under
	//     agents/<id>/sessions and are visible only via the per-agent stores.
	//     The previous code unconditionally picked sharedSessionStore here,
	//     so any non-shared session produced "session not found" on the next
	//     user message even though the SPA could read its transcript through
	//     the REST GET /api/v1/sessions/{id} endpoint (which already uses
	//     ResolveSessionStore).
	//
	//   * Minting a new session: keep using the shared store so every fresh
	//     chat lands in the modern shared layout — that part was never broken.

	if hcm.sessionID != "" {
		hcm.store = hcm.h.agentLoop.ResolveSessionStore(hcm.sessionID)
		if hcm.store == nil {
			// Truly unknown session — surface it explicitly so the SPA can
			// render the "session not found" toast/banner rather than silently
			// publishing the message to the bus against a non-existent session.
			slog.Warn(
				"ws: session not found (no store owns it)",
				"session_id", hcm.sessionID,
			)
			sidCopy := hcm.sessionID
			sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				Message:   "session not found",
				SessionId: &sidCopy,
			})
			return true
		}
	} else {
		hcm.store = hcm.h.agentLoop.GetSessionStore()
		if hcm.store == nil {
			hcm.store = hcm.h.agentLoop.GetAgentStore(hcm.targetAgentID)
		}
	}

	// A kickoff-flagged frame with no usable session store (a
	// degenerate configuration, not the normal path) must be REJECTED like
	// every other kickoff-cannot-complete case rather than silently
	// falling through to the no-store tail below, which would publish the
	// raw kickoff instruction to the bus as an ordinary message.
	if hcm.setupKickoff && hcm.store == nil {
		slog.Warn("ws: workspace setup kickoff rejected — no session store available",
			"workspace_id", hcm.workspaceID, "chat_id", hcm.chatID)
		sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "workspace setup could not be started",
		})
		return true
	}
	return false
}

// collectAcceptedMedia filters the client-supplied media refs down to well-formed media:// refs under the inbound caps, counting every drop.
func (hcm *wsHandlerHandleChatMessage) collectAcceptedMedia() {
	// kickoffInstruction holds the SERVER-BUILT driving prompt for a kickoff
	// turn (see doc comment above) — populated only when setupKickoff is true
	// and the consume below succeeds. The client-supplied `content` is never
	// used as msg.Content for a kickoff turn.

	// #254: thread client-supplied media refs into the inbound message so the
	// agent loop resolves them into multimodal content blocks. Only accept
	// Hard-cap inbound media to prevent resource exhaustion. Refs beyond
	// maxInboundMediaRefs and refs exceeding maxInboundRefLen are dropped and
	// counted against the inbound-dropped counter.
	//
	// Computed HERE — before the transcript-append block below — so that
	// D2 (library-spec) can persist the accepted, validated set as
	// session.TranscriptEntry.Attachments. It used to be computed AFTER
	// that block, which meant the persisted transcript never had access to
	// the media this very message carried.
	const maxInboundMediaRefs = 16
	const maxInboundRefLen = 256

	for i, ref := range hcm.mediaRefs {
		if i >= maxInboundMediaRefs {
			hcm.wc.inboundDropped.Add(1)
			slog.Warn("ws: media array exceeds cap — dropping remaining refs",
				"chat_id", hcm.chatID, "session_id", hcm.sessionID,
				"dropped_from_index", i, "total_supplied", len(hcm.mediaRefs))
			break
		}
		if len(ref) > maxInboundRefLen {
			hcm.wc.inboundDropped.Add(1)
			slog.Warn("ws: dropping oversized ref in message frame",
				"chat_id", hcm.chatID, "session_id", hcm.sessionID,
				"ref_prefix", ref[:32])
			continue
		}
		// Accept only well-formed media:// refs (non-empty ID validated by
		// ParseMediaRef — rejects bare "media://" with empty ID, non-prefixed
		// strings, raw paths, and HTTP URLs that a buggy channel might emit).
		// Non-matching strings are a client error or smuggling attempt — drop
		// and count them via the inboundDropped counter.
		if _, err := media.ParseMediaRef(ref); err == nil {
			hcm.acceptedMedia = append(hcm.acceptedMedia, ref)
		} else {
			hcm.wc.inboundDropped.Add(1)
			truncated := ref
			if len(truncated) > 64 {
				truncated = truncated[:64] + "…"
			}
			slog.Warn("ws: dropping invalid media:// ref in message frame",
				"chat_id", hcm.chatID, "session_id", hcm.sessionID,
				"ref_prefix", truncated)
		}
	}
}

// recordSessionAndTranscript consumes the workspace-setup kickoff, mints or resumes the session, and persists the user (or neutral kickoff) transcript entry.
func (hcm *wsHandlerHandleChatMessage) recordSessionAndTranscript() bool {
	if hcm.store != nil {
		// Workspace-setup kickoff idempotency guard: clear SetupPending exactly
		// once, under the per-workspace lock (workspace.LockID). ANY outcome
		// other than a clean consume — duplicate (already ran) or a read/write
		// failure against the workspace file — is REJECTED outright: no
		// session minted, no transcript entry written, nothing published.
		if hcm.setupKickoff {
			outcome, wsName, wsDescription := hcm.h.consumeWorkspaceSetupKickoff(hcm.workspaceID)
			switch outcome {
			case kickoffDuplicate:
				sidCopy := hcm.sessionID
				sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:      string(generated.WsFrameTypeError),
					Message:   "workspace setup has already run",
					SessionId: &sidCopy,
				})
				return true
			case kickoffFailed:
				sidCopy := hcm.sessionID
				sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:      string(generated.WsFrameTypeError),
					Message:   "workspace setup could not be started",
					SessionId: &sidCopy,
				})
				return true
			case kickoffConsumed:
				// Proceed — SetupPending is now cleared on disk. Build the
				// SERVER-CANONICAL driving instruction from the workspace's
				// own name/description now, while it's in hand — the
				// client-supplied `content` is never used for a kickoff turn.
				hcm.kickoffInstruction = buildWorkspaceKickoffInstruction(wsName, wsDescription)
			default:
				// Defensive: a future kickoffOutcome value this switch doesn't
				// know about must never silently fall through as if it were
				// kickoffConsumed — reject outright, same as every other
				// kickoff-cannot-complete case.
				slog.Warn("ws: workspace setup kickoff: unrecognized outcome — rejecting",
					"workspace_id", hcm.workspaceID, "outcome", outcome)
				sidCopy := hcm.sessionID
				sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:      string(generated.WsFrameTypeError),
					Message:   "workspace setup could not be started",
					SessionId: &sidCopy,
				})
				return true
			}
		}
		if hcm.sessionID == "" {
			// No session_id in frame: mint a new session so all subsequent frames have one.
			meta, err := hcm.store.NewSession(session.SessionTypeChat, "webchat", hcm.targetAgentID)
			if err != nil {
				slog.Error("ws: could not create session", "error", err)
				// A successful kickoff consume just cleared SetupPending —
				// if minting the session then fails, the one-time interview would
				// otherwise be silently lost. Best-effort restore.
				if hcm.setupKickoff {
					hcm.h.restoreWorkspaceSetupPending(hcm.workspaceID)
				}
				sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: fmt.Sprintf("could not create session: %v", err),
				})
				return true
			}
			hcm.sessionID = meta.ID
			hcm.h.mu.Lock()
			hcm.h.sessionIDs[hcm.chatID] = meta.ID
			hcm.h.mu.Unlock()
			var title string
			if hcm.setupKickoff {
				// The kickoff instruction text must not leak into the sidebar
				// as a session title — use a fixed, human-readable one instead.
				title = "Workspace setup"
			} else {
				titleRunes := []rune(hcm.content)
				if len(titleRunes) > 60 {
					title = string(titleRunes[:57]) + "..."
				} else {
					title = hcm.content
				}
			}
			// Stamp the session owner from the authenticated WebSocket user (SEC-2/#406).
			// wc.userID is set at auth time (FR-073); empty on dev-mode bypass.
			ownerCopy := hcm.wc.userID
			metaPatch := session.MetaPatch{Title: &title, Owner: &ownerCopy}
			// M4: bind the new session to the active workspace so created tasks
			// land on this workspace's board (not the agent's default).
			if hcm.workspaceID != "" {
				wsCopy := hcm.workspaceID
				metaPatch.WorkspaceID = &wsCopy
			}
			if err := hcm.store.SetMeta(meta.ID, metaPatch); err != nil {
				if hcm.setupKickoff {
					// A kickoff turn that fails to persist its title/owner/
					// workspace stamp would run UNBOUND from the workspace
					// that triggered it — worse than a plain warn-and-continue.
					// Treat this as a hard failure: restore the flag, delete
					// the just-minted orphan session, and reject before the
					// session_started ack (below) is ever sent.
					slog.Warn(
						"ws: workspace setup kickoff: could not persist session title/owner/workspace — rejecting",
						"session_id", meta.ID, "error", err)
					hcm.h.rollbackKickoffSession(hcm.store, hcm.workspaceID, meta.ID, hcm.chatID)
					sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
						Type:    string(generated.WsFrameTypeError),
						Message: "workspace setup could not be started",
					})
					return true
				}
				slog.Warn("ws: could not set session title/owner", "session_id", meta.ID, "error", err)
			}
			// Ack the new session_id so the SPA can associate all subsequent frames.
			startedFrame := generated.SessionStartedFrame{
				Type:      string(generated.WsFrameTypeSessionStarted),
				SessionId: meta.ID,
			}
			if hcm.targetAgentID != "" {
				aid := hcm.targetAgentID
				startedFrame.AgentId = &aid
			}
			sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeSessionStarted), startedFrame)
		} else {
			// This branch — an existing, client-supplied session_id — is
			// reachable only by a NORMAL (non-kickoff) message: the mint-only
			// guard near the top of this function already rejected any
			// setupKickoff frame carrying a non-empty session_id, and format
			// was already validated up front there too. No kickoff-restore
			// compensation is needed here as a result.
			//
			// Validate that the session actually exists in the store.
			existingMeta, err := hcm.store.GetMeta(hcm.sessionID)
			if err != nil {
				slog.Warn("ws: session not found", "session_id", hcm.sessionID, "error", err)
				sidCopy := hcm.sessionID
				sendConnGenFrame(hcm.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:      string(generated.WsFrameTypeError),
					Message:   "session not found",
					SessionId: &sidCopy,
				})
				return true
			}
			// M4: track the ACTIVE workspace on every message, not just the first
			// one. The SPA resends the CURRENTLY active workspace_id on every
			// outbound frame (src/store/chat.ts sendMessage, read live from
			// useWorkspacesStore) specifically so a task/delegation this turn
			// creates lands on whatever workspace the operator is looking at
			// right now — including an ongoing chat session that started in one
			// workspace and is continuing in another. A stale "first binding
			// wins" rule broke that: a delegation edge wired on a workspace's
			// Team tab AFTER the session's first bind would never be consulted
			// by resolveEffectiveWorkspaceID (pkg/agent/loop.go), which reads
			// this same session meta fresh every turn — so the UI's "Saved just
			// now" was genuine (see TestWorkspaceDelegation_EdgeWiredViaTeamTabPersistsForLiveSession)
			// but the running session kept enforcing/advertising delegation against its
			// ORIGINAL workspace until the operator started a brand new session.
			// Only skip the rewrite when this message carries no workspace_id at
			// all (workspaceID == "") — an absent value must never blank out an
			// existing binding (e.g. a non-workspace-aware channel message
			// resuming a workspace-bound session), it just leaves it as-is.
			if hcm.workspaceID != "" && existingMeta != nil && existingMeta.WorkspaceID != hcm.workspaceID {
				wsCopy := hcm.workspaceID
				if err := hcm.store.SetMeta(hcm.sessionID, session.MetaPatch{WorkspaceID: &wsCopy}); err != nil {
					slog.Warn("ws: could not bind workspace to session", "session_id", hcm.sessionID, "error", err)
				}
			}
			// Track for streamer.
			hcm.h.mu.Lock()
			if hcm.h.sessionIDs[hcm.chatID] == "" {
				hcm.h.sessionIDs[hcm.chatID] = hcm.sessionID
			}
			hcm.h.mu.Unlock()
		}

		// ADR-066 D4 / FR-015: this handler persists the user message BEFORE
		// the bus publish, but processMessage is the enforcement point for
		// the user-message bound and refuses an over-bound message with NO
		// transcript entry. So the one thing this intake does for the bound
		// is skip that early write when processMessage is about to refuse —
		// the refusal reply itself comes back through the ordinary outbound
		// path (token + done frames, never an error frame). A kickoff turn
		// discards the client content entirely, so it is never over-bound.
		overUserBound := !hcm.setupKickoff &&
			agent.UserMessageChars(hcm.content) > hcm.h.agentLoop.UserMessageBound()
		if hcm.sessionID != "" && !overUserBound {
			entry := session.TranscriptEntry{
				ID:        uuid.New().String(),
				Role:      "user",
				AgentID:   hcm.targetAgentID,
				Content:   hcm.content,
				Timestamp: time.Now().UTC(),
				// D2 (library-spec, 2026-07-29 UAT): persist which files this
				// message attached. Previously this field was never set even
				// though it has existed on TranscriptEntry all along — a later
				// turn (or a DIFFERENT AGENT after a handoff, exactly what
				// happened in the UAT: Mia -> Ray) had no durable record of
				// what was uploaded, only the live in-flight message. Built
				// from acceptedMedia (the validated ref set), not the raw
				// client-supplied mediaRefs.
				Attachments: buildTranscriptAttachments(hcm.h.agentLoop.GetMediaStore(), hcm.acceptedMedia, hcm.workspaceID),
			}
			if hcm.setupKickoff {
				// Record the kickoff trigger as a system-role event, not a user
				// chat bubble. AgentID stays targetAgentID (Ava) so replay/
				// hydration attributes this entry to her on a fresh turn.
				entry.Type = session.EntryTypeSystem
				entry.Role = "system"
				// The PERSISTED/REPLAYED entry carries neutral, fixed
				// content — never the client-supplied kickoff instruction, and
				// not even the SERVER-BUILT one either (session replay renders
				// this as a system pill). The turn itself is instead driven by
				// the SERVER-BUILT kickoffInstruction via msg.Content below —
				// see turnContent — never by this entry or by `content`.
				entry.Content = "Workspace setup started."
			}
			if err := hcm.store.AppendTranscript(hcm.sessionID, entry); err != nil {
				slog.Warn("ws: could not record user message", "session_id", hcm.sessionID, "error", err)
			} else {
				hcm.transcriptPersisted = true
			}
			// The workspace.setup_consumed audit entry is emitted further
			// below, AFTER a successful bus publish — not here. Emitting it
			// at this point (the previous placement) ran before the publish
			// that could still fail, producing a false "consumed" audit
			// record even on a failure that had just restored the flag and
			// rolled back this very session.
		}
	}
	return false
}

// buildInboundMessage assembles the bus.InboundMessage from the turn content (client content, or the server-built kickoff instruction), agent/model metadata, and the accepted media.
func (hcm *wsHandlerHandleChatMessage) buildInboundMessage() {
	// The turn is driven by the client-supplied content EXCEPT for a kickoff,
	// which uses the SERVER-BUILT canonical instruction assembled above from
	// the workspace's own name/description — the client-supplied content is
	// discarded entirely for a kickoff turn (see doc comment).
	turnContent := hcm.content
	if hcm.setupKickoff {
		turnContent = hcm.kickoffInstruction
	}

	hcm.msg = bus.InboundMessage{
		Channel: "webchat",
		Sender: bus.SenderInfo{
			CanonicalID: "webchat_user",
		},
		// FR-017: carry the WS-authenticated gateway principal (set at auth
		// time, e.g. "cli" or the account's username) so the agent loop can
		// stamp audit.Entry.User for turn attribution. This is the ONLY site that sets
		// GatewayUserID — it is a dedicated carrier, NOT Sender.Username, so that
		// platform channels (which fill Sender.Username with the platform handle)
		// can never have their sender misattributed as a gateway principal.
		// Empty under dev-mode bypass / legacy env-token auth (wc.userID is left
		// unset there) — the audit stamp then stays empty rather than guessing.
		GatewayUserID: hcm.wc.userID,
		ChatID:        hcm.chatID,
		Content:       turnContent,
		SessionID:     hcm.sessionID,
		Media:         hcm.acceptedMedia,
		// UserInitiated (ADR-049 Gap #8/r2, R6): the webchat WS `message`
		// handler is the "Web WS message handler (authenticated gateway
		// user)" origination point — always a genuine live user action.
		UserInitiated: true,
		// OperatorPrompt (ADR-085 BROWSER-FR-029): a fail-closed sibling of
		// UserInitiated, set true ONLY here, pkg/gateway/sse.go and
		// pkg/channels/base.go::HandleMessage — the three sites where the
		// OPERATOR composed this message on THIS session. It is what
		// releases a held browser wheel (pkg/agent/loop.go::processMessage,
		// via AgentLoop.SetBrowserWheelReleaseHook) before the turn begins.
		// Deliberately NOT reused from UserInitiated (which is also true on
		// the question-card resume path, where a release would hand the
		// browser back mid-drive — see ws_ask_user.go, which must NOT set
		// this field).
		OperatorPrompt: true,
	}
	if hcm.agentID != "" {
		// Format already validated up front, before the kickoff consume step
		// (see the check next to the worker-agent guard near the top of this
		// function) — no re-validation needed here.
		hcm.msg.Metadata = map[string]string{"agent_id": hcm.agentID}
	}
	// FR-010: forward per-turn model override to the bus so the agent loop's
	// switch-compress path can route THIS turn to the chosen model. Trim
	// whitespace first; empty / whitespace-only values are dropped so the agent
	// falls back to its configured model. The map is created lazily here so a
	// model_name-only frame still produces a populated Metadata on the wire.
	trimmedModel := strings.TrimSpace(hcm.modelName)
	if trimmedModel != "" {
		if hcm.msg.Metadata == nil {
			hcm.msg.Metadata = map[string]string{}
		}
		hcm.msg.Metadata["model_name"] = trimmedModel
	}
}

// kickoffOutcome is the result of consumeWorkspaceSetupKickoff.
type kickoffOutcome int

// consumeWorkspaceSetupKickoff clears workspaceID's SetupPending flag exactly
// once, under the workspace's own per-ID lock (workspace.LockID) so concurrent
// kickoff frames (e.g. two tabs opening the same brand-new workspace at once)
// serialize against each other AND against every other writer of this same
// workspace file — a REST PUT/DELETE/delegation-PUT racing this consume can no
// longer resurrect a just-cleared flag with a stale write-back, clobber this
// write, or (for DELETE) have this consume resurrect a just-deleted file as a
// ghost: a delete-then-kickoff race always serializes so the kickoff's
// readWorkspaceFile either sees the workspace fully intact or (post-delete)
// fails with errWorkspaceNotFound, which this function maps to kickoffFailed
// — it never recreates the file.
//
// On a kickoffConsumed outcome, also returns the workspace's Name and
// Description (read under the same lock, from the same on-disk record that
// was just consumed) so the caller can build the SERVER-CANONICAL turn
// instruction from them instead of trusting client-supplied content. Both
// are empty strings for a non-consumed outcome.
func (h *WSHandler) consumeWorkspaceSetupKickoff(
	workspaceID string,
) (outcome kickoffOutcome, name string, description string) {
	unlock := workspace.LockID(workspaceID)
	defer unlock()

	w, err := readWorkspaceFile(h.home, workspaceID)
	if err != nil {
		slog.Warn("ws: workspace setup kickoff: could not read workspace file — rejecting",
			"workspace_id", workspaceID, "error", err)
		return kickoffFailed, "", ""
	}
	if !w.SetupPending {
		slog.Warn("ws: workspace setup kickoff: setup already ran — rejecting duplicate",
			"workspace_id", workspaceID)
		return kickoffDuplicate, "", ""
	}
	w.SetupPending = false
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeWorkspaceFile(h.home, w); err != nil {
		slog.Warn("ws: workspace setup kickoff: could not persist cleared setup_pending — rejecting",
			"workspace_id", workspaceID, "error", err)
		return kickoffFailed, "", ""
	}
	return kickoffConsumed, w.Name, w.Description
}

// buildTranscriptAttachments resolves each accepted media ref into a
// session.Attachment for persistence on the user's TranscriptEntry (D2,
// library-spec). A ref that fails to resolve (deleted file, integrity
// failure, cross-workspace mismatch) is logged and skipped rather than
// aborting the whole message — an attachment record is best-effort
// metadata, not load-bearing for the turn itself (the raw ref still reaches
// the agent loop via msg.Media regardless of what this function returns).
//
// Path prefers the EXACT workspace-relative path the D-1 dual-write staged
// the file at (agent.LookupUploadWorkPath), falling back to the best-effort
// plain-name formula (agent.FallbackAnnouncedUploadPath) for a workspace ref
// whose write-time record is unavailable (e.g. a gateway restart between
// the upload and this message), and finally to the bare ref for a
// non-workspace ref (legacy media://<uuid>, channel-native attachments),
// which has no workspace-relative path at all.
func buildTranscriptAttachments(store media.MediaStore, refs []string, callerWorkspace string) []session.Attachment {
	if store == nil || len(refs) == 0 {
		return nil
	}
	opts := media.ResolveOpts{}
	if callerWorkspace != "" {
		opts = media.WithCallerWorkspace(callerWorkspace)
	}
	out := make([]session.Attachment, 0, len(refs))
	for _, ref := range refs {
		localPath, meta, err := store.ResolveWithMetaOpts(ref, opts)
		if err != nil {
			slog.Warn("ws: could not resolve media ref for transcript attachment",
				"ref", ref, "error", err)
			continue
		}
		var size int64
		if info, statErr := os.Stat(localPath); statErr == nil {
			size = info.Size()
		} else {
			slog.Warn("ws: could not stat media file for transcript attachment size",
				"ref", ref, "error", statErr)
		}
		path := ref
		if media.IsWorkspaceRef(ref) {
			if workPath, ok := agent.LookupUploadWorkPath(ref); ok {
				path = workPath
			} else if fallback := agent.FallbackAnnouncedUploadPath(meta.Filename); fallback != "" {
				path = fallback
			}
		}
		out = append(out, session.Attachment{
			// DetectAttachmentType, NOT DetectFileClass. This value crosses
			// the wire, so it must be the contract's media-category enum
			// (image|audio|video|file). DetectFileClass returns the
			// presentation-noun class (image|document|file) used to phrase
			// the LLM guidance line — "document" is not a legal wire value.
			// Using it here shipped a BLOCKER: the SPA's strict Zod schema
			// rejected the whole messages payload, so chat history refused
			// to load at all after any document upload (live UAT 2026-07-29).
			Type:     agent.DetectAttachmentType(meta.ContentType, meta.Filename),
			Path:     path,
			Size:     size,
			MIMEType: meta.ContentType,
		})
	}
	return out
}

// buildWorkspaceKickoffInstruction assembles the SERVER-CANONICAL prompt that
// drives a workspace-setup kickoff turn, from the workspace's own name and
// (optional) description — never from client-supplied content. See the
// handleChatMessage doc comment for why: an arbitrary client instruction
// driving Ava's first turn on a new workspace is a forensics gap and an
// i18n/stale-SPA drift risk that a fixed, server-built prompt closes.
func buildWorkspaceKickoffInstruction(name, description string) string {
	instruction := fmt.Sprintf("The workspace %q was just created.", name)
	if desc := strings.TrimSpace(description); desc != "" {
		instruction += fmt.Sprintf(" Its description: %s.", desc)
	}
	instruction += " Introduce yourself and interview the user about this workspace's purpose" +
		" so you can determine which agents and skills its team needs, then set up the team."
	return instruction
}

// rollbackKickoffSession undoes a partially-completed workspace-setup kickoff
// after a post-mint failure (SetMeta or PublishInbound): it restores the
// workspace's SetupPending flag (best-effort, see restoreWorkspaceSetupPending)
// AND deletes the just-minted "Workspace setup" session so a repeated
// failure does not accumulate orphan empty sessions — mirroring the
// rollbackCreatedSessions precedent in rest_workspaces.go. Also clears the
// chatID→sessionID tracking entry if it still points at the session being
// deleted, so a subsequent frame on the same connection does not resolve to
// a session that no longer exists.
//
// store may be nil (defensive only — every call site already holds a
// non-nil store by construction) and sessionID may be empty (the NewSession-
// failure path has nothing to delete yet); both are treated as "nothing to
// roll back beyond the flag restore".
func (h *WSHandler) rollbackKickoffSession(store *session.UnifiedStore, workspaceID, sessionID, chatID string) {
	h.restoreWorkspaceSetupPending(workspaceID)
	if store != nil && sessionID != "" {
		if err := store.DeleteSession(sessionID); err != nil {
			slog.Warn("ws: workspace setup kickoff: rollback session delete failed",
				"session_id", sessionID, "error", err)
		}
	}
	h.mu.Lock()
	if h.sessionIDs[chatID] == sessionID {
		delete(h.sessionIDs, chatID)
	}
	h.mu.Unlock()
}

// restoreWorkspaceSetupPending re-sets workspaceID's SetupPending flag to true
// after a successful kickoff consume (consumeWorkspaceSetupKickoff returned
// kickoffConsumed) was followed by a downstream failure — minting the new
// session, looking up an existing session's meta, or publishing the turn to
// the bus — that means the one-time setup interview never actually ran (Fix
// 2). Acquires the same per-workspace lock as consumeWorkspaceSetupKickoff so
// the restore itself cannot race a concurrent writer.
//
// Best-effort: a read/write failure here is logged and otherwise ignored —
// the caller has already committed to sending the user an error frame for the
// original failure, and there is no further fallback. A restore failure
// leaves SetupPending=false despite no interview having run, which is a
// degraded but non-corrupting outcome (the operator can still trigger team
// setup manually via the workspace's Team tab).
func (h *WSHandler) restoreWorkspaceSetupPending(workspaceID string) {
	unlock := workspace.LockID(workspaceID)
	defer unlock()

	w, err := readWorkspaceFile(h.home, workspaceID)
	if err != nil {
		slog.Warn("ws: workspace setup kickoff: could not read workspace file to restore setup_pending",
			"workspace_id", workspaceID, "error", err)
		return
	}
	w.SetupPending = true
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeWorkspaceFile(h.home, w); err != nil {
		slog.Warn("ws: workspace setup kickoff: could not restore setup_pending after downstream failure",
			"workspace_id", workspaceID, "error", err)
	}
}
