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
// BrowserBuiltinMetadata builds every browser tool struct a SECOND time, for
// the central /api/v1/tools catalog. A change to the struct shape breaks it, and
// a tool added to RegisterTools but not here is simply absent from the catalog
// with nothing failing — so the two lists are asserted to agree by NAME rather
// than by count alone.
//
// "AllEleven" in the name is now historical and is kept deliberately: the test
// is cited by that name in the §10.1 traceability table of
// docs/internal/specs/browser-workspace-ownership-spec.md (row 4a), and
// renaming it here would orphan that row. The catalog was eleven tools when
// the row was written; ADR-072 D2 added six (select_option, press_key, hover,
// snapshot, handle_dialog, upload_file — the capability spec's §2.1 row for
// metadata.go calls for exactly those "six additions"), so it is SEVENTEEN.
//
// The count and the name were both stale from the moment those six landed, and
// nothing noticed for two waves — this test was red on the wave-4 baseline
// 569685265 and unreported. The literal below is kept rather than derived from
// len(registered) precisely because a literal is what forces a human to look
// when the surface changes; it is the derived half (the name-by-name equality)
// that catches a one-sided edit.
//
// REGISTRY AND CATALOG ARE NOT THE SAME SET, and the difference is exactly one
// name. FR-029 holds browser_upload_file OUT of the registry until issue #659
// closes while keeping it IN the catalog ("held means unregistered, not
// unseeded" — see register.go and TestUploadFile_NotRegistered). So the
// assertion is catalog == registry ∪ {browser_upload_file}, which stays exact:
// any other tool present in one list and not the other still fails, and when
// #659 closes and the RegisterReplacing line lands, the union is unchanged and
// this test keeps passing.
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
	require.Len(t, metadata, 17,
		"the browser catalog is seventeen tools: the eleven that shipped before ADR-072 plus D2's "+
			"six (select_option, press_key, hover, snapshot, handle_dialog, upload_file). Still no "+
			"take-control tool (FR-070) — acquiring the operator's tab is implicit and has no surface")

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

	// The catalog is the registry plus FR-029's one held name, and nothing
	// else. Built from `registered` rather than written out by hand so that
	// adding a tool to both lists needs no edit here, while adding it to only
	// one still fails.
	wantCatalog := map[string]bool{"browser_upload_file": true}
	for name := range registered {
		wantCatalog[name] = true
	}
	require.Equal(t, wantCatalog, catalog,
		"the catalog and the per-agent registry must expose the same browser tools, with exactly "+
			"one documented exception: browser_upload_file is seeded and catalogued but NOT "+
			"registered while FR-029 holds it for issue #659. Any other tool present in one and "+
			"not the other is either invisible in Settings or advertised and uncallable")
	require.True(t, catalog["browser_upload_file"],
		"browser_upload_file must stay in the catalog while it is held out of the registry — "+
			"held means unregistered, not unseeded, and its name has to reach "+
			"buildKnownBuiltinToolNames or the policy seed describes a tool nothing knows about")
}
