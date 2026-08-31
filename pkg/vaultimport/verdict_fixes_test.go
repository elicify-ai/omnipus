// Omnipus — reproductions for the importer findings the 2026-08-31 review
// confirmed. Each test FAILED against the code as it stood; the fix is what
// turns it green.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// loadedSchemaSet renders the inferred schemas and loads them back through
// records.LoadSchemas — the REAL loader, never a hand-built SchemaSet, so a
// schema this package cannot actually write fails here rather than passing on
// a struct nobody parsed.
func loadedSchemaSet(t *testing.T, inferred map[string][]InferredProperty) *records.SchemaSet {
	t.Helper()
	set, report, err := schemaSetFromRendered(inferred)
	if err != nil {
		t.Fatalf("rendering and reloading the inferred schemas: %v", err)
	}
	if report != nil && !report.OK() {
		t.Fatalf("the written schemas are rejected by the real loader: %v", report.Rejections)
	}
	return set
}

// ---------------------------------------------------------------------------
// Finding 2 (CRITICAL) — a boolean property must not be inferred as an enum,
// because `enum` sits on the SAFE side of the truthy partition and a bare
// truthy filter then imports as `IS NOT NULL` with no loss and the view
// ENABLED. 200 task notes with `archived: true`/`false`: Obsidian returns the
// true ones, the imported view returns every note that declares `archived`.
// ---------------------------------------------------------------------------

func TestInfer_BooleanIsACheckboxNotAnEnum(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "a.md", "---\ntype: task\narchived: true\n---\n"),
		noteOnDisk(t, dir, "b.md", "---\ntype: task\narchived: false\n---\n"),
		noteOnDisk(t, dir, "c.md", "---\ntype: task\narchived: True\n---\n"),
	}
	props := InferSchema(CollectTypeGroups(notes)["task"], BuildNameIndex(notes))
	if len(props) != 1 {
		t.Fatalf("expected one property, got %d", len(props))
	}
	if props[0].Type != records.TypeCheckbox {
		t.Fatalf("archived inferred as %q, want %q — an enum of [false,true] is on the SAFE side of the truthy partition and lets a bare truthy filter import broadened",
			props[0].Type, records.TypeCheckbox)
	}
	if len(props[0].EnumValues) != 0 {
		t.Fatalf("a checkbox declares no enum values, got %v", props[0].EnumValues)
	}
}

// TestInfer_BooleanSchemaRoundTripsAndValidates proves the checkbox
// declaration is not merely a different string: the written schema loads
// through the real loader and the notes validate against it.
func TestInfer_BooleanSchemaRoundTripsAndValidates(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "a.md", "---\ntype: task\narchived: true\n---\n"),
		noteOnDisk(t, dir, "b.md", "---\ntype: task\narchived: false\n---\n"),
	}
	props := InferSchema(CollectTypeGroups(notes)["task"], BuildNameIndex(notes))
	set := loadedSchemaSet(t, map[string][]InferredProperty{"task": props})
	recs := []records.Record{notes[0].Rec, notes[1].Rec}
	rep := records.Validate(set, recs, records.ValidateOptions{})
	for _, rr := range rep.Records {
		if !rr.Valid() {
			t.Fatalf("%s does not validate against the schema this run produced: %+v", rr.Path, rr.Findings)
		}
	}
}

// TestTruthy_BareFilterOnABooleanDisablesTheView is finding 2's end-to-end
// shape: the whole import of a base whose view filters on a bare boolean
// property must leave that view DISABLED with the loss named — never enabled.
func TestTruthy_BareFilterOnABooleanDisablesTheView(t *testing.T) {
	schemas := NewSchemaIndex(map[string][]InferredProperty{
		"task": {{Name: "archived", Type: records.TypeCheckbox}},
	})
	pb, err := ParseBaseFile([]byte(`
views:
  - type: table
    name: Archived
    filters:
      and:
        - type == "task"
        - archived
`))
	if err != nil {
		t.Fatalf("parsing the base: %v", err)
	}
	out, _ := TranslateBase(pb, "T.base", schemas, NewSlugRegistry())
	vo := out.Views[0]
	if !vo.Disabled {
		t.Fatalf("a bare truthy filter on a boolean property imported ENABLED with %d losses — FR-105 forbids exactly this broadening", len(vo.Losses))
	}
	if !strings.Contains(strings.Join(vo.DisablingLosses, "\n"), "archived") {
		t.Fatalf("the disabling loss does not name the property: %v", vo.DisablingLosses)
	}
}

// TestTruthy_EnumWithAFalsyDeclaredValueIsRefused closes the residual hole the
// checkbox fix leaves behind: an enum is only truthy-safe when NONE of its
// declared values is falsy in Obsidian's own JavaScript sense. An enum
// declaring `0` is not.
func TestTruthy_EnumWithAFalsyDeclaredValueIsRefused(t *testing.T) {
	schemas := NewSchemaIndex(map[string][]InferredProperty{
		"task": {{Name: "level", Type: records.TypeEnum, EnumValues: []string{"0", "high", "low"}}},
	})
	pb, err := ParseBaseFile([]byte(`
views:
  - type: table
    name: Levelled
    filters:
      and:
        - type == "task"
        - level
`))
	if err != nil {
		t.Fatalf("parsing the base: %v", err)
	}
	out, _ := TranslateBase(pb, "T.base", schemas, NewSlugRegistry())
	if !out.Views[0].Disabled {
		t.Fatalf("a bare truthy filter on an enum declaring `0` imported ENABLED — `IS NOT NULL` matches the record holding 0, which Obsidian's truthy test rejects")
	}
}

// ---------------------------------------------------------------------------
// Finding 3 (HIGH) — `&&` / `||` inside a filter expression are swallowed into
// the literal. With `!=` the emitted filter is NOT(status = '"done" && …'),
// which matches EVERY record, ships enabled, zero losses.
// ---------------------------------------------------------------------------

func TestLeaf_LogicalOperatorsAreRefusedNotSwallowed(t *testing.T) {
	for _, expr := range []string{
		`status != "done" && priority > 3`,
		`status == "open" || status == "blocked"`,
		`status == "open" && archived`,
		`!archived && status == "open"`,
	} {
		if got := parseLeaf(expr); got.Kind != leafUntranslatable {
			t.Errorf("parseLeaf(%q) = kind %v filter %+v; want leafUntranslatable — the trailing (.+) swallowed the rest of the expression into the literal",
				expr, got.Kind, got.Filter)
		}
	}
}

// TestLeaf_LogicalOperatorInsideAQuotedLiteralStillTranslates guards the fix
// against over-refusing: `&&` inside a quoted string is part of the VALUE, not
// an operator, and refusing it would be a loss this importer need not take.
func TestLeaf_LogicalOperatorInsideAQuotedLiteralStillTranslates(t *testing.T) {
	got := parseLeaf(`vendor == "Smith && Sons"`)
	if got.Kind != leafFilter {
		t.Fatalf("parseLeaf refused a legitimate literal containing `&&`: %+v", got)
	}
	if got.Filter.Values[0] != "Smith && Sons" {
		t.Fatalf("literal = %q, want %q", got.Filter.Values[0], "Smith && Sons")
	}
}

// TestTranslate_CompoundFileMethodIsRefused closes the same hole one pattern
// over. reFileMethod is anchored on a closing paren, so
// `file.inFolder("a") && file.inFolder("b")` matches it with the argument
// `a") && file.inFolder("b` — a folder name nothing is in, ANDed into the view
// as if the operator had written it. It must be lost, not translated.
func TestTranslate_CompoundFileMethodIsRefused(t *testing.T) {
	tr := TranslateFilterTree(`file.inFolder("a") && file.inFolder("b")`)
	if tr.Root == nil || tr.Root.Kind != rawKindLost {
		t.Fatalf("a compound file-method expression translated instead of being lost: %+v", tr.Root)
	}
}

// TestTranslate_LogicalOperatorDisablesTheView is finding 3 end to end.
func TestTranslate_LogicalOperatorDisablesTheView(t *testing.T) {
	schemas := NewSchemaIndex(map[string][]InferredProperty{
		"task": {
			{Name: "status", Type: records.TypeEnum, EnumValues: []string{"done", "open"}},
			{Name: "priority", Type: records.TypeInteger},
		},
	})
	pb, err := ParseBaseFile([]byte(`
views:
  - type: table
    name: Live
    filters:
      and:
        - type == "task"
        - status != "done" && priority > 3
`))
	if err != nil {
		t.Fatalf("parsing the base: %v", err)
	}
	out, _ := TranslateBase(pb, "T.base", schemas, NewSlugRegistry())
	if !out.Views[0].Disabled {
		t.Fatalf("a filter carrying `&&` imported ENABLED with losses %v — the negated whole-string equality matches every record", out.Views[0].Losses)
	}
}

// ---------------------------------------------------------------------------
// Finding 12 (security) — a note's `type:` value becomes a file path with no
// validation, so `type: ../../../pwned` writes a .yaml file outside the vault.
// ---------------------------------------------------------------------------

func TestRun_MaliciousTypeNameWritesNothingOutsideTheSchemaDirectory(t *testing.T) {
	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "evil.md"),
		[]byte("---\ntype: \"../../../pwned\"\nname: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "ok.md"),
		[]byte("---\ntype: task\nname: y\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := Run(vault, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Nothing may exist anywhere outside the vault.
	var escaped []string
	if err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasPrefix(p, vault+string(filepath.Separator)) {
			escaped = append(escaped, p)
		}
		return nil
	}); err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(escaped) > 0 {
		t.Fatalf("the import wrote outside the vault: %v", escaped)
	}

	// And the schema directory holds no traversal-shaped file either.
	entries, _ := os.ReadDir(records.SchemaDir(vault))
	for _, e := range entries {
		if strings.Contains(e.Name(), "pwned") {
			t.Fatalf("a schema was written for a type name that is not a legal record type: %s", e.Name())
		}
	}

	// The refusal must be REPORTED, not silent.
	if len(rep.RejectedTypes) == 0 {
		t.Fatalf("the illegal type name was dropped with nothing in the report to say so")
	}
	found := false
	for _, rt := range rep.RejectedTypes {
		if strings.Contains(rt.Type, "pwned") {
			found = true
			if len(rt.NotePaths) == 0 {
				t.Errorf("the rejection names no note; an operator cannot find the file to fix")
			}
			if rt.Reason == "" {
				t.Errorf("the rejection carries no reason")
			}
		}
	}
	if !found {
		t.Fatalf("rejected types = %+v, none names the offending type", rep.RejectedTypes)
	}

	// The legitimate type is unaffected.
	var sawTask bool
	for _, ts := range rep.Types {
		if ts.Type == "task" {
			sawTask = true
		}
	}
	if !sawTask {
		t.Fatalf("one illegal type name suppressed the legitimate ones: %+v", rep.Types)
	}
}

func TestValidRecordTypeName(t *testing.T) {
	legal := []string{"task", "legal-entity", "brand_kit", "deal2", "A"}
	illegal := []string{"", " ", "../x", "a/b", "a\\b", ".", "..", ".hidden", "a b", "a.yaml", strings.Repeat("x", 200)}
	for _, s := range legal {
		if _, ok := validRecordTypeName(s); !ok {
			t.Errorf("validRecordTypeName(%q) refused a legal name", s)
		}
	}
	for _, s := range illegal {
		if _, ok := validRecordTypeName(s); ok {
			t.Errorf("validRecordTypeName(%q) accepted a name that becomes a file path", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Finding 10 — `many` was set by ANY single list-valued note, so three notes
// writing `tags: urgent` and one writing `tags: [urgent, legal]` produced
// `many: true` and three validation errors the importer manufactured itself.
// ---------------------------------------------------------------------------

func TestInfer_ManyFollowsTheMajorityNotASingleNote(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "a.md", "---\ntype: task\ntags: urgent\n---\n"),
		noteOnDisk(t, dir, "b.md", "---\ntype: task\ntags: urgent\n---\n"),
		noteOnDisk(t, dir, "c.md", "---\ntype: task\ntags: legal\n---\n"),
		noteOnDisk(t, dir, "d.md", "---\ntype: task\ntags:\n  - urgent\n  - legal\n---\n"),
	}
	props := InferSchema(CollectTypeGroups(notes)["task"], BuildNameIndex(notes))
	if props[0].Many {
		t.Fatalf("tags inferred many=true from ONE list-valued note out of four — the other three then fail validation with an arity error this run manufactured")
	}
	if props[0].AritySplit == nil {
		t.Fatalf("the arity disagreement was resolved silently; it must be reported")
	}
	if props[0].AritySplit.ListCount != 1 || props[0].AritySplit.ScalarCount != 3 {
		t.Fatalf("arity split = %+v, want list=1 scalar=3", props[0].AritySplit)
	}
}

func TestInfer_ManyStillHoldsWhenTheListsAreTheMajority(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "a.md", "---\ntype: task\ntags:\n  - urgent\n---\n"),
		noteOnDisk(t, dir, "b.md", "---\ntype: task\ntags:\n  - legal\n---\n"),
		noteOnDisk(t, dir, "c.md", "---\ntype: task\ntags: urgent\n---\n"),
	}
	props := InferSchema(CollectTypeGroups(notes)["task"], BuildNameIndex(notes))
	if !props[0].Many {
		t.Fatalf("two list-valued notes against one scalar must still declare many")
	}
	if props[0].AritySplit == nil {
		t.Fatalf("the minority scalar note must still be reported")
	}
}

func TestInfer_UnanimousArityReportsNoSplit(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "a.md", "---\ntype: task\ntags:\n  - urgent\n---\n"),
		noteOnDisk(t, dir, "b.md", "---\ntype: task\ntags:\n  - legal\n---\n"),
	}
	props := InferSchema(CollectTypeGroups(notes)["task"], BuildNameIndex(notes))
	if !props[0].Many || props[0].AritySplit != nil {
		t.Fatalf("unanimous lists: many=%v split=%+v", props[0].Many, props[0].AritySplit)
	}
}

// ---------------------------------------------------------------------------
// Finding 11 — the supermajority numerator counted per (link × type) while the
// denominator counted per link, so an exact 50/50 tie was declared a
// supermajority and the winner picked alphabetically.
// ---------------------------------------------------------------------------

func TestInferRelation_AmbiguousLinkIsNotEvidenceForEitherType(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "companies/Acme.md", "---\ntype: company\n---\n"),
		noteOnDisk(t, dir, "vendors/Acme.md", "---\ntype: vendor\n---\n"),
		noteOnDisk(t, dir, "deals/1.md", "---\ntype: deal\nparty: \"[[Acme]]\"\n---\n"),
		noteOnDisk(t, dir, "deals/2.md", "---\ntype: deal\nparty: \"[[Acme]]\"\n---\n"),
		noteOnDisk(t, dir, "deals/3.md", "---\ntype: deal\nparty: \"[[Acme]]\"\n---\n"),
	}
	props := InferSchema(CollectTypeGroups(notes)["deal"], BuildNameIndex(notes))
	p := props[0]
	if p.Type == records.TypeRelation {
		t.Fatalf("every link resolves to BOTH a company and a vendor — an exact tie — yet `to: %s` was declared as a supermajority", p.To)
	}
	if p.RelationSplit == nil {
		t.Fatalf("the ambiguity was not reported at all")
	}
	if p.RelationSplit.AmbiguousLinks != 3 {
		t.Fatalf("ambiguous links = %d, want 3", p.RelationSplit.AmbiguousLinks)
	}
}

func TestInferRelation_UnambiguousSupermajorityStillDeclares(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "companies/Acme.md", "---\ntype: company\n---\n"),
		noteOnDisk(t, dir, "companies/Beta.md", "---\ntype: company\n---\n"),
		noteOnDisk(t, dir, "people/Cara.md", "---\ntype: person\nname: Cara\n---\n"),
		noteOnDisk(t, dir, "deals/1.md", "---\ntype: deal\nparty: \"[[Acme]]\"\n---\n"),
		noteOnDisk(t, dir, "deals/2.md", "---\ntype: deal\nparty: \"[[Beta]]\"\n---\n"),
		noteOnDisk(t, dir, "deals/3.md", "---\ntype: deal\nparty: \"[[Cara]]\"\n---\n"),
	}
	props := InferSchema(CollectTypeGroups(notes)["deal"], BuildNameIndex(notes))
	p := props[0]
	if p.Type != records.TypeRelation || p.To != "company" {
		t.Fatalf("2 of 3 unambiguous company links is a supermajority; got type=%s to=%q split=%+v", p.Type, p.To, p.RelationSplit)
	}
}

// ---------------------------------------------------------------------------
// Finding 18 — the importer knew 4 of the contract's 15 aggregate ops, and its
// refusal quoted a rule the contract explicitly retracted.
// ---------------------------------------------------------------------------

// TestAggregates_EveryContractOpIsReachable reads the CONTRACT, not the Go
// slice under test. An oracle taken from `allRecordAggregateOps` would be
// vacuous — shrinking that slice would shrink the oracle with it, which is
// exactly how the four-op map survived the contract growing to fifteen.
func TestAggregates_EveryContractOpIsReachable(t *testing.T) {
	contractOps := contractAggregateOps(t)
	if len(contractOps) < 15 {
		t.Fatalf("read %d ops out of RecordAggregate.yaml; the contract declares fifteen, so the parse is wrong", len(contractOps))
	}
	for _, op := range contractOps {
		if _, ok := aggregateOpFor(op); !ok {
			t.Errorf("aggregate op %q is in the contract's enum and the importer cannot emit it", op)
		}
	}
	declared := map[string]bool{}
	for _, op := range contractOps {
		declared[op] = true
	}
	for _, op := range allRecordAggregateOps {
		if !declared[string(op)] {
			t.Errorf("the importer lists %q, which RecordAggregate.yaml does not declare", op)
		}
	}
	if len(allRecordAggregateOps) != len(contractOps) {
		t.Errorf("the importer lists %d ops and the contract declares %d", len(allRecordAggregateOps), len(contractOps))
	}
}

// contractAggregateOps reads the op enum straight out of the contract file.
func contractAggregateOps(t *testing.T) []string {
	t.Helper()
	path := filepath.Join("..", "..", "contracts", "components", "schemas", "RecordAggregate.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the contract at %s: %v", path, err)
	}
	var doc struct {
		Properties struct {
			Op struct {
				Enum []string `yaml:"enum"`
			} `yaml:"op"`
		} `yaml:"properties"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return doc.Properties.Op.Enum
}

func TestAggregates_ObsidianTitleCasedAverageTranslates(t *testing.T) {
	r := leafResolver{recordType: "deal", schemas: NewSchemaIndex(map[string][]InferredProperty{
		"deal": {{Name: "amount", Type: records.TypeDecimal}},
	})}
	nodes, losses := translateSummaries(map[string]any{"amount": "Average"}, r)
	if len(losses) > 0 {
		t.Fatalf("Obsidian's `Average` was dropped: %v", losses)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected one aggregate node, got %d", len(nodes))
	}
}

func TestAggregates_RefusalDoesNotQuoteTheRetractedRule(t *testing.T) {
	r := leafResolver{recordType: "deal", schemas: NewSchemaIndex(map[string][]InferredProperty{
		"deal": {{Name: "amount", Type: records.TypeDecimal}},
	})}
	_, losses := translateSummaries(map[string]any{"amount": "Frobnicate"}, r)
	if len(losses) != 1 {
		t.Fatalf("expected exactly one loss, got %v", losses)
	}
	if strings.Contains(losses[0], "no avg") || strings.Contains(losses[0], "only sum/min/max/count") {
		t.Fatalf("the refusal still quotes the rule RecordAggregate.yaml retracted: %s", losses[0])
	}
}

// ---------------------------------------------------------------------------
// Finding 21 — `--dry-run` validated against schemas it did not write, under a
// heading that said otherwise. On a vault with no control plane yet, the
// SchemaSet was empty and every note reported as "not a record".
// ---------------------------------------------------------------------------

func TestRun_DryRunValidatesAgainstTheSchemasItWouldHaveWritten(t *testing.T) {
	vault := t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "a.md"),
		[]byte("---\ntype: task\nstatus: open\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "b.md"),
		[]byte("---\ntype: task\nstatus: done\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := Run(vault, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Validation.RecognisedRecords != 2 {
		t.Fatalf("dry run recognised %d records of 2 — it validated against the EMPTY on-disk schema set, not the schemas it would have written",
			rep.Validation.RecognisedRecords)
	}
	if rep.Validation.ValidRecords != 2 {
		t.Fatalf("dry run reports %d valid of 2", rep.Validation.ValidRecords)
	}
	// And it must not have written anything.
	if _, err := os.Stat(records.SchemaDir(vault)); !os.IsNotExist(err) {
		t.Fatalf("a dry run created %s", records.SchemaDir(vault))
	}
}

// ---------------------------------------------------------------------------
// Finding 24 — isWikilink was a second oracle: it read a BLOCK scalar reading
// `[[Acme]]` as a wikilink, while records.parseLinkValue refuses one by name
// under FR-030a. The property was declared `relation` and then failed
// validation on every such note.
// ---------------------------------------------------------------------------

func TestInfer_BlockScalarIsNotAWikilink(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "companies/Acme.md", "---\ntype: company\n---\n"),
		noteOnDisk(t, dir, "deals/1.md", "---\ntype: deal\nparty: |\n  [[Acme]]\n---\n"),
		noteOnDisk(t, dir, "deals/2.md", "---\ntype: deal\nparty: |\n  [[Acme]]\n---\n"),
	}
	props := InferSchema(CollectTypeGroups(notes)["deal"], BuildNameIndex(notes))
	if props[0].Type == records.TypeRelation {
		t.Fatalf("a block scalar was inferred as a relation; records.parseLinkValue refuses one by name (FR-030a), so every one of those notes then fails validation")
	}
}

// TestInfer_BlockScalarAgreesWithTheValidator is the same fact stated as the
// invariant that matters: whatever the importer declares, every observed note
// must validate against it.
func TestInfer_BlockScalarAgreesWithTheValidator(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "companies/Acme.md", "---\ntype: company\n---\n"),
		noteOnDisk(t, dir, "deals/1.md", "---\ntype: deal\nparty: |\n  [[Acme]]\n---\n"),
	}
	props := InferSchema(CollectTypeGroups(notes)["deal"], BuildNameIndex(notes))
	set := loadedSchemaSet(t, map[string][]InferredProperty{"deal": props})
	rep := records.Validate(set, []records.Record{notes[1].Rec}, records.ValidateOptions{})
	for _, rr := range rep.Records {
		if !rr.Valid() {
			t.Fatalf("the importer manufactured a validation error for the note it inferred from: %+v", rr.Findings)
		}
	}
}

// ---------------------------------------------------------------------------
// Finding 14 (import half) — a base view's `limit:` was never read, so a
// limited view imported unlimited, CONVERTED, with zero losses.
// ---------------------------------------------------------------------------

func TestView_LimitIsNotSilentlyDropped(t *testing.T) {
	schemas := NewSchemaIndex(map[string][]InferredProperty{
		"task": {{Name: "status", Type: records.TypeEnum, EnumValues: []string{"done", "open"}}},
	})
	pb, err := ParseBaseFile([]byte(`
views:
  - type: table
    name: Top Five
    limit: 5
    filters:
      and:
        - type == "task"
        - status == "open"
`))
	if err != nil {
		t.Fatalf("parsing the base: %v", err)
	}
	out, produced := TranslateBase(pb, "T.base", schemas, NewSlugRegistry())
	vo := out.Views[0]
	if len(produced) != 1 {
		t.Fatalf("expected one produced view, got %d", len(produced))
	}
	if !strings.Contains(string(produced[0].Bytes), "limit: 5") {
		t.Fatalf("the view's `limit: 5` never reached the written file:\n%s", produced[0].Bytes)
	}
	if vo.Status != OutcomeConverted {
		t.Fatalf("carrying a limit faithfully is not a loss; status=%s losses=%v", vo.Status, vo.Losses)
	}
}

func TestView_UnreadableLimitIsANamedLoss(t *testing.T) {
	schemas := NewSchemaIndex(map[string][]InferredProperty{
		"task": {{Name: "status", Type: records.TypeEnum, EnumValues: []string{"done", "open"}}},
	})
	pb, err := ParseBaseFile([]byte(`
views:
  - type: table
    name: Odd
    limit: "lots"
    filters:
      and:
        - type == "task"
`))
	if err != nil {
		t.Fatalf("parsing the base: %v", err)
	}
	out, _ := TranslateBase(pb, "T.base", schemas, NewSlugRegistry())
	vo := out.Views[0]
	if len(vo.Losses) == 0 {
		t.Fatalf("an unreadable `limit:` was dropped in silence")
	}
	if !vo.Disabled {
		t.Fatalf("a dropped row-count bound lets the view return MORE rows than the original, so it must disable it: losses=%v", vo.Losses)
	}
}

// ---------------------------------------------------------------------------
// The guards that keep the fixes from eroding.
// ---------------------------------------------------------------------------

// TestInfer_EveryDeclaredPropertyValidatesEveryNoteItWasInferredFrom is the
// invariant behind findings 10 and 24, stated once: this importer must never
// write a schema that the very notes it read fail against on an ARITY or SHAPE
// ground it could have seen. A manufactured error is worse than a loose
// declaration, because the operator has no way to tell the two apart.
func TestInfer_EveryDeclaredPropertyValidatesEveryNoteItWasInferredFrom(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		// arity: three scalars against one list (finding 10)
		noteOnDisk(t, dir, "n1.md", "---\ntype: thing\ntags: a\n---\n"),
		noteOnDisk(t, dir, "n2.md", "---\ntype: thing\ntags: b\n---\n"),
		noteOnDisk(t, dir, "n3.md", "---\ntype: thing\ntags: c\n---\n"),
		// block scalar reading as a wikilink (finding 24)
		noteOnDisk(t, dir, "n4.md", "---\ntype: thing\ntags: d\nowner: |\n  [[Cara]]\n---\n"),
		noteOnDisk(t, dir, "n5.md", "---\ntype: thing\ntags: e\nowner: |\n  [[Cara]]\n---\n"),
		noteOnDisk(t, dir, "people/Cara.md", "---\ntype: person\nname: Cara\n---\n"),
		// An empty list is a LIST for arity purposes even though it holds no
		// VALUE. records.Validate's absence gate catches a missing key, an
		// explicit null and FR-007a's empty string — not an empty SEQUENCE —
		// so `labels: []` reaches the arity check. These four notes carry no
		// value evidence at all, so the property classifies as text; if the
		// arity evidence were gated on non-emptiness too, `many` would come
		// out false and every one of them would fail.
		noteOnDisk(t, dir, "n6.md", "---\ntype: other\nlabels: []\nname: a\n---\n"),
		noteOnDisk(t, dir, "n7.md", "---\ntype: other\nlabels: []\nname: b\n---\n"),
		noteOnDisk(t, dir, "n8.md", "---\ntype: other\nlabels: []\nname: c\n---\n"),
		noteOnDisk(t, dir, "n9.md", "---\ntype: other\nlabels: []\nname: d\n---\n"),
	}
	groups := CollectTypeGroups(notes)
	idx := BuildNameIndex(notes)
	inferred := map[string][]InferredProperty{}
	for typ, g := range groups {
		inferred[typ] = InferSchema(g, idx)
	}
	set := loadedSchemaSet(t, inferred)

	recs := make([]records.Record, 0, len(notes))
	for _, n := range notes {
		recs = append(recs, n.Rec)
	}
	rep := records.Validate(set, recs, records.ValidateOptions{})
	for _, rr := range rep.Records {
		for _, f := range rr.Findings {
			if f.Code == records.FindingArity || f.Code == records.FindingNotAWikilink {
				t.Errorf("%s: the importer manufactured a %s finding about a note it read: %s", rr.Path, f.Code, f.Reason)
			}
		}
	}
}

// TestRun_DryRunWritesNothingAnywhere pins the other half of finding 21's fix:
// the dry run now renders and loads real files, so it has to be provable that
// none of them lands in the operator's vault.
func TestRun_DryRunWritesNothingAnywhere(t *testing.T) {
	vault := t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "a.md"),
		[]byte("---\ntype: task\nstatus: open\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "T.base"), []byte(
		"views:\n  - type: table\n    name: All\n    filters:\n      and:\n        - type == \"task\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, vault)
	if _, err := Run(vault, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	after := treeSnapshot(t, vault)
	if len(before) != len(after) {
		t.Fatalf("a dry run changed the vault: before %v, after %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("a dry run changed the vault at %s (was %s)", after[i], before[i])
		}
	}
}

func treeSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// TestRun_DryRunAndRealRunAgreeOnEveryReportedNumber is the strongest form of
// finding 21's fix: a dry run is only useful if it PREDICTS the real run.
func TestRun_DryRunAndRealRunAgreeOnEveryReportedNumber(t *testing.T) {
	build := func(dir string) {
		mustWrite(t, filepath.Join(dir, "a.md"), "---\ntype: task\nstatus: open\ndue: 2026-01-01\n---\n")
		mustWrite(t, filepath.Join(dir, "b.md"), "---\ntype: task\nstatus: done\ndue: 2026-02-01\n---\n")
		mustWrite(t, filepath.Join(dir, "c.md"), "---\ntype: note\ntitle: x\n---\n")
		mustWrite(t, filepath.Join(dir, "T.base"),
			"views:\n  - type: table\n    name: Open\n    filters:\n      and:\n        - type == \"task\"\n        - status == \"open\"\n")
	}
	dryDir, realDir := t.TempDir(), t.TempDir()
	build(dryDir)
	build(realDir)

	dry, err := Run(dryDir, false)
	if err != nil {
		t.Fatalf("dry Run: %v", err)
	}
	live, err := Run(realDir, true)
	if err != nil {
		t.Fatalf("real Run: %v", err)
	}
	if dry.Validation.RecognisedRecords != live.Validation.RecognisedRecords ||
		dry.Validation.ValidRecords != live.Validation.ValidRecords ||
		dry.Validation.InvalidRecords != live.Validation.InvalidRecords ||
		dry.Validation.ErrorFindingCount != live.Validation.ErrorFindingCount {
		t.Fatalf("dry run and real run disagree:\n  dry  %+v\n  real %+v", dry.Validation, live.Validation)
	}
	if (dry.ViewReload == nil) != (live.ViewReload == nil) {
		t.Fatalf("view reload presence differs: dry=%v real=%v", dry.ViewReload != nil, live.ViewReload != nil)
	}
	if dry.ViewReload != nil && dry.ViewReload.OK() != live.ViewReload.OK() {
		t.Fatalf("view reload verdict differs: dry=%v real=%v", dry.ViewReload.OK(), live.ViewReload.OK())
	}
	if !dry.DryRun || live.DryRun {
		t.Fatalf("the report must say which run produced it: dry.DryRun=%v live.DryRun=%v", dry.DryRun, live.DryRun)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
