package channels

import (
	"sync"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// ChannelFactory is a constructor function that creates a Channel from config,
// an instance ID, a resolved SecretBundle, and a message bus. The instanceID
// identifies which config.ChannelInstanceConfig entry to use (the map key in
// cfg.Channels), enabling N-per-type support (ADR-029). The bundle supplies all
// plaintext secrets without touching the process environment.
// Each channel subpackage registers one or more factories via init().
type ChannelFactory func(cfg *config.Config, instanceID string, secrets credentials.SecretBundle, bus *bus.MessageBus) (Channel, error)

// PrerequisiteChecker verifies an enabled channel instance has the config
// fields its factory needs before construction is attempted. It returns
// ok=true when the instance is constructable, or ok=false plus a
// human-readable missing-fields string when the instance must be skipped
// with a misconfiguration warning. A channel type with no registered
// checker activates without prerequisite gating (factory-side validation
// handles the rest).
type PrerequisiteChecker func(inst config.ChannelInstanceConfig) (missingFields string, ok bool)

var (
	factoriesMu sync.RWMutex
	factories   = map[string]ChannelFactory{}

	prerequisitesMu sync.RWMutex
	prerequisites   = map[string]PrerequisiteChecker{}
)

// RegisterFactory registers a named channel factory. Called from subpackage init() functions.
func RegisterFactory(name string, f ChannelFactory) {
	factoriesMu.Lock()
	defer factoriesMu.Unlock()
	factories[name] = f
}

// RegisterPrerequisite registers a per-type prerequisite checker for a channel
// type. Called from channel subpackage init() functions alongside
// RegisterFactory, or centrally from this package's own init (see
// prerequisites.go). A type with no registered checker activates without any
// prerequisite gating.
func RegisterPrerequisite(channelType string, f PrerequisiteChecker) {
	prerequisitesMu.Lock()
	defer prerequisitesMu.Unlock()
	prerequisites[channelType] = f
}

// getFactory looks up a channel factory by name.
func getFactory(name string) (ChannelFactory, bool) {
	factoriesMu.RLock()
	defer factoriesMu.RUnlock()
	f, ok := factories[name]
	return f, ok
}

// getPrerequisite looks up the prerequisite checker for a channel type, if any.
func getPrerequisite(channelType string) (PrerequisiteChecker, bool) {
	prerequisitesMu.RLock()
	defer prerequisitesMu.RUnlock()
	f, ok := prerequisites[channelType]
	return f, ok
}
