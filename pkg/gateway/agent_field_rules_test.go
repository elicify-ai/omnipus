// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestSubagent3pForbiddenFieldsDrift walks every top-level field of
// gen.AgentUpdateRequest via reflection, sets ONLY that one field to a
// non-nil sentinel value, and asserts firstForbiddenSubagent3pField's verdict
// matches subagent3pForbiddenUpdateFields membership (keyed by the field's
// `json` tag name). This pins the documented list against the actual runtime
// checks in firstForbiddenSubagent3pField: adding a forbidden field to one
// without the other fails this test.
func TestSubagent3pForbiddenFieldsDrift(t *testing.T) {
	forbidden := make(map[string]bool, len(subagent3pForbiddenUpdateFields))
	for _, f := range subagent3pForbiddenUpdateFields {
		forbidden[f] = true
	}

	typ := reflect.TypeOf(gen.AgentUpdateRequest{})
	require.Equal(t, reflect.Struct, typ.Kind())

	seenJSONFields := make(map[string]bool, typ.NumField())

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		name, _, _ := strings.Cut(jsonTag, ",")
		if name == "" {
			continue
		}
		seenJSONFields[name] = true

		// Every AgentUpdateRequest field is a pointer (partial-update PATCH
		// semantics: "field absent" vs "field explicitly set" must be
		// distinguishable). This holds structurally for the generated type
		// today — guard rather than silently skip if that ever changes, since
		// a non-pointer field would make this test's "unset" sentinel
		// technique invalid.
		if field.Type.Kind() != reflect.Pointer {
			t.Fatalf(
				"AgentUpdateRequest.%s (json:%q) is not a pointer type — "+
					"the drift test's zero-value/sentinel technique assumes every field is optional",
				field.Name, name,
			)
		}

		t.Run(name, func(t *testing.T) {
			req := gen.AgentUpdateRequest{}
			reqVal := reflect.ValueOf(&req).Elem()
			sentinel := reflect.New(field.Type.Elem()) // non-nil pointer to a zero value of the pointee type
			reqVal.FieldByIndex(field.Index).Set(sentinel)

			_, isForbidden := firstForbiddenSubagent3pField(&req)
			assert.Equal(t, forbidden[name], isForbidden,
				"field %q: subagent3pForbiddenUpdateFields says forbidden=%v but "+
					"firstForbiddenSubagent3pField returned forbidden=%v — the list and the "+
					"check have drifted apart",
				name, forbidden[name], isForbidden)
		})
	}

	// Reverse direction: every name in subagent3pForbiddenUpdateFields must
	// correspond to an actual AgentUpdateRequest JSON field. Catches a typo or
	// a stale entry left behind after a wire-schema rename.
	for _, f := range subagent3pForbiddenUpdateFields {
		assert.True(t, seenJSONFields[f],
			"subagent3pForbiddenUpdateFields contains %q, which is not a JSON field on gen.AgentUpdateRequest", f)
	}
}

// TestFirstForbiddenSubagent3pField_NilRequest guards the nil-safety of the
// helper (defensive; callers always pass a non-nil pointer today, but a nil
// dereference here would panic the whole updateAgent handler).
func TestFirstForbiddenSubagent3pField_NilRequest(t *testing.T) {
	field, forbidden := firstForbiddenSubagent3pField(nil)
	assert.Equal(t, "", field)
	assert.False(t, forbidden)
}

// jsonFieldNames walks the top-level fields of a struct type via reflection
// and returns the set of JSON field names (the part of the `json` tag before
// the first comma), skipping fields with no tag or an explicit "-". Shared by
// TestSubagent3pCreateVsUpdateForbiddenFieldsDrift below.
func jsonFieldNames(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()
	require.Equal(t, reflect.Struct, typ.Kind())
	names := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		jsonTag := typ.Field(i).Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		name, _, _ := strings.Cut(jsonTag, ",")
		if name == "" {
			continue
		}
		names[name] = true
	}
	return names
}

// TestSubagent3pCreateVsUpdateForbiddenFieldsDrift pins the CREATE variant
// (gen.AgentCreateRequestSubagent3p) against the UPDATE forbidden-field list
// (subagent3pForbiddenUpdateFields), in both directions:
//
//  1. Every field name in subagent3pForbiddenUpdateFields (forbidden on PUT)
//     must NOT exist as a JSON field on AgentCreateRequestSubagent3p — a
//     field the update path rejects must never have been settable on create
//     in the first place (forbidden-on-update implies absent-from-3p-create).
//  2. Every JSON field present on gen.AgentCreateRequestMain but absent from
//     gen.AgentCreateRequestSubagent3p must be accounted for: either it is in
//     subagent3pForbiddenUpdateFields, or it is in the documented
//     subagent3pCreateOnlyExemptFields map. A field satisfying neither is a
//     newly-added Main-only property nobody has decided how subagent_3p
//     should treat — this test fails until it is deliberately placed in one
//     of the two sets.
//
// This complements TestSubagent3pForbiddenFieldsDrift above (which only pins
// agent_field_rules.go's list against itself, via firstForbiddenSubagent3pField);
// this test additionally pins it against the generated create-side types.
func TestSubagent3pCreateVsUpdateForbiddenFieldsDrift(t *testing.T) {
	forbidden := make(map[string]bool, len(subagent3pForbiddenUpdateFields))
	for _, f := range subagent3pForbiddenUpdateFields {
		forbidden[f] = true
	}

	set3p := jsonFieldNames(t, reflect.TypeOf(gen.AgentCreateRequestSubagent3p{}))
	setMain := jsonFieldNames(t, reflect.TypeOf(gen.AgentCreateRequestMain{}))

	// Direction 1: forbidden-on-update implies absent-from-3p-create.
	for name := range forbidden {
		assert.False(t, set3p[name],
			"field %q is in subagent3pForbiddenUpdateFields (rejected on PUT) but IS present on "+
				"gen.AgentCreateRequestSubagent3p — a subagent_3p create could set a field its own PUT then rejects",
			name)
	}

	// Direction 2: every Main-only field (present on Main, absent from 3p)
	// must be accounted for by the forbidden list or the exemption map.
	for name := range setMain {
		if set3p[name] {
			continue
		}
		_, exempt := subagent3pCreateOnlyExemptFields[name]
		assert.True(t, forbidden[name] || exempt,
			"field %q is present on AgentCreateRequestMain, absent from AgentCreateRequestSubagent3p, but is "+
				"neither in subagent3pForbiddenUpdateFields nor in subagent3pCreateOnlyExemptFields — add it to "+
				"one so the omission is a deliberate, reviewed decision", name)
	}
}
