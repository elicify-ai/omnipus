// Omnipus — the drift guard between the summary ops knowledge_find ADVERTISES
// and the ones it ACCEPTS (FR-150).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// The tool schema advertised four ops — count, sum, min, max — for as long as
// request.go accepted fifteen. Nothing failed: the eleven that were missing
// worked perfectly whenever a model happened to guess one, so every test in
// this package passed and the capability was three-quarters unreachable in
// production. Restoring the eleven without this guard only resets the clock.
//
// THE ORACLE IS THE GENERATED SOURCE, NOT A LIST WRITTEN HERE. oapi-codegen
// emits the members as constants plus a `Valid()` switch and nothing iterable,
// and Go cannot reflect over constants — so the only way to ENUMERATE what
// request.go accepts is to read the generated file's const block. That is what
// generatedAggregateOps does. A test that instead listed the fifteen would be a
// third hand-maintained list, which is the defect, not the fix.
//
// It fails in BOTH directions and names the ops, because "advertised and
// accepted differ" is not actionable and "median, stddev, unique are accepted
// but not advertised" is.
// ---------------------------------------------------------------------------

// generatedAggregateOpsPath is the generated file the guard reads. A move
// breaks this test loudly, which is the correct failure: a guard that silently
// found no members would report perfect agreement over an empty set.
const generatedAggregateOpsPath = "../../api/generated/openapi_types.gen.go"

// generatedEnumTypeName is the type whose const members are the accepted set.
const generatedEnumTypeName = "VaultFindAggregateOp"

// generatedAggregateOps reads every member of the generated enum out of the
// generated source.
//
// It matches on the CONST'S DECLARED TYPE (`X VaultFindAggregateOp = "y"`),
// not on a name prefix, so a member whose Go identifier does not start with
// the type name is still found.
func generatedAggregateOps(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.FromSlash(generatedAggregateOpsPath), nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v\nThe generated types moved. This guard cannot enumerate the accepted "+
			"summary ops without them; point generatedAggregateOpsPath at the new location.",
			generatedAggregateOpsPath, err)
	}

	var out []string
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != generatedEnumTypeName {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("const value %s is not a quoted string: %v", lit.Value, err)
				}
				out = append(out, s)
			}
		}
	}

	if len(out) == 0 {
		t.Fatalf("no %s members were found in %s.\nA guard that finds no members reports agreement over "+
			"an empty set, which is exactly the false green this file exists to prevent.",
			generatedEnumTypeName, generatedAggregateOpsPath)
	}
	sort.Strings(out)
	return out
}

// TestAdvertisedAggregateOpsMatchTheAcceptedEnum is the deliverable.
func TestAdvertisedAggregateOpsMatchTheAcceptedEnum(t *testing.T) {
	accepted := generatedAggregateOps(t)
	advertised := AdvertisedAggregateOps()

	inAdvertised := map[string]bool{}
	for _, op := range advertised {
		inAdvertised[op] = true
	}
	inAccepted := map[string]bool{}
	for _, op := range accepted {
		inAccepted[op] = true
	}

	var invisible []string
	for _, op := range accepted {
		if !inAdvertised[op] {
			invisible = append(invisible, op)
		}
	}
	if len(invisible) > 0 {
		t.Errorf("knowledge_find ACCEPTS these summary ops but does not ADVERTISE them: %s.\n"+
			"A model doing strict structured output against the advertised enum cannot emit them at all, "+
			"so each one is an implemented, tested, contract-defined capability that is unreachable in "+
			"practice. Add each to aggregateOpCatalog in tool.go, with a gloss.",
			strings.Join(invisible, ", "))
	}

	var phantom []string
	for _, op := range advertised {
		if !inAccepted[op] {
			phantom = append(phantom, op)
		}
	}
	if len(phantom) > 0 {
		t.Errorf("knowledge_find ADVERTISES these summary ops but does not ACCEPT them: %s.\n"+
			"Every one is a refusal a model was invited to earn. Remove each from aggregateOpCatalog in "+
			"tool.go, or add it to the contract's VaultFindAggregate enum and regenerate.",
			strings.Join(phantom, ", "))
	}

	// The advertised set must also survive the ACTUAL validator, not merely a
	// re-reading of the same source the oracle came from. Valid() is the
	// function request.go calls, so this is the one assertion that exercises
	// the accept path itself rather than a description of it.
	for _, op := range advertised {
		if !generated.VaultFindAggregateOp(op).Valid() {
			t.Errorf("advertised op %q is rejected by generated.VaultFindAggregateOp.Valid(), "+
				"which is the function request.go validates with", op)
		}
	}
}

// TestAdvertisedAggregateOpsAreTheOnesTheReducerImplements closes the third
// side of the triangle.
//
// summaries.go keeps its OWN op constants and derives allSummaryOps() from the
// per-domain tables — that list is what a refusal quotes to a caller who named
// an op outside the fifteen. Advertised ≡ accepted proves the model can ask for
// them; this proves the reducer has a case for each and the refusal text names
// the same set. Without it, an op could be advertised, accepted, and answered
// with reduceAggregate's "not a summary this build can compute".
func TestAdvertisedAggregateOpsAreTheOnesTheReducerImplements(t *testing.T) {
	advertised := append([]string(nil), AdvertisedAggregateOps()...)
	sort.Strings(advertised)

	implemented := allSummaryOps()

	if strings.Join(advertised, ",") != strings.Join(implemented, ",") {
		t.Errorf("the advertised ops and the ops summaries.go implements differ.\n"+
			"advertised:  %s\nimplemented: %s\n"+
			"An op in the first list only is answered by reduceAggregate's default branch; an op in the "+
			"second only is a summary no model can ask for.",
			strings.Join(advertised, ", "), strings.Join(implemented, ", "))
	}
}

// TestEveryAdvertisedAggregateOpCarriesAGloss keeps the descriptions honest.
//
// An enum of fifteen bare words tells a model that `median` exists and nothing
// about how it differs from `avg`, which is most of the way back to the defect.
func TestEveryAdvertisedAggregateOpCarriesAGloss(t *testing.T) {
	desc := aggregateOpDescription()
	for _, d := range aggregateOpCatalog {
		if strings.TrimSpace(d.help) == "" {
			t.Errorf("advertised op %q carries no gloss in aggregateOpCatalog", d.op)
			continue
		}
		if !strings.Contains(desc, string(d.op)+" — "+d.help) {
			t.Errorf("the rendered op description does not carry %q's gloss; the enum and the prose "+
				"have stopped coming from the same table", d.op)
		}
		if strings.Contains(d.help, aggregateOpSeparator) {
			t.Errorf("%q's gloss contains the separator %q, so a reader cannot tell where one op's "+
				"description ends and the next begins", d.op, aggregateOpSeparator)
		}
	}
}

// TestToolSchemaAdvertisesEveryAcceptedOp asserts over the schema AS THE MODEL
// RECEIVES IT — the map Parameters() returns, marshalled the way the tool
// registry marshals it — rather than over the catalogue the schema is built
// from.
//
// The distinction is the whole point: the bug was not a wrong catalogue, it was
// a schema that did not carry one. A test that only checked the catalogue would
// have passed throughout.
func TestToolSchemaAdvertisesEveryAcceptedOp(t *testing.T) {
	raw, err := json.Marshal(Parameters())
	if err != nil {
		t.Fatalf("marshalling the tool schema: %v", err)
	}

	var schema struct {
		Properties struct {
			Aggregate struct {
				Items struct {
					Properties struct {
						Op struct {
							Enum        []string `json:"enum"`
							Description string   `json:"description"`
						} `json:"op"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"aggregate"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("re-reading the tool schema: %v", err)
	}

	got := schema.Properties.Aggregate.Items.Properties.Op.Enum
	if len(got) == 0 {
		t.Fatal("the tool schema's aggregate.op carries no enum at all — the model is told nothing " +
			"about which summaries exist")
	}

	present := map[string]bool{}
	for _, op := range got {
		present[op] = true
	}
	var missing []string
	for _, op := range generatedAggregateOps(t) {
		if !present[op] {
			missing = append(missing, op)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the tool schema the model receives omits these accepted summary ops: %s",
			strings.Join(missing, ", "))
	}

	if d := schema.Properties.Aggregate.Items.Properties.Op.Description; !strings.Contains(d, "median") {
		t.Errorf("the tool schema's aggregate.op description does not distinguish the ops it lists; got %q", d)
	}
}
