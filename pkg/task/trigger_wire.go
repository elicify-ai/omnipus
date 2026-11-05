// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

// trigger_wire.go — the one trigger conversion REST and the task tools share
// (tool-parity spec r17 §7.2 TK-30): the internal Trigger built from its wire
// parts, and the refusal of the removed timing keys. Moved here from the
// gateway so create_task and update_task accept exactly what REST accepts.

import "strings"

// LegacyTimingKeysMessage reports the removed trigger keys (session-core
// DEL-19 / C-TIMING) found among a trigger config's undeclared properties. The
// wire schema keeps `config` open, so a client that still sends `every_ms` or
// `cron_expr` is not rejected by decoding; it is refused here, by name,
// instead of being accepted and silently never firing. Empty means none.
func LegacyTimingKeysMessage(extra map[string]any) string {
	var found []string
	for _, key := range []string{"every_ms", "cron_expr"} {
		if _, ok := extra[key]; ok {
			found = append(found, key)
		}
	}
	if len(found) == 0 {
		return ""
	}
	return "trigger config." + strings.Join(found, ", config.") +
		" is no longer supported; use a recurring trigger with config.rrule"
}

// BuildTrigger constructs an internal trigger from its primitive wire parts.
// The generated request structs each have their own anonymous trigger type
// with an identically shaped config, so callers decompose them and pass the
// primitives here. The pointers are copied, never aliased.
func BuildTrigger(kind string, atMs *int64, rrule *string, dtstartMs *int64, tz *string) *Trigger {
	tr := &Trigger{Type: TriggerType(kind)}
	if atMs != nil {
		v := *atMs
		tr.Config.AtMs = &v
	}
	if rrule != nil {
		v := *rrule
		tr.Config.Rrule = &v
	}
	if dtstartMs != nil {
		v := *dtstartMs
		tr.Config.DtstartMs = &v
	}
	if tz != nil {
		v := *tz
		tr.Config.Tz = &v
	}
	return tr
}
