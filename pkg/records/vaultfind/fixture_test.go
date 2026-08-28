// Omnipus — shared fixtures for the vault_find tests.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package vaultfind

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// The fixture vocabulary is the greenhouse, not the CRM one, and that is a rule
// rather than a whim: ADR-068 D0 says the product ships MECHANISM and the vault
// ships CONVENTION, so a test corpus talking about companies, deals and pipeline
// stages teaches the opposite by example. Every record type, property and value
// below is the test's own declaration and none of it ships.
const plantSchemaYAML = `
schema_version: 1
type: plant
label: Plant
identity:
  prefix: PL
properties:
  species:   { type: text, required: true }
  condition: { type: enum, values: [seedling, growing, dormant] }
  planted:   { type: date }
  height_cm: { type: decimal }
  cuttings:  { type: integer }
  bed:       { type: relation, to: bed }
  keeper:    { type: person }
  labels:    { type: text, many: true }
`

// plantSet loads the fixture schema through records.LoadSchemas — the same path
// production uses — rather than assembling a SchemaSet by hand.
//
// The distinction matters: a hand-built set would skip the loader's own
// rejections, so a schema this package could never actually load would still
// pass every test here.
func plantSet(t *testing.T) *records.SchemaSet {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plant.yaml"), []byte(plantSchemaYAML), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("the fixture schema was rejected: %v", report.Rejections)
	}
	return set
}

// fixture is a live properties index over a corpus the test declares.
type fixture struct {
	t     *testing.T
	store propindex.Store
	set   *records.SchemaSet
	text  *stubText
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "properties.db")
	store, err := propindex.Open(context.Background(), path, propindex.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return &fixture{t: t, store: store, set: plantSet(t), text: &stubText{hits: map[string]TextHit{}}}
}

// write indexes one note from its literal source, through the same parser the
// validator uses. Nothing here hand-assembles a stored row: a fixture that
// bypassed BuildNoteRows would exercise the storage of values the product never
// produces, and would go on passing after the real derivation broke.
func (f *fixture) write(path, src string) propindex.NoteRows {
	f.t.Helper()
	b := []byte(src)
	rec := records.ParseRecord(path, b)
	sc, _ := f.set.Get(rec.TypeName())
	rows := propindex.BuildNoteRows(rec, sc, b, propindex.SourceHash(b))
	if err := f.store.UpsertNote(context.Background(), rows); err != nil {
		f.t.Fatalf("UpsertNote(%s): %v", path, err)
	}
	f.text.hits[path] = TextHit{Path: path, SourceHash: rows.SourceHash, Score: 1}
	return rows
}

func (f *fixture) deps() Deps {
	return Deps{Schemas: f.set, Store: f.store, Epoch: 8814}
}

// depsWithText wires the plain-word half. It is separate because most tests are
// about the TYPED half, and a test that silently searched text as well would be
// asserting two things while claiming to assert one.
func (f *fixture) depsWithText() Deps {
	d := f.deps()
	d.Text = f.text
	return d
}

// plant writes one well-formed record.
func (f *fixture) plant(n int, condition, height string) {
	f.t.Helper()
	f.write(fmt.Sprintf("garden/plant-%04d.md", n), fmt.Sprintf(`---
type: plant
id: PL-%04d
species: Monstera deliciosa
condition: %s
planted: 2026-03-%02d
height_cm: %s
cuttings: 3
bed: "[[Bed %d]]"
keeper: "[[Rosa]]"
labels: [indoor, humid]
---

# Plant %d

- [ ] repot in spring
- [x] moved to the east window
`, n, condition, (n%27)+1, height, n%4, n))
}

// ---------------------------------------------------------------------------
// A STUB TEXT INDEX
//
// It is a stub for BLEVE, not for anything this package owns. The ranking, the
// fielded indexing and the fusion are Stage 2 agents 1-3's; what vault_find owes
// is the INTERSECTION with the typed half, and that is what these tests assert.
// ---------------------------------------------------------------------------

type stubText struct {
	hits  map[string]TextHit
	only  []string
	terms []generated.VaultTermCount
	err   error
}

func (s *stubText) Search(_ context.Context, _ string, _ int) ([]TextHit, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out []TextHit
	for _, p := range s.only {
		if h, ok := s.hits[p]; ok {
			out = append(out, h)
		}
	}
	return out, nil
}

func (s *stubText) NearestTerms(_ context.Context, _ string, _ int) ([]generated.VaultTermCount, error) {
	return s.terms, nil
}

// ---------------------------------------------------------------------------
// REQUEST BUILDERS — small, so a test reads as the question it is asking
// ---------------------------------------------------------------------------

func req(mut ...func(*generated.VaultFindRequest)) generated.VaultFindRequest {
	r := generated.VaultFindRequest{}
	for _, m := range mut {
		m(&r)
	}
	return r
}

func withType(t string) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) { r.Type = &t }
}

func withFilter(n generated.VaultFilterNode) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) { r.Filter = &n }
}

func leaf(property, op string, value ...string) generated.VaultFilterNode {
	o := generated.VaultFilterNodeOp(op)
	n := generated.VaultFilterNode{Property: &property, Op: &o}
	switch {
	case op == "IN":
		vs := append([]string{}, value...)
		n.Values = &vs
	case len(value) == 1:
		n.Value = &value[0]
	}
	return n
}

func notNode(inner generated.VaultFilterNode) generated.VaultFilterNode {
	return generated.VaultFilterNode{Not: &inner}
}

// ---------------------------------------------------------------------------
// ASSERTIONS EVERY vault_find TEST SHARES
// ---------------------------------------------------------------------------

// mustFind runs a query and asserts the shared invariants over the response, so
// that AC-P1 and AC-P2 are checked on EVERY response every test in this package
// produces rather than in one test that could be deleted.
func mustFind(t *testing.T, d Deps, r generated.VaultFindRequest) generated.VaultFindResponse {
	t.Helper()
	resp, err := Find(context.Background(), d, r)
	if err != nil {
		t.Fatalf("Find: unexpected refusal: %v", err)
	}
	assertResponseInvariants(t, resp)
	return resp
}

// mustRefuse runs a query that must NOT succeed, and asserts it refused rather
// than returning an empty success.
func mustRefuse(t *testing.T, d Deps, r generated.VaultFindRequest) generated.VaultFindResponse {
	t.Helper()
	resp, err := Find(context.Background(), d, r)
	if err == nil {
		t.Fatalf("Find returned SUCCESS with %d rows where a refusal was required. "+
			"An empty success is indistinguishable from an empty vault, which is the "+
			"failure this whole surface exists to remove.", len(resp.Rows))
	}
	if !resp.Refused {
		t.Errorf("the error was returned but the response does not say refused:true; "+
			"a caller must be able to tell \"here is none of it\" from \"here is some of it\" "+
			"without parsing prose. Got complete=%v refused=%v", resp.Complete, resp.Refused)
	}
	if len(resp.Rows) != 0 {
		t.Errorf("a refusal returned %d rows; no partial answer is ever returned over a refused set",
			len(resp.Rows))
	}
	assertResponseInvariants(t, resp)
	return resp
}

// assertResponseInvariants is AC-P1 and AC-P2, enforced everywhere.
func assertResponseInvariants(t *testing.T, resp generated.VaultFindResponse) {
	t.Helper()

	// AC-P1 — a verdict of `no` with an empty problem list is a defect: either
	// the reason is named or the verdict is wrong.
	if !resp.Complete && len(resp.Problems) == 0 {
		t.Errorf("COMPLETE is no and PROBLEMS is empty. One of the two is wrong: "+
			"either name what was excluded, or say the answer is complete. reason=%q",
			deref(resp.CompleteReason))
	}
	// Every problem carries a remedy. A problem that states only what went wrong
	// leaves the caller with nothing to do next, which in an agentic loop means
	// guessing.
	for i, p := range resp.Problems {
		if p.Fix == nil || strings.TrimSpace(*p.Fix) == "" {
			t.Errorf("problem[%d] (%s) names no fix: %q", i, p.Code, p.Reason)
		}
		if strings.TrimSpace(p.Reason) == "" {
			t.Errorf("problem[%d] (%s) has an empty reason, which is indistinguishable from silence", i, p.Code)
		}
	}
	// FR-126 — every response ends with something addressable.
	if len(resp.Next) == 0 {
		t.Errorf("the response carries no NEXT actions; a response with no next call " +
			"forces the model to invent arguments")
	}
	// The counts cannot lie about each other.
	if resp.Counts.Shown > resp.Counts.Evaluated {
		t.Errorf("counts claim %d shown of %d evaluated", resp.Counts.Shown, resp.Counts.Evaluated)
	}
	if resp.Counts.Shown != len(resp.Rows) {
		t.Errorf("counts.shown is %d but the response carries %d rows; the wire object and "+
			"the text it renders to must not disagree about what was returned",
			resp.Counts.Shown, len(resp.Rows))
	}

	assertBlockOrder(t, Render(resp))
}

// assertBlockOrder is AC-P2: header → rows → totals → problems → next, asserted
// as an ORDER rather than as a presence.
func assertBlockOrder(t *testing.T, out string) {
	t.Helper()
	if !strings.HasPrefix(out, "COMPLETE:") {
		t.Fatalf("the response does not open with its completeness verdict. "+
			"A caveat that arrives after the evidence arrives after the conclusion "+
			"has formed. Got:\n%s", firstLine(out))
	}
	order := []string{"COMPLETE:", "QUERY:", "TOTALS:", "PROBLEMS (", "NEXT"}
	last := -1
	for _, marker := range order {
		i := strings.Index(out, marker)
		if i < 0 {
			continue
		}
		if i < last {
			t.Errorf("block %q appears out of order in the rendered response:\n%s", marker, out)
		}
		last = i
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// rowIDs is what a test asserts against — the identifiers, in order.
func rowIDs(resp generated.VaultFindResponse) []string {
	out := make([]string, 0, len(resp.Rows))
	for _, r := range resp.Rows {
		out = append(out, rowID(r))
	}
	return out
}
