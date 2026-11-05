// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// SetAgentEnvSensitiveSource binds the read of every stored external-worker
// env value (r17 SR-2, defence in depth beside the SR-3 argument
// redaction). The agent records live outside config.json, so the owner of
// the record store supplies the read. Clones keep the source.
func (c *Config) SetAgentEnvSensitiveSource(source func() ([]string, error)) {
	c.sensitiveMu.Lock()
	defer c.sensitiveMu.Unlock()
	c.agentEnvSource = source
}

// RefreshAgentEnvSensitiveValues re-reads the stored env values and makes
// them the env part of the masking set: a value set by a write is masked
// from now on, a cleared or removed one no longer is. This config's replacer
// rebuilds on next use and the process-wide replacer is republished with the
// registered plaintexts plus the env values. A failed read keeps the
// previous env values as well (never unmasks on an error) and is logged.
func (c *Config) RefreshAgentEnvSensitiveValues() {
	sensitiveRegistrationMu.Lock()
	defer sensitiveRegistrationMu.Unlock()
	c.sensitiveMu.Lock()
	defer c.sensitiveMu.Unlock()
	if c.agentEnvSource != nil {
		values, err := c.agentEnvSource()
		if err != nil {
			slog.Error("credential masking: some stored agent environment values could not be read; keeping the previous values as well",
				"error", err)
			values = append(values, c.agentEnvValues...)
		}
		c.agentEnvValues = unique(values)
	}
	c.sensitiveCache = nil
	logger.SetSensitiveValueReplacer(newSensitiveReplacer(c.publishedSensitiveValues()))
}

// agentEnvSensitiveValues is the env part of the masking set: the last read
// of the stored records plus any env value this config's own roster holds.
// Must be called with sensitiveMu held.
func (c *Config) agentEnvSensitiveValues() []string {
	values := append([]string(nil), c.agentEnvValues...)
	for i := range c.Agents.List {
		sub := c.Agents.List[i].Subagents
		if sub == nil || sub.Executor == nil {
			continue
		}
		for _, v := range sub.Executor.EnvOverrides {
			if v != "" {
				values = append(values, v)
			}
		}
	}
	return values
}

// publishedSensitiveValues is the process-wide masking set: the registered
// plaintexts plus the env values. Must be called with sensitiveMu held.
func (c *Config) publishedSensitiveValues() []string {
	return unique(append(append([]string{}, c.registeredSensitive...), c.agentEnvSensitiveValues()...))
}
