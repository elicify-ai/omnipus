package browser

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// register_resolver_test.go — FR-002a.
//
// The reported defect in ADR-072 §1.1 is not "the wrong browser was chosen". It
// is that no choice was ever made: each tool closed over the manager it was
// REGISTERED with, so which browser a call drove was decided at registration
// time by which agent happened to be registering, and could not follow the turn.
//
// These tests pin the structural half of the fix, because the behavioural half
// can be satisfied by a tool that resolves correctly today and is quietly given
// a captured manager again tomorrow.

// TestRegisterTools_NoBoundManagerField asserts that no registered browser tool
// holds a *BrowserManager in any field, at any depth.
//
// The scope is deliberately tools.Tool implementers and nothing else:
// CaptureSession, LiveViewRegistry, LiveView and BrowserCoordinator all hold
// manager references legitimately — they are per-browser objects, not per-turn
// ones — and flagging them would make this test unfixable rather than useful.
func TestRegisterTools_NoBoundManagerField(t *testing.T) {
	registry := tools.NewToolRegistry()
	mgr, err := NewBrowserManager(BrowserConfig{}, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	require.NoError(t, RegisterTools(registry, newFixedResolver(mgr), true, t.TempDir(), true))

	managerType := reflect.TypeOf((*BrowserManager)(nil))
	checked := 0

	for _, tool := range append(registry.GetAll(), BrowserBuiltinMetadata()...) {
		v := reflect.ValueOf(tool)
		for v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			continue
		}
		typ := v.Type()
		checked++
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			require.NotEqual(t, managerType, f.Type,
				"%s.%s is a captured *BrowserManager. A tool that holds a manager drives whichever "+
					"browser it was REGISTERED with, not the one this turn is rooted in — which is "+
					"the ADR-072 §1.1 defect. Resolve it per Execute via ManagerResolver instead.",
				typ.Name(), f.Name)
		}
	}

	require.GreaterOrEqual(t, checked, 11,
		"the reflection walk inspected %d tool structs; if it inspects none it passes vacuously", checked)
}

// TestBrowserBuiltinMetadata_ConstructsAllEleven is the second construction
// site nobody finds from register.go.
//
// BrowserBuiltinMetadata builds the same eleven tool structs a SECOND time, for
// the central /api/v1/tools catalog. A change to the struct shape breaks it, and
// a tool added to RegisterTools but not here is simply absent from the catalog
// with nothing failing — so the two lists are asserted to agree by NAME rather
// than by count alone.
func TestBrowserBuiltinMetadata_ConstructsAllEleven(t *testing.T) {
	registry := tools.NewToolRegistry()
	mgr, err := NewBrowserManager(BrowserConfig{}, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	require.NoError(t, RegisterTools(registry, newFixedResolver(mgr), true, t.TempDir(), true))

	registered := map[string]bool{}
	for _, tool := range registry.GetAll() {
		registered[tool.Name()] = true
	}

	metadata := BrowserBuiltinMetadata()
	require.Len(t, metadata, 11, "the browser tool surface is eleven tools (FR-070: no take-control tool)")

	catalog := map[string]bool{}
	for _, tool := range metadata {
		name := tool.Name()
		require.NotEmpty(t, name)
		require.Equal(t, tools.CategoryBrowser, tool.Category())
		require.NotEmpty(t, tool.Description(),
			"%s's metadata instance must produce a description without a manager — "+
				"these instances are NEVER Execute()d and carry a nil resolver", name)
		catalog[name] = true
	}

	require.Equal(t, registered, catalog,
		"the catalog and the per-agent registry must expose exactly the same browser tools; "+
			"a tool present in one and not the other is either invisible in Settings or "+
			"advertised and uncallable")
}
