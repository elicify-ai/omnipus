// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "reflect"

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
	c.CarryChannelOrderGeneration(instance, generation)
	if c.channelOrderChanged == nil {
		c.channelOrderChanged = make(map[string]bool)
	}
	c.channelOrderChanged[instance] = true
}

// CarryChannelOrderGeneration preserves an identified saved/read snapshot.
// Unlike Set, it does not declare that this write changed the channel.
func (c *Config) CarryChannelOrderGeneration(instance string, generation uint64) {
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
		c.CarryChannelOrderGeneration(instance, generation)
	}
}

// CopyChannelWriteFrom also preserves the exact entries protected by this
// unpublished write's guards. Clone deliberately copies only snapshot identity.
func (c *Config) CopyChannelWriteFrom(source *Config) {
	c.CopyChannelOrderFrom(source)
	if source == nil {
		return
	}
	for instance := range source.channelOrderChanged {
		generation, _ := source.ChannelOrderGeneration(instance)
		c.SetChannelOrderGeneration(instance, generation)
	}
}

// MergeUnchangedChannelOrders is ONLY for a fresh disk-write publication, never
// an old reload snapshot. A direct admission can update an unrelated channel's
// private identity while this write is loading. Preserve that newer identity
// atomically at publication, but never replace an entry this write changed.
func (c *Config) MergeUnchangedChannelOrders(source *Config) {
	if source == nil {
		return
	}
	for instance, generation := range source.channelOrder {
		if c.channelOrderChanged[instance] {
			continue
		}
		before, existed := source.Channels[instance]
		after, exists := c.Channels[instance]
		current, _ := c.ChannelOrderGeneration(instance)
		if generation > current && existed == exists && reflect.DeepEqual(before, after) {
			c.CarryChannelOrderGeneration(instance, generation)
		}
	}
}
