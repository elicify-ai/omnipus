package structcall_test

// Instrument tests for the struct caller (#1963, Part L). The helper is only
// trusted if it (1) fills a struct argument by field name and reads the result
// back by field name, with named string types going in and coming out as plain
// strings, (2) calls a method expression whose receiver is the first argument
// (the shape of tools.ToolSurface.Plan), and (3) refuses, with an error that
// names what is wrong, every shape it cannot call: no function, a field that
// does not exist, a value of the wrong kind, a wrong argument count and a
// result that is not a struct. The types below are local stand-ins with the
// same shape as the two production authorities; nothing here decides a product
// result.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/tests/helpers/structcall"
)

type policy string

type decisionInput struct {
	Key      string
	Granted  bool
	Global   policy
	GodMode  bool
	internal string
}

type decisionResult struct {
	Outcome        policy
	PermissionOpen bool
	secret         string
}

func decide(in decisionInput) decisionResult {
	result := decisionResult{Outcome: "deny", secret: "hidden" + in.internal}
	if in.Granted && in.Global == "allow" {
		result.Outcome = "allow"
	}
	if in.GodMode {
		result.PermissionOpen = true
	}
	return result
}

type surface struct{ Levels map[string]string }

type planResult struct {
	InRequest    bool
	ManifestLine string
}

func (s surface) Plan(name, description string, denied bool) planResult {
	if denied {
		return planResult{}
	}
	if s.Levels[name] == "full" {
		return planResult{InRequest: true}
	}
	return planResult{ManifestLine: name + " - " + description}
}

func TestStructCall_FillsAndReadsByFieldName(t *testing.T) {
	t.Run("named string types go in from plain strings and come back as plain strings", func(t *testing.T) {
		got := structcall.Call(t, decide, structcall.Fields{"Key": "notes", "Granted": true, "Global": "allow"})
		require.Equal(t, "allow", got.Str(t, "Outcome"))
		require.False(t, got.Bool(t, "PermissionOpen"))
		_, leaked := got["secret"]
		require.False(t, leaked, "an unexported result field is not read")
	})

	t.Run("a field the caller does not name keeps its zero value", func(t *testing.T) {
		got := structcall.Call(t, decide, structcall.Fields{"Key": "notes", "Granted": true})
		require.Equal(t, "deny", got.Str(t, "Outcome"), "Global stayed empty, so the decision denies")
	})

	t.Run("a method expression takes its receiver first and a map field is filled", func(t *testing.T) {
		full := structcall.Call(t, surface.Plan, structcall.Fields{"Levels": map[string]string{"serve_web": "full"}}, "serve_web", "d", false)
		require.True(t, full.Bool(t, "InRequest"))
		listed := structcall.Call(t, surface.Plan, structcall.Fields{"Levels": map[string]string{"serve_web": "hidden"}}, "serve_web", "Serve it.", false)
		require.False(t, listed.Bool(t, "InRequest"))
		require.Equal(t, "serve_web - Serve it.", listed.Str(t, "ManifestLine"))
		denied := structcall.Call(t, surface.Plan, structcall.Fields{}, "serve_web", "Serve it.", true)
		require.Equal(t, "", denied.Str(t, "ManifestLine"))
	})
}

func TestStructCall_RefusesEveryShapeItCannotCall(t *testing.T) {
	for _, row := range []struct {
		name string
		fn   any
		args []any
		want string
	}{
		{"nothing is bound", nil, nil, "no function is bound"},
		{"a typed nil function", (func(decisionInput) decisionResult)(nil), []any{structcall.Fields{}}, "no function is bound"},
		{"not a function", true, nil, "no function is bound"},
		{"a field that does not exist", decide, []any{structcall.Fields{"Trust": "ask"}}, `has no settable field "Trust"`},
		{"an unexported field", decide, []any{structcall.Fields{"internal": "x"}}, `has no settable field "internal"`},
		{"a value of the wrong kind", decide, []any{structcall.Fields{"Granted": "yes"}}, "cannot be stored"},
		{"an int never becomes a string", decide, []any{structcall.Fields{"Key": 65}}, "cannot be stored"},
		{"a nil value", decide, []any{structcall.Fields{"Key": nil}}, "a nil value cannot be stored"},
		{"too few arguments", surface.Plan, []any{structcall.Fields{}}, "passes 1 argument(s)"},
		{"fields for a parameter that is not a struct", surface.Plan, []any{structcall.Fields{}, structcall.Fields{}, "d", false}, "not a struct"},
		{"a function with no struct result", func(string) string { return "" }, []any{"x"}, "reads one struct result"},
	} {
		t.Run(row.name, func(t *testing.T) {
			_, err := structcall.Invoke(row.fn, row.args...)
			require.ErrorContains(t, err, row.want)
		})
	}
}
