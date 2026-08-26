// Omnipus — the package's contract as a CONSUMER sees it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// This file is deliberately in package records_test, not records. Everything a
// test inside the package can reach — an unexported index, a private helper —
// is invisible here, which is the only way to prove that a consumer with
// nothing but the exported API can build a working record type.
//
// It exists because a consumer could NOT. Property carried an unexported
// valuePos index with no constructor and no setter, so an externally-built enum
// property answered EnumPosition with (0, false) for every value: every
// legitimately declared value was rejected as impermissible, with an error that
// helpfully listed the permitted values — including the one being rejected.
// An in-package test could not see it (compare_truthtable_test.go simply
// assigned p.valuePos, which no consumer can do), which is why the defect
// survived a full suite.

package records_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// externalWidgetSchema declares only `name`. The property under test is added
// afterwards, from outside the package, so the loader never touches it.
const externalWidgetSchema = `schema_version: 1
type: widget
properties:
  name: { type: text, required: true }
`

func externalSchemaSet(t *testing.T) *records.SchemaSet {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "widget.yaml"), []byte(externalWidgetSchema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("fixture schema must load cleanly: %v", report.Rejections)
	}
	return set
}

// attach hangs an externally-built property off a loaded schema. Properties and
// PropertyOrder are exported, so this is exactly what a consumer assembling a
// record type in memory would do.
func attach(t *testing.T, set *records.SchemaSet, p *records.Property) *records.Schema {
	t.Helper()
	sc, ok := set.Get("widget")
	if !ok {
		t.Fatalf("fixture schema did not load")
	}
	sc.Properties[p.Name] = p
	sc.PropertyOrder = append(sc.PropertyOrder, p.Name)
	return sc
}

// TestExternal_EnumPropertyBuiltOutsideThePackageValidates is the proof. A
// consumer builds an enum property with a plain struct literal — the obvious
// thing to write, and the only thing available before NewProperty existed — and
// a record carrying a declared value must VALIDATE, not be rejected as
// impermissible.
func TestExternal_EnumPropertyBuiltOutsideThePackageValidates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) *records.Property
	}{
		{
			name: "struct literal",
			build: func(t *testing.T) *records.Property {
				return &records.Property{
					Name:       "status",
					Type:       records.TypeEnum,
					RecordType: "widget",
					Values: []records.EnumValue{
						{Name: "todo", Position: 0},
						{Name: "doing", Position: 1},
						{Name: "done", Position: 2},
					},
				}
			},
		},
		{
			name: "NewProperty",
			build: func(t *testing.T) *records.Property {
				p, err := records.NewProperty(records.Property{
					Name:       "status",
					Type:       records.TypeEnum,
					RecordType: "widget",
					Values: []records.EnumValue{
						{Name: "todo"}, {Name: "doing"}, {Name: "done"},
					},
				})
				if err != nil {
					t.Fatalf("NewProperty: %v", err)
				}
				return p
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := externalSchemaSet(t)
			prop := tc.build(t)
			attach(t, set, prop)

			// The ordering oracle must answer, or nothing downstream can.
			for i, name := range []string{"todo", "doing", "done"} {
				pos, ok := prop.EnumPosition(name)
				if !ok {
					t.Fatalf("EnumPosition(%q) said the value is not in the set — but the property itself declares it. Permitted values: %v", name, prop.PermittedValues())
				}
				if pos != i {
					t.Fatalf("FR-010: %q is declared at position %d; EnumPosition said %d", name, i, pos)
				}
			}

			rec := records.ParseRecord("notes/a.md", []byte("---\ntype: widget\nname: A\nstatus: doing\n---\nbody\n"))
			rep := records.ValidateRecord(set, rec, records.ValidateOptions{})

			if !rep.Recognised {
				t.Fatalf("the note declares a type the set holds; it must be recognised")
			}
			if !rep.Valid() {
				f := rep.Errors()[0]
				t.Fatalf("a record holding a DECLARED enum value must validate; it was rejected as %q: %s (permitted: %v)", f.Code, f.Reason, f.Permitted)
			}

			// And the resolved value must be the real one, not an empty shell.
			pv := records.ResolveProperty(rec, prop)
			if pv.State != records.StatePresent {
				t.Fatalf("expected the property to be present, got %s", pv.State)
			}
			if len(pv.Values) != 1 || pv.Values[0].Enum.Name != "doing" || pv.Values[0].Enum.Position != 1 {
				t.Fatalf("the resolved value must carry the declared enum member; got %+v", pv.Values)
			}
		})
	}
}

// TestExternal_EnumPropertyStillRejectsUndeclaredValues is the other half. A
// property that accepts everything would also "pass" the test above, so this
// pins that FR-011 still bites — and that the rejection names the real set.
func TestExternal_EnumPropertyStillRejectsUndeclaredValues(t *testing.T) {
	set := externalSchemaSet(t)
	attach(t, set, &records.Property{
		Name:       "status",
		Type:       records.TypeEnum,
		RecordType: "widget",
		Values:     []records.EnumValue{{Name: "todo"}, {Name: "done"}},
	})

	for _, value := range []string{"shipped", "Done", "TODO"} {
		rec := records.ParseRecord("notes/a.md", []byte("---\ntype: widget\nname: A\nstatus: "+value+"\n---\n"))
		rep := records.ValidateRecord(set, rec, records.ValidateOptions{})
		if rep.Valid() {
			t.Fatalf("FR-011 / D4: %q is not a declared value (matching is exact-case) and must be rejected", value)
		}
		f := rep.Errors()[0]
		if f.Code != records.FindingEnumNotPermitted {
			t.Fatalf("expected %q for %q, got %q: %s", records.FindingEnumNotPermitted, value, f.Code, f.Reason)
		}
		// The old failure listed the rejected value among the permitted ones.
		for _, p := range f.Permitted {
			if p == value {
				t.Fatalf("the rejection lists %q as permitted while rejecting it; permitted = %v", value, f.Permitted)
			}
		}
		if len(f.Permitted) != 2 {
			t.Fatalf("FR-011 requires the permitted set to be named; got %v", f.Permitted)
		}
	}
}

// TestExternal_NewPropertyRefusesABrokenDeclaration checks the constructor is
// worth reaching for: it gives a consumer the loader's own refusals instead of
// a property that is quietly wrong.
func TestExternal_NewPropertyRefusesABrokenDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name string
		decl records.Property
		want string
	}{
		{"no name", records.Property{Type: records.TypeText}, "must declare a name"},
		{"no type", records.Property{Name: "p"}, "declares no type"},
		{"unknown type", records.Property{Name: "p", Type: records.PropertyType("colour")}, "not a supported property type"},
		{"relation without a target", records.Property{Name: "p", Type: records.TypeRelation}, "must declare its target record type"},
		{"values on a relation", records.Property{Name: "p", Type: records.TypeRelation, To: "person", Values: []records.EnumValue{{Name: "a"}}}, "`values` is only meaningful on an enum"},
		{"enum with no values", records.Property{Name: "p", Type: records.TypeEnum}, "an enum must declare its `values`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := records.NewProperty(tc.decl)
			if err == nil {
				t.Fatalf("expected a refusal, got a property: %+v", p)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected a refusal containing %q, got %q", tc.want, err)
			}
		})
	}
}
