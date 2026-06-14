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
//   - Recall: BM25 ranking via bleve's default BM25 scorer. Top-N results
//     returned as []SearchHit with (ID, Score).
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

	"github.com/dapicom-ai/omnipus/pkg/memrooms"
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
	// Score is the BM25 relevance score (higher = more relevant).
	Score float64
}

// RoomIndex holds an open bleve scorch index for a single room.
// It is safe for concurrent reads; writes are serialised by the caller.
type RoomIndex struct {
	idx         bleve.Index
	memoriesDir string
	mu          sync.Mutex // serialises Index() / Rebuild() calls
}

// OpenOrCreate opens the scorch index at <room.Root>/.index/bleve/, or creates
// it if absent. If the index is corrupt (open fails after creation check), the
// index directory is removed and rebuilt from the .md sources in room.MemoriesDir.
//
// The caller must call Close() when done.
func OpenOrCreate(room memrooms.Room) (*RoomIndex, error) {
	idxPath := filepath.Join(room.Root, IndexSubdir)

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

	ri := &RoomIndex{idx: idx, memoriesDir: room.MemoriesDir}

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
}

// openOrCreateAt opens a scorch index at path if it exists, or creates one.
func openOrCreateAt(idxPath string) (bleve.Index, error) {
	if _, statErr := os.Stat(idxPath); os.IsNotExist(statErr) {
		// Does not exist — create a new scorch index.
		if mkErr := os.MkdirAll(filepath.Dir(idxPath), 0o700); mkErr != nil {
			return nil, fmt.Errorf("create parent dir: %w", mkErr)
		}
		return bleve.NewUsing(idxPath, buildMapping(), scorch.Name, "", nil)
	}
	// Exists — open it.
	idx, err := bleve.Open(idxPath)
	if err != nil {
		return nil, err
	}
	return idx, nil
}

// buildMapping returns the bleve IndexMapping for memory documents.
// Text fields use the default "en" analyzer (Porter stemmer + stopwords).
// Keyword fields use keyword analyzer (exact match, no stemming).
func buildMapping() *bleveMapping.IndexMappingImpl {
	m := bleve.NewIndexMapping()

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
// Safe to call concurrently — serialised by the internal mu lock.
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
// Safe to call concurrently — serialised by the internal mu lock.
func (ri *RoomIndex) Delete(id string) error {
	ri.mu.Lock()
	defer ri.mu.Unlock()
	if err := ri.idx.Delete(id); err != nil {
		return fmt.Errorf("memrooms/index: delete document %s: %w", id, err)
	}
	return nil
}

// Search executes a BM25 full-text query against the index.
// Returns up to limit results ordered by descending BM25 score.
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
		// BM25 over all indexed fields.
		// Default operator is OR (MatchQueryOperatorOr = 0) — any term hit ranks the doc.
		mq := bleve.NewMatchQuery(query)
		req = bleve.NewSearchRequestOptions(mq, limit, 0, false)
	}
	req.Fields = []string{} // scores only — we fetch full content from .md

	res, err := ri.idx.Search(req)
	if err != nil {
		return nil, fmt.Errorf("memrooms/index: search %q: %w", query, err)
	}

	hits := make([]SearchHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		hits = append(hits, SearchHit{ID: h.ID, Score: h.Score})
	}
	return hits, nil
}

// Rebuild wipes and recreates the bleve index from the room's .md files.
// Call this after corruption or when the index is suspected stale.
// Safe to call concurrently — serialised by the internal mu lock.
func (ri *RoomIndex) Rebuild() error {
	ri.mu.Lock()
	defer ri.mu.Unlock()
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
	return ri.idx.DocCount()
}

// Close releases the bleve index. The caller must not use ri after Close returns.
func (ri *RoomIndex) Close() error {
	ri.mu.Lock()
	defer ri.mu.Unlock()
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
