package channels

import (
	"sync"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// ChannelFactory is a constructor function that creates a Channel from config,
// a resolved SecretBundle, and a message bus. The bundle supplies all plaintext
// secrets without touching the process environment.
// Each channel subpackage registers one or more factories via init().
type ChannelFactory func(cfg *config.Config, secrets credentials.SecretBundle, bus *bus.MessageBus) (Channel, error)

var (
	factoriesMu sync.RWMutex
	factories   = map[string]ChannelFactory{}
)

// RegisterFactory registers a named channel factory. Called from subpackage init() functions.
func RegisterFactory(name string, f ChannelFactory) {
	factoriesMu.Lock()
	defer factoriesMu.Unlock()
	factories[name] = f
}

// getFactory looks up a channel factory by name.
func getFactory(name string) (ChannelFactory, bool) {
	factoriesMu.RLock()
	defer factoriesMu.RUnlock()
	f, ok := factories[name]
	return f, ok
}

// RegisteredFactoryNames returns the sorted names of every factory currently
// registered. Exposed for the half-wired-channel regression guard in
// pkg/gateway: any name passed to initChannel("name", ...) in manager.go MUST
// resolve to a registered factory, otherwise the operator-visible config knob
// is dead code at runtime (see issue #161).
func RegisteredFactoryNames() []string {
	factoriesMu.RLock()
	defer factoriesMu.RUnlock()
	names := make([]string, 0, len(factories))
	for n := range factories {
		names = append(names, n)
	}
	return names
}
