// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package component

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// PackagedOfficeOptions selects the target platform of a packaged
// LibreOffice folder (build time; office-preview spec §10).
type PackagedOfficeOptions struct {
	// GOOS is "darwin", "linux" or "windows"; empty means runtime.GOOS.
	GOOS string
	// GOARCH is recorded in Manifest.Platform; empty means runtime.GOARCH.
	GOARCH string
}

// packagedOfficeApp is the app bundle's name inside a packaged macOS folder.
const packagedOfficeApp = "LibreOffice.app"

// WritePackagedOfficeManifest fingerprints the packaged LibreOffice folder
// at dir and writes dir/manifest.json with trust "packaged": schema 1,
// component "office", the version from the install's own metadata (the
// supported 26.8.x series only) and paths relative to dir, so the resolver's
// packaged slots (Resolver.PackagedCandidates) accept the folder wherever it
// is installed.
//
// Layout of dir: on Linux and Windows the LibreOffice install itself
// (program/ directly inside); on macOS the folder holding LibreOffice.app.
// On macOS it also writes dir/services/ with the AD-3-filtered .rdb files
// beside the app, never inside the signed bundle.
//
// It is a build-time step of the release pipeline (scripts/office-pack),
// never run by the gateway; nothing in dir is executed. An existing
// manifest.json, and on macOS services/, are replaced.
func WritePackagedOfficeManifest(dir string, opts PackagedOfficeOptions) error {
	goos, goarch := opts.GOOS, opts.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	root, err := realDir(dir)
	if err != nil {
		return err
	}
	locateFrom, prefix := root, ""
	if goos == "darwin" {
		// The manifest and the filtered services list sit beside the app:
		// writing into the bundle would break its signature.
		if filepath.Ext(root) == ".app" {
			return fmt.Errorf("%s is an app bundle; pass the folder that holds %s", dir, packagedOfficeApp)
		}
		locateFrom, prefix = filepath.Join(root, packagedOfficeApp), packagedOfficeApp
	}
	layout, err := locateOffice(locateFrom, goos)
	if err != nil {
		return err
	}
	want := root
	if prefix != "" {
		if want, err = filepath.EvalSymlinks(locateFrom); err != nil {
			return err
		}
	}
	if layout.root != want {
		// locateOffice also accepts a program/ folder or a parent of the
		// app; a packaged manifest must sit exactly where the resolver
		// looks for it.
		return fmt.Errorf("%s is not the LibreOffice install folder (found the install at %s)", dir, layout.root)
	}
	m, registry, err := buildOfficeManifest(layout, goos, goarch, prefix)
	if err != nil {
		return err
	}
	m.Trust = TrustPackaged
	if registry != nil {
		if err = writePackagedRegistry(filepath.Join(root, m.Registry), registry); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// The packaged folder is installed read-only for everyone; the manifest
	// carries no secret.
	return fileutil.WriteFileAtomic(filepath.Join(root, ManifestFile), append(data, '\n'), 0o644)
}

func writePackagedRegistry(regDir string, registry map[string][]byte) error {
	if err := os.RemoveAll(regDir); err != nil {
		return err
	}
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		return err
	}
	for name, data := range registry {
		if err := fileutil.WriteFileAtomic(filepath.Join(regDir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
