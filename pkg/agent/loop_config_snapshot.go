package agent

import (
	"context"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// WithConfigReadLock inspects a config and dependent state without allowing a
// concurrent MutateConfig to publish a new owner of a credential reference.
// The callback must not mutate cfg or call GetConfig, SwapConfig or MutateConfig.
// Copy any values needed after the callback; do not retain mutable config maps.
func (al *AgentLoop) WithConfigReadLock(inspect func(*config.Config) error) error {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return inspect(al.cfg)
}

// WithConfigReadLockContext is WithConfigReadLock with a cancellable lock wait.
// The callback has the same read-only restrictions and must respect ctx itself.
func (al *AgentLoop) WithConfigReadLockContext(ctx context.Context, inspect func(*config.Config) error) error {
	retry := time.NewTicker(10 * time.Millisecond)
	defer retry.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if al.mu.TryRLock() {
			defer al.mu.RUnlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			return inspect(al.cfg)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-retry.C:
		}
	}
}
