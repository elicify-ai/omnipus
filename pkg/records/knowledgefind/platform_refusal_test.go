// Omnipus — ADR-068 D16.2a / spec FR-020h, AC-F6: on a build with no properties
// index, knowledge_find refuses BY NAME and never returns an empty success.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// NO BUILD TAG, deliberately. This file must compile and run under BOTH tag
// sets: under `records_no_sqlite` it is the assertion, and on an ordinary build
// records.AssertRefusesWhenIndexUnavailable is a no-op — asserting the opposite
// contract there would be asserting that a working feature does not work.
//
//	go test -tags goolm,stdjson,records_no_sqlite ./pkg/records/...

package knowledgefind

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// refusalSchemaYAML is declared here rather than shared with fixture_test.go
// because that file is SQLite-tagged and this one is not.
const refusalSchemaYAML = `
schema_version: 1
type: plant
label: Plant
properties:
  species:   { type: text }
  condition: { type: enum, values: [seedling, growing, dormant] }
  height_cm: { type: decimal }
  bed:       { type: relation, to: bed }
`

func refusalSchemas(t *testing.T) *records.SchemaSet {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plant.yaml"), []byte(refusalSchemaYAML), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("fixture schema rejected: %v", report.Rejections)
	}
	return set
}

// silentText satisfies the text index without needing one. On a SQLite-less
// build the point is that the PLATFORM gate fires first — before any store is
// touched — so what the text index would have said is irrelevant.
type silentText struct{}

func (silentText) Search(context.Context, string, int) ([]TextHit, error) { return nil, nil }
func (silentText) SourceHash(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (silentText) NearestTerms(context.Context, string, int) ([]generated.VaultTermCount, error) {
	return nil, nil
}

func refusalDeps(t *testing.T) Deps {
	t.Helper()
	// Store is deliberately nil: on a build without SQLite there IS no store to
	// open, and the gate must fire before anything reaches for one.
	return Deps{Schemas: refusalSchemas(t), Store: nil, Text: silentText{}, Epoch: 1}
}

// The four entry points ADR-068 D15 names for knowledge_find. Each costs one test,
// and each asserts the REAL call — not a re-derivation of what "refuses by name"
// means, which is the whole reason records.AssertRefusesWhenIndexUnavailable
// exists as a shared contract rather than as a paragraph.
//
// What this catches that no static check can: a knowledge_find that queries bleve,
// finds no typed candidates because there is no properties index, and reports
// "no records matched". That answer is indistinguishable from an empty vault,
// and it is the confidently-wrong answer this whole ADR was written to remove.

func TestKnowledgeFind_TypedFilter_RefusesOnSQLiteLessBuild(t *testing.T) {
	d := refusalDeps(t)
	records.AssertRefusesWhenIndexUnavailable(t, records.CapabilityTypedFilter,
		func() (generated.VaultFindResponse, error) {
			return Find(context.Background(), d, req(
				withType("plant"),
				withFilter(leaf("condition", "=", "growing")),
			))
		})
}

func TestKnowledgeFind_RelationJoin_RefusesOnSQLiteLessBuild(t *testing.T) {
	d := refusalDeps(t)
	records.AssertRefusesWhenIndexUnavailable(t, records.CapabilityRelationJoin,
		func() (generated.VaultFindResponse, error) {
			join := []string{"bed"}
			r := req(withType("plant"))
			r.Join = &join
			return Find(context.Background(), d, r)
		})
}

func TestKnowledgeFind_Grouping_RefusesOnSQLiteLessBuild(t *testing.T) {
	d := refusalDeps(t)
	records.AssertRefusesWhenIndexUnavailable(t, records.CapabilityGrouping,
		func() (generated.VaultFindResponse, error) {
			group := []string{"condition"}
			r := req(withType("plant"))
			r.GroupBy = &group
			return Find(context.Background(), d, r)
		})
}

func TestKnowledgeFind_Aggregation_RefusesOnSQLiteLessBuild(t *testing.T) {
	d := refusalDeps(t)
	records.AssertRefusesWhenIndexUnavailable(t, records.CapabilityAggregation,
		func() (generated.VaultFindResponse, error) {
			aggs := []generated.VaultFindAggregate{{Op: "sum", Property: strPtr("height_cm")}}
			r := req(withType("plant"))
			r.Aggregate = &aggs
			return Find(context.Background(), d, r)
		})
}

// TestKnowledgeFind_RefusalIsRenderedForTheModel checks the OTHER half of the
// contract, which the shared assertion cannot see: the refusal has to arrive as
// text the model can act on, not only as a Go error a caller logs.
//
// It runs on every build. On a SQLite-capable one there is no platform refusal
// to render, so it exercises the shape through a refusal that always exists.
func TestKnowledgeFind_RefusalIsRenderedForTheModel(t *testing.T) {
	d := refusalDeps(t)
	out, err := Call(context.Background(), d, []byte(`{"type":"plant","nonesuch":1}`))
	if err == nil {
		t.Fatalf("an undeclared argument was accepted")
	}
	for _, want := range []string{"COMPLETE: no", "REFUSED", "PROBLEMS", "NEXT"} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered refusal is missing %q:\n%s", want, out)
		}
	}
}
