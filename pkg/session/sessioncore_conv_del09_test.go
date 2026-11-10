// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core CONV — the DEL-09 removal contract (K/source proof),
// pkg/session slice.
//
// Spec: docs/internal/specs/session-core-spec.md DEL-09 ("`pkg/session/unified.go
// ::migrateLegacy`, ... DELETE general importer helpers/hooks and runtime
// fallback. Only saved-chat decoding needed for the one-time CONV boot
// conversion survives inside CONV"); FR-038 ("finish ... canonical producer
// before removal"); SC-002 ("Every logical DEL ID has its K/B compiler/source
// and applicable behavioral evidence; canonical positive controls pass").
//
// CONV is the replacement for the legacy runtime reader, so DEL-09's
// `migrateLegacy` may be deleted only once CONV exists — the removal is the
// replacement's contract, owned by this pack (WC-1 RED-CONV brief), not by the
// U2 pack. This test is a declaration-presence check over pkg/session's
// non-test Go sources, with positive controls so an "absent" verdict is a real
// absence and not the scanner's default.

package session

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

// DEL-09 / FR-038: the legacy runtime reader `UnifiedStore.migrateLegacy` is
// removed from the runtime once CONV is the sole legacy reader.
func TestSessionCoreConv_Del09LegacyRuntimeReaderIsAbsent(t *testing.T) {
	decls := convDeclNames(t, ".")

	// Instrument controls: the scanner can see real declarations (a kept symbol
	// reads present) and does not default unknown names to absent.
	require.True(t, decls["readUnifiedMeta"],
		"control: the scanner sees a real pkg/session declaration (readUnifiedMeta)")
	require.False(t, decls["convDel09AbsenceSentinel"],
		"control: a fabricated name must read absent")

	require.False(t, decls["migrateLegacy"],
		"DEL-09: UnifiedStore.migrateLegacy must be deleted once CONV is the sole legacy reader")
}

// convDeclNames parses every non-test .go file in dir and returns the set of
// top-level declaration names (funcs, types, consts, vars).
func convDeclNames(t *testing.T, dir string) map[string]bool {
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
