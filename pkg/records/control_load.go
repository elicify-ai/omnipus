// Omnipus — checked, bounded loading of a knowledge base's record schemas
// and saved views for implicit callers (#1017: views shown in listings).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// WHY A SECOND ENTRY POINT
//
// LoadSchemas and LoadViews read host paths with os.ReadDir/os.ReadFile. That
// is right for a caller that is changing a knowledge base and must see every
// file in it (knowledge_configure, knowledge_edit, the importer). It is not
// right for a caller that SERVES saved views to a reader: a Library or agent
// listing, the views and base-views endpoints, opening a view, vault search,
// knowledge_find and knowledge_describe (#1017, team-lead decision
// 2026-11-08). Such a caller must not read anything the reader it serves may
// not read, must not follow links out of the knowledge base, and must not let
// one planted file make the answer slow, large or stuck.
//
// LoadControlChecked therefore reads only through a ControlFS the caller
// builds on its own checked handle (LoadControlCheckedAt: an os.Root at the
// knowledge base), refuses links and non-regular files, and stays inside
// ListingLoadLimits. Every serving caller uses the same limits, so the set a
// listing shows and the set the endpoints serve are one set. Parsing, duplicate and schema checks are the
// same code LoadSchemas/LoadViews run (loadSchemaPaths/loadViewPaths), so a
// file that loads here loads identically there.
// ---------------------------------------------------------------------------

// Limits for serving saved views (team-lead product decisions, 2026-11-08, from
// security review S3 of #1017). One named place: every serving caller uses
// ListingLoadLimits, and nothing else sets these numbers.
const (
	// ListingMaxFileBytes is the most read from one schema or view file.
	ListingMaxFileBytes int64 = 256 << 10 // 256 KiB
	// ListingMaxCandidates is the most candidate files read from one
	// records or views directory.
	ListingMaxCandidates = 128
	// ListingMaxTotalBytes is the most read from all schema and view files
	// of one load (one listing).
	ListingMaxTotalBytes int64 = 4 << 20 // 4 MiB
)

// LoadLimits bounds one LoadControlChecked call.
type LoadLimits struct {
	MaxFileBytes  int64
	MaxCandidates int
	MaxTotalBytes int64
}

// ListingLoadLimits returns the product limits above as a LoadLimits.
func ListingLoadLimits() LoadLimits {
	return LoadLimits{
		MaxFileBytes:  ListingMaxFileBytes,
		MaxCandidates: ListingMaxCandidates,
		MaxTotalBytes: ListingMaxTotalBytes,
	}
}

// ControlFS is a knowledge base seen through a caller's own checked I/O.
// Every name is slash-separated and relative to the knowledge base root
// (for example ".omnipus-vault/views/open.yaml").
type ControlFS interface {
	// Lstat describes name without following a final symbolic link.
	Lstat(name string) (fs.FileInfo, error)
	// ReadDir lists the directory name.
	ReadDir(name string) ([]fs.DirEntry, error)
	// OpenRegular opens name for reading without ever blocking, and returns
	// it only when the opened object is a regular file the caller's reader
	// may read.
	OpenRegular(name string) (fs.File, error)
}

// Reasons a candidate file is skipped. Each becomes the file's per-file
// "unreadable" rejection, so the views endpoint still counts and names it
// (L13) while a listing simply does not show it.
var (
	errCandidateNotRegular = errors.New("it is a link or a special file, and only regular files are loaded")
	errCandidateTooMany    = fmt.Errorf("the folder holds more than %d candidate files, and the rest are not loaded", ListingMaxCandidates)
	errCandidateTooLarge   = fmt.Errorf("it is larger than %d KiB, the limit for one file", ListingMaxFileBytes>>10)
	errBudgetSpent         = fmt.Errorf("the %d MiB limit for one load was already used by other files", ListingMaxTotalBytes>>20)
)

// CheckedControl is what one checked load read: the record schemas, and the
// saved views validated against them, each with its load report.
type CheckedControl struct {
	Schemas      *SchemaSet
	SchemaReport *SchemaLoadReport
	Views        *ViewSet
	ViewReport   *ViewLoadReport
}

// LoadControlChecked loads a knowledge base's record schemas and then its
// saved views, validated against those schemas, through cfs and within
// limits. vaultRoot is used only to name files in reports (SavedView.
// SourcePath, rejection paths); nothing is opened through it.
//
// A missing marker, records or views directory is an empty set, exactly as
// in LoadSchemas/LoadViews. A control directory that is a link or not a
// directory, a directory cfs refuses, or a cancelled ctx is an error: the
// caller logs it and shows no views. A refused, oversized or surplus file is
// a per-file rejection, never an error.
func LoadControlChecked(ctx context.Context, cfs ControlFS, vaultRoot string, limits LoadLimits) (CheckedControl, error) {
	reader := &checkedReader{ctx: ctx, cfs: cfs, vaultRoot: vaultRoot, limits: limits, remaining: limits.MaxTotalBytes}

	schemaPaths, err := reader.candidates(VaultMarkerDirName + "/" + RecordsDirName)
	if err != nil {
		return CheckedControl{}, fmt.Errorf("reading schema directory: %w", err)
	}
	schemas, schemaReport, err := loadSchemaPaths(schemaPaths, reader.read)
	if err != nil {
		return CheckedControl{}, fmt.Errorf("loading record schemas: %w", err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return CheckedControl{}, ctxErr
	}

	viewPaths, err := reader.candidates(VaultMarkerDirName + "/" + ViewsDirName)
	if err != nil {
		return CheckedControl{}, fmt.Errorf("reading views directory: %w", err)
	}
	views, viewReport, err := loadViewPaths(viewPaths, schemas, reader.read)
	if err != nil {
		return CheckedControl{}, fmt.Errorf("loading saved views: %w", err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return CheckedControl{}, ctxErr
	}
	return CheckedControl{Schemas: schemas, SchemaReport: schemaReport, Views: views, ViewReport: viewReport}, nil
}

// LoadViewSetChecked is LoadControlChecked for a caller that needs only the
// saved views and their report.
func LoadViewSetChecked(ctx context.Context, cfs ControlFS, vaultRoot string, limits LoadLimits) (*ViewSet, *ViewLoadReport, error) {
	c, err := LoadControlChecked(ctx, cfs, vaultRoot, limits)
	if err != nil {
		return nil, nil, err
	}
	return c.Views, c.ViewReport, nil
}

// checkedReader carries one load's ControlFS, limits and remaining budget.
// paths handed to the shared loaders are report paths under vaultRoot; byPath
// maps each back to its relative name and its directory-entry facts.
type checkedReader struct {
	ctx       context.Context
	cfs       ControlFS
	vaultRoot string
	limits    LoadLimits
	remaining int64
	byPath    map[string]candidate
}

type candidate struct {
	rel     string
	regular bool // the directory entry itself (not followed) is a regular file
	surplus bool // beyond MaxCandidates in its directory
}

// candidates lists dir's schema/view candidates by the same name rule as
// LoadSchemas/LoadViews (no directories, no dot files, .yaml/.yml only), in
// sorted order, after checking that the marker directory and dir are real
// directories rather than links.
func (r *checkedReader) candidates(dir string) ([]string, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	for _, d := range []string{VaultMarkerDirName, dir} {
		info, err := r.cfs.Lstat(d)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s is a link; control folders are read only when they are real folders", d)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s is not a folder", d)
		}
	}
	entries, err := r.cfs.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	if r.byPath == nil {
		r.byPath = map[string]candidate{}
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !isControlCandidateName(e.Name()) {
			continue
		}
		rel := path.Join(dir, e.Name())
		p := filepath.Join(r.vaultRoot, filepath.FromSlash(rel))
		r.byPath[p] = candidate{
			rel:     rel,
			regular: e.Type().IsRegular(),
			surplus: len(paths) >= r.limits.MaxCandidates,
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

// isControlCandidateName is the LoadSchemas/LoadViews name rule: no dot
// files (the .seq allocator state lives beside the schemas), .yaml or .yml.
func isControlCandidateName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

// read is the shared loaders' read function: it opens one candidate through
// the ControlFS and reads at most the per-file limit and the remaining
// budget. Every refusal is returned as an error, which the shared loaders
// turn into that file's "unreadable" rejection.
func (r *checkedReader) read(p string) ([]byte, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	c, known := r.byPath[p]
	switch {
	case !known:
		return nil, fmt.Errorf("%s was not listed by this load", p)
	case c.surplus:
		return nil, errCandidateTooMany
	case !c.regular:
		return nil, errCandidateNotRegular
	case r.remaining <= 0:
		return nil, errBudgetSpent
	}
	f, err := r.cfs.OpenRegular(c.rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	limit := min(r.limits.MaxFileBytes, r.remaining)
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	r.remaining -= int64(len(data))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		if limit < r.limits.MaxFileBytes {
			return nil, errBudgetSpent
		}
		return nil, errCandidateTooLarge
	}
	return data, nil
}
