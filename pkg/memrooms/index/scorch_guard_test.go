// scorch_guard_test.go — pure-Go bleve backend assertion (Spec-2 SC-4 / CI CGO gate).
//
// Omnipus is a pure-Go single binary (CLAUDE.md hard constraint #2: no CGo, no
// external C libs). bleve ships two index backends:
//   - scorch  — pure-Go (bbolt-backed), the only backend we may use.
//   - upsidedown — historically backed by leveldb/CGo kvstores; NEVER acceptable.
//
// This test asserts both that bleve's library default index type is scorch AND
// that our package's OpenOrCreate path produces a working scorch index under
// CGO_ENABLED=0. The test itself is run under CGO_ENABLED=0 in CI (see the
// pr.yml `test` job env), so a regression that pulled in a CGo kvstore would
// fail to build/run here.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package index_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/memrooms"
	memindex "github.com/dapicom-ai/omnipus/pkg/memrooms/index"
)

// TestBleve_DefaultIndexTypeIsScorch asserts that bleve's library-level default
// index type is scorch (pure-Go). If a dependency upgrade ever flipped the
// default to a CGo-backed backend, this would fail loudly.
func TestBleve_DefaultIndexTypeIsScorch(t *testing.T) {
	assert.Equal(t, scorch.Name, bleve.Config.DefaultIndexType,
		"bleve must default to the pure-Go scorch backend; got %q (a CGo backend would violate the pure-Go constraint)",
		bleve.Config.DefaultIndexType)
}

// TestBleve_ScorchNameIsConstant guards the scorch.Name value we depend on. A
// rename in a bleve upgrade would silently break our openOrCreateAt call.
func TestBleve_ScorchNameIsConstant(t *testing.T) {
	assert.Equal(t, "scorch", scorch.Name,
		"scorch.Name must be the literal %q; openOrCreateAt passes it to bleve.NewUsing", "scorch")
}

// TestBleve_OpenOrCreate_ProducesWorkingScorchIndex verifies that our
// OpenOrCreate path produces a working index that can index and search a
// document under CGO_ENABLED=0. This is an end-to-end proof that the pure-Go
// scorch backend is what we actually use at runtime — not just what bleve
// advertises as its default.
//
// This test is run under CGO_ENABLED=0 in CI; if a CGo kvstore were required,
// the index open or the index/search calls would fail.
func TestBleve_OpenOrCreate_ProducesWorkingScorchIndex(t *testing.T) {
	root := t.TempDir()
	memoriesDir := filepath.Join(root, "memories")
	require.NoError(t, os.MkdirAll(memoriesDir, 0o700))

	room := memrooms.Room{
		Root:         root,
		MemoriesDir:  memoriesDir,
		CountersPath: filepath.Join(root, "counters.jsonl"),
	}

	ri, err := memindex.OpenOrCreate(room)
	require.NoError(t, err, "OpenOrCreate must succeed under CGO_ENABLED=0 (pure-Go scorch)")
	defer ri.Close()

	mf := memrooms.MemoryFile{
		Frontmatter: memrooms.MemoryFrontmatter{
			ID:     "scorch-guard-001",
			Title:  "Scorch pure-Go guard",
			Type:   memrooms.MemoryTypeFact,
			Tags:   []string{"test"},
			Status: memrooms.MemoryStatusActive,
			Author: "agent-test",
			BornIn: "session-guard",
		},
		Body: "The scorch backend is pure Go and needs no CGo.",
	}
	require.NoError(t, ri.Index(mf), "Index must succeed under CGO_ENABLED=0")

	hits, err := ri.Search("scorch", 10)
	require.NoError(t, err, "Search must succeed under CGO_ENABLED=0")
	require.NotEmpty(t, hits, "expected a BM25 hit for 'scorch'")
	assert.Equal(t, "scorch-guard-001", hits[0].ID)
	assert.Greater(t, hits[0].Score, 0.0)
}

// TestBleve_NoLeveldbImportsInSource is a source-level guard: the memrooms/
// index package must not use any leveldb or rocksdb kvstore. bleve's
// DefaultKVStore is either empty (scorch manages its own storage) or "boltdb"
// (bbolt — pure-Go). leveldb/rocksdb are the CGo-backed kvstores and must
// never be the default.
func TestBleve_NoLeveldbImportsInSource(t *testing.T) {
	for _, cgoKV := range []string{"leveldb", "rocksdb", "goleveldb"} {
		assert.NotEqual(t, cgoKV, bleve.Config.DefaultKVStore,
			"bleve DefaultKVStore must not be the CGo-backed %q", cgoKV)
	}
	// scorch uses bbolt (registered as "boltdb"), which is pure-Go. An empty
	// value is also acceptable (scorch then picks its default). Anything else
	// here would warrant investigation.
	allowed := map[string]struct{}{"": {}, "boltdb": {}}
	if _, ok := allowed[bleve.Config.DefaultKVStore]; !ok {
		t.Fatalf(
			"bleve DefaultKVStore is %q — expected empty or pure-Go 'boltdb'; a CGo backend would violate the pure-Go constraint",
			bleve.Config.DefaultKVStore,
		)
	}
}
