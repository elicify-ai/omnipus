package agent

import (
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// registerEmailToolsForAgent registers the M11 email tools (read_inbox,
// search_email, read_message, send_email, reply) on the given agent IF and ONLY
// IF that agent owns at least one enabled mailbox whose password resolves.
// Email is a TOOL surface, not a conversational channel — a mailbox belongs to
// one (agent, workspace) pair, and the same agent can hold a different mailbox
// in each workspace it belongs to (different roles, different inboxes).
//
// The tools receive the agent's FULL workspace→transport map and resolve the
// active workspace from the turn context at execution time
// (tools.ToolWorkspaceID) — never from a model-supplied parameter, so the
// model cannot address another workspace's (or another agent's) mailbox.
//
// Each password is resolved from os.Getenv(password_ref), the env-injection
// pattern populated by credentials.InjectFromConfig (the same mechanism
// provider keys and skill marketplace credentials use). A pair whose password
// is missing/unresolvable is skipped with a WARN; when NO pair survives, no
// email tools are registered — an agent without a mailbox never sees them.
func registerEmailToolsForAgent(cfg *config.Config, agentID string, agent *AgentInstance) {
	if cfg == nil || agent == nil {
		return
	}
	byWorkspace, ok := cfg.Mailboxes[agentID]
	if !ok || len(byWorkspace) == 0 {
		return
	}

	transports := make(tools.EmailTransports, len(byWorkspace))
	// Deterministic iteration so skip-WARNs and the registration log are stable.
	wsIDs := make([]string, 0, len(byWorkspace))
	for wsID := range byWorkspace {
		wsIDs = append(wsIDs, wsID)
	}
	sort.Strings(wsIDs)

	for _, wsID := range wsIDs {
		if strings.TrimSpace(wsID) == "" {
			slog.Warn("email tools: dropping mailbox with empty workspace key",
				"agent_id", agentID)
			continue
		}
		mb := byWorkspace[wsID]
		if !mb.Enabled {
			continue
		}
		ref := strings.TrimSpace(mb.PasswordRef)
		if ref == "" {
			slog.Warn("email tools: mailbox enabled but no password_ref — skipping pair",
				"agent_id", agentID, "workspace_id", wsID)
			continue
		}
		password := os.Getenv(ref)
		if password == "" {
			slog.Warn("email tools: mailbox password did not resolve — skipping pair",
				"agent_id", agentID, "workspace_id", wsID, "password_ref", ref)
			continue
		}

		client, err := email.NewClient(email.Account{
			IMAPHost: mb.IMAPHost,
			IMAPPort: mb.IMAPPort,
			SMTPHost: mb.SMTPHost,
			SMTPPort: mb.SMTPPort,
			Username: mb.Username,
			Password: password,
		})
		if err != nil {
			slog.Warn("email tools: mailbox transport construction failed — skipping pair",
				"agent_id", agentID, "workspace_id", wsID, "error", err)
			continue
		}
		transports[wsID] = client
	}

	if len(transports) == 0 {
		return
	}

	for _, t := range tools.EmailToolset(transports) {
		agent.Tools.Register(t)
	}
	slog.Info("email tools: registered for agent",
		"agent_id", agentID, "workspaces", strings.Join(wsIDs, ","), "mailboxes", len(transports))
}
