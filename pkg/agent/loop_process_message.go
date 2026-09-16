// loop_process_message.go: Process one inbound message: the processMessage stages

package agent

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// prepareInbound logs the inbound message, transcribes audio, and sends any deferred placeholder.
func (pm *agentLoopProcessMessage) prepareInbound() {
	// Add message preview to log (show full content for error messages)
	var logContent string
	if strings.Contains(pm.msg.Content, "Error:") || strings.Contains(pm.msg.Content, "error") {
		logContent = pm.msg.Content // Full content for errors
	} else {
		logContent = utils.Truncate(pm.msg.Content, 80)
	}
	logger.InfoCF(
		"agent",
		fmt.Sprintf("Processing message from %s:%s: %s", pm.msg.Channel, pm.msg.Sender.CanonicalID, logContent),
		map[string]any{
			"channel":     pm.msg.Channel,
			"chat_id":     pm.msg.ChatID,
			"sender_id":   pm.msg.Sender.CanonicalID,
			"session_key": pm.msg.SessionKey,
		},
	)

	var hadAudio bool
	pm.msg, hadAudio = pm.al.transcribeAudioInMessage(pm.ctx, pm.msg)

	// For audio messages the placeholder was deferred by the channel.
	// Now that transcription (and optional feedback) is done, send it.
	if hadAudio {
		if cm := pm.al.getChannelManager(); cm != nil {
			cm.SendPlaceholder(pm.ctx, pm.msg.Channel, pm.msg.ChatID)
		}
	}
}

// resolveWorkspace selects the active workspace from session, channel, or inbound metadata.
func (pm *agentLoopProcessMessage) resolveWorkspace() {
	// M4: bind the active workspace into this turn so a task an agent creates
	// (task_create / delegation) lands on the ACTIVE workspace's board rather
	// than the agent's default workspace. A web-chat / channel session that was
	// opened from a workspace carries the workspace ID on its meta (set via the
	// session-scope PUT or at session creation). Resolve it here so the tool
	// context (loop.go: WithWorkspaceID) carries it through to resolveWorkspaceID.
	// Falls back to the inbound metadata key when present (e.g. board-task runs),
	// and finally to "" — task_create then resolves the real default workspace.
	pm.workspaceID = ""
	if pm.transcriptStore != nil && pm.transcriptSessionID != "" {
		// FIX 1 (re-review of the re-review): distinguish a real meta-read
		// failure from "no workspace bound" — see
		// resolveWorkspaceIDForContinuation's doc comment (above,
		// ~line 3099) for the full rationale.
		if meta, mErr := pm.transcriptStore.GetMeta(pm.transcriptSessionID); mErr != nil {
			if !errors.Is(mErr, os.ErrNotExist) {
				logger.WarnCF("agent", "could not read session meta while resolving workspace; workspace unresolved",
					map[string]any{"session_id": pm.transcriptSessionID, "error": mErr.Error()})
			}
		} else if meta != nil {
			pm.workspaceID = meta.WorkspaceID
		}
	}
	if pm.workspaceID == "" {
		// The channel instance itself, before falling back to inbound metadata.
		//
		// resolveWorkspaceIDForContinuation has had this rung all along; this
		// path did not, and the asymmetry was the bug: a session created
		// BEFORE its channel was bound to a workspace keeps an empty
		// workspace_id forever (resolveOrCreateChannelSession returns early on
		// an index hit and never patches an existing session), and
		// resolveEffectiveWorkspaceID then silently substitutes the DEFAULT
		// workspace. Since ADR-037 makes delegation trust workspace-scoped,
		// that authorises delegation against the wrong workspace's trust
		// graph, and memory rooms, task placement and the working directory
		// degrade the same way.
		//
		// setChannelRouting now re-stamps existing sessions when a binding is
		// written, which repairs data. This closes it at resolution time as
		// well, so a session created by any path that never went through that
		// handler still resolves correctly — and so the two ladders stop
		// disagreeing on this axis, which is the same defect shape as the
		// default-agent divergence.
		if instanceID := inboundInstanceID(pm.msg); instanceID != "" {
			if cfg := pm.al.GetConfig(); cfg != nil {
				if inst, ok := cfg.Channels[instanceID]; ok && inst.WorkspaceID != "" {
					pm.workspaceID = inst.WorkspaceID
				}
			}
		}
	}
	if pm.workspaceID == "" {
		pm.workspaceID = inboundMetadata(pm.msg, "workspace_id")
	}
}

// prepareTurn builds the turn options and releases browser ownership for an operator prompt.
func (pm *agentLoopProcessMessage) prepareTurn() {
	pm.opts = processOptions{
		SessionKey:        pm.sessionKey,
		Channel:           pm.msg.Channel,
		ChatID:            pm.msg.ChatID,
		SenderID:          pm.msg.Sender.CanonicalID,
		SenderDisplayName: pm.msg.Sender.DisplayName,
		// FR-017: thread the authenticated gateway principal into the turn for
		// audit attribution. Only the gateway webchat WS path sets
		// msg.GatewayUserID (= wc.userID, the WS-authenticated identity, e.g.
		// "cli" or an admin username). Channel/task/scheduled inbound messages
		// never set it, so their turns leave audit.Entry.User empty structurally.
		// We deliberately do NOT read msg.Sender.Username here: channels populate
		// it with the platform handle (e.g. "@alice"), which is not a gateway
		// principal and must never be stamped as audit User.
		UserID:              gatewayPrincipal(pm.msg),
		UserInitiated:       userInitiated(pm.msg),
		UserMessage:         pm.msg.Content,
		Media:               pm.msg.Media,
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: pm.transcriptSessionID,
		TranscriptStore:     pm.transcriptStore,
		WorkspaceID:         pm.workspaceID,
		// Carry inbound metadata so runTurn can detect a per-thread model
		// switch (FR-011). The map is copied by reference — the turn flow
		// only reads from it.
		Metadata: pm.msg.Metadata,
	}

	// ADR-085 BROWSER-FR-029: release a held browser wheel BEFORE this turn
	// begins, if and only if msg.OperatorPrompt is true (set ONLY at the
	// three operator-originated publish sites: websocket.go, sse.go,
	// channels/base.go::HandleMessage — never here, never by the bus, never
	// by the async notifier or a goal-loop follow-up). A nil hook (no
	// gateway wired — headless/test builds) is a silent no-op. See
	// browser_deferral.go for the hook's registration and the fail-closed
	// contract on OperatorPrompt itself.
	invokeBrowserWheelReleaseHookIfOperatorPrompt(pm.ctx, pm.msg, pm.transcriptSessionID)
}
