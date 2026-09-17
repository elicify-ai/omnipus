// index_persist.go: Persist and load the index on disk — open-or-rebuild, the format and mapping guards, and index-directory permissions.

package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	bleveMapping "github.com/blevesearch/bleve/v2/mapping"
	bleveIndexAPI "github.com/blevesearch/bleve_index_api"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// openOrRebuild opens the bleve index under ix.blevePath, rebuilding it from
// scratch when what is on disk cannot be trusted. It returns the open index and
// the rebuild reason ("" if none).
//
// THE POINT OF THIS FUNCTION IS THAT AN UNTRUSTWORTHY INDEX CANNOT BE OPENED
// QUIETLY. There are three ways an index reaches us in a state that must not be
// searched, and each fails differently:
//
//  1. It will not open at all — corruption bleve itself detects. This was
//     already handled and still is.
//  2. It opens fine and its segments are silently wrong. This is ADR-068 F-0:
//     zapx v17.1.2 miscalculates chunk offsets while WRITING, so a search over
//     a 100,000-document index panics with a slice bound out of range — a panic
//     that is not recovered anywhere in bleve's call stack, so in the gateway it
//     is a process crash. Pinning zapx ≥ v17.1.4 fixes new writes and does
//     nothing whatever for segments already on disk. Only guard G1, the format
//     version, can see this: the bytes look valid until they are read.
//  3. It opens fine and its MAPPING is not the mapping the code now builds.
//     bleve.OpenUsing takes no mapping argument — the mapping persisted at
//     creation is authoritative forever after — so a field the code has since
//     added, or whose analyzer it has since changed, produces zero hits and NO
//     ERROR. Guard G2 catches this.
//
// G1 and G2 are both here because neither subsumes the other. G1 depends on a
// human remembering to bump indexFormatVersion; G2 depends on nobody
// remembering anything, and catches exactly the case where the bump was
// forgotten. G1 catches the case G2 cannot see at all — segments that are wrong
// while the mapping is right.
func (ix *Index) openOrRebuild() (bleve.Index, string, error) {
	if _, statErr := os.Stat(ix.blevePath); statErr != nil {
		if !errors.Is(statErr, fs.ErrNotExist) {
			return nil, "", fmt.Errorf("knowledge: stat index %s: %w", ix.blevePath, statErr)
		}
		// Nothing on disk to distrust. Not a rebuild: there was no index.
		bidx, err := ix.createFreshIndex()
		return bidx, "", err
	}

	reason := ix.formatStaleReason() // G1
	if reason == "" {
		bidx, err := bleve.OpenUsing(ix.blevePath, bleveOpenConfig())
		switch {
		case err != nil:
			reason = fmt.Sprintf("the index could not be opened (%v)", err)
		default:
			if drift := mappingDrift(bidx.Mapping()); drift != "" { // G2
				closeIndexQuietly(bidx, ix.blevePath)
				reason = "the index was written with a different document mapping: " + drift
			} else {
				return bidx, "", nil
			}
		}
	}

	slog.Warn("knowledge: index on disk cannot be trusted; discarding it and rebuilding from the collection",
		"path", ix.blevePath, "root", ix.root, "reason", reason)
	if rmErr := os.RemoveAll(ix.blevePath); rmErr != nil {
		return nil, "", fmt.Errorf("knowledge: remove untrusted index %s: %w", ix.blevePath, rmErr)
	}
	if rmErr := os.Remove(ix.formatPath); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("knowledge: remove stale index format %s: %w", ix.formatPath, rmErr)
	}
	bidx, err := ix.createFreshIndex()
	if err != nil {
		return nil, "", err
	}
	return bidx, reason, nil
}

// createFreshIndex creates an empty index with the CURRENT mapping and stamps
// the current format version beside it.
//
// It removes the manifest first, and that removal is load-bearing rather than
// tidy: the manifest is what makes Sync incremental, so a manifest that
// outlives its index makes the next Sync skip every file as "unchanged" against
// documents that no longer exist — an empty index that reports itself complete,
// which is precisely the silent no-op this whole path exists to make impossible.
func (ix *Index) createFreshIndex() (bleve.Index, error) {
	if mkErr := os.MkdirAll(filepath.Dir(ix.blevePath), indexDirMode); mkErr != nil {
		return nil, fmt.Errorf("knowledge: create index parent dir %s: %w", filepath.Dir(ix.blevePath), mkErr)
	}
	if rmErr := os.Remove(ix.manifestPath); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
		return nil, fmt.Errorf("knowledge: remove stale manifest %s: %w", ix.manifestPath, rmErr)
	}
	bidx, err := bleve.NewUsing(ix.blevePath, buildIndexMapping(), scorch.Name, scorch.Name, bleveOpenConfig())
	if err != nil {
		return nil, fmt.Errorf("knowledge: create index %s: %w", ix.blevePath, err)
	}
	// The stamp is written AFTER the index exists and its failure is fatal: an
	// index with no stamp is an index this function would rebuild again on the
	// next open, forever, and a rebuild loop nobody is told about is worse than
	// a failed open somebody is.
	if err := writeIndexFormat(ix.formatPath); err != nil {
		closeIndexQuietly(bidx, ix.blevePath)
		return nil, err
	}
	return bidx, nil
}

// bleveOpenConfig is the runtime config every open and create passes to scorch.
func bleveOpenConfig() map[string]any {
	return map[string]any{"bolt_timeout": boltOpenTimeout}
}

// closeIndexQuietly closes an index we are abandoning. The close error cannot be
// returned — we are already on an error path and the caller's error is the one
// that explains what happened — but it is not discarded either: a close that
// fails leaves a bolt lock held, which is the next thing that will go wrong.
func closeIndexQuietly(bidx bleve.Index, path string) {
	if bidx == nil {
		return
	}
	if err := bidx.Close(); err != nil {
		slog.Warn("knowledge: closing abandoned index failed", "path", path, "error", err)
	}
}

// indexFormat is the sidecar's content. It is deliberately one integer: a
// record with more in it is a record with more ways to disagree with itself,
// and everything else worth knowing (the bleve and zapx versions in force) is
// in go.mod, where it cannot drift from what is actually linked.
type indexFormat struct {
	Version int `json:"version"`
}

// readIndexFormat reports the format version recorded beside the index.
//
// A MISSING SIDECAR IS VERSION 0, NOT AN ERROR, AND 0 IS NEVER CURRENT. Every
// index written before this file existed has no sidecar, and those are exactly
// the indexes that may hold the corrupt segments. "Absent" must therefore mean
// "rebuild", never "assume fine".
//
// A sidecar that exists but cannot be read or parsed returns its error, and the
// caller rebuilds on that too: an unreadable record of what wrote the index is
// no better than no record.
func readIndexFormat(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read index format %s: %w", path, err)
	}
	var f indexFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, fmt.Errorf("parse index format %s: %w", path, err)
	}
	return f.Version, nil
}

// indexFormatVersion is the CURRENT on-disk index format — guard G1.
//
// BUMP THIS WHENEVER SEGMENTS WRITTEN BY OLDER CODE MUST NOT BE SEARCHED. That
// covers two different things and both are real:
//
//   - the WRITER changed in a way that makes older bytes wrong. Version 1 is
//     this case. Everything written before it may have been written by
//     zapx v17.1.2, which miscalculates chunk offsets and produces segments
//     that panic the process on a search once a collection is large enough to
//     force a big merge (ADR-068 F-0, ~100,000 documents). The version pin to
//     zapx ≥ v17.1.4 fixes what is written next and repairs nothing already
//     written; this constant is the other half of that fix.
//   - the MAPPING changed such that old documents lack fields, or carry them
//     under a different analyzer. G2 (mappingDrift) also catches that, and is
//     the guard that does not depend on this constant being bumped — but a bump
//     is cheaper to reason about and fires before the index is even opened.
//
// Version 2 is the second case: ADR-068 D21.1 set the mapping's ScoringModel to
// BM25, having found that bleve was scoring TF-IDF everywhere while thirteen
// places in the tree said otherwise. The scoring model is a property of the
// PERSISTED mapping, so an index written under version 1 keeps scoring TF-IDF
// however the code is compiled. Nothing fails; the ranking is simply not the
// one the code asks for. That is why it is both a bump here and a comparison in
// mappingDrift — a scoring change that does not force a rebuild is a change
// that has not happened.
//
// A rebuild costs one full re-index of the collection and never costs an answer:
// the notes on disk are the source of truth and the index is derived data.
// Getting this wrong in the cautious direction is a slow start-up. Getting it
// wrong in the other direction is a crash in the gateway.
// Version 3 is ADR-068 D21.2 + D16.5: the document gained title, headings,
// prop_key, prop_value, prop and a stored source_hash, and the body field
// stopped carrying the frontmatter block. Every one of those is also a MAPPING
// change, so G2 sees it too — but G1 fires before the index is even opened, and
// an index written under version 2 holds documents whose body text still
// contains the YAML and whose property fields do not exist at all. A field
// query against one of those documents returns zero hits and no error.
//
// Version 4 is UAT 2026-09-13 D-129 / D-99: every prose field moved from the
// stock "en" analyzer to "en_folded" (analyzer_folded.go — Unicode NFC, then
// ASCII folding, before stop words and stemming). A version-3 dictionary holds
// "café" and "café" as two terms and never "cafe"; under the new query
// path a search for any of the three would miss two of them. G2 sees the
// analyzer name change on every prose field too; G1 fires first.
const indexFormatVersion = 4

// writeIndexFormat stamps the current format version, atomically and 0600
// (FR-032, same rules as the manifest it sits beside).
func writeIndexFormat(path string) error {
	data, err := json.MarshalIndent(indexFormat{Version: indexFormatVersion}, "", "  ")
	if err != nil {
		return fmt.Errorf("knowledge: encode index format: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, data, indexFileMode); err != nil {
		return fmt.Errorf("knowledge: write index format %s: %w", path, err)
	}
	if err := os.Chmod(path, indexFileMode); err != nil {
		return fmt.Errorf("knowledge: set mode on index format %s: %w", path, err)
	}
	return nil
}

// formatStaleReason is guard G1: it reports, in a sentence, why the index on
// disk is not in the current format — or "" when it is.
func (ix *Index) formatStaleReason() string {
	got, err := readIndexFormat(ix.formatPath)
	if err != nil {
		return fmt.Sprintf("its format record could not be read (%v)", err)
	}
	switch got {
	case indexFormatVersion:
		return ""
	case 0:
		return fmt.Sprintf(
			"it carries no format record, so it was written before the index format was tracked and may hold "+
				"segments from a writer that corrupts them at scale (current format is %d)", indexFormatVersion)
	default:
		return fmt.Sprintf("it was written in index format %d and the current format is %d", got, indexFormatVersion)
	}
}

// mappingDrift is guard G2: it compares the mapping PERSISTED inside the index
// against the mapping buildIndexMapping produces now, and returns the first
// difference as a sentence — or "" when they agree.
//
// It exists because bleve.OpenUsing takes no mapping argument. The mapping
// written at creation is authoritative for the life of the index, so code that
// declares a new field, or changes an existing field's analyzer, gets an index
// that quietly ignores the change: queries against the new field return zero
// hits and no error. There is no failure to notice — which is why this is a
// comparison and not an error check.
//
// It compares the settings that decide whether a query can work at all —
// type, analyzer, index, store, docvalues, term vectors, _all membership — not
// just field NAMES. A name-only comparison would pass an index whose `name`
// field was built with the keyword analyzer while the code now says `en`, and
// the same query would return a different number of hits depending on which
// mapping was actually in force, with no way for the caller to tell.
//
// It also compares in BOTH directions, and compares the dynamic settings: a
// field the code has stopped declaring, or an index built when dynamic mapping
// was on, are equally not the index this code expects.
func mappingDrift(persisted bleveMapping.IndexMapping) string {
	want := buildIndexMapping()

	declared := make([]string, 0, len(want.DefaultMapping.Properties))
	for name := range want.DefaultMapping.Properties {
		declared = append(declared, name)
	}
	sort.Strings(declared)

	// Absent fields first, across the whole declaration, THEN per-field
	// settings: a field the persisted index does not hold at all is the more
	// fundamental (and more actionable) drift, and reporting it must not
	// depend on whether an alphabetically earlier field happens to differ in
	// a setting — since D-129 changed every prose field's analyzer, "body"
	// would otherwise always be reported ahead of a missing "title".
	for _, name := range declared {
		if persisted.FieldMappingForPath(name).Type == "" {
			return fmt.Sprintf("field %q is absent from the persisted mapping", name)
		}
	}
	for _, name := range declared {
		got := persisted.FieldMappingForPath(name)
		if d := fieldMappingDrift(name, got, want.FieldMappingForPath(name)); d != "" {
			return d
		}
	}

	impl, ok := persisted.(*bleveMapping.IndexMappingImpl)
	if !ok {
		// Every field the code declares has been checked; only the reverse
		// direction, the dynamic settings and the scoring model are
		// unreachable. Say so out loud rather than reporting a clean
		// comparison that was not made.
		slog.Warn("knowledge: persisted mapping is not an IndexMappingImpl; "+
			"undeclared-field, dynamic-setting and scoring-model drift cannot be checked",
			"type", fmt.Sprintf("%T", persisted))
		return ""
	}
	if impl.DefaultMapping == nil {
		return "the persisted mapping has no default document mapping"
	}
	// The scoring model decides how every hit is RANKED, and bleve reads it from
	// this persisted mapping rather than from the mapping the code now builds
	// (index_impl.go loads the stored mapping at open; isBM25Enabled then asks
	// that object, not ours). An index written before ADR-068 D21.1 therefore
	// keeps scoring TF-IDF for the rest of its life with no error and no empty
	// result to notice — a silent wrong answer, which is the whole reason this
	// function is a comparison rather than an error check.
	//
	// Empty is compared as bleve resolves it, not as a string: "" means
	// DefaultScoringModel (TF-IDF), so an empty persisted model and an explicit
	// "tf-idf" are the same index and must not be reported as drift.
	if gotModel, wantModel := effectiveScoringModel(impl), effectiveScoringModel(want); gotModel != wantModel {
		return fmt.Sprintf("the persisted mapping scores with %q and the code declares %q",
			gotModel, wantModel)
	}
	if impl.DefaultMapping.Dynamic != want.DefaultMapping.Dynamic {
		return fmt.Sprintf("the persisted default document mapping has dynamic=%t, the code declares dynamic=%t",
			impl.DefaultMapping.Dynamic, want.DefaultMapping.Dynamic)
	}
	if impl.IndexDynamic != want.IndexDynamic {
		return fmt.Sprintf("the persisted mapping has index_dynamic=%t, the code declares index_dynamic=%t",
			impl.IndexDynamic, want.IndexDynamic)
	}
	if impl.StoreDynamic != want.StoreDynamic {
		return fmt.Sprintf("the persisted mapping has store_dynamic=%t, the code declares store_dynamic=%t",
			impl.StoreDynamic, want.StoreDynamic)
	}

	extra := make([]string, 0)
	for name := range impl.DefaultMapping.Properties {
		if _, still := want.DefaultMapping.Properties[name]; !still {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Sprintf("field %q is in the persisted mapping and the code no longer declares it", extra[0])
	}
	return ""
}

// effectiveScoringModel reports the scoring model an index mapping ACTUALLY
// ranks with, resolving the empty string the way bleve does rather than
// treating it as a distinct value. bleve's isBM25Enabled tests the field
// against "bm25" and everything else — empty included — falls through to
// index.DefaultScoringModel, which is TF-IDF.
//
// Comparing the raw strings instead would report drift between an index written
// with "" and code declaring "tf-idf" when the two rank identically, and a
// guard that fires on a difference that does not exist is a guard that gets
// switched off.
func effectiveScoringModel(m *bleveMapping.IndexMappingImpl) string {
	if m == nil || m.ScoringModel == "" {
		return bleveIndexAPI.DefaultScoringModel
	}
	return m.ScoringModel
}

// fieldMappingDrift compares one field's settings. The order of the checks is
// the order the differences are reported in, which keeps the message stable for
// a given pair of mappings.
func fieldMappingDrift(name string, got, want bleveMapping.FieldMapping) string {
	switch {
	case got.Type != want.Type:
		return fmt.Sprintf("field %q has type %q in the persisted mapping, the code declares %q",
			name, got.Type, want.Type)
	case got.Analyzer != want.Analyzer:
		return fmt.Sprintf("field %q uses analyzer %q in the persisted mapping, the code declares %q",
			name, got.Analyzer, want.Analyzer)
	case got.Index != want.Index:
		return fmt.Sprintf("field %q has index=%t in the persisted mapping, the code declares index=%t",
			name, got.Index, want.Index)
	case got.Store != want.Store:
		return fmt.Sprintf("field %q has store=%t in the persisted mapping, the code declares store=%t",
			name, got.Store, want.Store)
	case got.DocValues != want.DocValues:
		return fmt.Sprintf("field %q has docvalues=%t in the persisted mapping, the code declares docvalues=%t",
			name, got.DocValues, want.DocValues)
	case got.IncludeTermVectors != want.IncludeTermVectors:
		return fmt.Sprintf(
			"field %q has include_term_vectors=%t in the persisted mapping, the code declares include_term_vectors=%t",
			name, got.IncludeTermVectors, want.IncludeTermVectors)
	case got.IncludeInAll != want.IncludeInAll:
		return fmt.Sprintf(
			"field %q has include_in_all=%t in the persisted mapping, the code declares include_in_all=%t",
			name, got.IncludeInAll, want.IncludeInAll)
	}
	return ""
}

// enforceIndexPermissions asserts FR-032 over the whole index directory:
// directories 0700, files 0600. bleve gets most of this right on its own, but
// its index_meta.json is created 0666 and would otherwise be world-readable
// under a typical umask — and the index holds the full text of every note.
func enforceIndexPermissions(dir string) error {
	if err := filepath.WalkDir(dir, enforceEntryPermissions); err != nil {
		return fmt.Errorf("knowledge: enforce index permissions on %s: %w", dir, err)
	}
	return nil
}

// enforceEntryPermissions is the per-entry half of enforceIndexPermissions,
// split out so the vanished-file path below can be tested deterministically —
// the race that motivates it cannot be triggered on demand.
//
// A FILE THAT NO LONGER EXISTS IS NOT AN ERROR HERE, and that is the whole
// point of this function. The walk runs over a LIVE scorch index — SyncWith
// calls it immediately after batch.commit(), which is exactly when scorch's
// background merger fires and DELETES the segments it just merged away. So
// there are three moments where a .zap file can vanish underneath us:
//
//  1. walkErr — WalkDir could not stat an entry it had already enumerated.
//  2. d.Info() — DirEntry.Info lstats LAZILY, so the entry came from an
//     earlier ReadDir and the file may be gone by the time we ask.
//  3. os.Chmod — gone in the window between Info and the chmod itself.
//
// Before this, any one of them aborted the walk and failed the whole Sync for
// no real reason. Observed once for real (lstat .../000000000005.zap: no such
// file or directory) on a 500-note fixture; on a 100k-note collection the
// merger is not an edge case, it is the normal path.
//
// This does NOT weaken FR-032: a file that is not there cannot have the wrong
// permissions, and every file still present is still checked and chmod'ed.
func enforceEntryPermissions(path string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil
		}
		return walkErr
	}
	want := indexFileMode
	if d.IsDir() {
		want = indexDirMode
	}
	info, statErr := d.Info()
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("enforceEntryPermissions: %w", statErr)
	}
	if info.Mode().Perm() == want {
		return nil
	}
	if err := os.Chmod(path, want); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("enforceEntryPermissions: %w", err)
	}
	return nil
}

// buildIndexMapping defines the document shape.
//
// Body is INDEXED BUT NOT STORED and carries no term vectors: FR-050a requires
// excerpts to be re-read from disk at query time, so an index that could hand
// back note text would be a stale copy waiting to happen — and storing 100,000
// note bodies twice is the memory budget MV-2/MV-3 do not have.
//
// IncludeInAll is off on every field. The composite _all field would double the
// indexing cost to serve queries this package never issues: like
// pkg/memrooms/index, we query the real fields explicitly, because a match
// query against _all silently returns nothing when the field analyzers differ.
//
// ScoringModel is set EXPLICITLY to BM25 (ADR-068 D21.1). bleve's default is
// TF-IDF (`DefaultScoringModel = TFIDFScoring`, bleve_index_api
// indexing_options.go), and leaving this field empty is not "unspecified" — it
// is a positive choice of TF-IDF, which is what this package shipped with while
// its own comments claimed BM25. The difference is not cosmetic: BM25 saturates
// term frequency, so a note that repeats a term twenty times stops accruing
// score, whereas TF-IDF keeps rewarding it. Over a note collection that is the
// difference between ranking the note ABOUT a topic first and ranking the note
// that merely says the word most often first.
//
// This is also why indexFormatVersion is bumped alongside it and why
// mappingDrift compares it: the scoring model is read from the mapping
// PERSISTED IN THE INDEX (bleve resolves it via isBM25Enabled over the mapping
// loaded at open, not the mapping the code builds), so without a forced rebuild
// this line would change nothing whatsoever on any index already on disk.
func buildIndexMapping() *bleveMapping.IndexMappingImpl {
	m := bleve.NewIndexMapping()
	m.ScoringModel = bleveIndexAPI.BM25Scoring
	// The custom prose analyzer is registered on the mapping itself, so the
	// definition is persisted with the index (D-129, analyzer_folded.go). The
	// only way this can fail is a programming error in the definition — a
	// misspelt component name — which the package's own tests exercise on
	// every build, so a panic here is a build-time fact, not a runtime one.
	if err := registerProseAnalyzer(m); err != nil {
		panic("knowledge: prose analyzer definition rejected: " + err.Error())
	}

	body := bleve.NewTextFieldMapping()
	body.Analyzer = proseAnalyzerName
	body.Store = false
	body.IncludeTermVectors = false
	body.IncludeInAll = false
	body.DocValues = false

	name := bleve.NewTextFieldMapping()
	name.Analyzer = proseAnalyzerName
	name.Store = false
	name.IncludeTermVectors = false
	name.IncludeInAll = false
	name.DocValues = false

	pathField := bleve.NewTextFieldMapping()
	pathField.Analyzer = "keyword"
	pathField.Store = true
	pathField.IncludeTermVectors = false
	pathField.IncludeInAll = false
	pathField.DocValues = false

	kind := bleve.NewTextFieldMapping()
	kind.Analyzer = "keyword"
	kind.Store = true
	kind.IncludeTermVectors = false
	kind.IncludeInAll = false
	kind.DocValues = false

	offset := bleve.NewNumericFieldMapping()
	offset.Store = true
	offset.Index = false
	offset.IncludeInAll = false
	offset.DocValues = false

	// ADR-068 D21.2's fields. THE MAPPING STAYS CLOSED: these are six fixed
	// names, and an operator's own property names arrive as TERMS inside
	// prop_key / prop rather than as fields. fields.go states the reasoning at
	// length, because "index the property keys" reads like a request for a
	// dynamic mapping and it is not one.
	title := bleve.NewTextFieldMapping()
	title.Analyzer = proseAnalyzerName
	title.Store = false
	title.IncludeTermVectors = false
	title.IncludeInAll = false
	title.DocValues = false

	headings := bleve.NewTextFieldMapping()
	headings.Analyzer = proseAnalyzerName
	headings.Store = false
	headings.IncludeTermVectors = false
	headings.IncludeInAll = false
	headings.DocValues = false

	// Keyword, because a property key is an identifier and must not be stemmed:
	// `status` and `statuses` are two different properties, and the `en`
	// analyzer maps both to "statu".
	propKey := bleve.NewTextFieldMapping()
	propKey.Analyzer = "keyword"
	propKey.Store = false
	propKey.IncludeTermVectors = false
	propKey.IncludeInAll = false
	propKey.DocValues = false

	// Prose, because a property VALUE is read by a person: a search for
	// "prospect" should find `status: prospecting`.
	propValue := bleve.NewTextFieldMapping()
	propValue.Analyzer = proseAnalyzerName
	propValue.Store = false
	propValue.IncludeTermVectors = false
	propValue.IncludeInAll = false
	propValue.DocValues = false

	// Keyword for the same reason as propKey, and doubly so: the whole
	// `key=value` string is one term, which is what makes an exact pair query
	// exact.
	prop := bleve.NewTextFieldMapping()
	prop.Analyzer = "keyword"
	prop.Store = false
	prop.IncludeTermVectors = false
	prop.IncludeInAll = false
	prop.DocValues = false

	// D16.5's freshness token: STORED AND NOT INDEXED. Nobody searches for a
	// hash, and indexing 100,000 distinct 64-byte terms would buy a query
	// nobody issues at the cost of a term dictionary entry per document. It is
	// exactly the shape `offset` already has, which is why the retrieval path
	// for it is a path this package has already proven.
	sourceHash := bleve.NewTextFieldMapping()
	sourceHash.Analyzer = "keyword"
	sourceHash.Store = true
	sourceHash.Index = false
	sourceHash.IncludeTermVectors = false
	sourceHash.IncludeInAll = false
	sourceHash.DocValues = false

	doc := bleve.NewDocumentMapping()
	doc.AddFieldMappingsAt(fieldPath, pathField)
	doc.AddFieldMappingsAt(fieldName, name)
	doc.AddFieldMappingsAt(fieldKind, kind)
	doc.AddFieldMappingsAt(fieldOffset, offset)
	doc.AddFieldMappingsAt(fieldTitle, title)
	doc.AddFieldMappingsAt(fieldHeadings, headings)
	doc.AddFieldMappingsAt(fieldPropKey, propKey)
	doc.AddFieldMappingsAt(fieldPropValue, propValue)
	doc.AddFieldMappingsAt(fieldProp, prop)
	doc.AddFieldMappingsAt(fieldSourceHash, sourceHash)
	doc.AddFieldMappingsAt(fieldBody, body)
	doc.Dynamic = false

	m.DefaultMapping = doc
	m.IndexDynamic = false
	m.StoreDynamic = false
	return m
}
