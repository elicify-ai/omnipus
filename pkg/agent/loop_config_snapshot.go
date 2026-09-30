package agent

import "github.com/elicify-ai/omnipus/pkg/config"

// WithConfigReadLock inspects a config and dependent state without allowing a
// concurrent MutateConfig to publish a new owner of a credential reference.
// The callback must not mutate cfg or call GetConfig, SwapConfig or MutateConfig.
// Copy any values needed after the callback; do not retain mutable config maps.
func (al *AgentLoop) WithConfigReadLock(inspect func(*config.Config) error) error {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return inspect(al.cfg)
}
