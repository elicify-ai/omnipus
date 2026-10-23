package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

var errAsyncNotifyOriginWarningRejected = errors.New("async notification origin warning: outbound enqueue not accepted")

var asyncNotifyToolToken = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,95}$`)

// asyncNotifySourceLabel maps only existing producer tags to fixed public copy.
// A source tag is descriptive, never finished-tool identity or authorization.
func asyncNotifySourceLabel(sourceKind string) string {
	switch sourceKind {
	case "goal_idle_settle", "goal_claim_deferred", "steer_goal_loop":
		return "goal follow-up"
	case "task_goal_loop":
		return "task follow-up"
	case "plan_judge_met", "plan_judge_unmet", "plan_judge_unavailable", "plan_stalled", "plan_supervision_rearm", "plan_supervision_retry":
		return "plan update"
	}
	if strings.HasPrefix(sourceKind, "plan_") && plan.IsValidFailedReason(plan.FailedReason(strings.TrimPrefix(sourceKind, "plan_"))) {
		return "plan update"
	}
	if strings.HasPrefix(sourceKind, "message_parent:") && wakeableSessionMessageKinds[strings.TrimPrefix(sourceKind, "message_parent:")] {
		return "parent update"
	}
	return "asynchronous update"
}

func asyncNotifyShortRef(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:6])
}

func asyncNotifyFinishedTool(event AsyncNotifyEvent) (name, reference string) {
	if event.finishedToolName == "" || event.finishedToolCallID == "" {
		return "", ""
	}
	name = event.finishedToolName
	if !asyncNotifyToolToken.MatchString(name) {
		name = "background tool"
	}
	return name, asyncNotifyShortRef(string(event.finishedToolCallID))
}

// asyncNotifyDiagnosticFields always returns a fresh primitive-only allowlist.
// Neither raw causes, source tags, payload nor routing strings belong here.
func asyncNotifyDiagnosticFields(event AsyncNotifyEvent, noticeID string) map[string]any {
	fields := map[string]any{"source": asyncNotifySourceLabel(event.SourceKind), "notice_id": noticeID}
	if name, ref := asyncNotifyFinishedTool(event); ref != "" {
		name = logger.ScrubSensitiveValues(name)
		if asyncNotifyToolToken.MatchString(name) {
			fields["tool"] = name
		}
		fields["tool_ref"] = ref
	}
	if event.TranscriptSessionID != "" {
		fields["session_ref"] = asyncNotifyShortRef(event.TranscriptSessionID)
	}
	return fields
}

func (n *asyncNotifierImpl) asyncNotifyWarningText(event AsyncNotifyEvent) string {
	message := "This " + asyncNotifySourceLabel(event.SourceKind) + " notification did not wake the agent."
	if name, ref := asyncNotifyFinishedTool(event); ref != "" {
		message = fmt.Sprintf("Tool %s [%s] finished, but the agent was not woken by this notification.", name, ref)
	}
	if n.loop != nil {
		if cfg := n.loop.GetConfig(); cfg != nil {
			message = cfg.FilterSensitiveData(message)
		}
	}
	return logger.ScrubSensitiveValues(message)
}

// publishOriginWarning offers one system-authored warning to the supplied
// originating conversation. Acceptance is not a network delivery receipt.
func (n *asyncNotifierImpl) publishOriginWarning(ctx context.Context, event AsyncNotifyEvent, noticeID, content string) error {
	channelType, _ := config.ParseInstanceKey(event.Channel)
	if n.loop == nil || n.loop.bus == nil || event.ChatID == "" || event.TranscriptSessionID == "" || !isMessagingChannel(channelType) {
		return nil
	}
	allowed, err := n.loop.asyncNotifyOriginReachesUser(ctx, event.TranscriptSessionID)
	if err != nil || !allowed {
		return err
	}
	if n.loop.bus.TryPublishOutbound(bus.OutboundMessage{
		Channel: event.Channel, ChatID: event.ChatID, SessionID: event.TranscriptSessionID, Content: content,
	}) {
		return nil
	}
	logger.WarnCF("agent", "Async notification origin warning enqueue rejected", asyncNotifyDiagnosticFields(event, noticeID))
	return errAsyncNotifyOriginWarningRejected
}

// reportAsyncNotifyFailure does not repair the failed inbound handoff. Notice
// side effects keep caller values but detach an elapsed delivery deadline; no
// PublishInbound budget is extended. Intentional cancellation remains quiet.
func (n *asyncNotifierImpl) reportAsyncNotifyFailure(ctx context.Context, event AsyncNotifyEvent, noticeID string) error {
	if ctx.Err() == context.Canceled {
		return nil
	}
	if event.TranscriptSessionID == "" {
		return errors.New("async notification delivery warning: transcript session is unavailable")
	}
	noticeCtx := context.WithoutCancel(ctx)
	content := n.asyncNotifyWarningText(event)
	entry := session.TranscriptEntry{
		ID: noticeID, Type: session.EntryTypeSystem, Role: "system",
		Content: content, Timestamp: time.Now().UTC(),
	}
	var saveErr error
	var frame *generated.ReplayMessageFrame
	store := n.loop.ResolveSessionStore(event.TranscriptSessionID)
	if store == nil {
		saveErr = errors.New("async notification delivery warning: transcript store is unavailable")
	} else {
		var written bool
		written, saveErr = store.AppendTranscriptOnce(event.TranscriptSessionID, entry)
		if saveErr != nil {
			saveErr = fmt.Errorf("async notification delivery warning: save system notice: %w", saveErr)
		} else if written {
			stamp := entry.Timestamp.Format(time.RFC3339Nano)
			frame = &generated.ReplayMessageFrame{
				Type: string(generated.WsFrameTypeReplayMessage), SessionId: event.TranscriptSessionID,
				Id: &entry.ID, Role: entry.Role, Content: entry.Content, Timestamp: &stamp,
			}
		}
	}
	if ctx.Err() == context.Canceled {
		return saveErr
	}
	if n.loop.audienceFor(noticeCtx, steer.BoundaryTypedErrorFrame, event.TranscriptSessionID) != steer.AudienceNone {
		n.loop.emitEvent(EventKindError, EventMeta{Source: "async_notify", SessionKey: event.TranscriptSessionID}, ErrorPayload{
			Stage: "async_notify_delivery", Message: content, SessionID: event.TranscriptSessionID, ChatNotice: frame,
		})
	}
	if ctx.Err() == context.Canceled {
		return saveErr
	}
	return errors.Join(saveErr, n.publishOriginWarning(noticeCtx, event, noticeID, content))
}
