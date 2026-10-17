// Omnipus — AskUserQuestion wiring (askuserquestion-tool-spec v3)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"

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
	if store := al.GetSessionStore(); store != nil {
		store.SetSessionDeletedObserver(func(sessionID string) {
			if registry := al.getAskUserRegistry(); registry != nil {
				registry.ForgetDeletedSession(sessionID)
			}
		})
	}
}

// getAskUserRegistry returns the live registry, or nil when unwired.
func (al *AgentLoop) getAskUserRegistry() tools.AskUserQuestionRegistry {
	al.askUserRegistryMu.RLock()
	defer al.askUserRegistryMu.RUnlock()
	return al.askUserRegistry
}

// cancelPendingAskForScope carries the captured head and its failure through
// the one Stop request. It never resumes a cancelled turn or consumes a
// successor that the request did not target.
func (al *AgentLoop) cancelPendingAskForScope(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	targets, ok := ctx.Value(pendingAskStopContextKey{}).(*pendingAskStopTargets)
	if !ok {
		// cancel captures this standalone call's head once; no unchecked
		// context assertion is needed when there was no outer Stop binding.
		targets = &pendingAskStopTargets{targets: make(map[string]*pendingAskStopTarget)}
	}
	return targets.cancel(al, sessionID)
}
