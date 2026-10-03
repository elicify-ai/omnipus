//go:build goolm && stdjson

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func orphanACStructureNegatives(t *testing.T) {
	cases := []struct {
		name   string
		suffix []providers.Message
		err    string
	}{
		{"empty_declared_ID", []providers.Message{
			orphanACGroup("malformed declaration", "", "missing"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"duplicate_declared_ID", []providers.Message{
			orphanACGroup("duplicate declaration", "missing", "missing"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"duplicate_declared_ID_and_duplicate_marker", []providers.Message{
			orphanACGroup("duplicate declaration", "missing", "missing"), orphanACMarker("missing"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"duplicate_partial_results", []providers.Message{
			orphanACGroup("invalid partial group", "done", "missing"),
			orphanACResult("done", "first partial result"), orphanACResult("done", "duplicate partial result"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"duplicate_results_with_multiple_missing_markers", []providers.Message{
			orphanACGroup("invalid three-call group", "done", "missing-a", "missing-b"),
			orphanACResult("done", "first partial result"), orphanACResult("done", "duplicate partial result"),
			orphanACMarker("missing-a"), orphanACMarker("missing-b"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"duplicate_result_after_marker", []providers.Message{
			orphanACGroup("duplicate is still corruption", "done", "missing"),
			orphanACResult("done", "first partial result"), orphanACMarker("missing"), orphanACResult("done", "duplicate after record"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"unowned_result_in_group", []providers.Message{
			orphanACGroup("foreign result is not owned partial progress", "missing"),
			orphanACResult("undeclared", "foreign result"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"empty_result_ID_in_group", []providers.Message{
			orphanACGroup("empty result identity is corruption", "missing"),
			orphanACResult("", "result without ID"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"completed_group_with_duplicate_result", []providers.Message{
			orphanACGroup("duplicated completed result", "done"),
			orphanACResult("done", "complete result"), orphanACResult("done", "duplicate complete result"), orphanACMarker("done"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"resolved_result_separated_by_unrelated_control", []providers.Message{
			orphanACGroup("structurally separated result", "done"),
			{Role: "system", Content: "not a tool result"}, orphanACResult("done", "result after control"), orphanACMarker("done"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"standalone_result_and_unbound_marker", []providers.Message{
			orphanACResult("missing", "no assistant declaration exists"), orphanACMarker("missing"),
		}, "context request: orphan tool result at message 2"},
		{"complete_group_then_marker_then_orphan_duplicate", []providers.Message{
			orphanACGroup("complete group with later corruption", "done"),
			orphanACResult("done", "owned complete result"), orphanACMarker("done"), orphanACResult("done", "unowned duplicate after record"),
		}, "context request: orphan tool result at message 5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := append([]providers.Message{{Role: "user", Content: "invalid structure must not be sanitized"}}, tc.suffix...)
			orphanACAssertUnsupported(t, raw, tc.err)
		})
	}
}
