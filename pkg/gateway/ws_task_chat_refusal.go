// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"fmt"
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

// taskChatReadFailureText refuses a message to a worker session whose
// lifecycle record could not be read, so Omnipus could not tell whether it is
// an external worker's task chat (SFH-R5-3). The read error goes to the log
// only. Approved wording: prompt-r4 TEXT.md §5.
const taskChatReadFailureText = "I couldn't deliver your message because I couldn't read this task's state. " +
	"Check disk space and permissions, then retry."

// taskChatRefusalNotSavedText follows a task-chat refusal that could not be
// saved; the person's message was not saved either (SFH-R5-1). Approved
// wording: prompt-r4 TEXT.md §4, external task chat.
const taskChatRefusalNotSavedText = "I couldn't save your message or the refusal above, so both may be missing after a reload. " +
	"Copy them before reloading."

// taskChatRefusal is the outcome of classifying an existing session's frame
// as a message into an external worker's Board task chat: the refusal to send
// (text, for owner) or, when the session's lifecycle record could not be
// read, the read failure (readErr). It is decided read-only in
// resolveTargetAgent and acted on only after the frame's session and
// workspace checks passed (adoptExistingSession, architect F2), so a frame
// those checks reject is rejected exactly as before: no saved row, no CLI
// name.
type taskChatRefusal struct {
	owner   string
	text    string
	readErr error
}

// classifyExternalTaskRunChat decides, without writing or sending anything,
// whether a frame addressed to the existing session of worker owner is a
// message into that worker's Board task-run chat on an external CLI (founder
// Q13 B; letting such input through is G16 on epic #1969). nil means the
// frame is not one: a non-worker owner, a missing record, a delegated helper
// (SteeredBy set — a person's message into its pane keeps the helper path),
// a native worker or a non-task origin. A record that exists but cannot be
// read is returned as a read failure, never as "not a task chat" (SFH-R5-3):
// the frame stops before anything is saved or admitted.
func (hcm *wsHandlerHandleChatMessage) classifyExternalTaskRunChat(owner string) *taskChatRefusal {
	if hcm.frameSessionID == "" || !isWorkerAgentID(hcm.h.agentLoop.GetConfig(), owner) {
		return nil
	}
	owner = strings.TrimSpace(owner)
	lifecycle := hcm.h.agentLoop.GetSessionLifecycleStore()
	if lifecycle == nil {
		// No lifecycle store: no session has a lifecycle, so none is a task run.
		return nil
	}
	rec, err := lifecycle.Load(hcm.frameSessionID)
	if err != nil {
		if errors.Is(err, session.ErrLifecycleNotFound) {
			return nil
		}
		return &taskChatRefusal{owner: owner, readErr: fmt.Errorf("read lifecycle of session %s: %w", hcm.frameSessionID, err)}
	}
	if rec == nil || rec.SteeredBy != nil || !rec.Is3P || rec.Origin == nil ||
		rec.Origin.Kind != session.OriginKindTask {
		return nil
	}
	cli := ""
	if ac := findAgentConfig(hcm.h.agentLoop.GetConfig(), owner); ac != nil &&
		ac.Subagents != nil && ac.Subagents.Executor != nil {
		cli = ac.Subagents.Executor.CLI
	}
	return &taskChatRefusal{owner: owner, text: taskChatRefusalText(agent.CLIDisplayName(cli))}
}

// refuseTaskChat sends and saves the refusal classifyExternalTaskRunChat
// decided, once the frame's session and workspace checks have passed. The
// refusal is tagged with that session and the frame's client message id, so
// it shows in that task's chat as a refusal of that message, and it is saved in the session's transcript so a reload still
// shows it. The person's message is not saved, nothing is published to the
// bus and no session, lifecycle or task record is touched. A refusal that
// could not be saved is followed by a separate notice saying so
// (SFH-R5-1), so the person is not told it will survive a reload.
func (hcm *wsHandlerHandleChatMessage) refuseTaskChat(r *taskChatRefusal) {
	text := r.text
	if r.readErr != nil {
		logsafeError("ws: could not read the lifecycle record of a worker session; refusing the message",
			"session_id", hcm.frameSessionID, "agent_id", r.owner, "error", r.readErr)
		text = taskChatReadFailureText
	} else {
		logsafeWarn("ws: refusing a chat message to an external worker's task run",
			"session_id", hcm.frameSessionID, "agent_id", r.owner)
	}
	_, saveErr := hcm.h.agentLoop.AppendSessionRefusal(hcm.frameSessionID, r.owner, "", text)
	// Message-scoped (session and client message id): a live turn in that
	// chat keeps running. The row's failed tick is the intake's own deferred
	// one (handleChatMessageWithOrigin: not admitted), sent after this error.
	hcm.h.sendRecipientRefusalError(hcm.wc, hcm.frameSessionID, hcm.clientMessageID, text)
	if saveErr != nil {
		logsafeError("ws: a task-chat refusal could not be saved; it will be missing after a reload",
			"session_id", hcm.frameSessionID, "error", saveErr)
		hcm.h.sendRecipientRefusalError(hcm.wc, hcm.frameSessionID, hcm.clientMessageID, taskChatRefusalNotSavedText)
	}
}
