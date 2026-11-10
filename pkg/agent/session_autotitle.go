package agent

import (
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// autoTitleAfterFirstReply titles an untitled, non-main session from the
// person's first message once the first reply has been written. It reuses
// session.TitleFromFirstMessage (the one titler a person-started chat already
// uses) and session.SetTitleIfEmpty, which refuses a main and never replaces an
// existing title, so a title set by hand survives. Best effort: a title is a
// label, so a failure is logged and never fails the turn.
func (ts *turnState) autoTitleAfterFirstReply(finalContent string) {
	if ts.transcriptStore == nil || ts.transcriptSessionID == "" || finalContent == "" {
		return
	}
	title := session.TitleFromFirstMessage(ts.opts.UserMessage)
	if title == "" {
		return
	}
	if _, err := ts.transcriptStore.SetTitleIfEmpty(ts.transcriptSessionID, title); err != nil {
		logger.WarnCF("agent", "could not auto-title session after its first reply",
			map[string]any{"session_id": ts.transcriptSessionID, "error": err.Error()})
	}
}
