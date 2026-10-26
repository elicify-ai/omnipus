// loop_inbound.go: Turn an inbound message into a routed agent

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/constants"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/utils"
	"github.com/google/uuid"
)

// gatewayPrincipal returns the WS-authenticated gateway principal that an
// inbound message carries for audit attribution (FR-017), or "" when none.
//
// It reads ONLY bus.InboundMessage.GatewayUserID — the dedicated carrier set
// solely by the gateway webchat WS path (pkg/gateway/websocket.go, from
// wc.userID). It deliberately ignores Sender.Username: production channels
// (Telegram, Discord, IRC, Matrix, Google Chat, WeiXin) populate Sender.Username
// with the platform handle (e.g. "@alice"), which is NOT a gateway principal and
// must never be stamped as audit User. Channel/task/scheduled inbound messages
// never set GatewayUserID, so this returns "" for them — leaving audit.Entry.User
// empty structurally rather than by a runtime channel-name guard.
func gatewayPrincipal(msg bus.InboundMessage) string {
	return msg.GatewayUserID
}

// userInitiated returns msg's fail-closed origin signal (ADR-049 Gap #8/r2,
// R6) for threading onto processOptions.UserInitiated. It reads ONLY
// bus.InboundMessage.UserInitiated — set true exclusively by the gateway
// webchat WS `message` handler and by channel adapters' HandleMessage (a
// real platform sender). Every other producer of an InboundMessage
// (async-notifier, followUps re-publish, ProcessDirect/ProcessDirectWithChannel)
// leaves the field at its zero value, so this returns false for them by
// construction — mirroring gatewayPrincipal's "read the one dedicated
// carrier, never infer" discipline above.
func userInitiated(msg bus.InboundMessage) bool {
	return msg.UserInitiated
}

// ScheduledJobInfo carries the schedule/job identity that ProcessScheduled
// callers inject into the run context via WithScheduledJobContext. The
// auto-deny path reads it so the emitted audit entry names the responsible
// schedule (F-13 / O-3 observability requirement, issue #342).
type ScheduledJobInfo struct {
	JobID   string
	JobName string
}

// scheduledJobContextKey is the unexported context key for ScheduledJobInfo.
type scheduledJobContextKey struct{}

// WithScheduledJobContext returns a child context carrying the schedule
// identity. Call this in the cron fire path (pkg/gateway/schedules.go
// RunScheduled) before calling ProcessScheduled so the auto-deny audit entry
// can include the job id and name.
func WithScheduledJobContext(ctx context.Context, jobID, jobName string) context.Context {
	return context.WithValue(ctx, scheduledJobContextKey{}, ScheduledJobInfo{
		JobID:   jobID,
		JobName: jobName,
	})
}

// scheduledJobContextFrom retrieves the ScheduledJobInfo from ctx, or returns
// a zero-value struct when no info was injected (interactive / non-scheduled
// runs). The boolean reports whether info was present.
func scheduledJobContextFrom(ctx context.Context) (ScheduledJobInfo, bool) {
	v, ok := ctx.Value(scheduledJobContextKey{}).(ScheduledJobInfo)
	return v, ok
}

type continuationTarget struct {
	SessionKey string
	SessionID  string
	Channel    string
	ChatID     string
	// WorkspaceID is the workspace this continuation's turn should run inside
	// — see buildContinuationTarget's resolution comment (FIX 1 re-review).
	WorkspaceID string
}

// ErrNoContinuationTarget is a sentinel returned by buildContinuationTarget
// for messages that have no continuation target by design (e.g. the
// synthetic "system" channel). Callers use errors.Is to distinguish this
// expected no-target case from a genuine resolution failure.
var ErrNoContinuationTarget = errors.New("no continuation target for message")

func (al *AgentLoop) buildContinuationTarget(msg bus.InboundMessage) (*continuationTarget, error) {
	if msg.Channel == "system" {
		return nil, ErrNoContinuationTarget
	}

	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		return nil, err
	}

	return &continuationTarget{
		SessionKey:  resolveScopeKey(route, msg.SessionKey),
		SessionID:   msg.SessionID,
		Channel:     msg.Channel,
		ChatID:      msg.ChatID,
		WorkspaceID: al.resolveWorkspaceIDForContinuation(msg),
	}, nil
}

// resolveWorkspaceIDForContinuation resolves the workspace a steering
// continuation (Continue/continueWithSteeringMessages) should run inside
// (FIX 1 re-review). continueWithSteeringMessages previously left
// processOptions.WorkspaceID unset entirely, so a steering-continued turn's
// tool media silently degraded to the private/global room exactly like the
// other four gap sites this fix pass covers.
//
// buildContinuationTarget is called AFTER the triggering turn's own
// processMessage call has already returned (session_worker.go's runLoop) —
// msg here is session_worker's own copy of the inbound message, not the one
// processMessage mutated internally (Go passes bus.InboundMessage by value),
// so msg.SessionID reflects only what was already on the message BEFORE that
// turn ran. Two mechanisms cover this, both lifted directly from
// processMessage's own resolution (loop.go's "M4" comment, ~line 5350) rather
// than invented fresh:
//
//  1. msg.SessionID already set (always true for webchat — the gateway
//     websocket handler stamps it on the message before publish, so no
//     mutation-visibility gap applies) — resolve the session's own meta,
//     the authoritative source once a session exists.
//  2. msg.SessionID empty (the common case for a channel message that had
//     no session yet when session_worker dispatched it — processMessage
//     lazily creates one internally via resolveOrCreateChannelSession, but
//     that mutation is invisible here) — fall back to the bound channel
//     instance's own configured WorkspaceID, the exact same value
//     resolveOrCreateChannelSession itself would have seeded the new
//     session's meta with, so this independently recomputes the identical
//     answer rather than guessing.
//
// Falls back to the inbound metadata key last, matching processMessage's own
// final fallback. Returns "" (never guessed) when none of the above apply —
// e.g. an unbound channel with no session, the "system" and unrouted-message
// cases buildContinuationTarget already short-circuits above.
func (al *AgentLoop) resolveWorkspaceIDForContinuation(msg bus.InboundMessage) string {
	if msg.SessionID != "" {
		if store := al.ResolveSessionStore(msg.SessionID); store != nil {
			// FIX 1 (re-review of the re-review): a real meta-read failure
			// (corrupt meta.json, decode error, I/O error — see
			// pkg/session/unified.go's readMetaLocked) must not take the
			// identical silent path as "this session legitimately has no
			// workspace". os.ErrNotExist (no meta.json yet — a genuinely new
			// session) is the one expected, silent case; anything else is a
			// storage-integrity signal worth a WARN, matching the standard
			// the WRITE side of this same data already holds itself to
			// (pkg/gateway/schedules.go's stampScheduledSessionWorkspace).
			if meta, mErr := store.GetMeta(msg.SessionID); mErr != nil {
				if !errors.Is(mErr, os.ErrNotExist) {
					logger.WarnCF("agent",
						"continuation: could not read session meta while resolving workspace; workspace unresolved",
						map[string]any{"session_id": msg.SessionID, "error": mErr.Error()})
				}
			} else if meta != nil && meta.WorkspaceID != "" {
				return meta.WorkspaceID
			}
		}
	}
	if instanceID := inboundInstanceID(msg); instanceID != "" {
		if cfg := al.GetConfig(); cfg != nil {
			if inst, ok := cfg.Channels[instanceID]; ok && inst.WorkspaceID != "" {
				return inst.WorkspaceID
			}
		}
	}
	return inboundMetadata(msg, "workspace_id")
}

var audioAnnotationRe = regexp.MustCompile(`\[(voice|audio)(?::[^\]]*)?\]`)

// transcribeAudioInMessage resolves audio media refs, transcribes them, and
// replaces audio annotations in msg.Content with the transcribed text.
// Returns the (possibly modified) message and true if audio was transcribed.
func (al *AgentLoop) transcribeAudioInMessage(ctx context.Context, msg bus.InboundMessage) (bus.InboundMessage, bool) {
	store := al.GetMediaStore()
	if al.transcriber == nil || store == nil || len(msg.Media) == 0 {
		return msg, false
	}

	// Transcribe each audio media ref in order.
	var transcriptions []string
	for _, ref := range msg.Media {
		path, meta, err := store.ResolveWithMetaOpts(ref, media.ResolveOpts{})
		if err != nil {
			logger.WarnCF("voice", "Failed to resolve media ref", map[string]any{"ref": ref, "error": err})
			continue
		}
		if !utils.IsAudioFile(meta.Filename, meta.ContentType) {
			continue
		}
		result, err := al.transcriber.Transcribe(ctx, path)
		if err != nil {
			logger.WarnCF("voice", "Transcription failed", map[string]any{"ref": ref, "error": err})
			transcriptions = append(transcriptions, "")
			continue
		}
		transcriptions = append(transcriptions, result.Text)
	}

	if len(transcriptions) == 0 {
		return msg, false
	}

	al.sendTranscriptionFeedback(ctx, msg.Channel, msg.ChatID, msg.MessageID, transcriptions)

	// Replace audio annotations sequentially with transcriptions.
	idx := 0
	newContent := audioAnnotationRe.ReplaceAllStringFunc(msg.Content, func(match string) string {
		if idx >= len(transcriptions) {
			return match
		}
		text := transcriptions[idx]
		idx++
		return "[voice: " + text + "]"
	})

	// Append any remaining transcriptions not matched by an annotation.
	for ; idx < len(transcriptions); idx++ {
		newContent += "\n[voice: " + transcriptions[idx] + "]"
	}

	msg.Content = newContent
	return msg, true
}

// sendTranscriptionFeedback sends feedback to the user with the result of
// audio transcription if the option is enabled. It uses Manager.SendMessage
// which executes synchronously (rate limiting, splitting, retry) so that
// ordering with the subsequent placeholder is guaranteed.
func (al *AgentLoop) sendTranscriptionFeedback(
	ctx context.Context,
	channel, chatID, messageID string,
	validTexts []string,
) {
	cfg := al.GetConfig()
	if !cfg.Voice.EchoTranscription {
		return
	}
	cm := al.getChannelManager()
	if cm == nil {
		return
	}

	var nonEmpty []string
	for _, t := range validTexts {
		if t != "" {
			nonEmpty = append(nonEmpty, t)
		}
	}

	var feedbackMsg string
	if len(nonEmpty) > 0 {
		feedbackMsg = "Transcript: " + strings.Join(nonEmpty, "\n")
	} else {
		feedbackMsg = "No voice detected in the audio"
	}

	err := cm.SendMessage(ctx, bus.OutboundMessage{
		Channel:          channel,
		ChatID:           chatID,
		Content:          feedbackMsg,
		ReplyToMessageID: messageID,
	})
	if err != nil {
		logger.WarnCF("voice", "Failed to send transcription feedback", map[string]any{"error": err.Error()})
	}
}

// inferMediaType determines the media type ("image", "audio", "video", "file")
// from a filename and MIME content type.
func inferMediaType(filename, contentType string) string {
	ct := strings.ToLower(contentType)
	fn := strings.ToLower(filename)

	if strings.HasPrefix(ct, "image/") {
		return "image"
	}
	if strings.HasPrefix(ct, "audio/") || ct == "application/ogg" {
		return "audio"
	}
	if strings.HasPrefix(ct, "video/") {
		return "video"
	}

	// Fallback: infer from extension
	ext := filepath.Ext(fn)
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return "image"
	case ".mp3", ".wav", ".ogg", ".m4a", ".flac", ".aac", ".wma", ".opus":
		return "audio"
	case ".mp4", ".avi", ".mov", ".webm", ".mkv":
		return "video"
	}

	return "file"
}

func (al *AgentLoop) resolveMessageRoute(msg bus.InboundMessage) (routing.ResolvedRoute, *AgentInstance, error) {
	registry := al.GetRegistry()

	// ADR-091 identity — steered-session route pinning (2026-09-27): a
	// message addressed INTO a steered child's session runs under the child's
	// record-pinned agent (LifecycleRecord.AgentID), never the SPA's sticky
	// agent-picker dropdown, a handoff pin, or the channel cascade. The child
	// is not a chat surface that can be re-targeted: SteerLauncher.Launch
	// pinned its agent, and reconstructSteeredTurn already refuses to rebuild
	// the child's turn under any other. Live evidence this branch was
	// missing: the e2e steered-session-reachability repro routed a steer
	// typed into the child's own view as "Routed to explicit agent
	// (dropdown) agent_id=jim" into a worker-pinned child, and the resulting
	// jim turn re-executed the child's original delegate task inside the
	// child's session. Unresolvable pinned agent fails closed — falling
	// through would silently re-target the child, the exact defect this
	// branch exists to prevent.
	if pinned := al.pinnedSteeredAgentID(msg.SessionID); pinned != "" {
		agent, ok := registry.GetAgent(pinned)
		if !ok {
			return routing.ResolvedRoute{}, nil, fmt.Errorf("steered session %q is pinned to agent %q, which is not registered", msg.SessionID, pinned)
		}
		logger.InfoCF("agent", "Routed to steered session's pinned agent", map[string]any{
			"session_id": msg.SessionID,
			"agent_id":   pinned,
		})
		sk := agentSessionKey(pinned, msg)
		return routing.ResolvedRoute{AgentID: pinned, SessionKey: sk}, agent, nil
	}

	// Explicit agent_id in message metadata names the agent for a new chat.
	// For an existing session it may only name that session's canonical
	// owner (§6.4): a different agent is refused, naming both, never
	// re-targeted (CHECK F02).
	if rawExplicitID := inboundMetadata(msg, "agent_id"); rawExplicitID != "" {
		// CHECK F01: a case spelling of the system agent is the system agent.
		explicitID := strings.TrimSpace(rawExplicitID)
		if strings.EqualFold(explicitID, string(coreagent.IDOmnipus)) {
			explicitID = string(coreagent.IDOmnipus)
		}
		lookupID := explicitID
		if owner := al.existingSessionOwner(msg); owner != "" {
			// Only the WU-1 system owner keeps case-fold matching (#1330).
			omnipusOwner := explicitID == string(coreagent.IDOmnipus) && strings.EqualFold(owner, explicitID)
			if owner != explicitID && !omnipusOwner {
				logger.WarnCF("agent", "Explicit agent_id conflicts with the session owner; refused",
					map[string]any{"agent_id": explicitID, "owner": owner, "session_id": msg.SessionID})
				return routing.ResolvedRoute{}, nil, fmt.Errorf(
					"this chat belongs to %s; a message naming %s cannot run in it", owner, explicitID)
			}
			// A matching id runs under the owner's stored spelling.
			explicitID = owner
		}
		if agent, ok := registry.GetAgent(lookupID); ok {
			// A worker is a delegation-only labor tier — never a chat target,
			// and there is no default agent to fall back to (FR-120): refuse.
			if agent.IsWorker() {
				logger.WarnCF("agent", "Explicit agent_id references a worker (not a chat target); refused",
					map[string]any{"agent_id": explicitID, "session_id": msg.SessionID})
				return routing.ResolvedRoute{}, nil, fmt.Errorf("agent %q is a worker and cannot be a chat target", explicitID)
			} else {
				logger.InfoCF("agent", "Routed to explicit agent (dropdown)", map[string]any{
					"agent_id":   explicitID,
					"session_id": msg.SessionID,
					"workspace":  agent.Home,
				})
				sk := agentSessionKey(explicitID, msg)
				return routing.ResolvedRoute{AgentID: explicitID, SessionKey: sk}, agent, nil
			}
		} else {
			logger.ErrorCF("agent", "explicit agent_id not found in registry", map[string]any{
				"agent_id":       explicitID,
				"registered_ids": registry.ListAgentIDs(),
			})
			return routing.ResolvedRoute{}, nil, fmt.Errorf("the requested agent is not available")
		}
	}

	// §6.4: a message addressed to an existing session runs under that
	// session's canonical owner — never a default agent.
	if msg.SessionID != "" && msg.Channel != "system" {
		return al.routeToSessionOwner(registry, msg)
	}

	instanceID := inboundInstanceID(msg)
	identity := al.resolveInboundIdentity(instanceID)
	cfg := al.GetConfig()
	bound := false
	if identity != nil && strings.ToLower(strings.TrimSpace(identity.Kind)) == "agent" && cfg != nil {
		if inst, ok := cfg.Channels[instanceID]; ok && strings.TrimSpace(inst.WorkspaceID) != "" {
			bound = true
		}
	}
	route := registry.ResolveRoute(routing.RouteInput{
		Channel:       msg.Channel,
		AccountID:     inboundMetadata(msg, metadataKeyAccountID),
		Peer:          extractPeer(msg),
		ParentPeer:    extractParentPeer(msg),
		GuildID:       inboundMetadata(msg, metadataKeyGuildID),
		TeamID:        inboundMetadata(msg, metadataKeyTeamID),
		InstanceID:    instanceID,
		Identity:      identity,
		BoundInstance: bound,
	})
	// FR-121: resolveMessageRoute is side-effect-free (several callers resolve
	// the same message); the drop is logged, audited and shown on the
	// channel exactly once, at processMessage's rejection point.
	boundWS, intended := "", ""
	if bound {
		boundWS = strings.TrimSpace(cfg.Channels[instanceID].WorkspaceID)
		intended = strings.TrimSpace(identity.ID)
	}
	dropErr := func(reason string) *routeDropError {
		return &routeDropError{instanceID: instanceID, reason: reason, bound: bound, workspaceID: boundWS, intendedAgent: intended}
	}
	if route.Drop {
		return route, nil, dropErr(routeDropReason(route, identity))
	}
	agent, ok := registry.GetAgent(route.AgentID)
	if !ok || agent == nil {
		route.Drop = true
		return route, nil, dropErr(fmt.Sprintf("bound agent %s not found", route.AgentID))
	}
	if reason := al.boundMemberRefusal(boundWS, agent.ID); reason != "" {
		route.Drop = true
		return route, nil, dropErr(reason)
	}

	// A message addressed to a concrete chat session runs under that session's
	// own key, exactly as the pinned, explicit-agent and handoff branches above
	// already do. The routing cascade's key can be agent-level ("agent:<id>:main"),
	// which would make two chats of the same agent share one turn-registry slot
	// and one history bucket; the per-session admission keys on the session id
	// and relies on the turn registry being per session too.
	if msg.SessionID != "" {
		route.SessionKey = agentSessionKey(agent.ID, msg)
	}

	return route, agent, nil
}

func resolveScopeKey(route routing.ResolvedRoute, msgSessionKey string) string {
	if msgSessionKey != "" && strings.HasPrefix(msgSessionKey, sessionKeyAgentPrefix) {
		return msgSessionKey
	}
	return route.SessionKey
}

// agentSessionKey builds the per-agent session key combining agentID with the
// message's scope bucket. Uses session-scoped format when SessionID is known;
// falls back to chat-scoped format for channels that haven't minted a session.
//
// For channel inbound, the chat-scoped key uses msg.InstanceID when non-empty
// (ADR-029 FR-023, MAJ-002): two instances of the same channel type (e.g.
// "whatsapp.eu" and "whatsapp.us") with the same ChatID must NOT share a
// transcript key. Legacy channels that have not yet been updated to stamp
// InstanceID fall back to msg.Channel (the type), preserving existing behavior.
func agentSessionKey(agentID string, msg bus.InboundMessage) string {
	if msg.SessionID != "" {
		return fmt.Sprintf("agent:%s:session:%s", agentID, msg.SessionID)
	}
	// Use the stamped InstanceID when available (per-instance isolation);
	// fall back to the channel type for adapters that have not yet been updated.
	instanceOrChannel := msg.Channel
	if msg.InstanceID != "" {
		instanceOrChannel = msg.InstanceID
	}
	return fmt.Sprintf("agent:%s:chat:%s:%s", agentID, instanceOrChannel, msg.ChatID)
}

func (al *AgentLoop) resolveSteeringTarget(msg bus.InboundMessage) (string, string, bool) {
	if msg.Channel == "system" {
		return "", "", false
	}

	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil || agent == nil {
		return "", "", false
	}

	// Per-session worker scope: when msg.SessionID is set, append it so each
	// session for the same agent gets its OWN worker goroutine. Without this,
	// the routing layer's SessionKey collapses to "agent:<id>:<id>" for all
	// channels that haven't explicitly carried a session_key, and every
	// session for a given agent ends up sharing one worker — reintroducing
	// the serialization regression the per-session-worker design exists to fix.
	scope := resolveScopeKey(route, msg.SessionKey)
	if msg.SessionID != "" {
		scope = scope + ":" + msg.SessionID
	}
	return scope, agent.ID, true
}

func (al *AgentLoop) processSystemMessage(
	ctx context.Context,
	msg bus.InboundMessage,
) (string, error) {
	if msg.Channel != "system" {
		return "", fmt.Errorf(
			"processSystemMessage called with non-system message channel: %s",
			msg.Channel,
		)
	}
	if msg.AsyncTranscriptSessionID != "" && inboundMetadata(msg, "steer_message_id") != "" {
		return al.processSteeredSystemWake(ctx, msg)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]any{
			"sender_id": msg.Sender.CanonicalID,
			"chat_id":   msg.ChatID,
		})

	// Parse origin channel from chat_id (format: "channel:chat_id")
	var originChannel, originChatID string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
		originChatID = msg.ChatID[idx+1:]
	} else {
		originChannel = "cli"
		originChatID = msg.ChatID
	}

	// Extract subagent result from message content
	// Format: "Task 'label' completed.\n\nResult:\n<actual content>"
	content := msg.Content
	if idx := strings.Index(content, "Result:\n"); idx >= 0 {
		content = content[idx+8:] // Extract just the result part
	}

	// Skip internal channels - only log, don't send to user
	if constants.IsInternalChannel(originChannel) {
		logger.InfoCF("agent", "Subagent completed (internal channel)",
			map[string]any{
				"sender_id":   msg.Sender.CanonicalID,
				"content_len": len(content),
				"channel":     originChannel,
			})
		return "", nil
	}

	agent, discarded, err := al.resolveSystemMessageAgent(msg)
	if err != nil {
		return "", err
	}
	if discarded {
		return "", nil
	}

	transcriptSessionID, transcriptStore := al.resolveSystemMessageTranscript(msg)

	// SEC-1 (ADR-093 round 2, founder Q88): an AUTOMATIC wake — a system
	// message with no steer_message_id and no new human instruction — never
	// resumes a STOPPED root. The D2 launch-backstop removal left this path
	// able to start a fresh turn on the dead generation, and that turn could
	// launch and dispatch unstamped helpers under the stopped parent — beyond
	// Q1's accepted in-flight-launch race. What restarts a stopped
	// conversation is a NEW HUMAN instruction (founder case 6), never a wake
	// (MIN-004 / the 2026-10-06 stopped-root rule). A TERMINAL record is not
	// stopped: completed and failed roots keep receiving their wakes, which
	// run without reviving the record exactly as before. An unreadable record
	// refuses the wake too (fail-closed, same posture as the fence check) —
	// a missing record is the ordinary no-record case and passes.
	if transcriptSessionID != "" {
		if store := al.GetSessionLifecycleStore(); store != nil {
			rec, loadErr := store.Load(transcriptSessionID)
			switch {
			case loadErr == nil:
				if rec.State == session.LifecycleStopped || lifecycleInFlightStopFence(rec) {
					logger.WarnCF("agent", "adr093: automatic wake into a stopped conversation refused — a wake never resumes a stopped root; a new human instruction restarts the conversation",
						map[string]any{"session_id": transcriptSessionID, "state": string(rec.State), "chat_id": msg.ChatID})
					return "", fmt.Errorf("adr093: automatic wake into stopped session %q refused (a new human instruction restarts the conversation)", transcriptSessionID)
				}
			case errors.Is(loadErr, session.ErrLifecycleNotFound):
				// No record: nothing stopped, nothing to refuse.
			default:
				logger.ErrorCF("agent", "adr093: automatic wake dropped — the conversation's lifecycle record could not be read",
					map[string]any{"session_id": transcriptSessionID, "error": loadErr.Error()})
				return "", fmt.Errorf("adr093: automatic wake into session %q refused: read lifecycle record: %w", transcriptSessionID, loadErr)
			}
		}
	}

	// A-I4 round 6, Priority 2: scope the reconstructed turn's SessionKey to
	// the SPECIFIC originating session (mirroring agentSessionKey's
	// "agent:<id>:session:<sid>" convention every regular routed turn
	// already uses — pkg/agent/loop.go:4794) rather than the agent-wide
	// routing.BuildAgentMainSessionKey "agent:<id>:main" bucket this used to
	// hard-code unconditionally.
	//
	// SessionKey drives THREE things that must never be shared across
	// unrelated originating sessions: (1) agent.Sessions.GetHistory/
	// SetHistory/Save — the in-memory LLM conversation history this turn's
	// prompt is built from (loop.go:5415, 6223, 6367, 7819, 7944, 8084,
	// 8254, 8464, 8949-8951); (2) activeTurnStates registration/lookup; and
	// (3) the per-scope sessionWorker (pkg/agent/session_worker.go) that
	// serializes turns and steers same-scope late-arriving messages into an
	// ALREADY-RUNNING turn rather than starting a new one.
	//
	// Root cause this closes (A-I4 round 6 Priority 2 cross-session leak,
	// live-verified 2026-07): every delegate-completion notification for the
	// SAME agent — regardless of which real chat session originated the
	// underlying delegate/background work — used to collapse onto the ONE
	// "agent:<id>:main" key. Two delegate completions for the same agent
	// landing close together (routine under normal use — an orchestrator
	// like Jim commonly has several concurrent background delegates) then
	// shared: the same in-memory history bucket (so the second notify-turn's
	// LLM prompt was contaminated with the first, unrelated notify-turn's
	// conversation, producing a recap that blends or hallucinates facts from
	// a DIFFERENT session's delegation — reproduced live: a session that
	// only ever delegated to "Ava" received a persisted assistant turn
	// narrating a nonexistent "delegation to Ray" pulled from an unrelated
	// session's exchange); and the same sessionWorker scope (so the second
	// notify-turn could be STEERED into the first's still-running turn as a
	// mid-turn continuation instead of running as its own turn — see
	// session_worker.go's enqueue doc comment — further blending two
	// unrelated sessions' content into one LLM call).
	//
	// TranscriptSessionID/TranscriptStore (already correctly scoped by FIX
	// 5d above) only controls WHERE the turn's output is persisted — it does
	// nothing to isolate WHAT content that turn's own LLM call is built
	// from. Both must agree for one session's recap to never see another
	// session's data. Falls back to the unscoped main key only when no
	// origin session is known (a system message with no AsyncNotifier
	// origin), matching the pre-fix behavior for that narrower case.
	//
	// DEL-10 step 0: a system message with NO origin session no longer borrows an
	// agent-wide "agent:<id>:main" history bucket (in the one shared store that
	// name would be a session directory literally called "agent:<id>:main", and
	// ":" is not a legal Windows file name character). It runs without session
	// history, under a one-off scope key that is never persisted.
	noHistory := transcriptSessionID == ""
	sessionKey := fmt.Sprintf("agent:%s:system:%s", agent.ID, uuid.NewString())
	if !noHistory {
		sessionKey = fmt.Sprintf("agent:%s:session:%s", agent.ID, transcriptSessionID)
	}

	workspaceID := al.resolveSystemMessageWorkspaceID(msg, transcriptStore, transcriptSessionID)

	return al.runAgentLoop(ctx, agent, processOptions{
		SessionKey: sessionKey,
		Channel:    originChannel,
		ChatID:     originChatID,
		// ADR-088 D6b: mirrors processMessage's own SenderID threading
		// (msg.Sender.CanonicalID, above in this file) — without it, EVERY
		// system-channel-dispatched turn (not just the goal loop's) reaches
		// checkGoalLoopAfterTurn's origin gate with opts.SenderID always
		// empty, regardless of what Sender.CanonicalID the producer stamped
		// on the bus message. This is what let the goal-loop's idle-steer/
		// nudge/continue-push turns (pkg/agent/goal_triggers.go's
		// dispatchGoalAsyncFollowUp, which stamps
		// AsyncNotifyEvent.SenderCanonicalID = goalLoopFollowUpSenderID) be
		// silently dropped at the gate even after that stamping fix —
		// discovered while regression-testing D6b (goal_keeper_repairs_test.go's
		// TestKeeper_SenderGateUnwedged_TwoFullIdleCycles).
		SenderID:             msg.Sender.CanonicalID,
		UserMessage:          fmt.Sprintf("[System: %s] %s", msg.Sender.CanonicalID, msg.Content),
		DefaultResponse:      "Background task completed.",
		SendResponse:         true,
		SuppressToolFeedback: true,
		TranscriptSessionID:  transcriptSessionID,
		TranscriptStore:      transcriptStore,
		WorkspaceID:          workspaceID,
		NoHistory:            noHistory,
	})
}

// resolveSystemMessageAgent resolves the TRUE originating agent for a system
// message, extracted from processSystemMessage to keep it under the
// founder's function-size budget (scripts/budgets/functions.txt) with no
// behavior change.
//
// FIX 5d (#1): resolve the TRUE originating agent when the message carries
// one (AsyncNotifier.Notify sets AsyncOriginAgentID for an async tool/
// delegate result). A message with no async origin at all is discarded with
// an error log and an audit entry (omnipus-system-agent FR-131): there is no
// default agent to guess.
//
// UAT E-3: a NAMED origin that no longer resolves (the agent was deleted)
// is NOT re-homed onto the default agent any more. That fallback handed a
// deleted agent's goal-keeper push to Mia, who then worked and parked a
// goal that was never hers, delegating real work in the process — the
// same "no inheritance" identity violation ADR-032 forbids for delegation.
// The result is discarded, loudly: a WARN naming the missing agent, and a
// system note in the originating session so the user sees that a
// background update was dropped and why.
//
// discarded=true means the message was fully handled here (or could not be
// routed to any agent, but is not itself an error — the caller returns
// ("", nil) either way).
func (al *AgentLoop) resolveSystemMessageAgent(msg bus.InboundMessage) (agent *AgentInstance, discarded bool, err error) {
	if msg.AsyncOriginAgentID != "" {
		named, ok := al.GetRegistry().GetAgent(msg.AsyncOriginAgentID)
		if !ok || named == nil {
			logger.WarnCF(
				"agent",
				"processSystemMessage: async origin agent no longer exists; background result discarded rather than handed to another agent",
				map[string]any{
					"agent_id":   msg.AsyncOriginAgentID,
					"sender_id":  msg.Sender.CanonicalID,
					"session_id": msg.AsyncTranscriptSessionID,
				},
			)
			if msg.AsyncTranscriptSessionID != "" {
				if store := al.ResolveSessionStore(msg.AsyncTranscriptSessionID); store != nil {
					now := time.Now().UTC()
					if werr := store.AppendTranscriptStrict(msg.AsyncTranscriptSessionID, session.TranscriptEntry{
						ID:   fmt.Sprintf("async-origin-missing-%s-%d", msg.AsyncTranscriptSessionID, now.UnixNano()),
						Type: session.EntryTypeSystem,
						Role: "system",
						Content: fmt.Sprintf(
							"A background update for agent %q was not delivered: that agent no longer exists, "+
								"so the update was discarded instead of being handed to a different agent.",
							msg.AsyncOriginAgentID),
						Timestamp: now,
					}); werr != nil {
						logger.WarnCF("agent", "processSystemMessage: could not record the discarded-update note in the session",
							map[string]any{"session_id": msg.AsyncTranscriptSessionID, "error": werr.Error()})
					}
				} else {
					logger.WarnCF("agent", "processSystemMessage: discarded update's session not found; no note written",
						map[string]any{"session_id": msg.AsyncTranscriptSessionID})
				}
			}
			return nil, true, nil
		}
		agent = named
	}
	if agent == nil {
		// FR-131 / §6.4: no addressable origin — discard loudly, never hand
		// the message to a default agent.
		al.recordSystemMessageDiscard(msg)
		return nil, true, nil
	}
	return agent, false, nil
}

// systemMessageNoOriginReason is the visible discard reason for a system
// message with no addressable originating agent (FR-131).
const systemMessageNoOriginReason = "system message without an addressable agent"

// recordSystemMessageDiscard logs the discard at error level and audits it once.
func (al *AgentLoop) recordSystemMessageDiscard(msg bus.InboundMessage) {
	logger.ErrorCF("agent", "System message discarded: "+systemMessageNoOriginReason, map[string]any{
		"channel": msg.Channel, "chat_id": msg.ChatID, "sender_id": msg.Sender.CanonicalID,
		"reason": systemMessageNoOriginReason,
	})
	if al.auditLogger == nil {
		return
	}
	if err := al.auditLogger.Log(&audit.Entry{
		Event:    audit.EventSystemMessageDiscarded,
		Decision: audit.DecisionDeny,
		Details:  map[string]any{"channel": msg.Channel, "chat_id": msg.ChatID, "reason": systemMessageNoOriginReason},
	}); err != nil {
		logger.ErrorCF("agent", "audit write failed for a discarded system message",
			map[string]any{"error": err.Error()})
	}
}

// resolveSystemMessageTranscript resolves the originating turn's transcript
// session/store, extracted from processSystemMessage to keep it under the
// founder's function-size budget with no behavior change.
//
// FIX 5d (#2): resolve the originating turn's transcript session/store so
// this reconstructed turn persists into the SAME session the producing
// turn was writing to — the same "run a turn that must land in a
// specific, pre-existing session" pattern ProcessScheduled (loop.go) and
// steer_reconstruct.go::reconstructSteeredTurn already use: resolve the
// store, then thread processOptions.TranscriptSessionID and
// .TranscriptStore together. (This used to cite `spawnSubTurn`, which
// died with subturn.go and has zero definitions today.) Without this,
// persistence depended ENTIRELY on a live WebSocket connection still
// being open when the async result landed — if it had already closed,
// the result was silently, permanently lost. A session ID that no longer
// resolves to a store (deleted session) degrades to "not persisted" —
// the same outcome as before this fix, not a new failure mode.
func (al *AgentLoop) resolveSystemMessageTranscript(msg bus.InboundMessage) (transcriptSessionID string, transcriptStore *session.UnifiedStore) {
	if msg.AsyncTranscriptSessionID != "" {
		if store := al.ResolveSessionStore(msg.AsyncTranscriptSessionID); store != nil {
			transcriptSessionID = msg.AsyncTranscriptSessionID
			transcriptStore = store
		} else {
			logger.WarnCF(
				"agent",
				"processSystemMessage: async transcript session not found; result will not be persisted to a session",
				map[string]any{"session_id": msg.AsyncTranscriptSessionID},
			)
		}
	}
	return transcriptSessionID, transcriptStore
}

// resolveSystemMessageWorkspaceID resolves the workspace a reconstructed
// system-message turn should inherit, extracted from processSystemMessage
// to keep it under the founder's function-size budget with no behavior
// change.
//
// FIX 1 (re-review): mirror processMessage's WorkspaceID resolution
// (loop_process_message.go::agentLoopProcessMessage.resolveWorkspace, the
// "M4" comment) so a delegate-completion / async-
// notify turn reconstructed here also stamps bus.OutboundMediaMessage
// with the real workspace instead of silently falling back to the
// private/global room. The session this turn persists into
// (transcriptSessionID, already resolved by resolveSystemMessageTranscript,
// FIX 5d) is the authoritative source: it is the SAME session the producing
// turn wrote to, so its meta.WorkspaceID (stamped at session-creation time
// via resolveOrCreateChannelSession's channel-binding lookup) is exactly
// the workspace this reconstructed turn should inherit. Falls back to the
// inbound metadata key, matching processMessage's own final fallback, for
// callers that stamp workspace_id directly on the system message instead.
func (al *AgentLoop) resolveSystemMessageWorkspaceID(msg bus.InboundMessage, transcriptStore *session.UnifiedStore, transcriptSessionID string) string {
	workspaceID := ""
	if transcriptStore != nil && transcriptSessionID != "" {
		// FIX 1 (re-review of the re-review): distinguish a real meta-read
		// failure from "no workspace bound" — see
		// loop_inbound.go::AgentLoop.resolveWorkspaceIDForContinuation's doc
		// comment (this same file, near the top) for the full rationale.
		if meta, mErr := transcriptStore.GetMeta(transcriptSessionID); mErr != nil {
			if !errors.Is(mErr, os.ErrNotExist) {
				logger.WarnCF("agent",
					"delegate-completion: could not read session meta while resolving workspace; workspace unresolved",
					map[string]any{"session_id": transcriptSessionID, "error": mErr.Error()})
			}
		} else if meta != nil {
			workspaceID = meta.WorkspaceID
		}
	}
	if workspaceID == "" {
		workspaceID = inboundMetadata(msg, "workspace_id")
	}
	return workspaceID
}

// processSteeredSystemWake reconstructs a durable steering-session turn from
// its lifecycle record. Ordinary inbox wakes use their consumed marker;
// secret-only direct-bus wakes use the mandatory request-id claim instead.
func (al *AgentLoop) processSteeredSystemWake(ctx context.Context, msg bus.InboundMessage) (resp string, runErr error) {
	sessionID := msg.AsyncTranscriptSessionID
	messageID := inboundMetadata(msg, "steer_message_id")
	defer al.releaseWakePublication(sessionID, messageID)
	generation, err := strconv.Atoi(inboundMetadata(msg, "steer_generation"))
	if err != nil || generation < 0 {
		return "", fmt.Errorf("steer: wake %q has invalid generation", messageID)
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return "", fmt.Errorf("steer: wake: lifecycle store is not configured")
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return "", fmt.Errorf("steer: wake: load %q: %w", sessionID, err)
	}
	if al.steering != nil && !rec.Terminal() {
		al.steering.reopenScopeForGeneration(sessionID, generation)
	}
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		return "", fmt.Errorf("steer: wake: transcript store for %q is not available", sessionID)
	}
	rec, generation, secretHandled, secretErr := al.prepareSecretSteeredWake(ctx, &msg, rec, generation)
	if secretErr != nil || secretHandled {
		return "", secretErr
	}
	secretID := inboundMetadata(msg, secretResumeMetadataKey)
	marker := ""
	if secretID == "" {
		marker = "consumed " + messageID
	}
	entries, readErr := store.ReadTranscript(sessionID)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("steer: wake: read transcript: %w", readErr)
	}
	for _, entry := range entries {
		if (secretID == "" && entry.Content == marker) || (secretID != "" && entry.ID == secretrequest.ResumeEntryID(secretID)) {
			if secretID == "" {
				if inbox := al.GetMessageInboxStore(); inbox != nil {
					_ = inbox.Ack(sessionID, []string{messageID})
				}
			}
			return "", nil
		}
	}

	saved, consumed, savedErr := al.prepareSavedSystemWake(ctx, sessionID, messageID, rec.AgentID)
	if savedErr != nil || consumed {
		return "", savedErr
	}
	if held, holdErr := al.savedWakeHeldForQuestion(sessionID, messageID, saved); held || holdErr != nil {
		return "", holdErr
	}

	// D6/D7: a stopped session (landed, or with its current-generation Stop
	// in flight) does no compute until it is explicitly resumed. The wake
	// is neither consumed nor acknowledged: an inbox-backed report stays
	// pending. Goal follow-ups may have no inbox row, so their unretained
	// content must be operator-visible without logging the body. Steered
	// records are also refused by the admission reservation below.
	if rec.SteeredBy == nil && (rec.State == session.LifecycleStopped || lifecycleInFlightStopFence(rec)) {
		return "", al.logStoppedSystemWake(msg)
	}

	ts, err := al.reconstructSteeredTurn(rec, &steer.WakeInput{MessageID: messageID, Generation: generation})
	if err != nil {
		return "", err
	}

	// A wake is an ENTRY PATH, so it owes the same two checks
	// steer_launcher.go::dispatchSteeredSessionWithReservation owes, in the
	// same order and through the same primitives:
	//
	//   - I-3 "Admission": the concurrency counter counts TURNS EXECUTING
	//     RIGHT NOW (D9; founder round 9, "live turns only"). A woken turn
	//     that never called tryAdmit ran entirely outside the gate, so
	//     max_parallel_agents counted dispatches rather than work.
	//   - I-6's reservation: commitSteeredExecutionState re-runs
	//     reserveDispatch against the record's LIVE tail inside one atomic
	//     read-modify-write. steer_audience.go::Deliver's FR-B-013 check
	//     reads the recipient's record at SEND time; a Stop landing between
	//     that read and this turn was caught nowhere, and the stopped
	//     session went back to work.
	//
	// Both are scoped to STEERED sessions — steerAdmission's own population
	// (admission.go) and the one I-3 governs. An ordinary root woken here is
	// a human's own chat, not a delegated worker; it is neither gated nor
	// state-written by this path.
	release := func() {}
	gate := al.steerAdmission()
	entryLocked := false
	defer func() {
		if entryLocked {
			gate.entryMu.Unlock()
		}
	}()
	var claim executionClaim
	if rec.SteeredBy != nil {
		gate.entryMu.Lock()
		entryLocked = true
		// Another distinct wake for this queued admission is input, not a new
		// owner. Keep the queue locked through the claim-bound append so a
		// promotion cannot consume the pending list before this input lands.
		gate.mu.Lock()
		for i := range gate.queue {
			entry := &gate.queue[i]
			queuedClaim := entry.executionClaim()
			if entry.generation == generation && queuedClaim.matches(rec) {
				appendErr := al.commitQueuedSystemWakeLocked(lifecycle, entry, generation, messageID, msg.Content, rec.AgentID, saved)
				gate.mu.Unlock()
				return "", appendErr
			}
		}
		gate.mu.Unlock()
		if duplicateErr := al.checkNewAdmission(rec); duplicateErr != nil {
			return "", duplicateErr
		}
		// D2 round-4 R4-MAJ-001: this wake is an admission, so it mints its
		// run identity and stamps it durably BEFORE the gate enqueues it —
		// the same order dispatchSteeredSessionWithReservation owes. A
		// promotion validates this wake's SAME durable id; it never invents
		// a new owner for a queued admission.
		wakeRunID := freshRunID()
		wakeBootSeq := al.bootEpochFor()
		if stampErr := stampAdmissionExecution(lifecycle, sessionID, generation, wakeRunID, wakeBootSeq, rec.ExecutionID); stampErr != nil {
			return "", fmt.Errorf("steer: wake: %w", stampErr)
		}
		if identityErr := ts.setExecutionIdentity(wakeRunID, wakeBootSeq); identityErr != nil {
			return "", fmt.Errorf("steer: wake: %w", identityErr)
		}
		claim = executionClaim{SessionID: sessionID, Generation: generation, RunID: wakeRunID, BootSeq: wakeBootSeq}
		admitted, _, _ := gate.tryAdmitRun(sessionID, generation, wakeRunID, wakeBootSeq)
		if !admitted {
			// At the cap. Do not record a consumed marker or acknowledge any
			// inbox entry yet; this wake must reach the promoted turn. System
			// wakes need not have an inbox entry, so append their content to
			// the lifecycle record in the same durable mutation that queues
			// the session. The promotion consumes the whole ordered list before
			// starting; otherwise it would replay the launch instruction or
			// lose an earlier wake when a second one arrives.
			if commitErr := al.commitQueuedSystemWakeInput(lifecycle, claim, generation, messageID, msg.Content, rec.AgentID, saved); commitErr != nil {
				gate.removeQueuedExecution(claim)
				return "", commitErr
			}
			return "", nil
		}
		release = func() { al.drainSteerQueue(claim) }
	}

	// F2: an ordinary root woken by a hand-back runs as an admitted
	// ordinary execution (fresh identity, running state, its own
	// disposition), like any other entry, so a Stop selects and lands it. A
	// finished root starts its next round; a stopped one was held above.
	var rootExecution *executionDisposition
	if rec.SteeredBy == nil {
		d, admitted, admitErr := al.admitOrdinaryRootWake(ctx, msg, ts)
		if admitErr != nil {
			return "", admitErr
		}
		if admitted {
			rootExecution = d
			// The canonical consuming session owns the reminder on both worker
			// and direct replay entry. Run after release/checked settlement.
			defer al.remindUnansweredRequests(sessionID)
			defer func() { runErr = errors.Join(runErr, al.finishRoomTurn(ts.opts.roomCost, runErr)) }()
			release = func() { al.finishOrdinaryWakeExecution(msg, d) }
		}
	}

	ts.opts.UserMessage = msg.Content
	ts.userMessage = msg.Content
	// The goal loop's own follow-up (keeper reminder, deferred steer) keeps
	// its origin, so the woken turn's claim reaches the goal loop (D1).
	if msg.Sender.CanonicalID == goalLoopFollowUpSenderID {
		ts.opts.SenderID = goalLoopFollowUpSenderID
	}
	if !al.registerTurnIfAbsent(ts) {
		release()
		return "", steer.ErrStaleGeneration
	}
	if rec.SteeredBy != nil {
		if attachErr := al.attachSteeredDisposition(ts, claim); attachErr != nil {
			al.activeTurnStates.CompareAndDelete(sessionID, ts)
			release()
			return "", attachErr
		}
		release = func() { _ = al.finishExecutionDisposition(ts.opts.executionDisposition) }
	}
	if entryLocked {
		gate.entryMu.Unlock()
		entryLocked = false
	}
	if turnRegisteredTestHook != nil {
		turnRegisteredTestHook(sessionID, ts)
	}
	abort := func() {
		al.activeTurnStates.CompareAndDelete(sessionID, ts)
		release()
	}
	if rec.SteeredBy != nil {
		if dispatchStateWriteTestHook != nil {
			dispatchStateWriteTestHook(sessionID, generation)
		}
		running, commitErr := commitSteeredExecutionState(lifecycle, claim, session.LifecycleRunning, "")
		if commitErr != nil {
			abort()
			return "", commitErr
		}
		// commitSteeredExecutionState returns post-write truth (edge, goal
		// reference, created-at) — the same reason runDispatchedSteeredTurn
		// (steer_launcher.go) uses its own commit's return value rather than
		// its stale pre-write snapshot for the completion disposition below.
		rec = running
	}

	if proceed, consumeErr := al.consumeAdmittedWake(ctx, ts, store, sessionID, messageID, secretID, marker, rec.AgentID, saved); consumeErr != nil || !proceed {
		abort()
		return "", consumeErr
	}
	defer release()

	// Finding B (ADR-091 fix lane 1, CRITICAL, latent): this wake is an EXIT
	// path exactly like runDispatchedSteeredTurn's dispatch goroutine — it
	// owes the SAME post-turn disposition (completeSteeredTurn/
	// finishSteeredGoalTurn), or a successfully woken session ends its turn
	// still marked `running` forever (nothing else ever calls completion for
	// it), stalling its parent's quiet-subtree check indefinitely. No
	// deadline is applied to this re-entry: a steered session has no time
	// limit (issue #1306).
	runCtx := ctx
	// Same body dispatch as the first-run path: an external-CLI target is
	// driven through the shared runner on a wake too, never the native loop.
	result, err := al.runSteeredTurnBody(runCtx, rec, ts)
	ts, result, err = al.drainSteeredTurn(runCtx, rec, ts, result, err)
	al.disposeSteeredTurnResult(ts, rec, generation, result, err)
	rootExecution.recordTurnOutcome(err)
	return result.finalContent, err
}

// consumeAdmittedWake is processSteeredSystemWake's consumption step, run
// only once admission is certain. A secret-only wake has no inbox row: its
// successful request-id claim is the sole consumption boundary, and redundant
// writes after it could strand an already-claimed request before the body.
// Ordinary wakes (and saved wake inputs) still owe their one marker and
// acknowledgement. proceed=false with a nil error means a secret wake whose
// claim was already taken: stop without running the turn.
func (al *AgentLoop) consumeAdmittedWake(ctx context.Context, ts *turnState, store *session.UnifiedStore,
	sessionID, messageID, secretID, marker, agentID string, saved bool,
) (proceed bool, err error) {
	claimed, err := al.claimAdmittedSecretWake(secretID, sessionID, ts)
	if err != nil || !claimed {
		return false, err
	}
	if secretID != "" {
		return true, nil
	}
	if err := al.consumeSteeredWake(ctx, ts, store, sessionID, messageID, marker, agentID, saved); err != nil {
		return false, err
	}
	return true, nil
}

// consumeSteeredWake writes the wake's one durable consumption marker and its
// inbox acknowledgement, only once the turn is certain to run: a wake refused
// earlier must leave its entry pending for I-6's Revive (FR-B-013), never
// swallow it. A saved wake input is placed here, and its placement writes
// that same marker and ack, so one logical event keeps exactly one marker.
func (al *AgentLoop) consumeSteeredWake(ctx context.Context, ts *turnState, store *session.UnifiedStore,
	sessionID, messageID, marker, agentID string, saved bool,
) error {
	if saved {
		return al.placeSavedWakeAtEntry(ctx, ts, messageID, saved)
	}
	if err := store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID: "consumed-" + messageID, Type: session.EntryTypeSystem, Role: "system",
		Content: marker, AgentID: agentID, Timestamp: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("steer: wake: append consumed marker: %w", err)
	}
	if inbox := al.GetMessageInboxStore(); inbox != nil {
		if err := inbox.Ack(sessionID, []string{messageID}); err != nil {
			return fmt.Errorf("steer: wake: acknowledge %q: %w", messageID, err)
		}
	}
	return nil
}

// admitOrdinaryRootWake admits the wake of an ordinary root as an ordinary
// execution and binds ts to it (F2). It returns nil when no lifecycle store
// is wired (admitted false: nothing to admit).
func (al *AgentLoop) admitOrdinaryRootWake(ctx context.Context, msg bus.InboundMessage, ts *turnState) (d *executionDisposition, admitted bool, err error) {
	preparation, err := al.prepareOrdinarySessionExecution(ctx, msg.AsyncTranscriptSessionID, ts.opts, &handbackRevivalPrincipal)
	if err != nil {
		return nil, false, fmt.Errorf("steer: wake: %w", err)
	}
	d = preparation.execution
	if d == nil {
		return nil, false, nil
	}
	ts.opts.executionDisposition = d
	ts.opts.roomCost = preparation.roomCost
	ts.generation = d.claim.Generation
	if idErr := ts.setExecutionIdentity(d.claim.RunID, d.claim.BootSeq); idErr != nil {
		al.finishOrdinaryWakeExecution(msg, d)
		return nil, false, fmt.Errorf("steer: wake: %w", idErr)
	}
	return d, true, nil
}

// finishOrdinaryWakeExecution settles an ordinary root's wake execution and
// makes a settlement failure visible.
func (al *AgentLoop) finishOrdinaryWakeExecution(msg bus.InboundMessage, d *executionDisposition) {
	if err := al.finishExecutionDisposition(d); err != nil {
		al.reportOrdinarySettlementFailure(msg, err)
	}
}

// handbackRevivalPrincipal starts the next round of a finished ordinary root.
// It never continues a stopped root, including stopped(restart): that authority
// belongs to a human message or a normal scheduled/heartbeat trigger. Replayed
// helper reports stay in the inbox for that next turn, not boot-time compute.
var handbackRevivalPrincipal = steer.Principal{Kind: steer.PrincipalKindAgent, ID: "handback"}

// extractPeer extracts the routing peer from the inbound message's structured Peer field.
func extractPeer(msg bus.InboundMessage) *routing.RoutePeer {
	if msg.Peer.Kind == "" {
		return nil
	}
	peerID := msg.Peer.ID
	if peerID == "" {
		if msg.Peer.Kind == "direct" {
			peerID = msg.Sender.CanonicalID
		} else {
			peerID = msg.ChatID
		}
	}
	return &routing.RoutePeer{Kind: string(msg.Peer.Kind), ID: peerID}
}

func inboundMetadata(msg bus.InboundMessage, key string) string {
	if msg.Metadata == nil {
		return ""
	}
	return msg.Metadata[key]
}

// inboundInstanceID returns the channel-instance key a message arrived on
// (Spec-2 FR-2.5, ADR-029 FR-023/MAJ-002).
//
// Source priority:
//  1. msg.InstanceID — always stamped by the trusted BaseChannel adapter.
//  2. msg.Channel    — legacy fallback for single-instance adapters that have
//     not yet been updated to stamp InstanceID.
//
// The metadata["instance_id"] fallback that existed here has been removed
// (S-4 / security review 2026-07-02): msg.InstanceID is now the authoritative
// source stamped by the trusted adapter, and the Metadata map is
// content-adjacent (caller-controlled), making it a spoofing footgun.
// No adapter writes metadata["instance_id"] (confirmed by grep of pkg/channels/).
//
// The result is lower-cased to match the config map keys.
func inboundInstanceID(msg bus.InboundMessage) string {
	if id := strings.TrimSpace(msg.InstanceID); id != "" {
		return strings.ToLower(id)
	}
	return strings.ToLower(strings.TrimSpace(msg.Channel))
}

// resolveInboundIdentity returns the persisted routing identity for the channel
// instance a message arrived on (Spec-2 US-5 / FR-2.9), or nil when the instance
// has no identity override configured. The identity selects how an inbound
// message is attributed/routed: kind "agent" binds the connection to a specific
// agent; kind "user" (or no identity) leaves the normal binding cascade in
// effect. Returns a copy so the caller never mutates the live config.
func (al *AgentLoop) resolveInboundIdentity(instanceID string) *config.ChannelIdentity {
	if instanceID == "" {
		return nil
	}
	cfg := al.GetConfig()
	if cfg == nil || cfg.Channels == nil {
		return nil
	}
	inst, ok := cfg.Channels[instanceID]
	if !ok || inst.Identity == nil {
		return nil
	}
	id := *inst.Identity
	return &id
}

// extractParentPeer extracts the parent peer (reply-to) from inbound message metadata.
func extractParentPeer(msg bus.InboundMessage) *routing.RoutePeer {
	parentKind := inboundMetadata(msg, metadataKeyParentPeerKind)
	parentID := inboundMetadata(msg, metadataKeyParentPeerID)
	if parentKind == "" || parentID == "" {
		return nil
	}
	return &routing.RoutePeer{Kind: parentKind, ID: parentID}
}
