// Package index provides a bleve scorch full-text-search index over the per-room
// memory .md files (FR-7.4).
//
// Design decisions:
//   - Index type: scorch (pure-Go). The CGo leveldb/rocksdb backends are
//     NEVER used — this package ONLY calls bleve.NewUsing with scorch.Name and
//     an empty kvstore string (scorch handles its own storage).
//   - Index location: <room_root>/.index/bleve/ — DERIVED, rebuildable from
//     the .md sources at any time. Never committed to version control.
//   - Concurrency: bleve's scorch engine is internally goroutine-safe for
//     concurrent reads + single writer. The caller wraps the index writer in
//     the same shard mutex used for .md file writes so that a rebuild from
//     .md can never race with a concurrent Index() call.
//   - Rebuild-on-corruption: if OpenOrCreate fails (corrupt index) it removes
//     the index dir and rebuilds from the .md source files in MemoriesDir.
//   - Documents: one bleve document per memory, using the memory ID as the
//     document ID. Fields indexed: title (keyword+text), body (text), tags (text),
//     type (keyword), status (keyword), author (keyword).
//   - Recall: BM25 ranking. bleve's DEFAULT scorer is TF-IDF, not BM25
//     (`DefaultScoringModel = TFIDFScoring`), so buildMapping sets
//     ScoringModel to BM25 explicitly — see ADR-068 D21.1, which found this
//     package scoring TF-IDF while this comment claimed otherwise. Top-N
//     results returned as []SearchHit with (ID, Score).
//
// This package does NOT import pkg/agent or pkg/tools — it only knows about
// pkg/memrooms types.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package index

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	bleveMapping "github.com/blevesearch/bleve/v2/mapping"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"
	bleveIndexAPI "github.com/blevesearch/bleve_index_api"

	"github.com/elicify-ai/omnipus/pkg/memrooms"
)

const (
	// IndexSubdir is the relative path under a room root where the bleve index lives.
	// This is DERIVED — it is rebuilt from .md sources when missing or corrupt.
	IndexSubdir = ".index/bleve"

	// fieldTitle is the document field for the memory title (text + keyword).
	fieldTitle = "title"
	// fieldBody is the document field for the memory body text.
	fieldBody = "body"
	// fieldTags is the document field for the tag list (joined with space).
	fieldTags = "tags"
	// fieldType is the document field for the memory type enum.
	fieldType = "type"
	// fieldStatus is the document field for the lifecycle status.
	fieldStatus = "status"
	// fieldAuthor is the document field for the agent ID.
	fieldAuthor = "author"
)

// SearchHit is a single recall result from the bleve index.
type SearchHit struct {
	// ID is the memory ID (filename without .md extension).
	ID string
	// Score is the BM25 relevance score (higher = more relevant). BM25 is in
	// force only because buildMapping asks for it by name; see ADR-068 D21.1.
	Score float64
}

// boltOpenTimeout bounds how long a single scorch bbolt open will wait for the
// process-exclusive root.bolt file lock before returning an error. The shared
// registry (registry.go) guarantees we normally open each index exactly once, so
// this is defense-in-depth: a genuinely contended or stale lock must surface as
// an ERROR within a few seconds rather than hanging a single-binary process
// forever. Passed to bleve.OpenUsing via the scorch "bolt_timeout" config key.
const boltOpenTimeout = "5s"

// RoomIndex holds an open bleve scorch index for a single room.
//
// It is safe for concurrent reads; writes are serialized by the internal mu.
// A single *RoomIndex is SHARED process-wide across every caller that names the
// same on-disk index path (see registry.go) — the underlying bbolt handle is
// opened once and closed only when the last holder releases it. Sharing is safe
// because scorch is internally goroutine-safe for concurrent reads + a single
// serialized writer (mu).
type RoomIndex struct {
	// handleMu guards the idx POINTER, not the index's own concurrency (scorch
	// handles that itself). Rebuild replaces the handle — it must recreate the
	// index directory, because the mapping is fixed at creation and re-adding
	// documents to an already-open index cannot change it — so the pointer is
	// no longer write-once and the lock-free reads in Search/DocCount would be
	// a data race. Readers take RLock for the duration of their call, which
	// also closes the pre-existing use-after-Close window on the same field.
	handleMu    sync.RWMutex
	idx         bleve.Index
	idxPath     string
	memoriesDir string
	mu          sync.Mutex // serializes Index() / Rebuild() calls

	// regKey is the registry key (absolute, cleaned index path) under which this
	// handle is shared. Set by the registry on first acquire; used by Close() to
	// release this holder's reference. Empty for a handle not registry-managed.
	regKey string
}

// OpenOrCreate returns the shared scorch index at <room.Root>/.index/bleve/,
// opening or creating it on the first acquire and reusing the already-open
// handle on every subsequent acquire of the same absolute path. If the index is
// corrupt (open fails after the creation check), the index directory is removed
// and rebuilt from the .md sources in room.MemoriesDir.
//
// Because the underlying scorch index is backed by a process-exclusive bbolt
// file lock, the returned *RoomIndex is SHARED process-wide and reference
// counted by the registry: opening the same path twice never performs a second
// bbolt open (which would deadlock). The caller MUST call Close() exactly once
// when done — Close releases this caller's reference and only physically closes
// the handle when the last holder releases it.
func OpenOrCreate(room memrooms.Room) (*RoomIndex, error) {
	idxPath := filepath.Join(room.Root, IndexSubdir)
	key := registryKey(idxPath)

	return acquireShared(key, func() (*RoomIndex, error) {
		idx, err := openOrCreateAt(idxPath)
		if err != nil {
			// Corrupt or unreadable — wipe and rebuild.
			slog.Warn("memrooms/index: index open failed; removing and rebuilding",
				"path", idxPath, "error", err)
			if removeErr := os.RemoveAll(idxPath); removeErr != nil {
				return nil, fmt.Errorf("memrooms/index: remove corrupt index %s: %w", idxPath, removeErr)
			}
			idx, err = openOrCreateAt(idxPath)
			if err != nil {
				return nil, fmt.Errorf("memrooms/index: create fresh index %s: %w", idxPath, err)
			}
		}

		ri := &RoomIndex{idx: idx, idxPath: idxPath, memoriesDir: room.MemoriesDir}

		// Rebuild index content from .md sources if the index is empty.
		// This handles the case where the index dir was deleted externally.
		count, cntErr := ri.idx.DocCount()
		if cntErr != nil || count == 0 {
			if rebuildErr := ri.rebuildLocked(); rebuildErr != nil {
				slog.Warn("memrooms/index: initial rebuild failed",
					"path", idxPath, "error", rebuildErr)
				// Non-fatal: the index is open but empty; recall will return no results
				// until the next write.
			}
		}

		return ri, nil
	})
}

// scorchOpenConfig is the kvconfig handed to scorch on create/open. The
// "bolt_timeout" key bounds how long the root.bolt file-lock acquisition will
// block before returning an error (defense-in-depth against a contended or
// stale process-exclusive lock — see boltOpenTimeout). scorch parses it as a
// time.Duration string.
func scorchOpenConfig() map[string]any {
	return map[string]any{"bolt_timeout": boltOpenTimeout}
}

// openOrCreateAt opens a scorch index at path if it exists, or creates one.
//
// Both paths pass a bounded bolt_timeout so a process-exclusive root.bolt lock
// that is already held (which would otherwise block forever on bbolt's
// infinite-wait flock) instead returns an error within boltOpenTimeout. In
// normal operation the shared registry ensures we only open each path once, so
// the timeout never fires — it exists purely so a hung index open is impossible
// in this single-binary process.
func openOrCreateAt(idxPath string) (bleve.Index, error) {
	if _, statErr := os.Stat(idxPath); os.IsNotExist(statErr) {
		// Does not exist — create a new scorch index.
		if mkErr := os.MkdirAll(filepath.Dir(idxPath), 0o700); mkErr != nil {
			return nil, fmt.Errorf("create parent dir: %w", mkErr)
		}
		return bleve.NewUsing(idxPath, buildMapping(), scorch.Name, scorch.Name, scorchOpenConfig())
	}
	// Exists — open it with a bounded bbolt-lock timeout.
	idx, err := bleve.OpenUsing(idxPath, scorchOpenConfig())
	if err != nil {
		return nil, err
	}
	// The mapping written at creation is authoritative for the life of the
	// index: bleve.OpenUsing takes no mapping argument, and bleve resolves the
	// scoring model from that persisted object rather than from the mapping
	// this code builds now. An index created before ADR-068 D21.1 therefore
	// goes on scoring TF-IDF forever, with no error and no empty result to
	// notice. Nothing else in this package looks for that, so without this
	// check the D21.1 fix would apply only to rooms created after it shipped.
	if drift := mappingScoringDrift(idx.Mapping()); drift != "" {
		slog.Warn("memrooms/index: index on disk was built with a different scoring model; rebuilding",
			"path", idxPath, "reason", drift)
		if closeErr := idx.Close(); closeErr != nil {
			return nil, fmt.Errorf("memrooms/index: close stale-scoring index %s: %w", idxPath, closeErr)
		}
		if rmErr := os.RemoveAll(idxPath); rmErr != nil {
			return nil, fmt.Errorf("memrooms/index: remove stale-scoring index %s: %w", idxPath, rmErr)
		}
		return bleve.NewUsing(idxPath, buildMapping(), scorch.Name, scorch.Name, scorchOpenConfig())
	}
	return idx, nil
}

// mappingScoringDrift reports, in a sentence, why the scoring model persisted
// in an index differs from the one buildMapping now declares — or "" when they
// agree.
//
// It compares the EFFECTIVE model rather than the raw string, because bleve
// treats the empty string as DefaultScoringModel: an index written with "" and
// code declaring "tf-idf" rank identically and must not be reported as drift.
// A guard that fires on a difference that does not exist is a guard somebody
// switches off.
func mappingScoringDrift(persisted bleveMapping.IndexMapping) string {
	got, ok := persisted.(*bleveMapping.IndexMappingImpl)
	if !ok {
		// Say so rather than reporting a clean comparison that was not made.
		slog.Warn("memrooms/index: persisted mapping is not an IndexMappingImpl; "+
			"scoring-model drift cannot be checked",
			"type", fmt.Sprintf("%T", persisted))
		return ""
	}
	gotModel, wantModel := effectiveScoringModel(got), effectiveScoringModel(buildMapping())
	if gotModel == wantModel {
		return ""
	}
	return fmt.Sprintf("the persisted mapping scores with %q and the code declares %q", gotModel, wantModel)
}

// effectiveScoringModel resolves an index mapping's scoring model the way bleve
// does: anything that is not exactly "bm25" — the empty string included — falls
// through to index.DefaultScoringModel, which is TF-IDF.
func effectiveScoringModel(m *bleveMapping.IndexMappingImpl) string {
	if m == nil || m.ScoringModel == "" {
		return bleveIndexAPI.DefaultScoringModel
	}
	return m.ScoringModel
}

// buildMapping returns the bleve IndexMapping for memory documents.
// Text fields use the default "en" analyzer (Porter stemmer + stopwords).
// Keyword fields use keyword analyzer (exact match, no stemming).
//
// ScoringModel is set EXPLICITLY (ADR-068 D21.1). Leaving it empty selects
// bleve's default, which is TF-IDF, not BM25 — this package ranked memory
// recall with TF-IDF for its whole life while five comments in this file said
// BM25. The scoring model is a property of the mapping PERSISTED in the index,
// so it is also what mappingScoringDrift compares at open: an index created
// before this line keeps scoring TF-IDF no matter how the code is compiled.
func buildMapping() *bleveMapping.IndexMappingImpl {
	m := bleve.NewIndexMapping()
	m.ScoringModel = bleveIndexAPI.BM25Scoring

	textField := bleve.NewTextFieldMapping()
	textField.Analyzer = "en"

	keywordField := bleve.NewTextFieldMapping()
	keywordField.Analyzer = "keyword"

	docMapping := bleve.NewDocumentMapping()
	docMapping.AddFieldMappingsAt(fieldTitle, textField)
	docMapping.AddFieldMappingsAt(fieldBody, textField)
	docMapping.AddFieldMappingsAt(fieldTags, textField)
	docMapping.AddFieldMappingsAt(fieldType, keywordField)
	docMapping.AddFieldMappingsAt(fieldStatus, keywordField)
	docMapping.AddFieldMappingsAt(fieldAuthor, keywordField)

	m.DefaultMapping = docMapping
	return m
}

// memoryDoc is the struct indexed into bleve for each memory file.
type memoryDoc struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	Tags   string `json:"tags"` // space-joined for text indexing
	Type   string `json:"type"`
	Status string `json:"status"`
	Author string `json:"author"`
}

// Index adds or updates a memory in the bleve index.
// Uses the memory ID as the bleve document ID (stable across updates).
// Safe to call concurrently — serialized by the internal mu lock.
func (ri *RoomIndex) Index(mf memrooms.MemoryFile) error {
	ri.mu.Lock()
	defer ri.mu.Unlock()
	return ri.indexLocked(mf)
}

func (ri *RoomIndex) indexLocked(mf memrooms.MemoryFile) error {
	doc := memoryDoc{
		Title:  mf.Frontmatter.Title,
		Body:   mf.Body,
		Tags:   joinTags(mf.Frontmatter.Tags),
		Type:   string(mf.Frontmatter.Type),
		Status: string(mf.Frontmatter.Status),
		Author: mf.Frontmatter.Author,
	}
	if err := ri.idx.Index(mf.Frontmatter.ID, doc); err != nil {
		return fmt.Errorf("memrooms/index: index document %s: %w", mf.Frontmatter.ID, err)
	}
	return nil
}

// Delete removes a memory from the bleve index by ID.
// Safe to call concurrently — serialized by the internal mu lock.
func (ri *RoomIndex) Delete(id string) error {
	ri.mu.Lock()
	defer ri.mu.Unlock()
	if err := ri.idx.Delete(id); err != nil {
		return fmt.Errorf("memrooms/index: delete document %s: %w", id, err)
	}
	return nil
}

// Search executes a full-text query against the index, scored with BM25 as
// requested by buildMapping (bleve's own default is TF-IDF — ADR-068 D21.1).
// Returns up to limit results ordered by descending score.
// When query is empty, returns all documents (unranked, up to limit).
// Safe for concurrent reads.
func (ri *RoomIndex) Search(query string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	var req *bleve.SearchRequest
	if query == "" {
		// Return all documents (match all).
		mq := bleve.NewMatchAllQuery()
		req = bleve.NewSearchRequestOptions(mq, limit, 0, false)
	} else {
		// Query the text fields explicitly (title, body, tags).
		// We build a disjunction of per-field match queries so that the
		// "en" analyzer mapping is honored for each field.  A plain
		// bleve.NewMatchQuery targets the _all composite field whose
		// analyzer does not match the field-level "en" mapping, producing
		// zero hits even when terms are present.
		mqs := make([]bleveQuery.Query, 0, 3)
		for _, field := range []string{fieldTitle, fieldBody, fieldTags} {
			mq := bleveQuery.NewMatchQuery(query)
			mq.SetField(field)
			mqs = append(mqs, mq)
		}
		dq := bleve.NewDisjunctionQuery(mqs...)
		req = bleve.NewSearchRequestOptions(dq, limit, 0, false)
	}
	req.Fields = []string{} // scores only — we fetch full content from .md

	// RLock for the whole call: Rebuild swaps the handle and closes the old one,
	// so reading the pointer and then using it without the lock would race with
	// a rebuild and could search an index that has just been closed.
	ri.handleMu.RLock()
	res, err := ri.idx.Search(req)
	ri.handleMu.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("memrooms/index: search %q: %w", query, err)
	}

	hits := make([]SearchHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		hits = append(hits, SearchHit{ID: h.ID, Score: h.Score})
	}
	return hits, nil
}

// Rebuild re-populates the bleve index from the room's .md files, recreating
// the index DIRECTORY first if — and only if — the mapping it was built with is
// no longer the mapping buildMapping declares.
//
// That condition is the whole subtlety. bleve fixes a mapping at creation and
// bleve.OpenUsing takes no mapping argument, so re-adding documents to an
// already-open index — which is all this method used to do — cannot change the
// scoring model, the analyzers or the field set. A rebuild asked for precisely
// because something about the index was wrong would reinstate the wrong thing
// and report success.
//
// It is conditional rather than unconditional because of who calls it:
// syncRoomToDiskLocked (pkg/agent/memory.go) calls Rebuild on every change to
// the memories directory, which is a hot path reached from recall. Tearing down
// the bbolt handle and recreating the directory every time would add a file-lock
// acquisition and a fresh index creation to an operation that is already a full
// re-index, and — worse — a recreate that failed halfway would leave the handle
// CLOSED, turning a stale index into a dead one on a path that currently
// degrades gracefully. Comparing the mapping first costs one in-memory string
// comparison and keeps the common case exactly as cheap as it was.
//
// Safe to call concurrently — serialized by mu.
func (ri *RoomIndex) Rebuild() error {
	ri.mu.Lock()
	defer ri.mu.Unlock()

	drift := mappingScoringDrift(ri.idx.Mapping())
	if drift == "" {
		return ri.rebuildLocked()
	}
	if ri.idxPath == "" {
		// Nothing to recreate from. Repopulate and say plainly that the mapping
		// is NOT refreshed, rather than reporting a full rebuild that did not
		// happen — the silent no-op this method exists to stop having.
		slog.Warn("memrooms/index: index mapping is stale but its path is unknown; "+
			"rebuilding contents only, ranking will not change",
			"memories_dir", ri.memoriesDir, "reason", drift)
		return ri.rebuildLocked()
	}

	slog.Warn("memrooms/index: index mapping is stale; recreating the index rather than repopulating it",
		"path", ri.idxPath, "reason", drift)

	ri.handleMu.Lock()
	defer ri.handleMu.Unlock()

	if err := ri.idx.Close(); err != nil {
		return fmt.Errorf("memrooms/index: close index before rebuild %s: %w", ri.idxPath, err)
	}
	if err := os.RemoveAll(ri.idxPath); err != nil {
		return fmt.Errorf("memrooms/index: remove index for rebuild %s: %w", ri.idxPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(ri.idxPath), 0o700); err != nil {
		return fmt.Errorf("memrooms/index: create index parent dir %s: %w", filepath.Dir(ri.idxPath), err)
	}
	fresh, err := bleve.NewUsing(ri.idxPath, buildMapping(), scorch.Name, scorch.Name, scorchOpenConfig())
	if err != nil {
		return fmt.Errorf("memrooms/index: recreate index %s: %w", ri.idxPath, err)
	}
	ri.idx = fresh

	return ri.rebuildLocked()
}

// rebuildLocked populates the open bleve index from all .md files in memoriesDir.
// Caller MUST hold ri.mu.
func (ri *RoomIndex) rebuildLocked() error {
	memories, err := memrooms.ScanMemories(ri.memoriesDir)
	if err != nil {
		return fmt.Errorf("memrooms/index: scan memories for rebuild: %w", err)
	}

	batch := ri.idx.NewBatch()
	for _, mf := range memories {
		doc := memoryDoc{
			Title:  mf.Frontmatter.Title,
			Body:   mf.Body,
			Tags:   joinTags(mf.Frontmatter.Tags),
			Type:   string(mf.Frontmatter.Type),
			Status: string(mf.Frontmatter.Status),
			Author: mf.Frontmatter.Author,
		}
		if batchErr := batch.Index(mf.Frontmatter.ID, doc); batchErr != nil {
			return fmt.Errorf("memrooms/index: batch index %s: %w", mf.Frontmatter.ID, batchErr)
		}
	}
	if err := ri.idx.Batch(batch); err != nil {
		return fmt.Errorf("memrooms/index: batch commit rebuild: %w", err)
	}

	slog.Info("memrooms/index: index rebuilt",
		"memories_dir", ri.memoriesDir,
		"document_count", len(memories))
	return nil
}

// DocCount returns the number of indexed documents. Useful for diagnostics.
func (ri *RoomIndex) DocCount() (uint64, error) {
	ri.handleMu.RLock()
	defer ri.handleMu.RUnlock()
	return ri.idx.DocCount()
}

// Close releases THIS caller's reference to the shared index. The underlying
// bleve/bbolt handle is physically closed only when the last holder releases it;
// while any other holder remains, Close is a bookkeeping no-op and the handle
// stays open. This is what makes a shared workspace room safe: one MemoryStore's
// Close() can never close an index another MemoryStore is still using.
//
// The caller must not use ri after Close returns (its own reference is gone). A
// handle that was not registry-managed (regKey == "") is closed directly.
func (ri *RoomIndex) Close() error {
	if ri.regKey == "" {
		return ri.closeUnderlying()
	}
	return releaseShared(ri.regKey)
}

// closeUnderlying physically closes the bleve index. It is called only by the
// registry when the last holder releases the handle (or directly by Close for a
// non-registry-managed handle). The caller must not use ri afterwards.
func (ri *RoomIndex) closeUnderlying() error {
	ri.mu.Lock()
	defer ri.mu.Unlock()
	// handleMu too, so a Search already in flight finishes against a live index
	// rather than one closed underneath it. Index/Delete need no handleMu: mu
	// already serializes them against Rebuild, the only writer of the pointer.
	ri.handleMu.Lock()
	defer ri.handleMu.Unlock()
	if err := ri.idx.Close(); err != nil {
		return fmt.Errorf("memrooms/index: close: %w", err)
	}
	return nil
}

// joinTags concatenates a tag slice into a space-separated string for text indexing.
func joinTags(tags []string) string {
	result := ""
	for i, t := range tags {
		if i > 0 {
			result += " "
		}
		result += t
	}
	return result
}
