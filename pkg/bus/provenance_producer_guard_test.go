// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package bus

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenario S-111 (TestProgramProvenance_ProducerGuard — the I-2
// producer guard and fail-closed default), FR-041 (provenance required on
// turn-feeding messages), FR-045.
//
// Oracle from the SPEC: every producer that starts or feeds a turn carries a
// provenance. This source guard scans the production tree (non-test Go files)
// for constructions of the turn-feeding messages and fails naming any file
// whose construction leaves Provenance unset. RED expects exactly that
// failure today: the Provenance field does not exist yet, so every
// construction is unset.
//
// Scope note (recorded in test-disputes.md): the guard table names the
// carriers that exist at RED (bus.InboundMessage; pkg/agent's
// AsyncNotifyEvent). The steer, peer-request and durable work-record write
// request carriers gain their Provenance fields at GREEN — the same table
// then gains their rows, without changing this guard's mechanics. The
// scenario's runtime half (a producer without provenance starts a turn →
// withheld with the unresolved notice) is covered by the integration slice
// (TestProgramProvenance_HandOn).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// producerGuardTargets lists the turn-feeding message types the guard scans
// for, as package-qualified type names.
var producerGuardTargets = []struct {
	Package string // package name the literal is constructed with
	Type    string // type name
}{
	{"bus", "InboundMessage"},
	{"bus", "AsyncNotifyEvent"}, // pkg/agent aliases the bus package; both spellings are caught below
	{"agent", "AsyncNotifyEvent"},
}

func TestProgramProvenance_ProducerGuard(t *testing.T) {
	t.Run("every_turn_feeding_construction_carries_provenance", func(t *testing.T) {
		root := repoRootForGuard()
		violations := scanProvenanceProducerViolations(t, root, "")
		require.Empty(t, violations,
			"every turn-feeding message construction must carry Provenance (FR-041/I-2): %v", violations)
	})

	t.Run("test_only_producer_without_provenance_fails_the_guard", func(t *testing.T) {
		// Instrument check (spec S-111): the same guard run over the tree
		// PLUS a test-only file that builds a bus.InboundMessage without
		// Provenance must fail naming that file. The extra file is fed
		// in-memory; the repository is never modified.
		testOnly := "// Package main\npackage main\n\nimport \"github.com/elicify-ai/omnipus/pkg/bus\"\n\nvar msg = bus.InboundMessage{Channel: \"telegram\"}\n"
		root := repoRootForGuard()
		violations := scanProvenanceProducerViolations(t, root, testOnly)
		require.NotEmpty(t, violations,
			"instrument check: a construction without Provenance MUST fail the guard")
		found := false
		for _, v := range violations {
			if strings.Contains(v, "<test-only>") {
				found = true
			}
		}
		require.True(t, found, "the violation must name the offending file; got %v", violations)
	})

	t.Run("fail_closed_default", func(t *testing.T) {
		// Oracle: the zero value is `unresolved` (spec I-2) and unresolved is
		// never eligible (FR-010) — a producer that omits provenance is
		// withheld, never permissive.
		var zero Provenance
		require.True(t, zero.IsUnresolved(), "the zero Provenance must be unresolved")
		require.False(t, zero.Eligible(func(Origin) bool { return true }),
			"unresolved must be ineligible even when every setting allows")
		require.Empty(t, zero.Origins(), "unresolved carries no origins")
	})
}

// scanProvenanceProducerViolations parses every non-test Go file under root
// and reports every construction of a guard target whose composite literal
// does not assign Provenance. extraSource (when non-empty) is parsed as an
// additional file labelled "<test-only>".
func scanProvenanceProducerViolations(t *testing.T, root, extraSource string) []string {
	t.Helper()
	var violations []string
	scanOne := func(label, src string) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, label, src, 0)
		if err != nil {
			return // not parseable Go — other checks cover real files
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgID, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			for _, target := range producerGuardTargets {
				if pkgID.Name != target.Package || sel.Sel.Name != target.Type {
					continue
				}
				if !literalAssignsProvenance(lit) {
					violations = append(violations, fmt.Sprintf(
						"%s constructs %s.%s without Provenance", label, target.Package, target.Type))
				}
			}
			return true
		})
	}
	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "spa", "src", "docs", "design-system":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		scanOne(rel, string(b))
		return nil
	}))
	if extraSource != "" {
		scanOne("<test-only>", extraSource)
	}
	return violations
}

// literalAssignsProvenance reports whether a composite literal sets the
// Provenance key.
func literalAssignsProvenance(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Provenance" {
			return true
		}
	}
	return false
}

func repoRootForGuard() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed - cannot resolve repo root")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}
