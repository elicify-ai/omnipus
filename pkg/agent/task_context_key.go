package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// contextKey names the run-owned model archive, not the live-turn registry.
// Task routing remains keyed by (agent, task) for Stop and executor liveness,
// while each attempt owns its own durable session and recovery evidence.
func (ts *turnState) contextKey() string {
	if ts.opts.IsTaskRun && ts.opts.TranscriptSessionID != "" {
		return ts.opts.TranscriptSessionID
	}
	return ts.sessionKey
}

func (al *AgentLoop) modelContextKey(routingKey string) string {
	if ts := al.getActiveTurnState(routingKey); ts != nil {
		return ts.contextKey()
	}
	return routingKey
}

// Recall spans keep their routing bucket, but reads use the same run archive
// as normal model assembly and reconciliation.
func (t *RecallConversationTool) archiveKey(ctx context.Context, routingKey string) string {
	if tools.ToolRunningTaskID(ctx) != "" {
		if id := tools.ToolTranscriptSessionID(ctx); id != "" {
			return id
		}
	}
	if al, ok := t.spanSetter.(*AgentLoop); ok {
		return al.modelContextKey(routingKey)
	}
	return routingKey
}
