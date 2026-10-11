package session

import (
	"fmt"
	"strings"
	"time"
)

// maxAutoTitleRunes bounds a title derived from a message: 57 runes plus an
// ellipsis, 60 in all.
const (
	maxAutoTitleRunes = 60
	mainTitleSuffix   = " - Main Session"
)

// TitleFromFirstMessage is THE session titler for a conversation that was
// started by a person: the first message, whitespace-trimmed and cut to 60
// runes with an ellipsis. The WebSocket first-message path and the
// after-first-reply path share it so a session is titled the same way whichever
// door it was opened through. Empty when the message has no visible text.
func TitleFromFirstMessage(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) > maxAutoTitleRunes {
		return string(runes[:maxAutoTitleRunes-3]) + "..."
	}
	return content
}

// MainSessionTitle is the fixed title of a main session: "<Agent> - Main
// Session". It is derived on read from the agent's CURRENT display name rather
// than stored, so renaming the agent renames its main with no second write to
// keep in step.
func MainSessionTitle(agentName string) string {
	return strings.TrimSpace(agentName) + mainTitleSuffix
}

// SetTitleIfEmpty gives a session an automatic title only while it has none, and
// never to a main (a main's title is fixed, see MainSessionTitle). The check and
// the write happen under the session's own lock, so a title a person sets by
// hand between the reply and this call is never overwritten. It reports whether
// it wrote.
func (us *UnifiedStore) SetTitleIfEmpty(sessionID, title string) (bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return false, err
	}
	if strings.TrimSpace(title) == "" {
		return false, nil
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()

	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return false, err
	}
	if meta.Type == SessionTypeMain || strings.TrimSpace(meta.Title) != "" {
		return false, nil
	}
	meta.Title = title
	meta.UpdatedAt = time.Now().UTC()
	if err := us.u5WriteIdentityLocked(sessionID, meta); err != nil {
		return false, fmt.Errorf("unified_store: auto-title session %q: %w", sessionID, err)
	}
	return true, nil
}
