package agent

import "github.com/elicify-ai/omnipus/pkg/providers"

// SessionToolList is one session's loaded tool definitions. A tool added
// here stays advertised for that session until the session ends, and a
// second session does not see it. The skeleton retains nothing.
type SessionToolList struct {
	sessionID string
}

// NewSessionToolList returns the loaded-tool list for sessionID.
func NewSessionToolList(sessionID string) *SessionToolList {
	return &SessionToolList{sessionID: sessionID}
}

// SessionID returns the session this list belongs to.
func (s *SessionToolList) SessionID() string {
	if s == nil {
		return ""
	}
	return s.sessionID
}

// Add records a tool definition on this session. The skeleton does not store it.
func (s *SessionToolList) Add(def providers.ToolDefinition) {
	_, _ = s, def
}

// Remove drops a tool by name (a Deny, a removed assignment, or a disconnect).
// The skeleton has nothing to drop.
func (s *SessionToolList) Remove(name string) {
	_, _ = s, name
}

// Definitions returns the tools still advertised, in deterministic order.
// The skeleton returns nil.
func (s *SessionToolList) Definitions() []providers.ToolDefinition {
	_ = s
	return nil
}

// Contains reports whether name is still executable in this session.
// The skeleton reports false.
func (s *SessionToolList) Contains(name string) bool {
	_, _ = s, name
	return false
}

// CanonicalToolListBytes serialises defs in one order that does not depend on
// the input order. The skeleton returns nil.
func CanonicalToolListBytes(defs []providers.ToolDefinition) []byte {
	_ = defs
	return nil
}

// GoalNarrowedTools returns the tool list for one turn. narrow true is the
// goal-setting turn and uses goal. narrow false is the following ordinary
// turn and uses the session list. The skeleton returns nil.
func GoalNarrowedTools(session *SessionToolList, narrow bool, goal []providers.ToolDefinition) []providers.ToolDefinition {
	_, _, _ = session, narrow, goal
	return nil
}

// FrozenSkillList is the skill list advertised at the start of a session.
// Later turns keep this list even when the files on disk change. The
// skeleton does not keep the names it was given.
type FrozenSkillList struct {
	sessionID string
}

// FreezeSkillList captures the advertised skill list for sessionID.
func FreezeSkillList(sessionID string, names []string) *FrozenSkillList {
	_ = names
	return &FrozenSkillList{sessionID: sessionID}
}

// SessionID returns the session this list was frozen for.
func (f *FrozenSkillList) SessionID() string {
	if f == nil {
		return ""
	}
	return f.sessionID
}

// Names returns the advertised list. The skeleton returns nil.
func (f *FrozenSkillList) Names() []string {
	_ = f
	return nil
}

// TokenSizeRecord is a labelled token count or token estimate of the tool
// list and of the skill text for one agent. Estimator names the estimator.
// A byte count alone does not satisfy the record. The skeleton leaves every
// field at zero, including an empty Estimator.
type TokenSizeRecord struct {
	ToolListTokens  int
	SkillTextTokens int
	Estimator       string
}

// RecordTokenSizes records the token size of the tool list and the skill
// text. The skeleton returns the zero record.
func RecordTokenSizes(tools []providers.ToolDefinition, skillText string) TokenSizeRecord {
	_, _ = tools, skillText
	return TokenSizeRecord{}
}
