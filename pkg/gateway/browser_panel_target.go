package gateway

import (
	"context"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// browserPanelWorkspace reads the chat binding from the session store.
func browserPanelWorkspace(al *agent.AgentLoop, chatSessionID, userID string) (string, bool) {
	if al == nil || chatSessionID == "" {
		return "", false
	}
	store := al.ResolveSessionStore(chatSessionID)
	if store == nil {
		return "", false
	}
	meta, err := store.GetMeta(chatSessionID)
	if err != nil || meta == nil || meta.Owner != userID {
		return "", false
	}
	workspaceID := strings.TrimSpace(meta.WorkspaceID)
	return workspaceID, workspaceID != ""
}

// resolveBrowserPanelTarget validates the chat binding and the manager's actual tab set.
// An unresolved binding returns authorized=false; a manager failure returns its normal outcome.
func resolveBrowserPanelTarget(
	ctx context.Context, al *agent.AgentLoop, agentID, chatSessionID, userID string,
) (mgr *browser.BrowserManager, panelSessionID string, outcome agent.BrowserResolveOutcome, authorized bool) {
	workspaceID, ok := browserPanelWorkspace(al, chatSessionID, userID)
	if !ok {
		return nil, "", agent.BrowserResolveOK, false
	}
	key, err := browser.ResolveBrowsingKeyForAgent(config.OmnipusHomeDir(), agentID, workspaceID)
	if err == nil && key.WorkspaceID() != workspaceID {
		return nil, "", agent.BrowserResolveOK, false
	}
	mgr, outcome = al.BrowserManagerForAgent(ctx, agentID, workspaceID)
	if outcome != agent.BrowserResolveOK {
		return nil, "", outcome, true
	}
	if mgr == nil || mgr.BrowsingKey().WorkspaceID() != workspaceID {
		return nil, "", agent.BrowserResolveOK, false
	}
	owner, err := browser.TabOwnerSession(chatSessionID)
	if err != nil {
		return nil, "", agent.BrowserResolveOK, false
	}
	panelSessionID = mgr.PanelTabSetID(chatSessionID)
	if panelSessionID != mgr.OperatorSessionID() && panelSessionID != key.String()+"/"+owner.String() {
		return nil, "", agent.BrowserResolveOK, false
	}
	return mgr, panelSessionID, agent.BrowserResolveOK, true
}
