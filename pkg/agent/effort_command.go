package agent

// effort_command.go — the agent-side seams of the /effort command
// (effort-levels spec FR-023): the serving model's served capability, the
// conversation's own value (SessionMeta.reasoning_effort) and the agent's
// setting. The command writes the conversation only, never agent config.

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// errNoConversation is returned when /effort runs without a stored
// conversation to scope the setting to.
var errNoConversation = errors.New("effort: this command needs a saved conversation")

func (al *AgentLoop) wireEffortCommand(rt *commands.Runtime, agent *AgentInstance, opts *processOptions) {
	sessionID := ""
	if opts != nil {
		sessionID = opts.TranscriptSessionID
	}
	store := al.GetSessionStore()
	rt.GetEffortState = func() (commands.EffortState, error) {
		model, provider := rt.GetModelInfo()
		agent.mu.RLock()
		setting := agent.PrimaryEffort
		agent.mu.RUnlock()
		st := commands.EffortState{Model: model, AgentSetting: setting}
		if cat := providers.ProviderCatalog(); cat != nil {
			if h := cat.Resolve(provider, model); h.Found() {
				st.Known = true
				st.Levels, st.Switch, st.Reasons = h.ServedReasoning()
			}
		}
		if store != nil && sessionID != "" {
			meta, err := store.GetMeta(sessionID)
			if err != nil {
				return commands.EffortState{}, err
			}
			st.Current = meta.ReasoningEffort
		}
		return st, nil
	}
	rt.SetConversationEffort = func(value string) error {
		if store == nil || sessionID == "" {
			return errNoConversation
		}
		return store.SetMeta(sessionID, session.MetaPatch{ReasoningEffort: &value})
	}
}
