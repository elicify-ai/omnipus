package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ReadPersistedToolCeiling returns the global tool-policy ceiling as the next
// config load would see it: the persisted sandbox.tool_policies of
// <home>/config.json with the shipped default filled in for any key the file
// does not carry (what ReconcileToolPolicyCeiling adds on load). A missing
// config.json yields the shipped defaults. God mode is not applied.
func ReadPersistedToolCeiling(home string) (map[string]ToolPolicy, error) {
	out := map[string]ToolPolicy{}
	for k, v := range DefaultToolPolicyCeiling() {
		out[k] = ToolPolicy(v)
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var doc struct {
		Sandbox struct {
			ToolPolicies map[string]string `json:"tool_policies"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	for k, v := range doc.Sandbox.ToolPolicies {
		out[k] = ToolPolicy(v)
	}
	return out, nil
}
