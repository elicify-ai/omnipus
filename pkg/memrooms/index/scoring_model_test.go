// Package index — ADR-068 D21.1: memory-room recall must SCORE with BM25.
//
// Internal test (package index, not index_test) because it needs buildMapping
// to construct a PRE-FIX index by hand — one whose persisted mapping leaves
// ScoringModel empty, exactly as every room index written before D21.1 did.
//
// The oracle is bleve v2.6.1's two scoring functions, read out of the
// dependency rather than assumed. Both start from u = sqrt(freq) and the stored
// length norm n = 1/sqrt(fieldLength):
//
//	TF-IDF   score = idf · u · n             = idf · sqrt(freq/fieldLength)
//	BM25     score = idf · k1·u / (u + k1·K)   K = 1-b + b·fieldLength/avgFieldLength
//
// TF-IDF sees only term density, so a long memory that repeats a word hundreds
// of times beats a short one that mentions it twice. BM25's numerator saturates
// while its denominator keeps growing with raw length, and the order flips.
// That flip is what these tests assert — never the value of the setting.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"

	"github.com/elicify-ai/omnipus/pkg/memrooms"
)

const (
	// smTerm is an invented token: not an English stopword (the "en" analyzer
	// drops those) and carrying no suffix the Porter stemmer rewrites.
	smTerm   = "quorvex"
	smFiller = "padwordx"

	smRepeaterID = "mem-repeater"
	smConciseID  = "mem-concise"
)

// smBody builds a memory body of exactly length analysed tokens, freq of which
// are the query term. The two shapes are the same ones pkg/knowledge pins, and
// their measured scores under bleve v2.6.1 are:
//
//	           repeater (512 in 1,600)   concise (2 in 25)   winner
//	TF-IDF                    0.336320            0.168160   repeater
//	BM25                      0.006664            0.023866   concise
func smBody(freq, length int) string {
	return strings.TrimSpace(
		strings.Repeat(smTerm+" ", freq) + strings.Repeat(smFiller+" ", length-freq))
}

func smRepeaterBody() string { return smBody(512, 1600) }
func smConciseBody() string  { return smBody(2, 25) }

// smRoomWithCorpus creates a room holding the two discriminating memories.
// Titles deliberately avoid the query term: Search queries title, body and tags
// as a disjunction, and a title hit would add a signal the corpus is not
// trying to measure.
func smRoomWithCorpus(t *testing.T) memrooms.Room {
	t.Helper()
	root := t.TempDir()
	memoriesDir := filepath.Join(root, "memories")
	if err := os.MkdirAll(memoriesDir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", memoriesDir, err)
	}
	room := memrooms.Room{
		Root:         root,
		MemoriesDir:  memoriesDir,
		CountersPath: filepath.Join(root, "counters.jsonl"),
	}
	for id, body := range map[string]string{
		smRepeaterID: smRepeaterBody(),
		smConciseID:  smConciseBody(),
	} {
		mf := memrooms.MemoryFile{
			Frontmatter: memrooms.MemoryFrontmatter{
				ID:     id,
				Title:  "a note",
				Type:   memrooms.MemoryTypeFact,
				Status: memrooms.MemoryStatusActive,
				Author: "agent-test",
			},
			Body: body,
		}
		if err := memrooms.WriteMemoryFile(memoriesDir, mf); err != nil {
			t.Fatalf("write memory %s: %v", id, err)
		}
	}
	return room
}

// smSearchOrder opens the room's index and returns recall ids in rank order.
func smSearchOrder(t *testing.T, room memrooms.Room) []string {
	t.Helper()
	ri, err := OpenOrCreate(room)
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ri.Close(); closeErr != nil {
			t.Errorf("close room index: %v", closeErr)
		}
	})
	hits, err := ri.Search(smTerm, 10)
	if err != nil {
		t.Fatalf("Search(%q): %v", smTerm, err)
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.ID)
	}
	return out
}

// TestScoringModel_RoomRecallRanksWithBM25 is the assertion that dies if
// buildMapping stops asking for BM25. It drives the real OpenOrCreate/Search
// path over memories on disk and asks only which one came back first.
func TestScoringModel_RoomRecallRanksWithBM25(t *testing.T) {
	got := smSearchOrder(t, smRoomWithCorpus(t))
	if len(got) != 2 {
		t.Fatalf("Search(%q) = %v, want both memories", smTerm, got)
	}
	if got[0] != smConciseID {
		t.Errorf("Search(%q) ranked %v.\n"+
			"Want %q first: BM25 saturates term frequency and normalises length linearly, so a "+
			"1,600-token memory repeating the term 512 times cannot outrank a 25-token one that "+
			"mentions it twice.\n"+
			"Getting %q first is the signature of TF-IDF — bleve's DEFAULT, which applies whenever "+
			"the mapping persisted in the index leaves ScoringModel unset (ADR-068 D21.1).",
			smTerm, got, smConciseID, smRepeaterID)
	}
}

// TestScoringModel_StaleRoomIndexIsRebuilt pins the half of D21.1 that a
// ranking test alone cannot see. bleve.OpenUsing takes no mapping argument and
// resolves the scoring model from the mapping stored INSIDE the index, so a
// room indexed before this fix would rank with TF-IDF forever however the code
// is compiled — no error, no empty result, nothing to notice. This package had
// no mapping guard of any kind, so without the open-time check the fix would
// have reached only rooms created after it shipped.
func TestScoringModel_StaleRoomIndexIsRebuilt(t *testing.T) {
	room := smRoomWithCorpus(t)

	// Build a pre-fix index by hand at the exact path OpenOrCreate will use:
	// the production mapping with ScoringModel left empty, which is precisely
	// what shipped before ADR-068 D21.1.
	idxPath := filepath.Join(room.Root, IndexSubdir)
	if err := os.MkdirAll(filepath.Dir(idxPath), 0o700); err != nil {
		t.Fatalf("mkdir index parent: %v", err)
	}
	stale := buildMapping()
	stale.ScoringModel = ""
	staleIdx, err := bleve.NewUsing(idxPath, stale, scorch.Name, scorch.Name, scorchOpenConfig())
	if err != nil {
		t.Fatalf("create stale index: %v", err)
	}
	if err := staleIdx.Close(); err != nil {
		t.Fatalf("close stale index: %v", err)
	}

	got := smSearchOrder(t, room)
	if len(got) != 2 {
		t.Fatalf("Search(%q) = %v, want both memories after the rebuild", smTerm, got)
	}
	if got[0] != smConciseID {
		t.Errorf("after opening a room whose index was written with a TF-IDF mapping, Search(%q) "+
			"ranked %v — still TF-IDF order.\n"+
			"openOrCreateAt must detect that the persisted mapping's scoring model differs from the "+
			"one buildMapping declares and recreate the index; otherwise every room that existed "+
			"before ADR-068 D21.1 keeps scoring TF-IDF with nothing to indicate it.",
			smTerm, got)
	}
}

// TestScoringModel_RebuildRecreatesTheMapping pins the second silent no-op
// found alongside the first: Rebuild() used to re-add every document to the
// ALREADY-OPEN index. A bleve mapping is fixed at creation, so that could never
// pick up a mapping change — an explicit rebuild, asked for precisely because
// something about the index was wrong, would reinstate the wrong mapping and
// report success.
//
// The test opens a room, forces the handle onto a TF-IDF index underneath it,
// calls Rebuild, and requires BM25 order afterwards.
func TestScoringModel_RebuildRecreatesTheMapping(t *testing.T) {
	room := smRoomWithCorpus(t)
	ri, err := OpenOrCreate(room)
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ri.Close(); closeErr != nil {
			t.Errorf("close room index: %v", closeErr)
		}
	})

	// Swap in a TF-IDF index under the same path, as if this handle had been
	// opened before D21.1. Rebuild must not merely repopulate it.
	if err := ri.idx.Close(); err != nil {
		t.Fatalf("close live index: %v", err)
	}
	if err := os.RemoveAll(ri.idxPath); err != nil {
		t.Fatalf("remove live index: %v", err)
	}
	stale := buildMapping()
	stale.ScoringModel = ""
	staleIdx, err := bleve.NewUsing(ri.idxPath, stale, scorch.Name, scorch.Name, scorchOpenConfig())
	if err != nil {
		t.Fatalf("create stale index: %v", err)
	}
	ri.idx = staleIdx

	if err := ri.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	hits, err := ri.Search(smTerm, 10)
	if err != nil {
		t.Fatalf("Search after Rebuild: %v", err)
	}
	got := make([]string, 0, len(hits))
	for _, h := range hits {
		got = append(got, h.ID)
	}
	if len(got) != 2 {
		t.Fatalf("Search(%q) after Rebuild = %v, want both memories", smTerm, got)
	}
	if got[0] != smConciseID {
		t.Errorf("Rebuild() left the index ranking in TF-IDF order (%v). A rebuild must recreate "+
			"the index DIRECTORY, not re-add documents to the already-open one: bleve fixes the "+
			"mapping at creation, so repopulating in place cannot change the scoring model, the "+
			"analyzers or the field set that made the rebuild necessary.", got)
	}
}
