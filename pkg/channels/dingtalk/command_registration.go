package dingtalk

import (
	"context"
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// Compile-time assertion that DingTalkChannel implements CommandRegistrarCapable.
var _ channels.CommandRegistrarCapable = (*DingTalkChannel)(nil)

// RegisterCommands logs the commands that should be registered in the DingTalk
// Open Platform console. DingTalk bot commands are configured via the developer
// console — there is no runtime API to register them programmatically. This
// method logs the expected command set so operators know what to configure
// manually (FR-1, FR-28).
func (c *DingTalkChannel) RegisterCommands(ctx context.Context, defs []commands.Definition) error {
	for _, def := range defs {
		if def.Name == "" || def.Description == "" {
			continue
		}
		logger.InfoCF("dingtalk", "dingtalk requires manual registration: /"+def.Name+" - "+def.Description,
			map[string]any{
				"command":     fmt.Sprintf("/%s", def.Name),
				"description": def.Description,
				"note":        "Register this command in the DingTalk Open Platform console under Bot Configuration",
			},
		)
	}
	return nil
}
