package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func (ts *turnState) appendFinalResponse(content string, response *providers.LLMResponse, systemNotice bool) error {
	if systemNotice {
		return ts.agent.Sessions.AddMessage(ts.sessionKey, "system", content)
	}
	return ts.appendFinalModelResponse(content, response)
}

// recordIterationLimitNotice uses the existing chat-only system entry and
// replay-message frame. It has its own identity and no model or model usage:
// the provider's tool rounds already carry their own accounting and narration.
func (ts *turnState) recordIterationLimitNotice(content string, messageBus *bus.MessageBus) error {
	if ts.transcriptStore == nil && ts.transcriptSessionID == "" {
		return nil // Deliberately transcript-less turn.
	}
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" {
		return fmt.Errorf("iteration limit notice: transcript store and session ID are both required")
	}
	entry := session.TranscriptEntry{
		ID: uuid.NewString(), Type: session.EntryTypeSystem, Role: "system",
		Content: content, Timestamp: time.Now().UTC(), TurnID: ts.turnID,
		AgentID: ts.resolveActiveAgentID(), GoalID: ts.goalID,
		ViewMembership: session.ViewMembershipChat,
	}
	if err := ts.transcriptStore.AppendTranscriptStrict(ts.transcriptSessionID, entry); err != nil {
		return fmt.Errorf("iteration limit notice: persist system entry: %w", err)
	}
	ts.mu.RLock()
	streamer := ts.lastStreamer
	ts.mu.RUnlock()
	// A provider without token streaming still needs the same live system
	// frame, rather than letting the channel's Send fallback invent tokens.
	if streamer == nil && messageBus != nil {
		ctx := ts.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if terminalStreamer, ok := messageBus.GetStreamer(ctx, ts.opts.Channel, ts.opts.ChatID, ts.transcriptSessionID); ok {
			streamer = terminalStreamer
			ts.setLastStreamer(streamer)
			ts.stampStreamerTurnID(streamer)
			ts.stampStreamerGoalID(streamer)
		}
	}
	if setter, ok := streamer.(interface {
		SetSystemNotice(generated.ReplayMessageFrame)
	}); ok {
		stamp := entry.Timestamp.Format(time.RFC3339Nano)
		frame := generated.ReplayMessageFrame{
			Type: string(generated.WsFrameTypeReplayMessage), SessionId: ts.transcriptSessionID,
			Role: entry.Role, Content: content, Id: &entry.ID, Timestamp: &stamp, TurnId: &entry.TurnID,
		}
		if entry.GoalID != "" {
			frame.GoalId = &entry.GoalID
		}
		setter.SetSystemNotice(frame)
	}
	return nil
}
