package generated

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func c85ControlNode(t *testing.T, s c85SchemaSet, doc string, path ...string) map[string]any {
	t.Helper()
	node, ok := yamlPath(t, "C-85 in-memory control", s.docs[doc], path...).(map[string]any)
	require.True(t, ok, "control must address an actual schema object")
	return node
}

// Section R2's five instrument controls are in-memory corruptions, never edits
// to production/contracts and never an accepted-differences baseline. Additional
// controls protect requiredness, closed objects, both reference targets and
// cycles. These check the comparator, not independent CHECK mutation coverage.
func TestContractCopies_C85ReferenceAwareNegativeControls(t *testing.T) {
	t.Run("relative_and_internal_references_plus_verified_annotation_agree", func(t *testing.T) {
		canonical, handSync := c85ResolvedCopies(t)
		require.Equal(t, canonical, handSync)
		origin := yamlPath(t, "resolved component", handSync, "properties", "history_origin")
		require.Equal(t, []any{"position"}, yamlPath(t, "resolved origin", origin, "required"))
		require.Equal(t, "^hp1_[A-Za-z0-9_-]{43}$", yamlPath(t, "resolved origin", origin, "properties", "position", "pattern"))
		require.Equal(t, false, yamlPath(t, "resolved origin", origin, "additionalProperties"))
	})
	cases := []struct {
		name    string
		path    []string
		key     string
		value   any
		remove  bool
		target  string
		wantErr string
	}{
		// Architect's exact five controls: origin bound, origin member, text
		// bound, unresolved reference and unrelated generator annotation.
		{"origin_position_minLength_drift", []string{"properties", "position"}, "minLength", 46, false, "origin", ""},
		{"missing_history_origin_member", []string{"properties"}, "history_origin", nil, true, "component", ""},
		{"member_named_title_is_wire_drift", []string{"properties"}, "title", map[string]any{"type": "string"}, false, "component", ""},
		{"member_named_description_is_wire_drift", []string{"properties"}, "description", map[string]any{"type": "string"}, false, "component", ""},
		{"frame_text_maxLength_drift", []string{"properties", "text"}, "maxLength", 2001, false, "component", ""},
		{"unresolved_external_reference", []string{"properties", "history_origin"}, "$ref", "./missing-origin.yaml", false, "component", "C-85 unresolved document reference"},
		{"unrelated_generator_annotation", []string{"properties", "history_origin"}, "x-go-type", "UnrelatedType", false, "component", "C-85 unexpected generator annotation"},
		{"origin_position_pattern_drift", []string{"properties", "position"}, "pattern", "^anything$", false, "origin", ""},
		{"origin_position_maxLength_drift", []string{"properties", "position"}, "maxLength", 48, false, "origin", ""},
		{"origin_position_requiredness_drift", nil, "required", []any{}, false, "origin", ""},
		{"origin_entry_id_requiredness_drift", nil, "required", []any{"position", "entry_id"}, false, "origin", ""},
		{"origin_entry_id_type_drift", []string{"properties", "entry_id"}, "type", "integer", false, "origin", ""},
		{"origin_closed_object_drift", nil, "additionalProperties", true, false, "origin", ""},
		{"frame_const_drift", []string{"properties", "type"}, "const", "different_frame", false, "component", ""},
		{"origin_enum_drift", []string{"properties", "position"}, "enum", []any{"invented"}, false, "origin", ""},
		{"unknown_extension_not_ignored", []string{"properties", "history_origin"}, "x-unrelated", "unexpected", false, "component", ""},
		{"unresolved_internal_reference", []string{"components", "schemas", "BrowserHandoverNoticeFrame", "properties", "history_origin"}, "$ref", "#/components/schemas/MissingOrigin", false, "async", "C-85 unresolved pointer"},
		{"inline_origin_target_is_inspected", []string{"components", "schemas", "HistoryEntryOrigin", "properties", "position"}, "minLength", 46, false, "async", ""},
		{"cyclic_reference_fails_closed", nil, "$ref", "./HistoryEntryOrigin.yaml", false, "origin", "C-85 cyclic reference"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := c85LoadSchemas(t) // fresh independent documents for every control
			doc := map[string]string{"origin": s.origin, "component": s.component, "async": s.async}[tc.target]
			node := c85ControlNode(t, s, doc, tc.path...)
			if tc.remove {
				require.Contains(t, node, tc.key, "instrument must remove a real member")
				delete(node, tc.key)
			} else {
				node[tc.key] = tc.value
			}
			canonical, handSync, err := s.copies()
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr, "uninspected/invalid reference cannot pass")
				return
			}
			require.NoError(t, err, "valid schema drift must reach the recursive comparison")
			require.NotEqual(t, canonical, handSync, "the one-sided corruption must be detected")
		})
	}
}
