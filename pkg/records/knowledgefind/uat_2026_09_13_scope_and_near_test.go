// Omnipus — regression coverage for the 2026-09-13 UAT findings D-46 (#698:
// knowledge_find could not address two knowledge bases and rows carried no
// provenance) and D-58 (free-text `near` answered a confident zero instead
// of saying near takes a note reference).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestUAT_D46_ResponseAndEveryRowCarryTheCollection — provenance is stamped
// on the response, on each row, and rendered once in the header.
func TestUAT_D46_ResponseAndEveryRowCarryTheCollection(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "40.0")
	f.plant(2, "growing", "41.0")
	d := f.deps()
	d.CollectionName = "UAT Vault"

	resp := mustFind(t, d, req(withType("plant")))
	if resp.Collection == nil || *resp.Collection != "UAT Vault" {
		t.Fatalf("D-46: the response must name its collection, got %v", resp.Collection)
	}
	if len(resp.Rows) != 2 {
		t.Fatalf("expected two rows, got %s", Render(resp))
	}
	for _, row := range resp.Rows {
		if row.Collection == nil || *row.Collection != "UAT Vault" {
			t.Errorf("D-46: row %s must carry its collection, got %v", row.Path, row.Collection)
		}
	}
	if !strings.Contains(Render(resp), "COLLECTION: UAT Vault\n") {
		t.Errorf("the rendered header must name the collection once:\n%s", Render(resp))
	}

	// A refusal is stamped too: a reader with two vaults must know WHICH one
	// refused.
	refused, _ := Find(context.Background(), d, req(withType("no-such-type")))
	if !refused.Refused || refused.Collection == nil || *refused.Collection != "UAT Vault" {
		t.Errorf("a refusal must carry the collection as well, got refused=%v collection=%v",
			refused.Refused, refused.Collection)
	}

	// No name given: nothing invented.
	plain := mustFind(t, f.deps(), req(withType("plant")))
	if plain.Collection != nil || (len(plain.Rows) > 0 && plain.Rows[0].Collection != nil) {
		t.Errorf("with no CollectionName the fields must stay absent, got %v", plain.Collection)
	}
}

// TestUAT_D58_FreeTextNearIsRefusedByName — Q-12: `{near:"budget overrun"}`
// answered "COMPLETE: yes — 0 records matched".
func TestUAT_D58_FreeTextNearIsRefusedByName(t *testing.T) {
	f := gardenCorpus(t)
	d := f.deps()
	d.ResolveNear = func(string) (string, bool) { return "", false }

	resp, _ := Find(context.Background(), d, req(withType("plant"), withNear("budget overrun", 1)))
	if !resp.Refused {
		t.Fatalf("D-58: a near that names no note must be refused, not answered with zero:\n%s", Render(resp))
	}
	var found bool
	for _, p := range resp.Problems {
		if p.Code == generated.NearUnresolved && strings.Contains(p.Reason, "budget overrun") &&
			strings.Contains(p.Reason, "not a text search") && p.Fix != nil && strings.Contains(*p.Fix, "words") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the refusal must be near_unresolved, quote the argument and point at words, got %+v", resp.Problems)
	}

	// Control: a real record anchor still traverses.
	d.ResolveNear = func(near string) (string, bool) { return "garden/" + near + ".md", true }
	ok := mustFind(t, d, req(withType("plant"), withNear("Greenhouse", 1)))
	if ok.Refused || len(ok.Rows) == 0 {
		t.Fatalf("a resolvable near must still answer:\n%s", Render(ok))
	}

	// Control: with NO note resolver wired, absence cannot be proven, so the
	// zero-hit answer stands rather than a refusal the engine cannot back.
	d.ResolveNear = nil
	unwired := mustFind(t, d, req(withType("plant"), withNear("budget overrun", 1)))
	if unwired.Refused {
		t.Fatalf("with no note resolver the engine cannot prove absence and must not refuse:\n%s", Render(unwired))
	}
}
