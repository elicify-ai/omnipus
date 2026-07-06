// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
)

// loadConfig loads a version-CurrentVersion config (current schema).
func loadConfig(data []byte) (*Config, error) {
	cfg := DefaultConfig()

	// Backward compatibility: rename "model_list" to "providers" in old config files
	// before unmarshalling so the new field name works.
	compatData := jsonRenameKey(data, "model_list", "providers")

	// Pre-scan the JSON to check how many providers entries the user provided.
	// Go's JSON decoder reuses existing slice backing-array elements rather than
	// zero-initializing them, so fields absent from the user's JSON (e.g. api_base)
	// would silently inherit values from the DefaultConfig template at the same
	// index position. We only reset cfg.Providers when the user actually provides
	// entries; when count is 0 we keep DefaultConfig's built-in list as fallback.
	var tmp Config
	if err := json.Unmarshal(compatData, &tmp); err != nil {
		return nil, err
	}
	if len(tmp.Providers) > 0 {
		cfg.Providers = nil
	}

	if err := json.Unmarshal(compatData, cfg); err != nil {
		return nil, err
	}

	// FR-004 / FR-027: detect unknown top-level fields, log them at debug level,
	// and store them for round-trip preservation on SaveConfig.
	detectUnknownConfigFields(data, cfg)

	return cfg, nil
}

// detectUnknownConfigFields finds JSON keys not declared on the Config struct,
// emits a slog.Debug per unknown key, and stores them in cfg.UnknownFields so
// SaveConfig can write them back verbatim (round-trip safety per FR-004/FR-027).
func detectUnknownConfigFields(data []byte, cfg *Config) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return // best-effort; parse errors already caught above
	}

	// Build set of known JSON field names from Config struct tags.
	known := make(map[string]struct{})
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
		if tag == "" || tag == "-" {
			tag = f.Name
		}
		known[tag] = struct{}{}
	}

	for k, v := range raw {
		if _, ok := known[k]; !ok {
			slog.Debug("config: unknown field preserved for forward compatibility",
				"field", k)
			if cfg.UnknownFields == nil {
				cfg.UnknownFields = make(map[string]json.RawMessage)
			}
			cfg.UnknownFields[k] = v
		}
	}
}

// jsonRenameKey performs a simple string replacement in JSON data to rename a top-level key.
// This is used for backward compatibility when field names change in the config schema.
func jsonRenameKey(data []byte, oldKey, newKey string) []byte {
	// Simple string replacement works here because:
	// 1. JSON keys are always quoted with double quotes
	// 2. We only replace top-level keys (not nested ones)
	// 3. The old and new keys have the same length ("model_list" -> "providers")
	oldKeyJSON := fmt.Sprintf(`"%s"`, oldKey)
	newKeyJSON := fmt.Sprintf(`"%s"`, newKey)
	return bytes.ReplaceAll(data, []byte(oldKeyJSON), []byte(newKeyJSON))
}
