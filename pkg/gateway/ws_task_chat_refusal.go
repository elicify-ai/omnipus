// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// taskChatRefusalText is the person-facing refusal of a chat message typed
// into an external worker's Board task chat (founder Q13 B; approved wording,
// prompt-r4 TEXT.md §3). cliDisplay is the fixed CLI display name
// (agent.CLIDisplayName); without one the "runs in {cli} and " clause is
// dropped, so no empty or literal slot is ever shown.
func taskChatRefusalText(cliDisplay string) string {
	if cliDisplay == "" {
		return "I couldn't deliver your message: this task doesn't accept chat messages here. " +
			"To change the work, open Tasks and stop the task or its plan before editing the task."
	}
	return "I couldn't deliver your message: this task runs in " + cliDisplay +
		" and doesn't accept chat messages here. " +
		"To change the work, open Tasks and stop the task or its plan before editing the task."
}

// refuseExternalTaskRunChat refuses a message frame addressed to an existing
// Board task-run session of a worker on an external CLI — whether the frame
// named the worker or left the agent to the session's owner (founder Q13 B;
// letting such input through is G16 on epic #1969). The refusal is tagged
// with that session and the frame's client message id, so it shows in that
// task's chat as a refusal of that message, and it is saved in the session's
// transcript so a reload still shows it. Nothing is published to the bus and
// no session, lifecycle or task record is touched. It reports whether the
// frame was refused. A delegated helper (SteeredBy set) is not a task run:
// a person's message into its pane keeps the helper path.
func (hcm *wsHandlerHandleChatMessage) refuseExternalTaskRunChat(owner string) bool {
	if hcm.frameSessionID == "" || !isWorkerAgentID(hcm.h.agentLoop.GetConfig(), owner) {
		return false
	}
	lifecycle := hcm.h.agentLoop.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false
	}
	rec, err := lifecycle.Load(hcm.frameSessionID)
	if err != nil {
		if !errors.Is(err, session.ErrLifecycleNotFound) {
			// An unreadable record cannot be classified; the worker gate in
			// resolveTargetAgent still decides an explicit worker frame.
			logsafeWarn("ws: could not read the lifecycle record of a worker session; task-chat refusal not applied",
				"session_id", hcm.frameSessionID, "error", err)
		}
		return false
	}
	if rec == nil || rec.SteeredBy != nil || !rec.Is3P || rec.Origin == nil ||
		rec.Origin.Kind != session.OriginKindTask {
		return false
	}
	cli := ""
	if ac := findAgentConfig(hcm.h.agentLoop.GetConfig(), owner); ac != nil &&
		ac.Subagents != nil && ac.Subagents.Executor != nil {
		cli = ac.Subagents.Executor.CLI
	}
	text := taskChatRefusalText(agent.CLIDisplayName(cli))
	logsafeWarn("ws: refusing a chat message to an external worker's task run",
		"session_id", hcm.frameSessionID, "agent_id", owner)
	if _, saveErr := hcm.h.agentLoop.AppendSessionRefusal(hcm.frameSessionID, strings.TrimSpace(owner), "", text); saveErr != nil {
		logsafeError("ws: the task-chat refusal was shown but could not be saved; it will not appear after a reload",
			"session_id", hcm.frameSessionID, "error", saveErr)
	}
	hcm.h.sendRecipientRefusal(hcm.wc, hcm.frameSessionID, hcm.clientMessageID, text)
	return true
}
