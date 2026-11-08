// Omnipus — list_directory's VIEW: lines: the saved views of a knowledge base
// placed in the listed folder (#1017, founder LQ2 A, 2026-11-07).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// knowledgeBaseMarkerFile is the marker document inside .omnipus-vault/ that
// carries a knowledge base's display_name. A literal for the same import-cycle
// reason as KnowledgeBaseMarkerNames (pkg/knowledge imports pkg/tools); it
// mirrors pkg/knowledge's markerFileName.
const knowledgeBaseMarkerFile = "vault.json"

// listdirLocator addresses folders through the listed directory's own
// confinement: slash-separated paths relative to the handle's os.Root when the
// handle is confined, absolute host paths (each re-checked against the
// carve-outs) when it is not. Nothing is ever reached that the handle itself
// could not reach.
type listdirLocator struct {
	h *PathHandle
}

func (l listdirLocator) confined() bool { return l.h.root != nil }

// start is the listed directory's own location.
func (l listdirLocator) start() string {
	if l.confined() {
		return filepath.ToSlash(l.h.rel)
	}
	return l.h.abs
}

// parent returns loc's parent, or ok=false at the top of the confinement.
func (l listdirLocator) parent(loc string) (string, bool) {
	if l.confined() {
		if loc == "." || loc == "" {
			return "", false
		}
		return path.Dir(loc), true
	}
	p := filepath.Dir(loc)
	return p, p != loc
}

func (l listdirLocator) join(loc, rel string) string {
	if rel == "" || rel == "." {
		return loc
	}
	if l.confined() {
		return path.Join(loc, rel)
	}
	return filepath.Join(loc, filepath.FromSlash(rel))
}

// host is the host path of loc, for the record loaders (which read host
// paths, exactly as the knowledge tools and the REST views endpoint do).
func (l listdirLocator) host(loc string) string {
	if l.confined() {
		return filepath.Join(l.h.root.Name(), filepath.FromSlash(loc))
	}
	return loc
}

func (l listdirLocator) isDir(loc string) bool {
	if l.confined() {
		info, err := l.h.root.Stat(filepath.FromSlash(loc))
		return err == nil && info.IsDir()
	}
	if fspolicy.IsCarveOut(loc, l.h.policy) {
		return false
	}
	info, err := os.Stat(loc)
	return err == nil && info.IsDir()
}

func (l listdirLocator) readFile(loc string) ([]byte, error) {
	if l.confined() {
		return l.h.root.ReadFile(filepath.FromSlash(loc))
	}
	if fspolicy.IsCarveOut(loc, l.h.policy) {
		return nil, ErrCarveOut
	}
	return os.ReadFile(loc)
}

// isKnowledgeBase applies the KB: rule (either marker directory present) to
// a folder.
func (l listdirLocator) isKnowledgeBase(loc string) bool {
	for _, marker := range KnowledgeBaseMarkerNames {
		if l.isDir(l.join(loc, marker)) {
			return true
		}
	}
	return false
}

// displayName is the knowledge base's marker display_name, else its folder
// name — the same fallback pkg/knowledge's scope discovery applies when the
// marker is absent or unreadable.
func (l listdirLocator) displayName(kbLoc string) string {
	fallback := filepath.Base(l.host(kbLoc))
	raw, err := l.readFile(l.join(kbLoc, KnowledgeBaseMarkerNames[0]+"/"+knowledgeBaseMarkerFile))
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
// a view is shown next to its source .base, otherwise at its knowledge base's
// root. The knowledge base is the deepest one enclosing the listed folder
// within the handle's confinement. A views or schemas load failure yields no
// lines and a warning — it never fails the listing.
func placedViewLines(h *PathHandle) []string {
	loc := listdirLocator{h: h}
	listed := loc.start()
	kbLoc, found := "", false
	for cur := listed; ; {
		if loc.isKnowledgeBase(cur) {
			kbLoc, found = cur, true
			break
		}
		next, ok := loc.parent(cur)
		if !ok {
			break
		}
		cur = next
	}
	if !found {
		return nil
	}
	folder := records.ViewPlacementRoot
	if listed != kbLoc {
		rel, err := filepath.Rel(filepath.FromSlash(kbLoc), filepath.FromSlash(listed))
		if err != nil {
			return nil
		}
		folder = filepath.ToSlash(rel)
	}

	kbHost := loc.host(kbLoc)
	schemas, _, err := records.LoadSchemas(kbHost)
	if err == nil {
		var views *records.ViewSet
		views, _, err = records.LoadViews(kbHost, schemas)
		if err == nil {
			return viewLinesFor(loc, kbLoc, folder, views)
		}
	}
	logger.WarnCF("tools", "list_directory: saved views could not be loaded; listing without VIEW: lines",
		map[string]any{"knowledge_base": kbHost, "error": err.Error()})
	return nil
}

func viewLinesFor(loc listdirLocator, kbLoc, folder string, views *records.ViewSet) []string {
	if views.Len() == 0 {
		return nil
	}
	dirs, kbs := map[string]bool{}, map[string]bool{}
	probe := records.ViewPlacementProbe{
		IsDir: func(rel string) bool {
			if v, seen := dirs[rel]; seen {
				return v
			}
			dirs[rel] = loc.isDir(loc.join(kbLoc, rel))
			return dirs[rel]
		},
		IsKnowledgeBase: func(rel string) bool {
			if v, seen := kbs[rel]; seen {
				return v
			}
			kbs[rel] = loc.isKnowledgeBase(loc.join(kbLoc, rel))
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
			kbName = loc.displayName(kbLoc)
		}
		lines = append(lines, formatViewLine(v, kbName))
	}
	return lines
}

// formatViewLine renders `VIEW: <label> (kind <kind>, view <name>, knowledge
// base <name>)`. A view whose file declares no kind omits the kind part rather
// than inventing one.
func formatViewLine(v *records.SavedView, kbName string) string {
	if v.Def.Kind != nil && strings.TrimSpace(string(*v.Def.Kind)) != "" {
		return fmt.Sprintf("VIEW: %s (kind %s, view %s, knowledge base %s)",
			v.DisplayLabel(), string(*v.Def.Kind), v.Name(), kbName)
	}
	return fmt.Sprintf("VIEW: %s (view %s, knowledge base %s)", v.DisplayLabel(), v.Name(), kbName)
}
