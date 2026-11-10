// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// ws_effort_choice.go — the chat frame's per-turn provider and effort
// (effort-levels spec S-15, S-75, dataset D5 rows 6-7): metadata.provider is
// a string of at most 64 characters and metadata.reasoning_effort a string of
// at most 32, checked whatever gateway.validate_inbound says; valid values
// ride the bus message metadata to the agent loop with model_name.

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/providers/effortdialect"
)

const frameProviderMaxChars = 64

type frameEffortChoice struct {
	provider string
	effort   string
	// effortSet is true when the frame carried reasoning_effort at all: ""
	// is then a deliberate "Not set" for this turn.
	effortSet bool
}

// parseFrameEffortChoice reads metadata.provider and
// metadata.reasoning_effort, returning a refusal message for an invalid one.
func parseFrameEffortChoice(meta map[string]any) (frameEffortChoice, string) {
	var c frameEffortChoice
	if raw, present := meta["provider"]; present && raw != nil {
		v, ok := raw.(string)
		if !ok {
			return c, "metadata.provider must be a string"
		}
		if utf8.RuneCountInString(v) > frameProviderMaxChars {
			return c, fmt.Sprintf("metadata.provider must be at most %d characters", frameProviderMaxChars)
		}
		c.provider = strings.TrimSpace(v)
	}
	if raw, present := meta[effortdialect.EffortOptionKey]; present {
		v, ok := raw.(string)
		if !ok {
			return c, restEffortNullMessage
		}
		if utf8.RuneCountInString(v) > restEffortMaxChars {
			return c, restEffortLengthMessage
		}
		c.effort, c.effortSet = strings.TrimSpace(v), true
	}
	return c, ""
}

// apply writes the choice into the bus message metadata.
func (c frameEffortChoice) apply(meta *map[string]string) {
	if c.provider == "" && !c.effortSet {
		return
	}
	if *meta == nil {
		*meta = map[string]string{}
	}
	if c.provider != "" {
		(*meta)["provider"] = c.provider
	}
	if c.effortSet && c.effort != "" {
		(*meta)[effortdialect.EffortOptionKey] = c.effort
	}
}
