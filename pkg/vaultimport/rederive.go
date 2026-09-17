// Omnipus — re-deriving ONE `.base` file's views after a raw text edit
// (D-119's index half, spec in FIX2-REPORT-index-find.md).
//
// WHY THIS FILE EXISTS. Views a user sees are the YAML files under
// <vault>/.omnipus-vault/views/, and before this entry the ONLY writer was
// the whole-vault import (run.go): records.LoadViews reads that directory and
// nothing else, and the `.base` a view came from is never opened again on any
// query path. So the raw-text editor the Library hands the operator for a
// `.base` was write-only — valid view YAML typed in, saved, verified on disk,
// and no view appeared. This is the single-base re-derivation that closes
// exactly that gap, called by the Library save door after the bytes land.
//
// ONE TRANSLATOR, NOT A SECOND ONE. The whole pipeline TranslateBase already
// runs (parse → resolve → render, with FR-105's named-loss machinery) is
// reused verbatim: this file supplies the three inputs that pipeline takes
// (parsed base, SchemaIndex, SlugRegistry) and then files what it produced.
// Nothing here re-reads a `.base`'s shape, re-decides a filter, or re-renders
// a view — a divergence between "what an import would write" and "what a
// re-derivation writes" is precisely the silent-second-implementation
// failure this package is written against.
//
// WHAT DIFFERS FROM AN IMPORT, DELIBERATELY:
//
//   - The schemas are the CURRENT ON-DISK schemas (records.LoadSchemas), not
//     a fresh inference pass. The vault has real schemas now; re-deriving a
//     view against re-inferred ones would let the translation disagree with
//     what records.ValidateViewAgainstSchemas accepts at load time.
//   - Slugs of views whose `source` is this `.base` are PRESERVED (the
//     operator's view file keeps its name across edits), and NEW names are
//     collision-checked against the loaded ViewSet so a re-derivation can
//     never overwrite another source's view.
//   - View files this source no longer declares are DELETED — but only on a
//     translation that did not refuse. A `.base` that no longer parses, or
//     whose every view fails to translate, keeps the previously written
//     views exactly as they were: destroying the last working view over a
//     refused edit would trade a stale view for no view, silently.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// RederiveBaseResult is the account of one re-derivation: what the edited
// `.base` produced on disk, so the calling door can log it and a test can
// assert it, without either re-reading the views directory to infer what
// happened.
type RederiveBaseResult struct {
	// BaseRelPath is the vault-relative `.base` path that was re-derived.
	BaseRelPath string
	// Status is the rolled-up BaseOutcome status. OutcomeRefused means
	// NOTHING was written or deleted — see this file's header for why a
	// refusal is non-destructive.
	Status Outcome
	// RefusedReason is set only when Status == OutcomeRefused.
	RefusedReason string
	// Views carries the per-view outcomes (losses, refusals, produced paths)
	// for the caller that wants to report more than the rolled-up status.
	Views []ViewOutcome
	// Written lists the view slugs whose YAML was written (new or changed).
	Written []string
	// Unchanged lists the view slugs whose re-derived YAML was byte-identical
	// to the file already on disk, which was therefore left alone.
	Unchanged []string
	// Deleted lists the view slugs whose files were removed because this
	// source no longer declares them.
	Deleted []string
	// KeptExisting lists the view slugs from this source that were left in
	// place because the translation refused (nothing is deleted on a refusal).
	KeptExisting []string
}

// RederiveBase re-translates one `.base` file's bytes as they stand on disk
// and files the views they declare, preserving this source's existing view
// slugs and deleting this source's views the edited file no longer declares.
//
// vaultRoot is the collection root; baseRelPath is vault-relative,
// slash-separated (the same spelling a produced view's `source:` carries, so
// the two can be matched).
//
// An error means the re-derivation could not even be attempted or could not
// file its output (the `.base` unreadable, the schema directory unreadable, a
// view file unwritable) — an infrastructure failure, distinct from a
// REFUSAL, which is a verdict about the base's content and comes back as
// Status == OutcomeRefused with the write on disk untouched.
// resolveViewWritePath verifies a view file's absolute path against the vault
// root with the knowledge layer's no-symlink rule (CollectionRoot.
// ResolveControlWritePath), so a control folder symlinked outside the vault
// refuses the write instead of being followed.
func resolveViewWritePath(vaultRoot, abs string) (string, error) {
	root, err := knowledge.NewCollectionRoot(knowledge.OSLinkFS(), vaultRoot)
	if err != nil {
		return "", fmt.Errorf("resolveViewWritePath: %w", err)
	}
	// Strip the caller's spelling of the root (on macOS /var is a link to
	// /private/var, and NewCollectionRoot resolved it), then run the
	// no-symlink check in the resolved root's space.
	rel, rerr := filepath.Rel(filepath.Clean(vaultRoot), filepath.Clean(abs))
	if rerr != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("vaultimport: %q is not under %q", abs, vaultRoot)
	}
	resolved, err := root.ResolveContainedNoSymlink(knowledge.OSLinkFS(), filepath.ToSlash(rel))
	if err != nil {
		return "", fmt.Errorf("resolveViewWritePath: %w", err)
	}
	return resolved, nil
}

func RederiveBase(vaultRoot, baseRelPath string) (*RederiveBaseResult, error) {
	abs := filepath.Join(vaultRoot, filepath.FromSlash(baseRelPath))
	data, err := knowledge.ReadNoteContent(nil, abs)
	if err != nil {
		return nil, fmt.Errorf("vaultimport: reading the base file %q: %w", baseRelPath, err)
	}

	// The CURRENT on-disk schemas — the same set records.LoadViews validates
	// against below and every query resolves against at serve time. Re-inferring
	// here would let a view be written that the loader immediately rejects.
	schemaSet, _, err := records.LoadSchemas(vaultRoot)
	if err != nil {
		return nil, fmt.Errorf("vaultimport: loading the schemas under %q: %w", vaultRoot, err)
	}
	schemaIdx := schemaIndexFromSchemas(schemaSet)

	// The views as they stand, loaded through the REAL loader so preservation
	// and collision-checking operate on exactly what the product serves.
	existing, _, err := records.LoadViews(vaultRoot, schemaSet)
	if err != nil {
		return nil, fmt.Errorf("vaultimport: loading the views under %q: %w", vaultRoot, err)
	}

	// Seed the ONE registry the translator takes: every slug another source
	// holds is reserved against new names, and every slug this source holds is
	// pinned to the view name that owns it, so an edited base keeps its files.
	slugs := NewSlugRegistry()
	var mine []string
	for _, v := range existing.Views() {
		if v.DeclaredSource() == baseRelPath {
			slugs.Pin(baseRelPath, v.DisplayLabel(), v.Name())
			mine = append(mine, v.Name())
			continue
		}
		slugs.Reserve(v.Name())
	}

	pb, parseErr := ParseBaseFile(data)
	if parseErr == nil {
		return fileTranslatedBase(vaultRoot, baseRelPath, pb, schemaIdx, slugs, mine)
	}
	// A base that no longer parses is a REFUSAL about content, not an
	// infrastructure error: the existing views stay, and the caller is
	// told why in the operator's own terms. KeptExisting is populated
	// because the refusal's own contract is to NAME what it deliberately
	// left on disk — an operator told only "refused" cannot tell a
	// non-destructive refusal from one that deleted their views.
	return &RederiveBaseResult{
		BaseRelPath:   baseRelPath,
		Status:        OutcomeRefused,
		RefusedReason: parseErr.Error(),
		KeptExisting:  mine,
	}, nil
}

// fileTranslatedBase is the parsable half of RederiveBase: translate the
// parsed base, write the views it declares, and delete this source's views
// the edited base no longer declares. Its error returns are the
// infrastructure failures RederiveBase's contract reserves the error channel
// for; a refusal is a verdict about the base's content and comes back as
// Status == OutcomeRefused — here via the translator, and for an unparsable
// base via RederiveBase's own return above.
func fileTranslatedBase(vaultRoot, baseRelPath string, pb *ParsedBase, schemaIdx *SchemaIndex, slugs *SlugRegistry, mine []string) (*RederiveBaseResult, error) {
	outcome, produced := TranslateBase(pb, baseRelPath, schemaIdx, slugs)

	res := &RederiveBaseResult{
		BaseRelPath:   baseRelPath,
		Status:        outcome.Status,
		RefusedReason: outcome.RefusedReason,
		Views:         outcome.Views,
	}
	if outcome.Status == OutcomeRefused {
		// Non-destructive by contract: a refused translation leaves every
		// existing view from this source exactly where it was.
		res.KeptExisting = mine
		return res, nil
	}

	viewsDir := records.ViewsDir(vaultRoot)
	producedSlugs := map[string]struct{}{}
	for _, pv := range produced {
		slug := filepath.Base(pv.RelPath)
		slug = slug[:len(slug)-len(filepath.Ext(slug))]
		producedSlugs[slug] = struct{}{}

		path := filepath.Join(viewsDir, filepath.Base(pv.RelPath))
		// D-14 follow-up: viewsDir is a control-folder path joined as text; a
		// symlinked .omnipus-vault/views would land this write outside the
		// vault. Verify with the knowledge layer's no-symlink rule first.
		if _, vErr := resolveViewWritePath(vaultRoot, path); vErr != nil {
			return nil, fmt.Errorf("vaultimport: view write refused: %w", vErr)
		}
		if current, rerr := os.ReadFile(path); rerr == nil && string(current) == string(pv.Bytes) {
			res.Unchanged = append(res.Unchanged, slug)
			continue
		}
		if werr := fileutil.WriteFileAtomic(path, pv.Bytes, generatedFilePerm); werr != nil {
			return nil, fmt.Errorf("vaultimport: writing view %q: %w", path, werr)
		}
		res.Written = append(res.Written, slug)
	}

	// Files this source no longer declares go, now that the translation
	// succeeded — the exact set a full re-import of this base would not
	// re-write, made explicit rather than left as an orphan an operator can
	// only find by listing the directory.
	for _, slug := range mine {
		if _, still := producedSlugs[slug]; still {
			continue
		}
		delPath := filepath.Join(viewsDir, slug+".yaml")
		if _, vErr := resolveViewWritePath(vaultRoot, delPath); vErr != nil {
			return nil, fmt.Errorf("vaultimport: view delete refused: %w", vErr)
		}
		if derr := os.Remove(delPath); derr != nil && !os.IsNotExist(derr) {
			return nil, fmt.Errorf("vaultimport: removing the view this base no longer declares (%s.yaml): %w", slug, derr)
		}
		res.Deleted = append(res.Deleted, slug)
	}

	sort.Strings(res.Written)
	sort.Strings(res.Unchanged)
	sort.Strings(res.Deleted)
	return res, nil
}

// schemaIndexFromSchemas builds the translator's SchemaIndex over the
// CURRENT on-disk schemas — the same declaration surface records and the
// query engine resolve against, flattened into the InferredProperty shape
// the translator consumes.
//
// The honesty-payload fields of InferredProperty (Ambiguity, NameEvidenced,
// …) stay nil: they are accounts of an INFERENCE decision, and this
// constructor reads a DECIDED schema, where no such decision was made. The
// translator never reads them; it reads Name/Type/Many/Required/To/
// EnumValues, all of which a loaded schema carries.
func schemaIndexFromSchemas(set *records.SchemaSet) *SchemaIndex {
	idx := &SchemaIndex{byType: map[string]map[string]InferredProperty{}}
	for _, typeName := range set.Types() {
		sc, ok := set.Get(typeName)
		if !ok {
			continue
		}
		m := map[string]InferredProperty{}
		for _, name := range sc.PropertyOrder {
			p, ok := sc.Property(name)
			if !ok || p == nil {
				continue
			}
			m[name] = InferredProperty{
				Name:       name,
				Type:       p.Type,
				Many:       p.Many,
				Required:   p.Required,
				To:         p.To,
				EnumValues: enumValuesOf(p),
			}
		}
		idx.byType[typeName] = m
	}
	return idx
}

// enumValuesOf renders a declared enum property's closed set in the
// InferredProperty shape, nil for every other type — matching what InferSchema
// leaves on a non-enum property, so the translator's enum-literal checks see
// the same shape from a decided schema as from an inferred one.
func enumValuesOf(p *records.Property) []string {
	if p.Type != records.TypeEnum {
		return nil
	}
	values := make([]string, 0, len(p.Values))
	for _, v := range p.Values {
		values = append(values, v.Name)
	}
	return values
}
