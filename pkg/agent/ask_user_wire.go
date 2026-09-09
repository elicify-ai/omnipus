// Omnipus — AskUserQuestion wiring (askuserquestion-tool-spec v3)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// SetAskUserRegistry installs the gateway-constructed AskUserQuestion
// pending registry (pkg/askuser.Registry via the tools.AskUserQuestionRegistry
// seam). The per-agent AskUserQuestionTool instances registered by
// registerSharedTools resolve it live per call, so this needs no
// re-registration pass — calling it once at boot (after the session store
// exists) is enough, and calling it before any reload is safe.
func (al *AgentLoop) SetAskUserRegistry(r tools.AskUserQuestionRegistry) {
	al.askUserRegistryMu.Lock()
	al.askUserRegistry = r
	al.askUserRegistryMu.Unlock()
}

// getAskUserRegistry returns the live registry, or nil when unwired.
func (al *AgentLoop) getAskUserRegistry() tools.AskUserQuestionRegistry {
	al.askUserRegistryMu.RLock()
	defer al.askUserRegistryMu.RUnlock()
	return al.askUserRegistry
}

// cancelPendingAskForScope cancels any pending AskUserQuestion set reachable
// from a cancel scope's session identity (spec US-6 S2: session Stop and
// channel `/cancel` cancel the set). No resume turn is dispatched — the user
// asked everything to stop; the terminal cancelled record persists for the
// collapsed-card render. A nil registry or empty key is a no-op: a parked
// set simply stays pending, which is the pre-wiring behavior.
func (al *AgentLoop) cancelPendingAskForScope(sessionID string) {
	if sessionID == "" {
		return
	}
	if r := al.getAskUserRegistry(); r != nil {
		r.CancelOnSessionStop(sessionID)
	}
}
