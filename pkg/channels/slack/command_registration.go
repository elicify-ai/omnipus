package slack

import (
	"context"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// slackCommandManifestNote is the guidance logged at startup so operators know
// exactly what to add to their Slack App manifest or dashboard.
// Slack slash commands are registered at the App level via the Slack dashboard
// or the apps.manifest.update API (requires a configuration token that most
// deployments do not provision).  The pragmatic v0.1 approach is to log the
// expected command set and return nil so startup is never blocked (FR-28:
// fall back to text parsing when platform registration is unavailable).
const slackCommandManifestNote = `Slack slash commands must be registered manually in the Slack App dashboard
(Features → Slash Commands) or via the Slack Manifest editor.  Add each
command below with the Request URL pointing at your bot's slash-command
webhook endpoint (e.g. https://yourhost/api/v1/slack/commands).`

// RegisterCommands implements channels.CommandRegistrarCapable for Slack.
// Because Slack does not provide a programmatic API for registering slash
// commands without a refreshable configuration token (which operators rarely
// provision), this implementation logs the expected command set so operators
// know what to configure in the Slack App dashboard.  It always returns nil so
// the channel starts successfully and falls back to text parsing (FR-28).
func (c *SlackChannel) RegisterCommands(_ context.Context, defs []commands.Definition) error {
	var names []string
	for _, def := range defs {
		if def.Name == "" || def.Description == "" {
			continue
		}
		names = append(names, "/"+def.Name+" — "+def.Description)
	}

	if len(names) == 0 {
		return nil
	}

	logger.InfoCF("slack", "Slack command registration (operator action required)", map[string]any{
		"note":     slackCommandManifestNote,
		"commands": strings.Join(names, "; "),
		"count":    len(names),
	})
	return nil
}
