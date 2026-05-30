//go:build !amd64 && !arm64 && !riscv64 && !mips64 && !ppc64

package feishu

import (
	"context"

	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/commands"
)

// Compile-time assertion that FeishuChannel implements CommandRegistrarCapable.
var _ channels.CommandRegistrarCapable = (*FeishuChannel)(nil)

// RegisterCommands is a stub for 32-bit architectures where Feishu is unsupported.
func (c *FeishuChannel) RegisterCommands(_ context.Context, _ []commands.Definition) error {
	return errUnsupported
}
