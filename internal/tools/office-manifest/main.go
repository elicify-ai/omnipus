// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Command office-manifest is a build-time tool of the release pipeline
// (scripts/office-pack; office-preview spec §10). It is not part of the
// omnipus binary and not a user command.
//
//	office-manifest write <folder>
//	    write the packaged LibreOffice manifest (trust "packaged") into an
//	    unpacked LibreOffice folder for this machine's platform; on macOS
//	    <folder> holds LibreOffice.app and also gets the filtered services/.
//
//	office-manifest resolve <omnipus executable>
//	    resolve both Office preview components the way the gateway does for
//	    an omnipus binary at that path, with no registered components, and
//	    fail unless both are packaged, verified and found in the expected
//	    slots. Used to check an installed tree.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "office-manifest:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: office-manifest write <folder> | resolve <omnipus executable>")
	}
	switch args[0] {
	case "write":
		if err := component.WritePackagedOfficeManifest(args[1], component.PackagedOfficeOptions{}); err != nil {
			return err
		}
		fmt.Printf("office-manifest: wrote %s\n", filepath.Join(args[1], component.ManifestFile))
		return nil
	case "resolve":
		return resolve(args[1])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func resolve(exe string) error {
	abs, err := filepath.Abs(exe)
	if err != nil {
		return err
	}
	if resolved, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = resolved
	}
	// An empty home: only the packaged slots can answer.
	home, err := os.MkdirTemp("", "office-manifest-home-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	r := &component.Resolver{Home: home, Executable: abs}
	failed := false
	for _, kind := range []component.Kind{component.KindOffice, component.KindPDFRenderer} {
		c, problem := r.Resolve(kind)
		if problem != nil {
			fmt.Printf("%s: NOT USABLE (%s); looked in %v\n", kind, problem.Reason, r.PackagedCandidates(kind))
			failed = true
			continue
		}
		if c.Manifest.Trust != component.TrustPackaged {
			fmt.Printf("%s: trust %q, want packaged\n", kind, c.Manifest.Trust)
			failed = true
			continue
		}
		fmt.Printf("%s: ready, %s, version %s, executable %s\n", kind, c.Source, c.Manifest.Version, c.Executable)
	}
	if failed {
		return fmt.Errorf("the components installed next to %s do not resolve", abs)
	}
	return nil
}
