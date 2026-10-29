//go:build windows

package clitrust

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// allowedWindowsExtensions are the program kinds a person may set (spec §3.2).
var allowedWindowsExtensions = map[string]bool{".exe": true, ".cmd": true, ".bat": true}

// checkPathShape is the Windows shape rule: an absolute drive or UNC path, no
// alternate data stream, no 8.3 short-name component, and an admitted
// extension. Short names and streams are refused because they are second
// spellings of a file that a location check could miss.
func checkPathShape(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%q is not an absolute path; enter a full drive path (C:\\...) or a UNC path (\\\\server\\share\\...)", path)
	}
	vol := filepath.VolumeName(path)
	if strings.Contains(path[len(vol):], ":") {
		return fmt.Errorf("%q names an alternate data stream; enter the plain file path", path)
	}
	for _, c := range splitComponents(path[len(vol):]) {
		if isShortNameComponent(c) {
			return fmt.Errorf("%q uses the 8.3 short name %q; enter the full long path", path, c)
		}
	}
	ext := strings.ToLower(filepath.Ext(path))
	if !allowedWindowsExtensions[ext] {
		return fmt.Errorf("%q has the extension %q; only .exe, .cmd and .bat programs are allowed", path, ext)
	}
	return nil
}

// isShortNameComponent reports the 8.3 short-name shape: a "~" followed by a
// digit (PROGRA~1, RUNNER~1).
func isShortNameComponent(c string) bool {
	for i := 0; i+1 < len(c); i++ {
		if c[i] == '~' && c[i+1] >= '0' && c[i+1] <= '9' {
			return true
		}
	}
	return false
}

// checkStep has no Windows implementation yet: Windows permission (ACL) and
// junction checks are deferred (founder decision 2026-10-30, "Windows later";
// tracked as a follow-up issue). Windows candidates get the shape checks
// above only, and the documentation does not claim more.
func checkStep(string, string, os.FileInfo) error { return nil }

// checkFinalFile requires a regular file; the admitted extension (checked in
// checkPathShape) is what makes it executable on Windows.
func checkFinalFile(final string) error {
	fi, err := os.Lstat(final)
	if err != nil {
		return fmt.Errorf("%q cannot be read: %w", final, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", final)
	}
	return nil
}
