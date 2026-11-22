// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package bus

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenario S-111 (TestProgramProvenance_ProducerGuard — the I-2
// producer guard and fail-closed default), FR-041 (provenance required on
// turn-feeding messages), FR-045.
//
// Oracle from the SPEC (S-111's Given): the guard covers constructions of
// `bus.InboundMessage`, the steer message, the peer request record,
// `AsyncNotifyEvent`, and the task, plan, schedule, goal and lifecycle write
// requests. The table below enumerates every carrier class that EXISTS at
// RED, by its real Go type; each gains its Provenance field at GREEN (I-2:
// "Durable writers take provenance as a required field of their write
// request"; the producer table names `LifecycleRecord.Provenance` verbatim):
//
//   - bus.InboundMessage                 — the web/channel inbound face
//   - bus.AsyncNotifyEvent               — the background completion carrier
//     (agent.AsyncNotifyEvent is the same type through pkg/agent's alias)
//   - bus.SessionMessageEvent            — the steer transport: parent_to_child
//     kinds (steer, respond) ride this event (pkg/bus/session_message.go)
//   - tools.PeerRequest                  — the peer request record
//   - tools.TaskWriteRequest             — the task write request
//   - session.LifecycleRecord            — every work session's lifecycle record
//
// The plan, schedule and goal write requests have no request types at RED
// (their writes flow through patches on pkg/plan.Plan, pkg/goal.Goal and the
// cron engine); their rows are added when GREEN names the types — recorded in
// test-disputes.md section G, not silently dropped.
//
// Matching resolves the file's own import table (an alias `bb` importing
// github.com/.../pkg/bus matches bus constructions; a same-named qualifier
// from a NON-omnipus path never matches) and catches the package's OWN
// bare-Ident constructions. Parse errors fail the guard naming the file — a
// file the guard cannot read is not a file the guard passed (CHECK F11).
//
// RED expects failures today: the Provenance fields do not exist yet, so
// every construction is unset and the guard names every producer file. The
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

// producerGuardTargets lists the turn-feeding carrier types the guard scans
// for: canonical package dir suffix under /pkg/ plus the type name.
var producerGuardTargets = []struct {
	Package string // package dir suffix (matched through the import table)
	Type    string // type name
}{
	{"bus", "InboundMessage"},
	{"bus", "AsyncNotifyEvent"},
	{"agent", "AsyncNotifyEvent"},
	{"bus", "SessionMessageEvent"},
	{"tools", "PeerRequest"},
	{"tools", "TaskWriteRequest"},
	{"session", "LifecycleRecord"},
}

func TestProgramProvenance_ProducerGuard(t *testing.T) {
	t.Run("every_turn_feeding_construction_carries_provenance", func(t *testing.T) {
		root := repoRootForGuard()
		violations := scanProvenanceProducerViolations(t, root, nil)
		require.Empty(t, violations,
			"every turn-feeding message construction must carry Provenance (FR-041/I-2): %v", violations)
	})

	t.Run("instrument_bare_ident_and_alias_and_foreign_qualifier", func(t *testing.T) {
		// Instrument checks (spec S-111's second run, extended per CHECK F11):
		// the guard fires on an own-package bare-Ident construction, on an
		// import-ALIASED construction, and stays silent for a qualifier that
		// resolves to a non-omnipus import path (no false positives from
		// same-named external types). Nothing is written to the repository.
		root := repoRootForGuard()
		ownPkg := "package bus\n\ntype probe struct{ m InboundMessage }\n\nvar p = probe{m: InboundMessage{Channel: \"telegram\"}}\n"
		aliased := "package probe\n\nimport bb \"github.com/elicify-ai/omnipus/pkg/bus\"\n\nvar msg = bb.InboundMessage{Channel: \"telegram\"}\n"
		foreign := "package probe\n\nimport \"example.com/other/pkg/bus\"\n\nvar msg = bus.InboundMessage{Channel: \"telegram\"}\n"
		plain := "package probe\n\nimport \"github.com/elicify-ai/omnipus/pkg/bus\"\n\nvar msg = bus.InboundMessage{Channel: \"telegram\"}\n"

		vOwn := scanProvenanceProducerViolations(t, root, [][2]string{{"<own-pkg>", ownPkg}})
		require.True(t, namesFile(vOwn, "<own-pkg>"),
			"instrument: the package's own bare-Ident construction is flagged: %v", vOwn)

		vAlias := scanProvenanceProducerViolations(t, root, [][2]string{{"<aliased>", aliased}})
		require.True(t, namesFile(vAlias, "<aliased>"),
			"instrument: an import-aliased construction is flagged: %v", vAlias)

		vForeign := scanProvenanceProducerViolations(t, root, [][2]string{{"<foreign>", foreign}})
		require.False(t, namesFile(vForeign, "<foreign>"),
			"instrument: a non-omnipus same-named qualifier is never flagged: %v", vForeign)

		vPlain := scanProvenanceProducerViolations(t, root, [][2]string{{"<plain>", plain}})
		require.True(t, namesFile(vPlain, "<plain>"),
			"instrument: the spec's own test-only producer fails the guard naming the file: %v", vPlain)
	})

	t.Run("unparseable_source_fails_the_guard", func(t *testing.T) {
		// A file the guard cannot parse must fail the guard loudly — never a
		// silent skip (CHECK F11).
		broken := "package probe\n\nthis is not go\n"
		v := scanProvenanceProducerViolations(t, repoRootForGuard(), [][2]string{{"<broken>", broken}})
		require.True(t, namesFile(v, "<broken>"),
			"instrument: an unparseable file fails the guard naming it: %v", v)
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

// namesFile reports whether any violation names the given file label.
func namesFile(violations []string, label string) bool {
	for _, v := range violations {
		if strings.Contains(v, label) {
			return true
		}
	}
	return false
}

// scanProvenanceProducerViolations parses every non-test Go file under root
// and reports every construction of a guard target whose composite literal
// does not assign Provenance. extraFiles (label, source pairs) are parsed as
// additional files labelled by their label. Parse errors are violations, not
// skips.
func scanProvenanceProducerViolations(t *testing.T, root string, extraFiles [][2]string) []string {
	t.Helper()
	var violations []string
	scanOne := func(label, src string) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, label, src, 0)
		if err != nil {
			violations = append(violations, fmt.Sprintf(
				"%s does not parse (%v) — the guard cannot clear a file it cannot read", label, err))
			return
		}
		aliases := importAliases(f)
		pkgName := f.Name.Name
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			var hit *struct {
				Package string
				Type    string
			}
			switch tt := lit.Type.(type) {
			case *ast.SelectorExpr:
				q, ok := tt.X.(*ast.Ident)
				if !ok {
					return true
				}
				if suffix, imported := aliases[q.Name]; imported {
					for i := range producerGuardTargets {
						if suffix == "/pkg/"+producerGuardTargets[i].Package && tt.Sel.Name == producerGuardTargets[i].Type {
							hit = &producerGuardTargets[i]
						}
					}
				}
			case *ast.Ident:
				for i := range producerGuardTargets {
					if pkgName == producerGuardTargets[i].Package && tt.Name == producerGuardTargets[i].Type {
						hit = &producerGuardTargets[i]
					}
				}
			}
			if hit == nil {
				return true
			}
			if !literalAssignsProvenance(lit) {
				violations = append(violations, fmt.Sprintf(
					"%s:%d constructs %s.%s without Provenance",
					label, fset.Position(lit.Pos()).Line, hit.Package, hit.Type))
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
	for _, pair := range extraFiles {
		scanOne(pair[0], pair[1])
	}
	return violations
}

// omnipusModulePrefix is the import-path prefix of this module; only
// imports under it can match a guard target.
const omnipusModulePrefix = "github.com/elicify-ai/omnipus"

// importAliases maps each file's local qualifier to the omnipus package dir
// suffix it imports (e.g. `bb` → /pkg/bus; the default qualifier of an
// unnamed import is the path's last element). Only imports of THIS module are
// recorded — a same-named qualifier from a non-omnipus path never matches a
// target (no false positives from external types).
func importAliases(f *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		i := strings.Index(path, omnipusModulePrefix+"/pkg/")
		if i < 0 {
			continue
		}
		suffix := path[i+len(omnipusModulePrefix):] // e.g. "/pkg/bus"
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			name = path[strings.LastIndex(path, "/")+1:]
		}
		aliases[name] = suffix
	}
	return aliases
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
