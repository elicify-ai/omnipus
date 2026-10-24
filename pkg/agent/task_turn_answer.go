package agent

import "strings"

// recordTaskModelAnswer collects actual assistant text from a successful model
// response, including text accompanying tool calls. Initial replies reach this
// point after response hooks and cleanup; empty-response retries use the same
// cleanup on a copy. Framework notices, reasoning, tool results and evidence
// are never answer producers.
func (rf *agentLoopRunTurnFallbacks) recordTaskModelAnswer() {
	if rf.rt.ts.opts.RunningTaskID == "" || !rf.responseFromModel || rf.response == nil {
		return
	}
	modelResponse := *rf.response
	stripOrphanToolCallMarkup(&modelResponse)
	if strings.TrimSpace(modelResponse.Content) != "" {
		rf.rt.taskModelAnswerParts = append(rf.rt.taskModelAnswerParts, modelResponse.Content)
	}
}
