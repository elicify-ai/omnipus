package systools

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

var ErrDelegatedAvaWrite = tools.ErrDelegatedAvaWrite

// ValidateConfigurationWriteContext is the shared execution guard for every
// configuration mutation tool. Ava may mutate only in an owner session;
// delegated Ava runs (depth > 0) are proposal-only. Other identities retain
// their existing authorization behavior.
func ValidateConfigurationWriteContext(ctx context.Context) error {
	return tools.ValidateConfigurationWriteContext(ctx)
}
