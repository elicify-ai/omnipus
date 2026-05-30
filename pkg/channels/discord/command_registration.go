package discord

import (
	"context"

	"github.com/bwmarrin/discordgo"

	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// RegisterCommands implements channels.CommandRegistrarCapable for Discord.
// It converts the given definitions to Discord ApplicationCommands and registers
// them globally via ApplicationCommandBulkOverwrite.  A global (guild-independent)
// registration is used because the bot typically serves multiple guilds.
//
// If the application ID cannot be determined from the session state (e.g. the
// session has not completed its READY handshake yet), registration is skipped
// with a warning and nil is returned so startup is not blocked (FR-28).
func (c *DiscordChannel) RegisterCommands(ctx context.Context, defs []commands.Definition) error {
	appID := c.appID()
	if appID == "" {
		logger.WarnCF("discord", "Cannot register commands: application ID not available (session not ready)", nil)
		return nil
	}

	appCmds := make([]*discordgo.ApplicationCommand, 0, len(defs))
	for _, def := range defs {
		if def.Name == "" || def.Description == "" {
			continue
		}
		appCmds = append(appCmds, &discordgo.ApplicationCommand{
			Name:        def.Name,
			Description: def.Description,
		})
	}

	if len(appCmds) == 0 {
		return nil
	}

	// "" as guildID means global registration (all guilds).
	_, err := c.session.ApplicationCommandBulkOverwrite(appID, "", appCmds)
	if err != nil {
		// FR-28: slash-command registration failures must not crash channel
		// startup. Other Tier A channels (Slack, Teams, Feishu, DingTalk,
		// GoogleChat) log WARN and return nil on platform API errors. Discord
		// matches that behavior: the channel continues to receive messages and
		// parses /cancel via text matching as a fallback.
		logger.WarnCF(
			"discord",
			"Discord command registration failed; text parsing still works (FR-28)",
			map[string]any{
				"error": err.Error(),
				"count": len(appCmds),
			},
		)
		return nil //nolint:nilerr // FR-28: log WARN and continue; text parsing still works
	}

	logger.InfoCF("discord", "Discord slash commands registered globally", map[string]any{
		"count": len(appCmds),
	})
	return nil
}

// appID returns the Discord application ID from the session state, or an empty
// string when the READY payload has not yet been received.
func (c *DiscordChannel) appID() string {
	if c.session == nil || c.session.State == nil {
		return ""
	}
	if app := c.session.State.Application; app != nil && app.ID != "" {
		return app.ID
	}
	// Fallback: the bot user ID is the same as the application ID on Discord.
	return c.botUserID
}
