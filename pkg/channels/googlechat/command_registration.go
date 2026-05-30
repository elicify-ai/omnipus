package googlechat

import (
	"context"
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// Compile-time assertion that GoogleChatChannel implements CommandRegistrarCapable.
var _ channels.CommandRegistrarCapable = (*GoogleChatChannel)(nil)

// RegisterCommands logs the commands that should be registered in the Google
// Cloud Console. Google Chat slash commands are configured via the Google Cloud
// Console under the Chat API configuration — there is no runtime API to
// register them programmatically. This method logs the expected command set so
// operators know what to configure manually (FR-1, FR-27).
func (c *GoogleChatChannel) RegisterCommands(ctx context.Context, defs []commands.Definition) error {
	for _, def := range defs {
		if def.Name == "" || def.Description == "" {
			continue
		}
		logger.InfoCF("google-chat", "google-chat requires manual registration: /"+def.Name+" - "+def.Description,
			map[string]any{
				"command":     fmt.Sprintf("/%s", def.Name),
				"description": def.Description,
				"note":        "Register this slash command in Google Cloud Console under APIs & Services > Google Chat API > Configuration",
			},
		)
	}
	return nil
}
