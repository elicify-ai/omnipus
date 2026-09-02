// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// event_name_contract_test.go — regression guard for issue #667.
//
// The bug: contracts/components/schemas/AuditEntry.yaml pinned `event` to
// `^[a-z_]+$`, which rejects a dot. Over 50 of this package's event names are
// dot-separated (skill.call, onboarding.admin_created, turn.cancel.attempt,
// browser.webrtc.stream_started, …). The generated Zod schema carries that
// pattern verbatim, AuditLogResponse wraps AuditEntry in `z.array(...)`, and
// src/lib/api.ts::request THROWS ApiSchemaError on a failed response rather
// than dropping single rows — so one dotted record blanked the entire
// Settings → Security → Audit Log screen. On any real install at least two
// dotted events (onboarding.admin_created, written once at admin
// registration; skill.call, written on every Skill tool call) guaranteed it.
//
// Nothing connected the Go constants to the wire contract, so the two drifted
// silently and the only symptom was a blank screen with no error anywhere in
// the Go tests.
//
// This file closes that gap from both ends:
//
//   - It reads the pattern out of contracts/components/schemas/AuditEntry.yaml
//     at run time. It is NOT hand-copied here, so narrowing the contract again
//     turns this test red instead of turning a screen blank.
//   - It enumerates event names by parsing pkg/audit's own source, so a new
//     constant is covered the day it is added rather than the day someone
//     remembers to update a list. This matters concretely: per-action browser
//     audit events are being added in the current wave.
package audit_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// auditPkgDir returns the absolute path of pkg/audit/ (the directory holding
// this test file).
func auditPkgDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(thisFile)
}

// parseAuditPkgFiles parses every non-test .go file in pkg/audit/ and calls fn
// for each parsed file. Source is the single source of truth for what this
// package can emit — a hand-copied list in a test goes stale the day someone
// adds an event, which is the exact failure mode #667 came from.
func parseAuditPkgFiles(t *testing.T, fn func(path string, file *ast.File)) {
	t.Helper()
	pkgDir := auditPkgDir(t)

	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("read pkg/audit dir: %v", err)
	}

	fset := token.NewFileSet()
	parsed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(pkgDir, name)
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		fn(path, file)
		parsed++
	}
	if parsed == 0 {
		t.Fatal("no non-test .go files found under pkg/audit/ — AST walk drifted; fix this test")
	}
}

// collectEventConsts returns every top-level `Event*` string constant declared
// across pkg/audit's non-test sources, keyed by constant identifier.
//
// Shared with events_exhaustive_test.go, which asserts the complementary
// property (IsValidEventName accepts them all) over the same set.
func collectEventConsts(t *testing.T) map[string]string {
	t.Helper()
	eventValues := map[string]string{}

	parseAuditPkgFiles(t, func(path string, file *ast.File) {
		for _, decl := range file.Decls {
			genDecl, isGenDecl := decl.(*ast.GenDecl)
			if !isGenDecl || genDecl.Tok != token.CONST {
				continue
			}
			for _, spec := range genDecl.Specs {
				valueSpec, isValueSpec := spec.(*ast.ValueSpec)
				if !isValueSpec {
					continue
				}
				for i, ident := range valueSpec.Names {
					if !strings.HasPrefix(ident.Name, "Event") {
						continue
					}
					// Every real Event* constant here is declared with an
					// inline string literal — never iota or a derived
					// expression. Skip anything else rather than guess.
					if i >= len(valueSpec.Values) {
						continue
					}
					lit, isBasicLit := valueSpec.Values[i].(*ast.BasicLit)
					if !isBasicLit || lit.Kind != token.STRING {
						continue
					}
					val, unquoteErr := strconv.Unquote(lit.Value)
					if unquoteErr != nil {
						t.Fatalf("unquote %s's value in %s: %v", ident.Name, path, unquoteErr)
					}
					eventValues[ident.Name] = val
				}
			}
		}
	})

	return eventValues
}

// collectIsValidEventNameLiterals returns the bare string literals listed
// directly in IsValidEventName's switch (pkg/audit/audit.go) — names like
// "skill.write", "workspace.create" and the legacy "project.*"/"milestone.*"
// families whose constants live in another package or nowhere at all.
//
// These are real wire values: they are written to audit.jsonl by production
// handlers and read back by GET /api/v1/audit-log, so the contract has to
// admit them just as much as it admits the named constants. Collecting them
// from the switch body rather than re-listing them keeps this test honest
// when the switch grows.
func collectIsValidEventNameLiterals(t *testing.T) []string {
	t.Helper()
	var literals []string
	found := false

	parseAuditPkgFiles(t, func(_ string, file *ast.File) {
		for _, decl := range file.Decls {
			funcDecl, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || funcDecl.Name.Name != "IsValidEventName" || funcDecl.Body == nil {
				continue
			}
			found = true
			ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
				lit, isBasicLit := n.(*ast.BasicLit)
				if !isBasicLit || lit.Kind != token.STRING {
					return true
				}
				val, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil {
					t.Fatalf("unquote literal %s in IsValidEventName: %v", lit.Value, unquoteErr)
				}
				literals = append(literals, val)
				return true
			})
		}
	})

	if !found {
		t.Fatal("IsValidEventName not found in pkg/audit — this test's AST walk drifted; fix it")
	}
	return literals
}

// contractEventPattern extracts the `pattern:` constraint that
// contracts/components/schemas/AuditEntry.yaml puts on the `event` property.
//
// Read from the contract file rather than restated here on purpose: this test
// exists to prove the Go event names satisfy THE CONTRACT, and a copy of the
// regex in the test would be one more thing that can drift from it. If someone
// narrows the pattern back to `^[a-z_]+$`, this test reds — which is the whole
// point of #667's regression guard.
func contractEventPattern(t *testing.T) *regexp.Regexp {
	t.Helper()

	schemaPath := filepath.Join(auditPkgDir(t), "..", "..",
		"contracts", "components", "schemas", "AuditEntry.yaml")
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read AuditEntry.yaml (%s): %v", schemaPath, err)
	}

	// The schema declares exactly one `pattern:` today, on `event`. Asserting
	// that keeps the extraction unambiguous: if a second pattern is ever added
	// to this schema, this fails loudly and tells the reader to scope the
	// lookup, instead of silently picking the wrong one.
	patternLine := regexp.MustCompile(`(?m)^\s*pattern:\s*'([^']*)'\s*$`)
	matches := patternLine.FindAllStringSubmatch(string(raw), -1)
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 quoted `pattern:` in AuditEntry.yaml, found %d — "+
			"scope this lookup to the `event` property", len(matches))
	}

	re, err := regexp.Compile(matches[0][1])
	if err != nil {
		t.Fatalf("AuditEntry.yaml's event pattern %q does not compile as a Go regexp: %v",
			matches[0][1], err)
	}
	return re
}

// TestEventNamesSatisfyWireContract is the #667 regression guard.
//
// Every event name this package can put on the wire — the declared Event*
// constants and the bare literals registered in IsValidEventName — must match
// the `event` pattern in contracts/components/schemas/AuditEntry.yaml.
//
// A violation here is not cosmetic. The generated Zod schema carries this
// pattern verbatim, AuditLogResponse validates `z.array(AuditEntry)`, and
// src/lib/api.ts::request throws on a schema-invalid response instead of
// dropping the offending row — so a single non-matching name blanks the whole
// Audit Log screen. An audit trail that silently shows nothing is worse than
// one that is visibly missing.
func TestEventNamesSatisfyWireContract(t *testing.T) {
	pattern := contractEventPattern(t)

	// Sanity-check the extracted pattern against a value the contract's own
	// `example:` field declares legal. If this fails, the extraction grabbed
	// the wrong regex and every assertion below would be meaningless.
	if !pattern.MatchString("tool_call") {
		t.Fatalf("extracted pattern %q rejects the contract's own example value "+
			"\"tool_call\" — wrong pattern extracted", pattern.String())
	}

	consts := collectEventConsts(t)
	if len(consts) == 0 {
		t.Fatal("no Event* const declarations found under pkg/audit/ — AST parse drifted; fix this test")
	}

	// all holds every wire value under test, tagged with where it came from so
	// a failure message points at the declaration to fix.
	type namedEvent struct{ source, value string }

	constNames := make([]string, 0, len(consts))
	for name := range consts {
		constNames = append(constNames, name)
	}
	sort.Strings(constNames)

	switchLiterals := collectIsValidEventNameLiterals(t)

	all := make([]namedEvent, 0, len(constNames)+len(switchLiterals))
	for _, name := range constNames {
		all = append(all, namedEvent{source: "pkg/audit const " + name, value: consts[name]})
	}
	for _, lit := range switchLiterals {
		all = append(all, namedEvent{source: "IsValidEventName literal", value: lit})
	}

	var violations []string
	for _, ev := range all {
		if !pattern.MatchString(ev.value) {
			violations = append(violations,
				ev.source+" = "+strconv.Quote(ev.value))
		}
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf(
			"%d audit event name(s) violate AuditEntry.yaml's event pattern %q (issue #667).\n"+
				"Each one blanks the entire Settings → Security → Audit Log screen, because "+
				"AuditLogResponse validates entries as z.array(AuditEntry) and src/lib/api.ts "+
				"throws on the whole response rather than dropping a row.\n"+
				"Fix the CONTRACT (widen the pattern), not the event names — audit logs are "+
				"append-only history whose HMAC chain covers this field, so on-disk names "+
				"cannot be rewritten.\n  %s",
			len(violations), pattern.String(), strings.Join(violations, "\n  "),
		)
	}
}
