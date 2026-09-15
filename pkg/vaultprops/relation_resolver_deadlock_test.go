// Omnipus — CONFIRMED #8: resolving a relation from inside a candidate or
// relation stream must not deadlock the store.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// knowledge_find hands ONE open store to two collaborators at once
// (find_tool.go's buildDeps): Deps.Store, which streams candidates and
// relations, and the RelationResolver, which answers "what record does this
// wikilink name?" by asking the SAME store for a point lookup. The resolver is
// called from INSIDE the stream — by the comparator on a relation-valued
// filter or group_by, and by the near/hops graph builder on every edge.
//
// The store opened its database with a pool of exactly one connection, and a
// stream holds that connection for as long as the caller is visiting rows. So
// the nested lookup asked database/sql for a connection that could only be
// returned by the stream that was waiting for the nested lookup. database/sql
// does not detect that; it blocks until the context is done. In production the
// context is the tool call's, so the turn hung with no error, no partial
// result and no log line.
//
// EVERY EXISTING TEST WIRED A STUB RESOLVER, so nothing in the suite ever put
// the same store on both sides of that seam. These tests do, against a real
// on-disk index, and each carries its OWN deadline so a regression FAILS in a
// few seconds instead of hanging the package.
// ---------------------------------------------------------------------------

// deadlockBudget is how long a nested lookup gets before the test calls it a
// deadlock. The work involved is two point queries against a three-note SQLite
// file — microseconds — so seconds of headroom cannot produce a false red on a
// loaded machine, and a real deadlock never finishes at any budget.
const deadlockBudget = 20 * time.Second

const dealSchema = "schema_version: 1\n" +
	"type: deal\n" +
	"properties:\n" +
	"  company: { type: relation, to: company }\n"

func dealNote(id, company string) string {
	return "---\ntype: deal\nid: " + id + "\ncompany: \"[[" + company + "]]\"\n---\n# " + id + "\n"
}

// relationVault is the smallest corpus that exercises the seam.
//
// TWO deals, not one, and that is not padding. database/sql releases a
// connection the moment a result set is exhausted, and streamCandidates flushes
// its LAST record after the loop has ended — so a one-record answer resolves
// its relation with the connection already back in the pool and hides the
// defect completely. The second record is what forces a flush (and therefore a
// nested lookup) from INSIDE the loop, while the stream still holds the
// connection. The two companies are distinct so the resolver's memo cannot
// serve the second from the first.
func relationVault(t *testing.T) (home, root string) {
	t.Helper()
	home = syncHome(t)
	root = syncVault(t, map[string]string{
		".omnipus-vault/records/deal.yaml":    dealSchema,
		".omnipus-vault/records/company.yaml": companySchemaV1,
		"Deals/Big deal.md":                   dealNote("DE-0001", "Acme"),
		"Deals/Small deal.md":                 dealNote("DE-0002", "Beta"),
		"Companies/Acme.md":                   companyNote,
		"Companies/Beta.md":                   companyNote,
	})
	if _, err := Sync(context.Background(), home, root, SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return home, root
}

// resolverOver builds the production RelationResolver over the SAME store the
// caller is about to stream from — the exact wiring find_tool.go's buildDeps
// performs.
func resolverOver(t *testing.T, ctx context.Context, root string, store propindex.Store) *RelationResolver {
	t.Helper()
	croot, err := knowledge.NewCollectionRoot(knowledge.OSLinkFS(), root)
	if err != nil {
		t.Fatalf("NewCollectionRoot: %v", err)
	}
	walk, err := knowledge.WalkContained(knowledge.OSLinkFS(), croot)
	if err != nil {
		t.Fatalf("WalkContained: %v", err)
	}
	return NewRelationResolver(ctx, knowledge.NewNoteIndex(walk.Files), store)
}

// withDeadlockBudget runs body on its own goroutine and fails the test if it
// has not returned within deadlockBudget.
//
// The goroutine is deliberately NOT joined on failure: a deadlocked
// database/sql wait only ends when its context does, and the whole point of
// this harness is that the test reports rather than hangs. The context handed
// to body is cancelled on the way out so the leaked goroutine unblocks and the
// process can still exit.
func withDeadlockBudget(t *testing.T, what string, body func(ctx context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadlockBudget)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- body(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-ctx.Done():
		t.Fatalf("%s did not finish within %s: the store deadlocked — a nested read issued from "+
			"inside a stream is waiting for the connection that stream is holding", what, deadlockBudget)
	}
}

// TestRelationResolver_ResolvingInsideACandidateStreamDoesNotDeadlock is the
// relation-valued filter / group_by path: the comparator calls the resolver
// from inside Candidates' own visit callback.
func TestRelationResolver_ResolvingInsideACandidateStreamDoesNotDeadlock(t *testing.T) {
	skipWithoutSQLite(t)

	home, root := relationVault(t)
	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()

	withDeadlockBudget(t, "a relation resolved from inside Candidates", func(ctx context.Context) error {
		resolve := resolverOver(t, ctx, root, store)
		var resolved int
		err := store.Candidates(ctx, propindex.Selector{RecordType: "deal"},
			func(c propindex.Candidate) (propindex.Verdict, error) {
				p, ok := c.Prop("company")
				if !ok || len(p.Elems) == 0 {
					return propindex.Rejected, nil
				}
				link, lok := records.ParseWikilink(p.Elems[0].Raw)
				if !lok {
					return propindex.Rejected, nil
				}
				if _, hit := resolve.Resolve(link); hit {
					resolved++
				}
				return propindex.Accepted, nil
			})
		if err != nil {
			return err
		}
		if resolved != 2 {
			t.Errorf("expected both deals' `company` relations to resolve to a record, resolved %d", resolved)
		}
		return nil
	})
}

// TestRelationResolver_ResolvingInsideARelationStreamDoesNotDeadlock is the
// near/hops path: buildRelationGraph opens Relations and resolves each edge
// from inside that stream's callback.
func TestRelationResolver_ResolvingInsideARelationStreamDoesNotDeadlock(t *testing.T) {
	skipWithoutSQLite(t)

	home, root := relationVault(t)
	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()

	withDeadlockBudget(t, "a relation resolved from inside Relations", func(ctx context.Context) error {
		resolve := resolverOver(t, ctx, root, store)
		var edges, resolved int
		err := store.Relations(ctx, propindex.Selector{}, func(h propindex.RelationHit) error {
			edges++
			link, lok := records.ParseWikilink(h.Relation.Raw)
			if !lok {
				return nil
			}
			if _, hit := resolve.Resolve(link); hit {
				resolved++
			}
			return nil
		})
		if err != nil {
			return err
		}
		if edges == 0 {
			t.Error("the fixture produced no relation edges; nothing above would mean anything")
		}
		if resolved != edges {
			t.Errorf("expected every edge to resolve, resolved %d of %d", resolved, edges)
		}
		return nil
	})
}
