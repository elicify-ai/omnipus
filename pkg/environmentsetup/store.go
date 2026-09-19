// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package environmentsetup

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// Shared-store layout under the Omnipus data root:
//
//	<dataRoot>/toolchains/environment/shared/
//	  published.json   additive set of published generation IDs, newest first
//	  .install.lock    cross-process publish lock (fileutil.WithFlock)
//	  <genID>/         one immutable generation directory (final path, never moved)
//
// Generation directories are allocated under their final name before an
// installation command runs; publication only adds the ID to published.json.
// Readers never enumerate the store directory — published.json decides
// visibility — so an in-flight (allocated but unpublished) directory is
// invisible until Commit.
const (
	storeRelative     = "toolchains/environment/shared"
	publishedFileName = "published.json"
	markerFileName    = ".omnipus-install.json"
	installLockName   = ".install.lock"
	storageVersion    = 1
	generationPrefix  = "gen-"
	cacheSubdirName   = "cache"
	tmpSubdirName     = "tmp"
	unixBinSubdirName = "bin"
	windowsBinSubdir  = "Scripts"
)

// SharedStoreDir returns the shared store root under the Omnipus data root.
func SharedStoreDir(dataRoot string) string {
	return filepath.Join(dataRoot, filepath.FromSlash(storeRelative))
}

// SharedGenerationDir returns the final path of one generation directory.
func SharedGenerationDir(dataRoot, generationID string) string {
	return filepath.Join(SharedStoreDir(dataRoot), generationID)
}

// SharedPublishedSetPath returns the path of the published-generation set file.
func SharedPublishedSetPath(dataRoot string) string {
	return filepath.Join(SharedStoreDir(dataRoot), publishedFileName)
}

// SharedInstallLockPath returns the cross-process publish lock path.
func SharedInstallLockPath(dataRoot string) string {
	return filepath.Join(SharedStoreDir(dataRoot), installLockName)
}

// publishedSet is the on-disk shape of published.json.
type publishedSet struct {
	Version     int      `json:"version"`
	Generations []string `json:"generations"` // newest first = PATH precedence order
}

// readPublishedSet loads and validates the published set. Only a missing
// store or set file means "nothing published" (ok=false); an unreadable,
// oversize or otherwise broken set is an ERROR, so a corrupt store can never
// be treated as an empty one and silently replaced by the next publication.
// Like all root-confined reads, a symlinked set file is refused rather than
// followed outside the store.
func readPublishedSet(storeDir string) (publishedSet, bool, error) {
	root, err := openConfinedRoot("shared store", storeDir)
	if err != nil {
		if fsErrorIsNotExist(err) {
			return publishedSet{}, false, nil // no store yet = nothing published
		}
		return publishedSet{}, false, err
	}
	defer root.Close()
	data, err := readControlFile(root, publishedFileName)
	if err != nil {
		if fsErrorIsNotExist(err) {
			return publishedSet{}, false, nil // absent set = nothing published
		}
		return publishedSet{}, false, fmt.Errorf("read %s: %w", publishedFileName, err)
	}
	var set publishedSet
	if err := json.Unmarshal(data, &set); err != nil {
		return publishedSet{}, false, fmt.Errorf("parse %s: %w", publishedFileName, err)
	}
	if set.Version != storageVersion {
		return publishedSet{}, false, fmt.Errorf("%s: unsupported storage version %d", publishedFileName, set.Version)
	}
	for _, id := range set.Generations {
		if !validGenerationID(id) {
			return publishedSet{}, false, fmt.Errorf("%s: invalid generation id %q", publishedFileName, id)
		}
	}
	return set, true, nil
}

// validGenerationID reports whether id is a safe single path component this
// package could have created (defense against hand-edited published.json).
func validGenerationID(id string) bool {
	if len(id) == 0 || len(id) > 100 || !strings.HasPrefix(id, generationPrefix) {
		return false
	}
	if strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// binSubdirsFor returns the conventional executable directory names for a
// platform in PATH-preference order. Windows gets both the venv-style
// Scripts (the prior, Python-derived convention) and the generic bin —
// arbitrary installers on Windows legitimately use either, and the tool has
// no package knowledge to pick one. Other platforms use bin.
func binSubdirsFor(goos string) []string {
	if goos == "windows" {
		return []string{windowsBinSubdir, unixBinSubdirName}
	}
	return []string{unixBinSubdirName}
}

// binSubdirs is the conventional executable directory list on this host.
func binSubdirs() []string { return binSubdirsFor(runtime.GOOS) }
