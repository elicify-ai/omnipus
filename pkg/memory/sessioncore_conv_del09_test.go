// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core CONV — the DEL-09 removal contract (K/source proof),
// pkg/memory slice.
//
// Spec: docs/internal/specs/session-core-spec.md DEL-09 ("`pkg/memory/migration.go
// ::MigrateFromJSON` and constructor/fallback import callers ... DELETE general
// importer helpers/hooks and runtime fallback"); FR-038; SC-002.
//
// `MigrateFromJSON` is the general saved-JSON importer CONV replaces; it may be
// deleted only once CONV exists. Owned by this pack (WC-1 RED-CONV brief), not
// by the U2 pack. Declaration-presence check over pkg/memory's non-test Go
// sources, with positive controls.

package memory

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

// DEL-09 / FR-038: the general saved-JSON importer `MigrateFromJSON` is removed
// from the runtime once CONV is the sole legacy reader.
func TestSessionCoreConv_Del09MigrateFromJSONIsAbsent(t *testing.T) {
	decls := convDeclNamesMemory(t, ".")

	// Instrument controls: a kept symbol reads present; a fabricated name reads
	// absent, so the verdict below is a real absence.
	require.True(t, decls["ArchivedMessage"],
		"control: the scanner sees a real pkg/memory declaration (ArchivedMessage)")
	require.False(t, decls["convDel09AbsenceSentinel"],
		"control: a fabricated name must read absent")

	require.False(t, decls["MigrateFromJSON"],
		"DEL-09: memory.MigrateFromJSON must be deleted once CONV is the sole legacy reader")
}

// convDeclNamesMemory parses every non-test .go file in dir and returns the set
// of top-level declaration names (funcs, types, consts, vars).
func convDeclNamesMemory(t *testing.T, dir string) map[string]bool {
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
	require.GreaterOrEqual(t, parsed, 3, "control: the scanner parsed the package's non-test Go sources")
	return out
}
