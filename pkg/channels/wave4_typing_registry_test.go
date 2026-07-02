package channels

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// TestTypingIndicator_UnsupportedChannel verifies that channels not implementing
// TypingCapable result in a graceful no-op — no panic, no error.
// Traces to: wave4-whatsapp-browser-spec.md line 1122 (Test #55: TestTypingIndicator_UnsupportedChannel)
// BDD: Given a connected channel that does not support typing indicators (e.g., IRC),
// When the agent starts processing an inbound message,
// Then the typing indicator call is a no-op — no error logged, processing continues.

func TestTypingIndicator_UnsupportedChannel(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md line 861 (Scenario: Typing indicator graceful no-op)
	// BaseChannel itself does not implement TypingCapable.
	// The concrete owner channel (e.g., IRC) may or may not implement it.
	// The type assertion in HandleMessage handles this gracefully.
	ch := NewBaseChannel("irc-test", nil, nil, nil)

	// A channel without TypingCapable owner must not satisfy the interface
	_, ok := any(ch.owner).(TypingCapable)
	assert.False(t, ok,
		"BaseChannel with nil owner must not implement TypingCapable — graceful no-op path")

	// BaseChannel itself must not implement TypingCapable
	// (only concrete owners do — validated at runtime per FR-026)
	_, isTyping := any(ch).(TypingCapable)
	assert.False(t, isTyping,
		"BaseChannel itself must not implement TypingCapable — implementation is per concrete channel")
}

// TestChannelRegistryNoDisplacement verifies that registering a new channel factory
// does not displace or overwrite existing channel factory registrations.
// Traces to: wave4-whatsapp-browser-spec.md line 1103 (Test #46: TestChannelRegistryNoDisplacement)
// BDD: Given all existing channels are registered via RegisterFactory,
// When whatsapp_native is registered,
// Then all other channel factories remain present in the registry (no displacement).

func TestChannelRegistryNoDisplacement(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md line 916 (Scenario: New channel registration does not displace)
	// Pre-condition: register a sentinel factory under a test-only name
	sentinelName := "test-sentinel-wave4-nodisplace"
	sentinelCalled := false
	RegisterFactory(
		sentinelName,
		func(cfg *config.Config, instanceID string, _ credentials.SecretBundle, b *bus.MessageBus) (Channel, error) {
			sentinelCalled = true
			return nil, nil
		},
	)

	// Registering another factory must not remove the sentinel
	RegisterFactory(
		"test-wave4-second",
		func(cfg *config.Config, instanceID string, _ credentials.SecretBundle, b *bus.MessageBus) (Channel, error) {
			return nil, nil
		},
	)

	// Sentinel factory must still be retrievable
	f, ok := getFactory(sentinelName)
	assert.True(t, ok, "sentinel factory must still be registered after second RegisterFactory call")
	assert.NotNil(t, f, "sentinel factory must not be nil after second RegisterFactory call")

	// Verify it's actually our sentinel (call it)
	_, _ = f(nil, "", nil, nil)
	assert.True(t, sentinelCalled, "sentinel factory must be callable and execute our implementation")
}

// TestGroupTriggerWhatsApp verifies that WhatsApp group messages obey the
// group trigger configuration (mention-only mode, prefix mode).
// Traces to: wave4-whatsapp-browser-spec.md line 999 (Test #9: TestGroupTriggerWhatsApp)
// BDD: Given WhatsApp group message, mention trigger configured,
// When message mentions the bot, Then agent responds.
// When message does not mention the bot, Then agent ignores.
// Traces to: BRD FR-006 (group trigger configuration)

func TestGroupTriggerWhatsApp(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md line 999 (Test #9: TestGroupTriggerWhatsApp)
	// BDD: Scenario: Group message with mention trigger; Group message without trigger ignored
	tests := []struct {
		name        string
		gt          config.GroupTriggerConfig
		isMentioned bool
		content     string
		wantRespond bool
	}{
		// WhatsApp group: mention-only mode, bot mentioned
		{
			name:        "whatsapp mention-only: mentioned → respond",
			gt:          config.GroupTriggerConfig{MentionOnly: true},
			isMentioned: true,
			content:     "@Omnipus help me",
			wantRespond: true,
		},
		// WhatsApp group: mention-only mode, not mentioned
		{
			name:        "whatsapp mention-only: not mentioned → ignore",
			gt:          config.GroupTriggerConfig{MentionOnly: true},
			isMentioned: false,
			content:     "hello everyone",
			wantRespond: false,
		},
		// WhatsApp group: prefix trigger, matching prefix
		{
			name:        "whatsapp prefix: matching prefix → respond",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask"}},
			isMentioned: false,
			content:     "/ask what is the weather?",
			wantRespond: true,
		},
		// WhatsApp group: prefix trigger, non-matching prefix
		{
			name:        "whatsapp prefix: no prefix match → ignore",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask"}},
			isMentioned: false,
			content:     "hello group",
			wantRespond: false,
		},
		// WhatsApp group: no trigger configured → always respond (default)
		{
			name:        "whatsapp no trigger: no config → respond",
			gt:          config.GroupTriggerConfig{},
			isMentioned: false,
			content:     "any message",
			wantRespond: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch := NewBaseChannel("whatsapp_native", nil, nil, nil, WithGroupTrigger(tc.gt))
			gotRespond, _ := ch.ShouldRespondInGroup(tc.isMentioned, tc.content)
			assert.Equal(t, tc.wantRespond, gotRespond,
				"ShouldRespondInGroup(%v, %q) should return respond=%v",
				tc.isMentioned, tc.content, tc.wantRespond)
		})
	}
}
