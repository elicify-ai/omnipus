package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
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
