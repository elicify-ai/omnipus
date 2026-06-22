package agent

import (
	"log/slog"
	"os"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/email"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// registerEmailToolsForAgent registers the M11 email tools (read_inbox,
// search_email, read_message, send_email, reply) on the given agent IF and ONLY
// IF that agent owns an enabled mailbox whose password resolves. Email is a TOOL
// surface, not a conversational channel — a mailbox is owned by exactly one agent
// (the key in cfg.Mailboxes) and surfaces in exactly one workspace.
//
// The password is resolved from os.Getenv(password_ref), the env-injection
// pattern populated by credentials.InjectFromConfig (the same mechanism provider
// keys and skill marketplace credentials use). When the mailbox is absent,
// disabled, or its password is missing/unresolvable, NO email tools are
// registered for the agent — so an agent without a mailbox never sees them.
func registerEmailToolsForAgent(cfg *config.Config, agentID string, agent *AgentInstance) {
	if cfg == nil || agent == nil {
		return
	}
	mb, ok := cfg.Mailboxes[agentID]
	if !ok || !mb.Enabled {
		return
	}
	ref := strings.TrimSpace(mb.PasswordRef)
	if ref == "" {
		slog.Warn("email tools: mailbox enabled but no password_ref — skipping registration",
			"agent_id", agentID)
		return
	}
	password := os.Getenv(ref)
	if password == "" {
		slog.Warn("email tools: mailbox password did not resolve — skipping registration",
			"agent_id", agentID, "password_ref", ref)
		return
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
		slog.Warn("email tools: mailbox transport construction failed — skipping registration",
			"agent_id", agentID, "error", err)
		return
	}

	for _, t := range tools.EmailToolset(client) {
		agent.Tools.Register(t)
	}
	slog.Info("email tools: registered for agent",
		"agent_id", agentID, "username", mb.Username, "workspace_id", mb.WorkspaceID)
}
