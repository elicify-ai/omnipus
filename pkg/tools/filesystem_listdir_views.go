// Omnipus — list_directory's VIEW: lines: the saved views of a knowledge base
// placed in the listed folder (#1017, founder LQ2 A, 2026-11-07).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHERE VIEW LINES APPEAR, AND WHAT THEY MAY READ
//
// Founder answer Q5 A (2026-11-08): views show only inside knowledge base
// folders. Architect decision A1: only a knowledge base that is itself a
// collection of the knowledge scope contributes views. pkg/tools cannot ask
// knowledge.ResolveScope (pkg/knowledge imports this package), so the scope
// is restated structurally, the way knowledge.discoverCollections finds a
// collection:
//
//   - the listed folder lies inside the turn's work folder or a granted
//     mount — a host folder outside both gets no VIEW lines;
//   - walking down from that root, the first folder whose marker is a real
//     directory (not a link) is the collection; every folder on the way is a
//     real directory, not a link and not one of the folders the scan skips,
//     and the collection is at most scopeMaxDepth folders below the root;
//   - a knowledge base inside that collection is not a collection, so the
//     collection's own placement rule decides (and never places a view
//     inside it).
//
// Security review S1-S3: every directory and file the views load touches is
// first authorized by ResolvePath with the turn's own policy — the same
// protected-path, read-confinement and mount checks read_file applies — and
// then read through an os.Root anchored at the work folder or mount, opened
// non-blocking, regular files only, within records.ListingLoadLimits. A
// refused file is simply not shown.
// ---------------------------------------------------------------------------

// knowledgeBaseMarkerFile is the marker document inside .omnipus-vault/ that
// carries a knowledge base's display_name. A literal for the same import-cycle
// reason as KnowledgeBaseMarkerNames (pkg/knowledge imports pkg/tools); it
// mirrors pkg/knowledge's markerFileName.
const knowledgeBaseMarkerFile = "vault.json"

// scopeMaxDepth mirrors knowledge.ScopeMaxDepth: a collection is found at
// most this many folders below a work folder or mount root.
const scopeMaxDepth = 4

// scopeSkippedDirNames mirrors pkg/knowledge's scanSkippedDirNames: folders
// the collection scan never descends into.
var scopeSkippedDirNames = map[string]bool{
	".obsidian": true, ".omnipus-vault": true, ".git": true, ".trash": true,
}

// viewScope is the anchored root the VIEW lines are computed in: the listing
// handle's own os.Root, or one opened at the granted mount a host-mode
// listing resolved into.
type viewScope struct {
	ctx     context.Context
	h       *PathHandle
	root    *os.Root
	abs     string // host path the root was opened at
	listed  string // listed folder, slash-separated, relative to root ("." = root)
	ownRoot bool   // root was opened here and must be closed here
}

func openViewScope(ctx context.Context, h *PathHandle) (*viewScope, bool) {
	if h.root != nil {
		return &viewScope{ctx: ctx, h: h, root: h.root, abs: h.root.Name(), listed: path.Clean(filepath.ToSlash(h.rel))}, true
	}
	mountRoot, ok := matchedAllowedRoot(h.abs, h.policy.AllowedRoots)
	if !ok {
		return nil, false // Q5 A: a host folder outside work and mounts
	}
	rel, err := filepath.Rel(mountRoot, h.abs)
	if err != nil {
		return nil, false
	}
	root, err := os.OpenRoot(mountRoot)
	if err != nil {
		return nil, false
	}
	return &viewScope{ctx: ctx, h: h, root: root, abs: mountRoot, listed: path.Clean(filepath.ToSlash(rel)), ownRoot: true}, true
}

func (s *viewScope) close() {
	if s.ownRoot {
		_ = s.root.Close()
	}
}

func (s *viewScope) host(rel string) string {
	return filepath.Join(s.abs, filepath.FromSlash(rel))
}

// realDir reports whether rel is a directory and not a link.
func (s *viewScope) realDir(rel string) bool {
	info, err := s.root.Lstat(filepath.FromSlash(rel))
	return err == nil && info.IsDir()
}

// isCollectionRoot applies the scan's marker rule: a marker directory that
// is a real directory, never a link to one.
func (s *viewScope) isCollectionRoot(rel string) bool {
	for _, marker := range KnowledgeBaseMarkerNames {
		if s.realDir(path.Join(rel, marker)) {
			return true
		}
	}
	return false
}

// collection walks from the root down to the listed folder and returns the
// collection enclosing it and the listed folder relative to that collection.
func (s *viewScope) collection() (kbRel, folder string, ok bool) {
	var parts []string
	if s.listed != "." {
		parts = strings.Split(s.listed, "/")
	}
	cur := "."
	for depth := 0; ; depth++ {
		if s.isCollectionRoot(cur) {
			folder = records.ViewPlacementRoot
			if depth < len(parts) {
				folder = strings.Join(parts[depth:], "/")
			}
			return cur, folder, true
		}
		if depth >= len(parts) || depth >= scopeMaxDepth || scopeSkippedDirNames[parts[depth]] {
			return "", "", false
		}
		cur = path.Join(cur, parts[depth])
		if !s.realDir(cur) {
			return "", "", false
		}
	}
}

// authorize is read_file's decision for one host path: ResolvePath with the
// turn's own policy. The handle is only the verdict; the read itself goes
// through the scope's anchored root.
func (s *viewScope) authorize(op FSOp, rel string) error {
	h, err := ResolvePath(s.ctx, s.h.policy, "list_directory", "", op, s.host(rel))
	if err != nil {
		return err
	}
	return h.Close()
}

// controlFS is the records.ControlFS of one collection inside the scope.
type controlFS struct {
	s     *viewScope
	kbRel string
}

func (c controlFS) full(name string) string { return path.Join(c.kbRel, name) }

func (c controlFS) Lstat(name string) (fs.FileInfo, error) {
	return c.s.root.Lstat(filepath.FromSlash(c.full(name)))
}

func (c controlFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := c.s.authorize(FSOpList, c.full(name)); err != nil {
		return nil, err
	}
	return fs.ReadDir(c.s.root.FS(), c.full(name))
}

func (c controlFS) OpenRegular(name string) (fs.File, error) {
	if err := c.s.authorize(FSOpRead, c.full(name)); err != nil {
		return nil, err
	}
	return records.RootControlFS{Root: c.s.root}.OpenRegular(c.full(name))
}

// displayName is the knowledge base's marker display_name, else its folder
// name — the same fallback pkg/knowledge's scope discovery applies when the
// marker is absent or unreadable. The marker is read like a view file.
func (c controlFS) displayName() string {
	fallback := filepath.Base(c.s.host(c.kbRel))
	f, err := c.OpenRegular(KnowledgeBaseMarkerNames[0] + "/" + knowledgeBaseMarkerFile)
	if err != nil {
		return fallback
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, records.ListingMaxFileBytes))
	if err != nil {
		return fallback
	}
	var marker struct {
		DisplayName string `json:"display_name"`
	}
	if json.Unmarshal(raw, &marker) != nil {
		return fallback
	}
	if name := strings.TrimSpace(marker.DisplayName); name != "" {
		return name
	}
	return fallback
}

// placedViewLines returns one VIEW: line per saved view placed in the listed
// folder, using the Library listing's placement rule (records.ViewPlacement):
// a view is shown next to its source .base, otherwise at its collection's
// root. A views or schemas load failure yields no lines and a warning — it
// never fails the listing.
func placedViewLines(ctx context.Context, h *PathHandle) []string {
	s, ok := openViewScope(ctx, h)
	if !ok {
		return nil
	}
	defer s.close()
	kbRel, folder, ok := s.collection()
	if !ok {
		return nil
	}
	cfs := controlFS{s: s, kbRel: kbRel}
	kbHost := s.host(kbRel)
	views, _, err := records.LoadViewSetChecked(ctx, cfs, kbHost, records.ListingLoadLimits())
	if err != nil {
		logger.WarnCF("tools", "list_directory: saved views could not be loaded; listing without VIEW: lines",
			map[string]any{"knowledge_base": kbHost, "error": err.Error()})
		return nil
	}
	return viewLinesFor(cfs, folder, views)
}

func viewLinesFor(cfs controlFS, folder string, views *records.ViewSet) []string {
	if views.Len() == 0 {
		return nil
	}
	s := cfs.s
	dirs, kbs := map[string]bool{}, map[string]bool{}
	probe := records.ViewPlacementProbe{
		IsDir: func(rel string) bool {
			if v, seen := dirs[rel]; seen {
				return v
			}
			info, err := s.root.Stat(filepath.FromSlash(cfs.full(rel)))
			dirs[rel] = err == nil && info.IsDir()
			return dirs[rel]
		},
		IsKnowledgeBase: func(rel string) bool {
			if v, seen := kbs[rel]; seen {
				return v
			}
			kbs[rel] = false
			for _, marker := range KnowledgeBaseMarkerNames {
				info, err := s.root.Stat(filepath.FromSlash(cfs.full(path.Join(rel, marker))))
				if err == nil && info.IsDir() {
					kbs[rel] = true
				}
			}
			return kbs[rel]
		},
	}
	var lines []string
	kbName := ""
	for _, v := range views.Views() {
		if records.ViewPlacement(v.DeclaredSource(), probe) != folder {
			continue
		}
		if kbName == "" {
			kbName = cfs.displayName()
		}
		lines = append(lines, formatViewLine(v, kbName))
	}
	return lines
}

// formatViewLine renders `VIEW: <label> (kind <kind>, view <name>, knowledge
// base <name>)`. A view whose file declares no kind omits the kind part rather
// than inventing one. Every value goes through listingText, so one view is
// always exactly one line (security review S4).
func formatViewLine(v *records.SavedView, kbName string) string {
	if v.Def.Kind != nil && strings.TrimSpace(string(*v.Def.Kind)) != "" {
		return fmt.Sprintf("VIEW: %s (kind %s, view %s, knowledge base %s)",
			listingText(v.DisplayLabel()), listingText(string(*v.Def.Kind)),
			listingText(v.Name()), listingText(kbName))
	}
	return fmt.Sprintf("VIEW: %s (view %s, knowledge base %s)",
		listingText(v.DisplayLabel()), listingText(v.Name()), listingText(kbName))
}

// listingText returns s unchanged when it is safe on one listing line, and
// otherwise as a Go-quoted string ("Alpha\nFILE: x") that strconv.Unquote
// turns back into the exact value. Quoting applies when s holds a control
// character (C0, DEL, C1, which includes CR and LF), a line or paragraph
// separator (U+2028, U+2029), or starts with a quote mark, so a value that
// happens to look quoted is never mistaken for an escaped one. Ordinary
// Unicode names are left exactly as they are.
func listingText(s string) string {
	if strings.HasPrefix(s, `"`) || strings.IndexFunc(s, breaksListingLine) >= 0 {
		return strconv.Quote(s)
	}
	return s
}

func breaksListingLine(r rune) bool {
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
}
