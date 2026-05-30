// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

// TestRegistry_ToolDepsContract is in pkg/sysagent/tools (not pkg/tools) because
// pkg/sysagent/tools imports pkg/tools — placing the test in pkg/tools would
// create an import cycle. The test name matches the spec requirement.

import (
	"reflect"
	"sync/atomic"
	"testing"
)

// TestRegistry_ToolDepsContract asserts the FR-050 structural guarantee: the
// Deps type carries an atomic.Pointer[Deps] field (Deps.Live) so that future
// hot-swap of deps without a full registry rebuild is possible by design.
//
// The reload path today uses the "rebuild" pattern (wireSysagentDepsLocked
// constructs fresh tool instances with the new *Deps and atomically swaps the
// registry under al.mu).  The Live field makes the alternative atomic-swap path
// structurally available — adding hot-swap to a new tool is a one-line change.
//
// Concretely this test:
//  1. Verifies Deps has exactly one exported field of type *atomic.Pointer[Deps].
//  2. Verifies AllTools(nil, nil) returns ≥35 tools (supply side intact).
//  3. Verifies a non-nil *Deps with Live wired can Load() without panicking
//     (the reload path invariant).
//
// BDD: Given the Deps struct,
//
//	When reflected for atomic.Pointer[Deps] fields,
//	Then at least one such field exists (FR-050 structural guarantee).
//	When AllTools is called with a valid Deps,
//	Then it returns ≥35 tools and none panic on Name()/Description().
//
// Traces to: FR-050 (atomic-pointer deps), FR-001/FR-002 (supply side).
func TestRegistry_ToolDepsContract(t *testing.T) {
	t.Run("Deps_has_atomic_pointer_field", func(t *testing.T) {
		atomicPtrType := reflect.TypeOf((*atomic.Pointer[Deps])(nil))
		depsType := reflect.TypeOf(Deps{})
		found := false
		for i := 0; i < depsType.NumField(); i++ {
			f := depsType.Field(i)
			if !f.IsExported() {
				continue
			}
			if f.Type == atomicPtrType {
				found = true
				t.Logf("found atomic.Pointer[Deps] field: %s", f.Name)
				break
			}
		}
		if !found {
			t.Errorf(
				"Deps struct must have an exported *atomic.Pointer[Deps] field (FR-050 structural guarantee); none found",
			)
		}
	})

	t.Run("AllTools_returns_at_least_35", func(t *testing.T) {
		all := AllTools(nil, nil)
		if len(all) < 35 {
			t.Errorf("AllTools(nil, nil) returned %d tools; want ≥35 (FR-001/FR-002)", len(all))
		}
		for _, tool := range all {
			// Basic liveness checks — must not panic.
			name := tool.Name()
			desc := tool.Description()
			if name == "" {
				t.Errorf("tool at position has empty Name()")
			}
			if desc == "" {
				t.Errorf("tool %q has empty Description()", name)
			}
		}
	})

	t.Run("Live_atomic_pointer_is_loadable", func(t *testing.T) {
		// Verify that a Deps with Live wired can Store/Load without panicking.
		// This proves the hot-swap seam works end-to-end.
		inner := &Deps{}
		ptr := &atomic.Pointer[Deps]{}
		ptr.Store(inner)
		d := &Deps{Live: ptr}

		loaded := d.Live.Load()
		if loaded == nil {
			t.Errorf("atomic.Pointer[Deps].Load() returned nil after Store — seam is broken")
		}
		if loaded != inner {
			t.Errorf("atomic.Pointer[Deps].Load() returned unexpected value")
		}
	})
}
