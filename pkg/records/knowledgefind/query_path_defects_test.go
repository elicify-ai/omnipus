// Omnipus — reproductions for the query-path defects found in the 2026-08-31
// review: the untyped view that could never be queried (#4), kind=record's inert
// enum member (#5), the blank `type` that discarded a view's own narrowing (#6),
// the unvalidated sort direction (#9), the unservable view reported as
// non-existent (#13), the view limit that was never applied (#14), the bound
// refusal that advised a call hitting the same bound (#15), the unknown key
// inside a filter leaf (#16), the response budget the problem list walked past
// (#22), the unbounded filter tree (#23) and the plain-word query refused on a
// build with no properties index (#29).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// SHARED SCAFFOLDING
// ---------------------------------------------------------------------------

// schemaSetFrom loads an arbitrary set of schema files through the production
// loader, so a test can declare TWO record types — which is what the untyped
// namespace's conflict rule is about and what the single-schema fixture cannot
// express.
func schemaSetFrom(t *testing.T, files map[string]string) *records.SchemaSet {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("a fixture schema was rejected: %v", report.Rejections)
	}
	return set
}

// untypedNote is an ORDINARY note: no `type:` key, so no schema declares any of
// its properties. FR-021e stores its frontmatter anyway; the untyped-view rule
// is what makes it queryable.
const untypedNote = `---
status: open
owner: Rosa
rank: 7
---

# A folder-scoped note
`

func rowPaths(resp generated.VaultFindResponse) []string {
	out := make([]string, 0, len(resp.Rows))
	for _, r := range resp.Rows {
		out = append(out, r.Path)
	}
	return out
}

func containsPath(resp generated.VaultFindResponse, want string) bool {
	for _, p := range rowPaths(resp) {
		if p == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// #4 — AN UNTYPED VIEW CAN BE QUERIED
// ---------------------------------------------------------------------------

// TestUntypedQuery_ResolvesAnUndeclaredPropertyByName is the contract's own
// sentence, executed: "A name no in-scope type declares resolves in the TEXT
// domain over the raw values."
//
// Before the fix namespace.resolve refused every ordinary property name the
// moment no `type` was given — so a folder-scoped base, which is what four of
// the founder's eighteen bases are, could be imported and never run.
func TestUntypedQuery_ResolvesAnUndeclaredPropertyByName(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")
	f.write("notes/folder-note.md", untypedNote)

	resp := mustFind(t, f.deps(), req(withFilter(leaf("status", "=", "open"))))

	if !containsPath(resp, "notes/folder-note.md") {
		t.Fatalf("an untyped query on an undeclared property returned %v; the note whose file "+
			"says `status: open` must be in the answer", rowPaths(resp))
	}
	if len(resp.Rows) != 1 {
		t.Errorf("expected exactly the one note carrying status=open, got %v", rowPaths(resp))
	}
	// An undeclared name that MATCHED carries no caveat: the vault plainly
	// holds the key, so a note about it would be the noise that trains a reader
	// to skip the header line.
	if strings.Contains(Render(resp), "no record type in scope declares") {
		t.Errorf("the undeclared-name note fired on an answer that returned rows:\n%s", Render(resp))
	}
}

// TestUntypedQuery_ACarriedKeyIsNeverNull is founder ruling 1, stated as the
// spec states it: "a note whose file says `status: open` must never answer TRUE
// to `status IS NULL`".
func TestUntypedQuery_ACarriedKeyIsNeverNull(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")
	f.write("notes/folder-note.md", untypedNote)

	resp := mustFind(t, f.deps(), req(withFilter(leaf("status", "IS NULL"))))

	if containsPath(resp, "notes/folder-note.md") {
		t.Errorf("`status IS NULL` returned the note whose frontmatter says `status: open`: %v",
			rowPaths(resp))
	}
	if !containsPath(resp, "garden/plant-0001.md") {
		t.Errorf("`status IS NULL` dropped a note that genuinely carries no `status` key: %v",
			rowPaths(resp))
	}
}

// TestUntypedQuery_ResolvesInTheDeclaringTypesDomain covers the other half of
// the rule: a name ONE in-scope type declares resolves in THAT type's domain,
// not in the text fallback — so an ordering comparison over a declared integer
// still works in an untyped query.
func TestUntypedQuery_ResolvesInTheDeclaringTypesDomain(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0") // cuttings: 3, declared integer
	f.write("notes/folder-note.md", untypedNote)

	resp := mustFind(t, f.deps(), req(withFilter(leaf("cuttings", ">", "2"))))

	if !containsPath(resp, "garden/plant-0001.md") {
		t.Fatalf("`cuttings > 2` over an untyped query returned %v; the declared integer domain "+
			"is what an undeclared-on-that-type name resolves in", rowPaths(resp))
	}
}

// TestUntypedQuery_RefusesTwoConflictingDeclarations is the contract's loud
// case: "Two in-scope types declaring one name with DIFFERENT types REFUSE the
// query naming both declarations — loud, never a silent domain split."
func TestUntypedQuery_RefusesTwoConflictingDeclarations(t *testing.T) {
	f := newFixture(t)
	f.set = schemaSetFrom(t, map[string]string{
		"plant.yaml": plantSchemaYAML,
		"tool.yaml": `
schema_version: 1
type: tool
label: Tool
properties:
  cuttings: { type: text }
`,
	})

	resp := mustRefuse(t, f.deps(), req(withFilter(leaf("cuttings", "=", "3"))))

	reason := resp.Problems[0].Reason
	for _, want := range []string{"plant", "tool", "cuttings"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the conflict refusal does not name %q, so the caller cannot tell which two "+
				"declarations disagree: %q", want, reason)
		}
	}
}

// TestUntypedQuery_SelectRendersAnUndeclaredColumn proves the fix reaches every
// property position, not only `filter`: namespace.resolve is the single
// resolution path for filter, sort, group_by, select and aggregate.
func TestUntypedQuery_SelectRendersAnUndeclaredColumn(t *testing.T) {
	f := newFixture(t)
	f.write("notes/folder-note.md", untypedNote)

	cols := []string{"owner"}
	r := req(withFilter(leaf("status", "=", "open")))
	r.Select = &cols

	resp := mustFind(t, f.deps(), r)
	out := Render(resp)
	if !strings.Contains(out, "owner Rosa") {
		t.Errorf("select of an undeclared property rendered no column:\n%s", out)
	}
}

// TestUntypedQuery_AZeroRowAnswerSaysNothingDeclaredTheName closes the one
// silent empty the untyped rule creates: a misspelled name cannot be refused
// there (every name is legal and resolves in the text domain), so the answer
// has to carry the fact instead.
func TestUntypedQuery_AZeroRowAnswerSaysNothingDeclaredTheName(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")
	f.write("notes/folder-note.md", untypedNote)

	resp := mustFind(t, f.deps(), req(withFilter(leaf("stauts", "=", "open"))))
	if len(resp.Rows) != 0 {
		t.Fatalf("a misspelled property matched %d rows", len(resp.Rows))
	}
	out := Render(resp)
	if !strings.Contains(out, "stauts") || !strings.Contains(out, "no record type in scope declares") {
		t.Errorf("a confident zero says nothing about the name it could not have matched:\n%s", out)
	}
}

// TestUntypedQuery_AZeroRowAnswerOverADeclaredNameIsNotAnnotated is the guard
// on the guard: the note must not fire on an ordinary empty result, or it
// becomes noise a reader learns to skip.
func TestUntypedQuery_AZeroRowAnswerOverADeclaredNameIsNotAnnotated(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")

	resp := mustFind(t, f.deps(), req(withFilter(leaf("condition", "=", "dormant"))))
	if strings.Contains(Render(resp), "no record type in scope declares") {
		t.Errorf("the undeclared-name note fired for a name plant declares:\n%s", Render(resp))
	}
}

// ---------------------------------------------------------------------------
// #5 — kind=record NARROWS
// ---------------------------------------------------------------------------

func TestKindRecord_NarrowsToNotesThatDeclareAType(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")
	f.write("notes/folder-note.md", untypedNote)

	kind := generated.VaultFindRequestKind(KindRecord)
	r := generated.VaultFindRequest{Kind: &kind}
	resp := mustFind(t, f.deps(), r)

	if containsPath(resp, "notes/folder-note.md") {
		t.Errorf("kind=record returned a note that declares no record type: %v. Its own doc "+
			"comment calls it \"a synonym for note narrowed to notes that declare a type\"; "+
			"advertised and inert is the one outcome that is not acceptable.", rowPaths(resp))
	}
	if !containsPath(resp, "garden/plant-0001.md") {
		t.Errorf("kind=record dropped a note that DOES declare a type: %v", rowPaths(resp))
	}
}

// ---------------------------------------------------------------------------
// #6 — A BLANK `type` DOES NOT DISCARD A VIEW'S NARROWING
// ---------------------------------------------------------------------------

// blankTypeViews is a saved view that narrows to `plant`.
type blankTypeViews struct{ req generated.VaultFindRequest }

func (v blankTypeViews) View(name string) (generated.VaultFindRequest, bool) {
	if name != "recent" {
		return generated.VaultFindRequest{}, false
	}
	return v.req, true
}
func (v blankTypeViews) Names() []string { return []string{"recent"} }

func TestBlankType_DoesNotDiscardASavedViewsNarrowing(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")
	f.write("notes/folder-note.md", untypedNote)

	plant := "plant"
	views := blankTypeViews{req: generated.VaultFindRequest{Type: &plant}}
	d := f.deps()
	d.Views = views

	blank := ""
	name := "recent"
	r := generated.VaultFindRequest{View: &name, Type: &blank}

	resp, err := Find(context.Background(), d, r)
	if err == nil {
		if containsPath(resp, "notes/folder-note.md") {
			t.Fatalf("{view:recent, type:\"\"} ran the view against EVERY note in the vault and "+
				"presented it as the view's answer: %v", rowPaths(resp))
		}
		t.Fatalf("a blank `type` was accepted; the contract says a present-but-blank type is a " +
			"typo for a type name, not a deliberate absence")
	}
	if !strings.Contains(resp.Problems[0].Reason, "type") {
		t.Errorf("the blank-type refusal does not name the argument: %q", resp.Problems[0].Reason)
	}
	if resp.Problems[0].Fix == nil || !strings.Contains(*resp.Problems[0].Fix, "omit") {
		t.Errorf("the blank-type refusal does not tell the caller to omit the key: %v",
			resp.Problems[0].Fix)
	}
}

func TestBlankKind_IsRefusedRatherThanIgnored(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")

	blank := generated.VaultFindRequestKind("")
	r := generated.VaultFindRequest{Kind: &blank}
	mustRefuse(t, f.deps(), r)
}

// ---------------------------------------------------------------------------
// #9 — A SORT DIRECTION IS VALIDATED
// ---------------------------------------------------------------------------

func TestSortDirection_IsRefusedWhenItIsNotADirection(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")

	bad := generated.VaultFindSortDirection("descending")
	sorts := []generated.VaultFindSort{{Property: "height_cm", Direction: &bad}}
	r := req(withType("plant"))
	r.Sort = &sorts

	resp := mustRefuse(t, f.deps(), r)
	if !strings.Contains(resp.Problems[0].Reason, "descending") {
		t.Errorf("the refusal does not quote the direction the caller wrote: %q",
			resp.Problems[0].Reason)
	}
}

// TestSortDirection_DescendingStillSortsDescending is the guard on the guard: a
// validation that also broke the working spelling would be a worse defect than
// the one it fixed.
func TestSortDirection_DescendingStillSortsDescending(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "10.0")
	f.plant(2, "growing", "90.0")

	desc := generated.VaultFindSortDirectionDesc
	sorts := []generated.VaultFindSort{{Property: "height_cm", Direction: &desc}}
	r := req(withType("plant"))
	r.Sort = &sorts

	resp := mustFind(t, f.deps(), r)
	if got := rowPaths(resp); len(got) != 2 || got[0] != "garden/plant-0002.md" {
		t.Errorf("desc no longer sorts descending: %v", got)
	}
}

// ---------------------------------------------------------------------------
// #13 — AN UNSERVABLE VIEW IS NOT REPORTED AS NON-EXISTENT
// ---------------------------------------------------------------------------

// refusingViews models the production loader: View reports ok=false for a view
// it cannot serve, and ServeRefusal carries the reason it could not carry
// through the interface.
type refusingViews struct{}

func (refusingViews) View(string) (generated.VaultFindRequest, bool) {
	return generated.VaultFindRequest{}, false
}
func (refusingViews) Names() []string { return []string{"other-view"} }
func (refusingViews) ServeRefusal(name string) (records.ViewServeRefusal, bool) {
	if name != "with-formulas" {
		return records.ViewServeRefusal{}, false
	}
	return records.ViewServeRefusal{
		Name:   name,
		Code:   records.ServeRefusalFormula,
		Reason: "it declares formulas, which knowledge_find's request cannot carry",
		Remedy: "write the filter directly, or drop the view's formulas",
	}, true
}

func TestUnservableView_IsNotReportedAsNonExistent(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")
	d := f.deps()
	d.Views = refusingViews{}

	name := "with-formulas"
	resp := mustRefuse(t, d, generated.VaultFindRequest{View: &name})

	reason := resp.Problems[0].Reason
	if strings.Contains(reason, "no saved view named") {
		t.Fatalf("a view that EXISTS and was written successfully is reported as non-existent: %q", reason)
	}
	if !strings.Contains(reason, "formulas") {
		t.Errorf("the refusal does not name why the view cannot be run: %q", reason)
	}
}

// ---------------------------------------------------------------------------
// #14 (query half) — A VIEW'S LIMIT IS APPLIED
// ---------------------------------------------------------------------------

func TestSavedViewLimit_IsApplied(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 5; i++ {
		f.plant(i, "growing", "40.0")
	}

	plant := "plant"
	two := 2
	views := blankTypeViews{req: generated.VaultFindRequest{Type: &plant, Limit: &two}}
	d := f.deps()
	d.Views = views

	name := "recent"
	resp := mustFind(t, d, generated.VaultFindRequest{View: &name})

	if len(resp.Rows) != 2 {
		t.Errorf("the view declares limit=2 and the answer carries %d rows; three surfaces state "+
			"one number and the one that runs used %d", len(resp.Rows), DefaultLimit)
	}
	if resp.LimitApplied == nil || *resp.LimitApplied != 2 {
		t.Errorf("limit_applied is %v, so the echo reports a limit the query did not run",
			resp.LimitApplied)
	}
}

// ---------------------------------------------------------------------------
// #15 — A BOUND REFUSAL DOES NOT ADVISE A CALL THAT HITS THE SAME BOUND
// ---------------------------------------------------------------------------

func TestBoundRefusal_DoesNotAdviseTheCallThatLoops(t *testing.T) {
	for _, code := range []generated.RecordProblemCode{
		generated.CandidateCapExceeded,
		generated.EvaluationBoundExceeded,
	} {
		r := refuse(problem(code, "bound exceeded", "narrow the scope"), nil)
		for _, a := range refusalActions(r) {
			if strings.Contains(a.Call, "aggregate=[{op:count}]") {
				t.Errorf("%s offers %q as the NEXT action. An aggregate-only query streams the "+
					"same candidates through the same two bounds, so a model that follows the "+
					"advice receives this refusal again, forever.", code, a.Call)
			}
		}
	}
}

// TestBoundRefusal_RemedyDoesNotPromiseATotal keeps the prose and the NEXT
// block from drifting apart again: the remedy sentence and the action must
// agree that an aggregate-only re-run is not the escape.
func TestBoundRefusal_RemedyDoesNotPromiseATotal(t *testing.T) {
	if strings.Contains(candidateCapRemedy, "ask for a total") {
		t.Errorf("the B2 remedy still tells the caller to ask for a total: %q", candidateCapRemedy)
	}
	if strings.Contains(narrowedCandidateRemedy, "ask for a total") {
		t.Errorf("the B1 remedy still tells the caller to ask for a total: %q", narrowedCandidateRemedy)
	}
}

// ---------------------------------------------------------------------------
// #16 — AN UNKNOWN KEY INSIDE A FILTER LEAF IS REFUSED BY NAME
// ---------------------------------------------------------------------------

func TestFilterLeaf_UnknownKeyIsRefusedByName(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")

	out, err := Call(context.Background(), f.deps(),
		[]byte(`{"type":"plant","filter":{"property":"condition","op":"=","value":"growing","negate":true}}`))
	if err == nil {
		t.Fatalf("`negate` inside a filter leaf was accepted and dropped in silence; the caller "+
			"receives the exact complement of what they asked for, with a \"complete\" "+
			"verdict:\n%s", out)
	}
	if !strings.Contains(out, "negate") {
		t.Errorf("the refusal does not name the key that was not understood:\n%s", out)
	}
}

func TestFilterLeaf_UnknownKeyIsFoundInsideACombinator(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")

	_, err := Call(context.Background(), f.deps(),
		[]byte(`{"type":"plant","filter":{"all":[{"property":"condition","op":"=","value":"growing"},`+
			`{"property":"cuttings","op":">","value":"1","case_sensitive":false}]}}`))
	if err == nil {
		t.Fatal("an unknown key nested inside {all:[...]} was dropped in silence")
	}
}

// TestFilterLeaf_TheDeclaredKeysAreAllAccepted is the guard on the guard: a
// key-set check that rejected a legal spelling would break every filter.
func TestFilterLeaf_TheDeclaredKeysAreAllAccepted(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")

	if _, err := Call(context.Background(), f.deps(),
		[]byte(`{"type":"plant","filter":{"not":{"any":[{"property":"condition","op":"IN","values":["growing"]},`+
			`{"property":"species","op":"IS NULL"}]}}}`)); err != nil {
		t.Fatalf("a filter using every legal node key was refused: %v", err)
	}
}

// ---------------------------------------------------------------------------
// #22 — THE RESPONSE BUDGET IS NOT BYPASSABLE BY THE PROBLEM LIST
// ---------------------------------------------------------------------------

func TestResponseBudget_IsNotBypassableByTheProblemList(t *testing.T) {
	// The cap is spec 4.2's own number, asserted against a literal for the same
	// reason the filter bounds are: a budget test that reads the budget from
	// the constant it guards cannot see the constant move.
	if ResponseBudgetBytes != 4000 {
		t.Fatalf("the stated hard cap is 4,000 bytes; this package enforces %d", ResponseBudgetBytes)
	}
	resp := generated.VaultFindResponse{
		Complete:  false,
		QueryEcho: "type=plant  limit=200",
		Counts:    generated.VaultFindCounts{Selected: 200, Evaluated: 200, Shown: 6},
		Totals:    []generated.VaultFindTotal{},
		Next:      []generated.VaultFindAction{},
	}
	for i := 0; i < 6; i++ {
		resp.Rows = append(resp.Rows, generated.VaultFindRow{
			Path:  fmt.Sprintf("garden/plant-%04d.md", i),
			Title: fmt.Sprintf("Plant %d", i),
			Cells: []generated.VaultFindCell{},
			Joins: []generated.VaultFindJoin{},
		})
	}
	for i := 0; i < 200; i++ {
		resp.Problems = append(resp.Problems, problem(generated.StaleRecord,
			fmt.Sprintf("garden/plant-%04d.md: the properties index and the text index disagree", i),
			"re-run to confirm; run knowledge_describe check_integrity if it persists",
			fmt.Sprintf("PL-%04d", i)))
	}

	if size := len(Render(resp)); size <= ResponseBudgetBytes {
		t.Fatalf("the fixture is not over budget (%d bytes), so it cannot demonstrate the bypass", size)
	}
	trimToBudget(&resp)
	if size := len(Render(resp)); size > ResponseBudgetBytes {
		t.Errorf("the rendered response is %d bytes against a stated hard cap of %d; trimToBudget "+
			"removes only ROWS while the problem list has no cap at all",
			size, ResponseBudgetBytes)
	}
	if len(resp.Rows) < minRenderedRows {
		t.Errorf("the budget trimmed past the row floor: %d rows left", len(resp.Rows))
	}
	if !strings.Contains(Render(resp), "not shown") {
		t.Errorf("problems were dropped without saying so:\n%s", Render(resp))
	}
}

// ---------------------------------------------------------------------------
// #23 — FR-023c'S 64-LEAF / DEPTH-8 BOUND IS ENFORCED ON A REQUEST
// ---------------------------------------------------------------------------

// THE NUMBERS BELOW ARE LITERALS, READ OFF FR-023c ("refused above 64 leaves
// or depth 8, naming which"), never MaxFilterLeaves/MaxFilterDepth. A test that
// builds its input FROM the constant it is checking cannot see the constant
// move: setting MaxFilterLeaves to 63 would silently rebuild the fixture at 63
// and still pass. The spec is the oracle; the constants are the subject.
const (
	specFilterLeafBound  = 64
	specFilterDepthBound = 8
)

func TestFilterTree_BoundsMatchTheRequirement(t *testing.T) {
	if MaxFilterLeaves != specFilterLeafBound || MaxFilterDepth != specFilterDepthBound {
		t.Fatalf("FR-023c states 64 leaves and depth 8; this package enforces %d and %d",
			MaxFilterLeaves, MaxFilterDepth)
	}
}

func TestFilterTree_LeafCountIsBounded(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")

	kids := make([]generated.VaultFilterNode, 0, specFilterLeafBound+1)
	for i := 0; i <= specFilterLeafBound; i++ {
		kids = append(kids, leaf("condition", "=", "growing"))
	}
	r := req(withType("plant"), withFilter(generated.VaultFilterNode{Any: &kids}))

	resp := mustRefuse(t, f.deps(), r)
	if !strings.Contains(resp.Problems[0].Reason, "leaves") {
		t.Errorf("the refusal does not name which bound was exceeded: %q", resp.Problems[0].Reason)
	}
}

func TestFilterTree_DepthIsBounded(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")

	// One `not` per level above the leaf; the leaf itself is a level, so
	// depth-8 is reached at seven wrappers and exceeded at eight.
	n := leaf("condition", "=", "growing")
	for i := 0; i < specFilterDepthBound; i++ {
		n = notNode(n)
	}
	r := req(withType("plant"), withFilter(n))

	resp := mustRefuse(t, f.deps(), r)
	if !strings.Contains(resp.Problems[0].Reason, "deep") {
		t.Errorf("the refusal does not name which bound was exceeded: %q", resp.Problems[0].Reason)
	}
}

// TestFilterTree_AtTheBoundStillRuns is the guard on the guard: an off-by-one
// that refused a legal tree would be its own defect.
func TestFilterTree_AtTheBoundStillRuns(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")

	kids := make([]generated.VaultFilterNode, 0, specFilterLeafBound)
	for i := 0; i < specFilterLeafBound; i++ {
		kids = append(kids, leaf("condition", "=", "growing"))
	}
	mustFind(t, f.deps(), req(withType("plant"), withFilter(generated.VaultFilterNode{Any: &kids})))

	n := leaf("condition", "=", "growing")
	for i := 0; i < specFilterDepthBound-1; i++ {
		n = notNode(n)
	}
	mustFind(t, f.deps(), req(withType("plant"), withFilter(n)))
}

// ---------------------------------------------------------------------------
// #29 — A PLAIN-WORD QUERY IS ANSWERED WITH NO PROPERTIES INDEX
// ---------------------------------------------------------------------------

// TestWordsOnly_IsAnsweredWithoutThePropertiesIndex holds propindex_stub.go's
// own promise: "What keeps working on such a build: knowledge_read, and the
// plain-word half of knowledge_find".
func TestWordsOnly_IsAnsweredWithoutThePropertiesIndex(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")
	f.plant(2, "growing", "50.0")
	f.text.only = []string{"garden/plant-0001.md", "garden/plant-0002.md"}

	d := f.deps()
	d.Store = nil // the shape of a build where the index cannot exist at all

	words := "monstera"
	resp, err := Find(context.Background(), d, generated.VaultFindRequest{Words: &words})
	if err != nil {
		t.Fatalf("a words-only query was refused on a build with no properties index, while "+
			"propindex_stub.go tells the operator it keeps working: %v", err)
	}
	assertResponseInvariants(t, resp)
	if len(resp.Rows) != 2 {
		t.Errorf("expected the two ranked text hits, got %v", rowPaths(resp))
	}
}

// TestTypedQuery_StillRefusesWithoutThePropertiesIndex is the other side: only
// the plain-word half keeps working, and a query that needs the index must
// still refuse rather than answer over a prefix of it.
func TestTypedQuery_StillRefusesWithoutThePropertiesIndex(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")
	f.text.only = []string{"garden/plant-0001.md"}

	d := f.deps()
	d.Store = nil

	words := "monstera"
	r := req(withFilter(leaf("condition", "=", "growing")))
	r.Words = &words
	mustRefuse(t, d, r)
}
