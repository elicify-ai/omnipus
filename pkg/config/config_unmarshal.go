// config_unmarshal.go: Custom JSON unmarshalling for flexible config field types (FlexibleStringSlice, MailboxesConfig legacy-shape migration)

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

func (f *FlexibleStringSlice) UnmarshalJSON(data []byte) error {
	// Accept a single JSON string for convenience, e.g.:
	// "text": "Thinking..."
	var singleString string
	if err := json.Unmarshal(data, &singleString); err == nil {
		*f = FlexibleStringSlice{singleString}
		return nil
	}

	// Accept a single JSON number too, to keep symmetry with mixed allow_from
	// payloads that may contain numeric identifiers.
	var singleNumber float64
	if err := json.Unmarshal(data, &singleNumber); err == nil {
		*f = FlexibleStringSlice{fmt.Sprintf("%.0f", singleNumber)}
		return nil
	}

	// Try []string first
	var ss []string
	if err := json.Unmarshal(data, &ss); err == nil {
		*f = ss
		return nil
	}

	// Try []interface{} to handle mixed types
	var raw []any
	if err := json.Unmarshal(data, &raw); err != nil {
		var s string
		// fail over to compatible to old format string
		if err = json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("FlexibleStringSlice.UnmarshalJSON: %w", err)
		}
		*f = []string{s}
		return nil
	}

	result := make([]string, 0, len(raw))
	for _, v := range raw {
		switch val := v.(type) {
		case string:
			result = append(result, val)
		case float64:
			result = append(result, fmt.Sprintf("%.0f", val))
		default:
			result = append(result, fmt.Sprintf("%v", val))
		}
	}
	*f = result
	return nil
}

// UnmarshalText implements encoding.TextUnmarshaler to support env variable parsing.
// It handles comma-separated values with both English (,) and Chinese (，) commas.
func (f *FlexibleStringSlice) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*f = nil
		return nil
	}

	s := string(text)
	// Replace Chinese comma with English comma, then split
	s = strings.ReplaceAll(s, "，", ",")
	parts := strings.Split(s, ",")

	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	*f = result
	return nil
}

// UnmarshalJSON accepts BOTH the current nested shape
// {"<agent>": {"<workspace>": {…}}} and the legacy 0.1.0 flat shape
// {"<agent>": {"enabled": …, "workspace_id": "…", …}}, lifting legacy entries
// under their embedded workspace_id. A legacy entry without a workspace_id is
// dropped with a WARN — it was unreachable anyway (the drainer and tool
// registration both require a workspace binding).
//
// Shape detection is per-agent-entry and STRICT: an entry is legacy-flat iff
// ALL its inner values are non-objects, nested iff ALL its inner values are
// objects. A MIX of both within one entry is malformed and returns a hard
// error rather than being silently folded — the previous any-one-non-object
// heuristic misclassified a mixed entry as legacy-flat, decoded the (object-
// valued) nested keys against MailboxConfig's scalar fields, produced an
// empty WorkspaceID, and silently dropped the entire roster under a
// misleading "legacy...unreachable" WARN. A config-load failure that names
// the offending agent is preferable to that silent data loss.
func (m *MailboxesConfig) UnmarshalJSON(data []byte) error {
	var nested map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &nested); err != nil {
		return fmt.Errorf("mailboxes: %w", err)
	}
	out := make(MailboxesConfig, len(nested))
	for agentID, inner := range nested {
		// Legacy detection: the flat shape's inner keys are FIELDS of
		// MailboxConfig ("enabled" is required-in-practice and always present in
		// persisted legacy entries), whose values are scalars — a nested entry's
		// inner values are objects. Probe every raw value so a mixed shape (some
		// object, some scalar) is caught rather than misclassified.
		allObjects := true
		allScalars := true
		for _, raw := range inner {
			trimmed := bytes.TrimLeft(raw, " \t\n\r")
			if len(trimmed) > 0 && trimmed[0] == '{' {
				allScalars = false
			} else {
				allObjects = false
			}
		}
		switch {
		case len(inner) == 0:
			// No inner keys — treat as an empty nested entry (nothing to fold).
			out[agentID] = map[string]MailboxConfig{}
		case allScalars:
			// Re-decode the whole inner object as ONE legacy MailboxConfig.
			rawEntry, err := json.Marshal(inner)
			if err != nil {
				return fmt.Errorf("mailboxes: agent %q: %w", agentID, err)
			}
			var mb MailboxConfig
			if err := json.Unmarshal(rawEntry, &mb); err != nil {
				return fmt.Errorf("mailboxes: legacy entry for agent %q: %w", agentID, err)
			}
			if mb.WorkspaceID == "" {
				slog.Warn("config: dropping legacy mailbox without workspace_id (unreachable)",
					"agent_id", agentID)
				continue
			}
			out[agentID] = map[string]MailboxConfig{mb.WorkspaceID: mb}
		case allObjects:
			byWorkspace := make(map[string]MailboxConfig, len(inner))
			for wsID, raw := range inner {
				if strings.TrimSpace(wsID) == "" {
					slog.Warn("config: dropping mailbox with empty workspace key",
						"agent_id", agentID)
					continue
				}
				var mb MailboxConfig
				if err := json.Unmarshal(raw, &mb); err != nil {
					return fmt.Errorf("mailboxes: agent %q workspace %q: %w", agentID, wsID, err)
				}
				// The inner map key is authoritative; keep the mirror field in sync.
				mb.WorkspaceID = wsID
				byWorkspace[wsID] = mb
			}
			out[agentID] = byWorkspace
		default:
			return fmt.Errorf("mailboxes: agent %q entry is malformed (mixed legacy/nested shape)", agentID)
		}
	}
	*m = out
	return nil
}
