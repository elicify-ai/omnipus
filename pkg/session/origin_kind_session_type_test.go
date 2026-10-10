// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Guard: every UnifiedSessionType maps to a valid OriginKind (I-1: "one value
// per UnifiedSessionType"). A new session type added without its origin kind
// made every ordinary turn in that session fail admission with
// `invalid origin kind "main"`.

package session

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// declaredSessionTypes reads the UnifiedSessionType constants straight from
// the package source, so a newly added type is covered without editing a list.
func declaredSessionTypes(t *testing.T) []UnifiedSessionType {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "unified.go", nil, 0)
	if err != nil {
		t.Fatalf("parse unified.go: %v", err)
	}
	var out []UnifiedSessionType
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "UnifiedSessionType" || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok {
				continue
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquote %s: %v", lit.Value, err)
			}
			out = append(out, UnifiedSessionType(v))
		}
	}
	return out
}

func TestEverySessionTypeMapsToAValidOriginKind(t *testing.T) {
	types := declaredSessionTypes(t)
	if len(types) < 8 {
		t.Fatalf("instrument check: expected to find at least the 8 known session types, found %d: %v", len(types), types)
	}
	for _, st := range types {
		if !IsValidSessionType(st) {
			t.Errorf("session type %q is declared but IsValidSessionType rejects it", st)
		}
		if !IsValidOriginKind(OriginKind(st)) {
			t.Errorf("session type %q has no matching valid OriginKind (I-1: one origin kind per session type)", st)
		}
	}
}
