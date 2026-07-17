// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// events_exhaustive_test.go — regression guard so IsValidEventName's
// allowlist (pkg/audit/audit.go) can never silently drift from the actual
// set of Event* constants declared across this package.

package audit_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestIsValidEventName_ExhaustiveOverEventConsts is the regression guard for
// a class of bug this same review wave found elsewhere (shell.go's
// backgroundCompletionResult missed the "canceled" case in a hand-maintained
// switch, see pkg/tools/shell.go): IsValidEventName's allowlist
// (pkg/audit/audit.go) is ITSELF a hand-maintained switch over every Event*
// constant declared anywhere in this package, and nothing previously
// verified that list stayed in sync with the constants it is supposed to
// cover. A future EventFoo constant added anywhere in pkg/audit and never
// added to IsValidEventName's switch would silently trip the unknown-event
// warn-once for every real emission of that event — exactly the failure
// mode IsValidEventName exists to make loud, except for its own omissions.
//
// This test parses every non-test *.go file in pkg/audit/ (the source of
// truth, not a hand-copied list here — mirrors the AST-parsing approach
// pkg/gateway/channels_wiring_test.go's TestNoHalfWiredChannels already
// uses for an analogous "wiring can silently drift" problem), collects
// every top-level const declaration whose name matches ^Event[A-Z] (this
// package's uniform naming convention for wire event-name constants, see
// events.go/audit.go/security_change.go), resolves each one's string
// literal VALUE directly from the source (not by importing the Go
// identifier — the point is to check the raw wire values IsValidEventName
// actually sees at runtime, the same way a real audit.Emit call would), and
// asserts audit.IsValidEventName accepts every single one.
func TestIsValidEventName_ExhaustiveOverEventConsts(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	pkgDir := filepath.Dir(thisFile)

	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("read pkg/audit dir: %v", err)
	}

	fset := token.NewFileSet()
	// eventValues maps const identifier name -> its string literal value, as
	// declared in source. Collected across every non-test file in the
	// package so a future new file is covered automatically.
	eventValues := map[string]string{}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		filePath := filepath.Join(pkgDir, name)
		file, parseErr := parser.ParseFile(fset, filePath, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", filePath, parseErr)
		}
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
					// Every real Event* constant in this package is declared
					// with an inline string literal value (either standalone
					// `const EventFoo = "..."` or one line per name inside a
					// `const ( ... )` block) — never via iota or a derived
					// expression. Skip anything that doesn't fit that shape
					// rather than guessing at its value.
					if i >= len(valueSpec.Values) {
						continue
					}
					lit, isBasicLit := valueSpec.Values[i].(*ast.BasicLit)
					if !isBasicLit || lit.Kind != token.STRING {
						continue
					}
					val, unquoteErr := strconv.Unquote(lit.Value)
					if unquoteErr != nil {
						t.Fatalf("unquote %s's value in %s: %v", ident.Name, filePath, unquoteErr)
					}
					eventValues[ident.Name] = val
				}
			}
		}
	}

	if len(eventValues) == 0 {
		t.Fatal(
			"no Event* const declarations found under pkg/audit/ — AST parse drifted or the package " +
				"was restructured; fix this test",
		)
	}

	names := make([]string, 0, len(eventValues))
	for name := range eventValues {
		names = append(names, name)
	}
	sort.Strings(names)

	var rejected []string
	for _, name := range names {
		val := eventValues[name]
		if !audit.IsValidEventName(audit.EventName(val)) {
			rejected = append(rejected, name+`="`+val+`"`)
		}
	}

	if len(rejected) > 0 {
		t.Fatalf(
			"IsValidEventName rejects %d declared Event* constant(s) — add them to the switch in pkg/audit/audit.go:\n  %s",
			len(rejected),
			strings.Join(rejected, "\n  "),
		)
	}
}
