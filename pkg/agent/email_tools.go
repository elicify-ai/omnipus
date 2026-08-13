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
// search_email, read_message, send_email, reply) on the given agent
// UNCONDITIONALLY, like every other builtin. Whether the agent may use them is
// its tool POLICY; whether it has an inbox to use them ON is data, resolved per
// turn.
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
// is missing/unresolvable is skipped with a WARN.
//
// A pair that does not survive — or an agent with no mailbox at all — costs the
// tool nothing at registration: EmailTransports.resolve reports it per turn
// ("no mailbox is configured for this agent", or names the workspaces that do
// have one). That is the whole point of registering anyway.
//
// This used to return early unless at least one mailbox resolved, so an agent
// without one never saw the tools. That gate conflated two separate questions —
// does this agent HAVE an inbox here (data, per agent+workspace, changes at
// runtime) versus MAY this agent use email at all (policy, per agent, set by
// the operator) — and let the first silently answer the second. Because the
// per-agent permissions screen is built from this execution registry, the
// operator could not see or set the permission at all: there was no row.
// Registering always restores that control without loosening anything, since a
// denied tool never reaches the model (tools.FilterToolsByPolicy).
func registerEmailToolsForAgent(cfg *config.Config, agentID string, agent *AgentInstance) {
	if cfg == nil || agent == nil {
		return
	}
	byWorkspace := cfg.Mailboxes[agentID]

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

	for _, t := range tools.EmailToolset(transports) {
		agent.Tools.Register(t)
	}
	slog.Info("email tools: registered for agent",
		"agent_id", agentID, "workspaces", strings.Join(wsIDs, ","), "mailboxes", len(transports))
}
