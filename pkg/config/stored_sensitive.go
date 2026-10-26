// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// SetStoredSensitiveSource binds the complete encrypted-store computation to
// this config, including every unused card version. Clones retain it; every
// registration recomputes under the shared publication lock (FR-089).
func (c *Config) SetStoredSensitiveSource(source func() ([]string, error)) {
	c.sensitiveMu.Lock()
	defer c.sensitiveMu.Unlock()
	c.storedSensitiveSource = source
}

func loadConfigWithSensitiveSource(path string, store CredentialStore, hook SelfHealWriteHook) (*Config, error) {
	cfg, err := loadConfigInternal(path, store, hook)
	if err != nil {
		return cfg, err
	}
	if source, ok := store.(interface{ SensitiveValues() ([]string, error) }); ok {
		cfg.SetStoredSensitiveSource(source.SensitiveValues)
	}
	return cfg, nil
}
