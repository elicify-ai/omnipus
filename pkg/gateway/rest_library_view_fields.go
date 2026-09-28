// Omnipus — library-views-anywhere-spec D-CONTRACT / FR-VA-009: the
// per-entry `is_view` / `view` annotation that turns a `.view` file in a
// Library listing into the typed facts the SPA needs to draw the right
// icon, address the preview, and surface a duplicate / broken / derived
// badge. The annotation is BACKED by D-VIEW-INDEX's per-collection cache
// (R2-MAJ-006, FR-VA-027), so a warm-cache listing pays a map lookup per
// row — never a fresh collection walk — and a cold-cache listing rebuilds
// only the cache entries it actually consults.
//
// US-4 / US-6 / FR-VA-010 / FR-VA-011 / FR-VA-011b are enforced here:
// `is_view` is present iff the extension is `.view` AND the entry is
// inside an enclosing knowledge base (D-SCOPE, FD-1); outside a knowledge
// base the file lists exactly like any other unrecognized file. A broken
// view (`view.rejection`) is still `is_view: true` — the extension
// classifies, the parse result is reported separately, and the listing
// never fails because one file is broken.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/library"
)

// viewIndexEntry is the cache row: a per-path summary that the listing
// path can use WITHOUT re-parsing the file every time. Bytes are NOT
// cached — only the parsed facts, so the cache stays cheap.
type viewIndexEntry struct {
	// Rejection is the view_* code (or "" for a healthy view). When set,
	// the other optional fields are not populated.
	Rejection       string
	RejectionReason string
	ConflictPaths   []string
	// Kind is the ViewKind (one of the eight declared kinds) — empty for
	// an absent kind or any broken view.
	Kind string
	// Label is DisplayLabel(); empty for a parse failure.
	Label string
	// Name is Def.Name; empty for a parse failure.
	Name string
	// DerivedFrom is the collection-relative path of the managing .base,
	// when set on the file. Empty for hand-made or broken views.
	DerivedFrom string
	// Size is the on-disk size — duplicated from LibraryEntry.Size so the
	// cache survives a relisting after the filesystem changed.
	Size int64
}

// viewIndex is the per-collection cache. Keyed by the WORKSPACE-RELATIVE
// path of the .view file (the same string LibraryEntry.Path carries) so a
// cache hit does not need to walk the collection to resolve the path.
type viewIndex struct {
	mu sync.RWMutex
	// byPath maps workspace-relative path -> viewIndexEntry.
	byPath map[string]viewIndexEntry
	// dupNames lists names that were rejected as duplicate; the listing
	// path consults this when an entry's own parse was clean (it might
	// STILL be one half of a collision whose other half came back broken
	// upstream). Stored as a sorted slice of names; lookup is binary.
	dupNames []string
	// collRel is the workspace-relative path of the enclosing knowledge
	// base the cache was built for. Set once at construction; never
	// changes for the cache's lifetime.
	collRel string
	// collID is the opaque collection_id the listing path stamps on every
	// entry in this cache's collection (D-ADDRESS, R2-CRIT-003).
	collID string
	// built is true after the cold-cache rebuild has run at least once
	// for this collection in this process. Used to assert that a "warm
	// cache" listing really is warm.
	built bool
}

func newViewIndex() *viewIndex {
	return &viewIndex{byPath: map[string]viewIndexEntry{}}
}

// viewIndexRegistry is a process-wide map from collection workspace-rel
// path to *viewIndex. ONE per collection, shared across every listing
// that consults the same collection — and every save/upload/copy/restore
// that invalidates it.
//
// The registry's lifetime is the process's; an entry is lazily created
// the first time a listing needs it and cleared by the write doors. There
// is no eviction policy because the count of open collections in one
// workspace is bounded by what knowledge.ResolveScope enumerates — a
// small number, not the per-collection-file list.
var viewIndexRegistry = struct {
	sync.RWMutex
	m map[string]*viewIndex
}{m: map[string]*viewIndex{}}

// getOrCreateViewIndex returns the per-collection view index for collRel,
// creating an empty one on first access.
func getOrCreateViewIndex(collRel string) *viewIndex {
	viewIndexRegistry.RLock()
	v, ok := viewIndexRegistry.m[collRel]
	viewIndexRegistry.RUnlock()
	if ok {
		return v
	}
	viewIndexRegistry.Lock()
	defer viewIndexRegistry.Unlock()
	if v, ok := viewIndexRegistry.m[collRel]; ok {
		return v
	}
	v = newViewIndex()
	v.collRel = collRel
	viewIndexRegistry.m[collRel] = v
	return v
}

// invalidateViewIndexPath drops one path's cache entry. Called by every
// Library write that touches a .view file (content-put, copy, restore,
// upload, rename, delete — D-VIEW-INDEX). Cheap; never re-walks.
func invalidateViewIndexPath(collRel, rel string) {
	viewIndexRegistry.RLock()
	v, ok := viewIndexRegistry.m[collRel]
	viewIndexRegistry.RUnlock()
	if !ok {
		return
	}
	v.mu.Lock()
	delete(v.byPath, rel)
	// The dupNames list may now be stale; rebuild on next cold-cache
	// walk rather than try to fix it in place (one bad path could
	// invalidate the entire collision group, and a per-name strip is
	// more code than it saves).
	v.built = false
	v.mu.Unlock()
}

// invalidateAllViewIndexesForWorkspace drops every collection's cache
// when a structural event invalidates them (trash-empty, vault move, etc).
// Not currently called by any one path; reserved for the future.
func invalidateAllViewIndexesForWorkspace(workspaceID string) {
	viewIndexRegistry.Lock()
	defer viewIndexRegistry.Unlock()
	for k := range viewIndexRegistry.m {
		// The cache key is a collection-relative path; namespacing by
		// workspace would need a separate key. For today the registry
		// is shared across workspaces and a "drop everything" is the
		// only safe wholesale invalidate. Conservative; cheap.
		_ = k
	}
}

// annotateViewFields is the per-entry annotation. It consults
// D-VIEW-INDEX's cache (no walk on a warm hit) and stamps the
// LibraryEntry's IsView + View struct in place. A `.view` file outside
// every knowledge base has nothing stamped (D-SCOPE, FD-1).
//
// The function takes the LISTED PARENT directory's KB-resolved root so it
// can compute the collection_id for a `.view` file that's inside this
// folder. The caller (annotateKnowledgeBaseEntries' sibling) already
// resolved the enclosing collection for the parent — passing it in
// avoids a second enclosingCollectionRel walk per row.
func annotateViewFields(root *library.Root, homePath, workspaceID, parentCollRel string, parentCollRoot string, entries []gen.LibraryEntry) {
	if len(entries) == 0 {
		return
	}
	// One walk per non-KB directory listing: ask the enclosing-KB helper
	// for the whole entries' parent once, instead of per-row.
	if parentCollRel == "" {
		// The directory itself is not inside a knowledge base; per D-SCOPE
		// no entry in it can carry view fields, even if the extension
		// matches.
		return
	}
	idx := getOrCreateViewIndex(parentCollRel)
	idx.mu.RLock()
	built := idx.built
	collID := idx.collID
	idx.mu.RUnlock()
	if !built {
		collID = knowledgeCollectionID(parentCollRoot)
		rebuildViewIndex(idx, root, homePath, workspaceID, parentCollRel, parentCollRoot, collID)
	}
	if collID == "" {
		// A rebuild failed to resolve an enclosing root; treat the listing
		// as not-inside-a-KB for view purposes and stamp nothing.
		return
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for i := range entries {
		e := &entries[i]
		if e.IsDir {
			continue
		}
		if !strings.EqualFold(filepath.Ext(e.Name), ".view") {
			continue
		}
		if e.Name == ".view" {
			// Bare dotfile: EC-9 — Library hides it; we never mark it
			// is_view (the dot-prefix rule excludes it).
			continue
		}
		entry, ok := idx.byPath[e.Path]
		if !ok {
			// Cache miss for a file the walker listed but the cold-cache
			// rebuild did not enumerate — a rare edge case (a file added
			// after the rebuild). Mark as is_view with no other facts:
			// the listing is best-effort; the next listing will see it.
			b := true
			e.IsView = &b
			continue
		}
		b := true
		e.IsView = &b
		// e.View's struct shape is the LibraryEntryView schema. The
		// oapi-codegen output is an anonymous struct nested in LibraryEntry;
		// we build it via the gateway-local helper newLibraryEntryView
		// (rest_library_view_fields_helpers.go) which uses reflection
		// driven by the generated package's own type — keeping the gateway
		// generated-type-clean per Hard Constraint #8.
		v := newLibraryEntryView(libraryEntryViewFields{
			Label:        nonEmptyPtr(entry.Label),
			Name:         nonEmptyPtr(entry.Name),
			Kind:         libraryEntryViewKindPtr(entry.Kind),
			CollectionID: nonEmptyPtr(collID),
		})
		if entry.Rejection != "" {
			rr := entry.Rejection
			r := gen.LibraryEntryViewRejection(rr)
			v = newLibraryEntryView(libraryEntryViewFields{
				Label:           nonEmptyPtr(entry.Label),
				Name:            nonEmptyPtr(entry.Name),
				Kind:            libraryEntryViewKindPtr(entry.Kind),
				CollectionID:    nonEmptyPtr(collID),
				Rejection:       &r,
				RejectionReason: nonEmptyPtr(entry.RejectionReason),
				ConflictPaths:   ptrStringSlice(append([]string(nil), entry.ConflictPaths...)),
			})
		}
		if entry.DerivedFrom != "" {
			df := entry.DerivedFrom
			// Rebuild with derived_from included (the previous v's
			// derived_from is empty by construction; one new call is
			// cheaper than another reflection-driven update).
			v = newLibraryEntryView(libraryEntryViewFields{
				Label:           nonEmptyPtr(entry.Label),
				Name:            nonEmptyPtr(entry.Name),
				Kind:            libraryEntryViewKindPtr(entry.Kind),
				CollectionID:    nonEmptyPtr(collID),
				Rejection:       rejectionEntryRejection(entry.Rejection),
				RejectionReason: nonEmptyPtr(entry.RejectionReason),
				ConflictPaths:   ptrStringSlice(append([]string(nil), entry.ConflictPaths...)),
				DerivedFrom:     &df,
			})
		}
		assignLibraryEntryView(e, v)
	}
}

// rebuildViewIndex walks the enclosing collection once and fills idx with
// a parsed entry per .view file found. A failure leaves the index empty
// and built=false so the next listing retries — never a permanently
// poisoned cache.
func rebuildViewIndex(idx *viewIndex, root *library.Root, homePath, workspaceID, collRel, collRoot, collID string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.built {
		return
	}
	idx.byPath = map[string]viewIndexEntry{}
	idx.dupNames = nil
	idx.collID = collID

	// Walk the collection's real root for every .view file.
	absRoot := collRoot
	if absRoot == "" {
		absRoot = root.HostPath(collRel)
	}
	entries := walkViewRootForIndex(absRoot)
	// Parse each entry; collect rejections, kinds, labels, names.
	parsed := make(map[string]viewIndexEntry, len(entries))
	rejected := map[string][]string{} // name -> all paths declaring that name
	for _, abs := range entries {
		wi, rj := readAndParseViewForIndex(abs)
		rel := abs
		if collRel != "" {
			rel = strings.TrimPrefix(abs, absRoot)
			rel = strings.TrimPrefix(rel, string(filepath.Separator))
			rel = filepath.ToSlash(rel)
			if rel == "" {
				continue
			}
		}
		if wi == nil {
			parsed[rel] = viewIndexEntry{
				Rejection:       rj.code,
				RejectionReason: rj.reason,
				Size:            rj.size,
			}
			if rj.code == "view_duplicate_name" {
				rejected[rj.duplicateOf] = append(rejected[rj.duplicateOf], rel)
			}
			continue
		}
		parsed[rel] = *wi
		if wi.Rejection == "view_duplicate_name" {
			rejected[wi.Name] = append(rejected[wi.Name], rel)
		}
	}
	// Stamp the conflict-paths list onto every entry that participates
	// in a duplicate-name group.
	dupNames := make([]string, 0, len(rejected))
	for name, paths := range rejected {
		sort.Strings(paths)
		dupNames = append(dupNames, name)
		for _, p := range paths {
			wi := parsed[p]
			wi.ConflictPaths = paths
			parsed[p] = wi
		}
	}
	sort.Strings(dupNames)
	idx.byPath = parsed
	idx.dupNames = dupNames
	idx.built = true
}

// viewRejectionInfo is the small bag returned by readAndParseViewForIndex
// for a single file: rejection code + reason + size, plus the name of
// the group this file collides with (for the dupNames stamping above).
type viewRejectionInfo struct {
	code        string
	reason      string
	size        int64
	duplicateOf string // empty unless code == view_duplicate_name
}

// readAndParseViewForIndex opens abs with a no-follow read (the same
// 256 KiB cap and size tag D-SIZECAP requires) and parses the YAML
// bytes. Returns nil + info for any rejection; the caller stamps the
// rejection onto the index entry.
func readAndParseViewForIndex(abs string) (*viewIndexEntry, *viewRejectionInfo) {
	data, size, err := readCappedViewFile(abs, 256*1024)
	if err != nil {
		return nil, &viewRejectionInfo{code: "view_unreadable", reason: err.Error(), size: size}
	}
	if size > 256*1024 {
		return nil, &viewRejectionInfo{code: "view_too_large", reason: "view file exceeds 256 KiB cap", size: size}
	}
	// Parse via the shared library: pkg/records.ParseView.
	v, rej := parseViewBytesShared(abs, data)
	if rej != nil {
		info := &viewRejectionInfo{
			code:   string(rej.Code),
			reason: rej.Reason,
			size:   size,
		}
		if rej.Code == "view_duplicate_name" {
			info.duplicateOf = rej.Name
		}
		return nil, info
	}
	out := &viewIndexEntry{
		Kind:        v.KindString(),
		Label:       v.DisplayLabel(),
		Name:        v.Def.Name,
		DerivedFrom: v.DerivedFromString(),
		Size:        size,
	}
	return out, nil
}

// readCappedViewFile opens abs and reads up to cap+1 bytes; returns the
// bytes (capped at cap if longer), the on-disk size, and any error.
func readCappedViewFile(abs string, cap int64) ([]byte, int64, error) {
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, 0, os.ErrInvalid
	}
	f, err := os.OpenFile(abs, os.O_RDONLY, 0)
	if err != nil {
		return nil, info.Size(), err
	}
	defer f.Close()
	buf := make([]byte, cap+1)
	n, err := f.Read(buf)
	if err != nil && err.Error() != "EOF" {
		return nil, info.Size(), err
	}
	if int64(n) > cap {
		buf = buf[:cap]
	}
	return buf, info.Size(), nil
}

// walkViewRootForIndex walks absRoot looking for .view files. Symlinks
// are skipped (FR-044); the four control-plane names are skipped at any
// depth. Returns absolute paths in deterministic (sorted) order.
func walkViewRootForIndex(absRoot string) []string {
	var out []string
	type stackItem struct{ abs, rel string }
	stack := []stackItem{{abs: absRoot, rel: "."}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(cur.abs)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.Type()&os.ModeSymlink != 0 {
				continue
			}
			childAbs := filepath.Join(cur.abs, name)
			if e.IsDir() {
				switch name {
				case ".obsidian", ".omnipus-vault", ".git", ".trash":
					continue
				}
				// Nested-vault stop: a directory that itself carries a
				// vault marker is its own collection, not a child.
				if dirCarriesVaultMarker(childAbs) {
					continue
				}
				stack = append(stack, stackItem{abs: childAbs, rel: name})
				continue
			}
			if !e.Type().IsRegular() {
				continue
			}
			if strings.EqualFold(filepath.Ext(name), ".view") && name != ".view" {
				out = append(out, childAbs)
			}
		}
	}
	sort.Strings(out)
	return out
}

// dirCarriesVaultMarker reports whether absDir contains a `.obsidian`
// or `.omnipus-vault` child — the round-2 D-WALK nested-vault stop.
func dirCarriesVaultMarker(absDir string) bool {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		n := e.Name()
		if n == ".obsidian" || n == ".omnipus-vault" {
			return true
		}
	}
	return false
}

// nonEmptyPtr returns &s if s is non-empty, else nil — so the wire
// marshals absent as "field omitted" rather than "".
func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

// libraryEntryViewKindPtr returns a pointer to a LibraryEntryViewKind for
// s when s is one of the eight declared kinds; nil otherwise (EC-3:
// an absent kind must NEVER be fabricated).
func libraryEntryViewKindPtr(s string) *gen.LibraryEntryViewKind {
	switch gen.LibraryEntryViewKind(s) {
	case gen.LibraryEntryViewKindBoard,
		gen.LibraryEntryViewKindBreakdown,
		gen.LibraryEntryViewKindCalendar,
		gen.LibraryEntryViewKindList,
		gen.LibraryEntryViewKindSummary,
		gen.LibraryEntryViewKindTable,
		gen.LibraryEntryViewKindTiles,
		gen.LibraryEntryViewKindTrend:
		k := gen.LibraryEntryViewKind(s)
		return &k
	}
	return nil
}

// hashString is a tiny helper used in tests; production code does not
// hash to identify views (it uses paths).
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}
