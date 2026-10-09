// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U3 (ordinary intake), pkg/agent slice — the K-class
// source check for the competing-intake removals.
//
// Spec: docs/internal/specs/session-core-spec.md FR-009; DEL-03, DEL-04,
// DEL-22/24; the §Explicit DELETE Requirements note that erasure is proven by
// "controlled source/caller checks ... Known-present and injected-forbidden
// controls test absence instruments; source text alone is not behaviour".
//
// This is a declaration-presence check over the package's non-test Go sources:
// the competing worker inbox, manual queue and steer modes must be DELETED,
// while the ONE settlement fence the spec keeps (F4 HOLD) must remain.

package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// FR-009 / DEL-03 / DEL-04 / DEL-22-24: "the existing steering FIFO/session
// runner MUST own ordinary intake through settlement on every entry path; no
// replacement FIFO." The competing worker inbox (DEL-03), the manual steer queue
// and user-selectable dequeue modes (DEL-04) are removed. DEL-02's KEEP list —
// the ordinary settlement fence — must survive.
func TestSessionCoreU3_CompetingIntakeDeclarationsAreGone(t *testing.T) {
	decls := packageDeclNames(t, ".")

	// Instrument positive control: the settlement fence is KEPT (F4 HOLD /
	// FR-009). If the scanner could not see real declarations, these would be
	// absent too — proving the "absent" results below are real absences.
	require.True(t, decls["prepareOrdinarySessionExecution"],
		"control: FR-009/F4 keeps prepareOrdinarySessionExecution (the ordinary settlement fence)")
	require.True(t, decls["awaitPreviousOrdinaryExecution"],
		"control: FR-009/F4 keeps awaitPreviousOrdinaryExecution (the settlement fence wait)")

	// Instrument control: a name that cannot exist must read absent, so an
	// "absent" verdict is not the scanner's default for unknown names.
	require.False(t, decls["sessionCoreU3AbsenceInstrumentSentinel"],
		"control: a fabricated name must read absent")

	for _, gone := range []string{
		"parseSteeringMode",   // DEL-04: user-selectable dequeue modes
		"manualSteeringScope", // DEL-04: the manual fallback queue
		"SteeringOneAtATime",  // DEL-04: the one-at-a-time steer mode
		"workerInboxCap",      // DEL-03: the competing worker inbox
	} {
		require.False(t, decls[gone],
			"%s must be deleted — FR-009 keeps ONE steering FIFO/runner and removes the competing worker inbox/manual queue/steer modes (DEL-03/04)", gone)
	}
}

// packageDeclNames parses every non-test .go file in dir and returns the set of
// top-level declaration names (funcs, types, consts, vars).
func packageDeclNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	fset := token.NewFileSet()
	parsed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		require.NoError(t, perr, "parse %s", name)
		parsed++
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				out[d.Name.Name] = true
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						out[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range s.Names {
							out[n.Name] = true
						}
					}
				}
			}
		}
	}
	require.Greater(t, parsed, 10, "control: the scanner parsed the package's non-test Go sources")
	return out
}
