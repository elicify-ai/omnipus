package gateway

import "github.com/elicify-ai/omnipus/pkg/session"

// withDisplayTitle returns the meta a client should see. A main session's title
// is fixed and derived on every read: "<Agent> - Main Session" from the agent's
// CURRENT display name, so renaming the agent renames its main and nothing
// stored can go stale. Any other session is returned unchanged.
func (a *restAPI) withDisplayTitle(m *session.UnifiedMeta) *session.UnifiedMeta {
	if m == nil || m.Type != session.SessionTypeMain {
		return m
	}
	name := a.resolveAgentName(m.AgentID)
	if name == "" {
		name = m.AgentID
	}
	out := m.Clone()
	out.Title = session.MainSessionTitle(name)
	return out
}
