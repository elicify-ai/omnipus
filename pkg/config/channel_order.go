// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// ChannelOrderGeneration is the private intent that produced this channel's
// exact configuration snapshot. An observation never rebinds it to a newer
// intent, even when the public configuration hashes happen to be identical.
func (c *Config) ChannelOrderGeneration(instance string) (uint64, bool) {
	if c == nil {
		return 0, false
	}
	generation, ok := c.channelOrder[instance]
	return generation, ok
}

// SetChannelOrderGeneration stamps an UNPUBLISHED candidate only. The map is
// immutable after the config is published; it is deliberately unexported and
// excluded from every JSON/YAML wire format.
func (c *Config) SetChannelOrderGeneration(instance string, generation uint64) {
	if c.channelOrder == nil {
		c.channelOrder = make(map[string]uint64)
	}
	c.channelOrder[instance] = generation
}

// CopyChannelOrderFrom carries the original snapshot identity, not current
// manager state. Writers override only the entries their exact write changes.
// Call only while constructing an unpublished candidate.
func (c *Config) CopyChannelOrderFrom(source *Config) {
	if source == nil {
		return
	}
	for instance, generation := range source.channelOrder {
		// Keep deletion stamps too: a stale absent-channel snapshot must not
		// remove a newly recreated resource with the same instance name.
		c.SetChannelOrderGeneration(instance, generation)
	}
}
