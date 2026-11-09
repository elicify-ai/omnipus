// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package component

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ProtectedFolder is one program folder agents can never get write access to
// (spec §3.2).
type ProtectedFolder struct {
	// Name is the component's name in refusal texts.
	Name string
	// Path is the folder as the slot or the registered manifest names it.
	Path string
	// Undecidable marks a slot location that could not be worked out. It
	// overlaps everything: an undecidable comparison counts as an overlap.
	Undecidable bool
}

// DisplayName is a component's name in person-facing text.
func DisplayName(kind Kind) string {
	if kind == KindOffice {
		return "LibreOffice"
	}
	return "PDF renderer"
}

// ProtectedFolders lists every program folder of both components: each
// packaged slot, whether or not anything is installed there (a packaged
// manifest is trusted by where it sits, so a folder planted in an empty slot
// would be trusted), and the root of each registered manifest. It reads the
// manifests on every call and caches nothing, because registration runs in a
// separate process. A registered manifest that cannot be read names no
// folder: that component is "damaged" and never runs (AD-4).
func ProtectedFolders(home string) []ProtectedFolder {
	resolver, err := NewResolver(home)
	var folders []ProtectedFolder
	for _, kind := range []Kind{KindOffice, KindPDFRenderer} {
		name := DisplayName(kind)
		if root, ok := registeredRoot(home, kind); ok {
			folders = append(folders, ProtectedFolder{Name: name, Path: root})
		}
		if err != nil {
			folders = append(folders, ProtectedFolder{Name: name, Path: "(unknown location: " + err.Error() + ")", Undecidable: true})
			continue
		}
		for _, slot := range resolver.PackagedCandidates(kind) {
			folders = append(folders, ProtectedFolder{Name: name, Path: slot})
		}
	}
	return folders
}

func registeredRoot(home string, kind Kind) (string, bool) {
	data, err := os.ReadFile(filepath.Join(RegisteredDir(home, kind), ManifestFile))
	if err != nil {
		return "", false
	}
	var m struct {
		Root string `json:"root"`
	}
	if json.Unmarshal(data, &m) != nil || m.Root == "" {
		return "", false
	}
	return m.Root, true
}

// FirstProtectedOverlap returns the first folder that path is inside, equal
// to, or contains, decided the way FirstOverlap decides it, or nil.
func FirstProtectedOverlap(folders []ProtectedFolder, path string) *ProtectedFolder {
	access := []WriteRoot{{Path: path}}
	for i := range folders {
		if folders[i].Undecidable || FirstOverlap(folders[i].Path, access) != nil {
			return &folders[i]
		}
	}
	return nil
}
