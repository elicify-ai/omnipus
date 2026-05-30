//go:build amd64 || arm64 || riscv64 || mips64 || ppc64

package feishu

import (
	"context"
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// Compile-time assertion that FeishuChannel implements CommandRegistrarCapable.
var _ channels.CommandRegistrarCapable = (*FeishuChannel)(nil)

// RegisterCommands logs the commands that should be registered in the Feishu
// Developer Console. Feishu (Lark) bot menu commands are configured via the
// open platform developer console — there is no runtime API to register them
// programmatically. This method logs the expected command set so operators
// know what to configure manually (FR-1, FR-27).
func (c *FeishuChannel) RegisterCommands(ctx context.Context, defs []commands.Definition) error {
	for _, def := range defs {
		if def.Name == "" || def.Description == "" {
			continue
		}
		logger.InfoCF("feishu", "feishu requires manual registration: /"+def.Name+" - "+def.Description,
			map[string]any{
				"command":     fmt.Sprintf("/%s", def.Name),
				"description": def.Description,
				"note":        "Register this command in the Feishu Developer Console under Bot > Bot Menu",
			},
		)
	}
	return nil
}
