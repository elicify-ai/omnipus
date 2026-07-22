//go:build darwin

// Package doctor - darwin Mach-O dependency walker for WARN-BROWSER-005
// (ADR-052 Phase 3, SEC-ADR052-007). The macOS counterpart to the Linux
// ELF walker in command_libs_linux.go.
//
// Chrome-for-Testing on macOS ships as a .app bundle that includes its
// own frameworks and dylibs in the Contents/Frameworks directory. The
// system libraries (/usr/lib/libSystem.B.dylib, /System/Library/*) are
// provided by macOS itself. This walker:
//
//  1. Opens the chrome binary as a Mach-O via parseMachODylibs (the
//     OS-agnostic byte parser in command_libs_macho.go).
//  2. For each dylib name found in an LC_LOAD_DYLIB load command, decides
//     whether it is present on this macOS host:
//     - @rpath/, @executable_path/, @loader_path/ prefixes are treated as
//     always-present - the .app bundle resolves these to its Frameworks
//     directory at runtime. If a bundled framework is actually missing,
//     the chrome binary will fail to launch and WARN-BROWSER-006 (hash
//     mismatch) or the launch failure itself is the diagnostic.
//     - Absolute paths (/usr/lib/libSystem.B.dylib etc.) are checked via
//     os.Stat. A missing system dylib indicates a corrupted macOS install.
//     - Bare names (rare on macOS) are checked against darwinSearchPaths.
//
// HC #2 (pure Go): no otool shell-out. Mirrors the ELF parser's no-ldd
// rule. otool requires Xcode Command Line Tools which may not be installed;
// the pure-Go parser keeps the check deterministic, offline, and CI-safe.
package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// darwinSearchPaths are the canonical macOS library directories probed
// in order for bare dylib names (rare - most macOS dylibs are referenced
// by absolute path or @rpath/ prefix). Mirrors the ELF parser's
// elfSearchPaths concept.
var darwinSearchPaths = []string{
	"/usr/lib",
	"/System/Library/Frameworks",
	"/System/Library/PrivateFrameworks",
	"/System/Library/Extensions",
	"/Library/Frameworks",
}

// missingChromeLibsMachO walks binPath's Mach-O LC_LOAD_DYLIB entries and
// returns the name of every dependency that is NOT present on the host.
// An empty/nil result means every dependency resolved. A non-Mach-O file
// returns a single synthetic entry ("not-a-macho-binary") so the warning
// surfaces the diagnostic instead of silently passing - mirroring the
// ELF parser's "not-an-elf-binary" contract.
//
// The parser is intentionally conservative: any structural error
// (truncated header, bad magic, implausible offset) returns a non-nil
// error with a descriptive message so the caller can decide whether to
// surface a warning or stay silent.
func missingChromeLibsMachO(binPath string) ([]string, error) {
	f, err := os.Open(binPath)
	if err != nil {
		return nil, fmt.Errorf("read chrome binary: %w", err)
	}
	defer f.Close()

	dylibs, err := parseMachODylibs(f)
	if err != nil {
		return nil, fmt.Errorf("parse mach-o: %w", err)
	}

	var missing []string
	for _, name := range dylibs {
		if name == notAMachOBinary {
			// Synthetic entry from the parser - propagate as-is.
			return []string{name}, nil
		}
		if !dylibResolves(name) {
			missing = append(missing, name)
		}
	}
	return missing, nil
}

// dylibResolves returns true if the named dylib is present on this macOS
// host. The resolution logic mirrors what dyld does at launch time for
// the subset of cases that matter for the doctor check:
//
//   - @rpath/, @executable_path/, @loader_path/ prefixes: treated as
//     always-present. The .app bundle ships its frameworks with these
//     prefixes resolved by the bundle's @rpath entries. If a bundled
//     framework is actually missing, Chrome will fail to launch at
//     runtime - the doctor check does not model the bundle's rpath
//     resolution (that would require parsing the @rpath load commands
//     and resolving each path, which is installer plumbing not a
//     correctness gate - same rationale as the ELF parser not modeling
//     RUNPATH/RPATH).
//   - Absolute paths: checked directly via os.Stat.
//   - Bare names: checked against darwinSearchPaths (rare on macOS).
func dylibResolves(name string) bool {
	// Bundle-relative prefixes: the .app bundle resolves these.
	// @rpath/X.dylib -> bundled framework in Contents/Frameworks
	// @executable_path/X.dylib -> relative to the executable
	// @loader_path/X.dylib -> relative to the loading binary
	if strings.HasPrefix(name, "@rpath/") ||
		strings.HasPrefix(name, "@executable_path/") ||
		strings.HasPrefix(name, "@loader_path/") {
		return true
	}

	// Absolute path - check directly.
	if filepath.IsAbs(name) {
		if _, err := os.Stat(name); err == nil {
			return true
		}
		return false
	}

	// Bare name - check canonical macOS search paths (rare).
	for _, dir := range darwinSearchPaths {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return true
		}
	}
	return false
}

// missingChromeLibsELF is a no-op stub on darwin. The ELF parser is
// Linux-only; this stub exists so that command_test.go (which has no
// build tag and references missingChromeLibsELF) compiles on darwin.
// The runtime GOOS gate in checkBrowserPackageChrome prevents this from
// being called on darwin, but the symbol must exist for compilation.
func missingChromeLibsELF(binPath string) ([]string, error) {
	_ = binPath
	return nil, nil
}
