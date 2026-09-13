// Omnipus — regression coverage for UAT 2026-09-13 D-31 (web/find half): a
// request whose shape does not match the generated type was refused with a
// raw Go decoder message naming Go struct fields and Go types, which says
// nothing an agent can act on and exposes implementation detail.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"context"
	"strings"
	"testing"
)

// TestUAT_D31_ShapeRefusalIsPlainNotGoInternals — every mis-shaped request
// below used to leak "cannot unmarshal ... into Go struct field ...". The
// refusal must instead name the argument, what was expected and what was
// received, in plain words.
func TestUAT_D31_ShapeRefusalIsPlainNotGoInternals(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "seedling", "10.5")
	d := f.deps()
	call := func(raw string) (string, error) { return Call(context.Background(), d, []byte(raw)) }

	cases := []struct {
		name string
		raw  string
		want []string // fragments the plain message must carry
	}{
		{
			name: "sort as a bare string",
			raw:  `{"type":"plant","sort":"planted"}`,
			want: []string{`"sort"`, "list", "text"},
		},
		{
			name: "filter.all as an object instead of a list",
			raw:  `{"type":"plant","filter":{"all":{"property":"condition","op":"=","value":"seedling"}}}`,
			want: []string{`"filter.all"`, "list", "object"},
		},
		{
			name: "limit as text",
			raw:  `{"type":"plant","limit":"10"}`,
			want: []string{`"limit"`, "whole number", "text"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := call(tc.raw)
			if err == nil {
				t.Fatalf("a mis-shaped request must be refused, got:\n%s", out)
			}
			for _, leak := range []string{"Go struct", "unmarshal", "generated.", "[]generated"} {
				if strings.Contains(out, leak) {
					t.Fatalf("D-31: the refusal leaks Go internals (%q):\n%s", leak, out)
				}
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("the refusal must say %q (field, expected, received):\n%s", w, out)
				}
			}
		})
	}
}
