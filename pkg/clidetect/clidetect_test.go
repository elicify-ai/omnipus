// Unit tests for the pure-Go external-CLI path detector (spec §TDD 1-7, 26).
//
// The detector's OS seams (lookPath / evalSymlinks / stat / userHomeDir /
// currentUser / getenv / goos) are injected so every branch — $PATH hit,
// well-known fallback, precedence, symlink resolution, symlink-error fallback,
// HOME-unset passwd fallback, and Windows PATHEXT — is exercised deterministically
// on Linux CI without touching the developer's real host layout.

package clidetect

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"testing"
)

// evalTempDir returns a fresh t.TempDir(), symlink-resolved. On macOS,
// t.TempDir() lives under /var/folders/... but /var is itself a symlink to
// /private/var; detect()'s resolve() step (clidetect.go) runs every hit
// through EvalSymlinks, so a path built from the raw (unresolved) temp dir
// would never equal got.Path on macOS even though the file is identical.
// Resolving the base here up front keeps both sides of the comparison on the
// same (canonical) path. EvalSymlinks is a no-op when nothing in the path is
// a symlink (e.g. Linux CI), so this is safe everywhere.
func evalTempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	resolved, err := filepath.EvalSymlinks(d)
	if err != nil {
		return d
	}
	return resolved
}

// writeExec writes a regular file with the executable bit set and returns its
// path. Used to stand in for a real installed binary under a temp well-known dir.
func writeExec(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// baseDetector returns a detector wired to the real filesystem seams but with a
// forced goos and a lookPath/home override, so well-known scanning hits real
// temp files while $PATH and $HOME are controlled.
func baseDetector(goos string, home string, lookPath func(string) (string, error)) *detector {
	return &detector{
		lookPath:     lookPath,
		evalSymlinks: filepath.EvalSymlinks,
		stat:         os.Stat,
		userHomeDir:  func() (string, error) { return home, nil },
		currentUser:  user.Current,
		getenv:       os.Getenv,
		goos:         goos,
	}
}

func alwaysMiss(string) (string, error) {
	return "", errors.New("not found in $PATH")
}

// TestDetect_FoundOnPath: a $PATH hit yields {installed, abs path, source=path}.
func TestDetect_FoundOnPath(t *testing.T) {
	tmp := evalTempDir(t)
	bin := writeExec(t, filepath.Join(tmp, "pathbin"), "claude")
	d := baseDetector("linux", tmp, func(name string) (string, error) {
		if name == "claude" {
			return bin, nil
		}
		return "", errors.New("miss")
	})
	got := d.detect("claude-code")
	if !got.Installed {
		t.Fatalf("expected installed, got %+v", got)
	}
	if got.Source != SourcePath {
		t.Errorf("source = %q, want %q", got.Source, SourcePath)
	}
	if got.Path != bin {
		t.Errorf("path = %q, want %q", got.Path, bin)
	}
}

// TestDetect_WellKnownFallback: a $PATH miss falls back to the well-known scan
// and reports source=well-known. Uses a REAL binary under ~/.local/bin removed
// from $PATH (spec SC-001 / F-07).
func TestDetect_WellKnownFallback(t *testing.T) {
	home := evalTempDir(t)
	want := writeExec(t, filepath.Join(home, ".local", "bin"), "codex")
	d := baseDetector("linux", home, alwaysMiss)
	got := d.detect("codex")
	if !got.Installed {
		t.Fatalf("expected installed via well-known, got %+v", got)
	}
	if got.Source != SourceWellKnown {
		t.Errorf("source = %q, want %q", got.Source, SourceWellKnown)
	}
	if got.Path != want {
		t.Errorf("path = %q, want %q", got.Path, want)
	}
}

// TestDetect_PathWinsOverWellKnown: when the binary exists both on $PATH and in
// a well-known dir, the $PATH result wins (source=path) and the scan is skipped.
func TestDetect_PathWinsOverWellKnown(t *testing.T) {
	home := t.TempDir()
	// A well-known copy exists...
	writeExec(t, filepath.Join(home, ".local", "bin"), "opencode")
	// ...but $PATH resolves first.
	pathBin := writeExec(t, filepath.Join(evalTempDir(t), "pbin"), "opencode")
	d := baseDetector("linux", home, func(name string) (string, error) {
		if name == "opencode" {
			return pathBin, nil
		}
		return "", errors.New("miss")
	})
	got := d.detect("opencode")
	if got.Source != SourcePath {
		t.Fatalf("source = %q, want %q (PATH must win)", got.Source, SourcePath)
	}
	if got.Path != pathBin {
		t.Errorf("path = %q, want %q", got.Path, pathBin)
	}
}

// TestDetect_SymlinkResolved: a symlinked hit resolves to its absolute target.
func TestDetect_SymlinkResolved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	realDir := evalTempDir(t)
	realBin := writeExec(t, realDir, "claude-real")
	home := t.TempDir()
	linkDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "claude")
	if err := os.Symlink(realBin, link); err != nil {
		t.Fatal(err)
	}
	d := baseDetector("linux", home, alwaysMiss)
	got := d.detect("claude-code")
	if !got.Installed {
		t.Fatalf("expected installed, got %+v", got)
	}
	if got.Path != realBin {
		t.Errorf("path = %q, want resolved target %q", got.Path, realBin)
	}
}

// TestDetect_SymlinkErrorFallback: when EvalSymlinks errors, resolve() falls
// back to Abs of the raw hit and still reports installed (G2-11).
func TestDetect_SymlinkErrorFallback(t *testing.T) {
	tmp := t.TempDir()
	bin := writeExec(t, filepath.Join(tmp, "b"), "claude")
	d := baseDetector("linux", tmp, func(string) (string, error) { return bin, nil })
	d.evalSymlinks = func(string) (string, error) { return "", errors.New("boom") }
	got := d.detect("claude-code")
	if !got.Installed {
		t.Fatalf("EvalSymlinks error must not drop the hit, got %+v", got)
	}
	if got.Path != bin {
		t.Errorf("path = %q, want Abs of raw hit %q", got.Path, bin)
	}
}

// TestDetect_Absent: not found anywhere → {installed:false, path:"", source:""}.
func TestDetect_Absent(t *testing.T) {
	home := t.TempDir() // empty — no well-known binaries
	d := baseDetector("linux", home, alwaysMiss)
	got := d.detect("codex")
	if got.Installed || got.Path != "" || got.Source != "" {
		t.Fatalf("expected zero Result, got %+v", got)
	}
}

// TestDetect_UnknownCLI: an unknown cli identifier returns the zero Result.
func TestDetect_UnknownCLI(t *testing.T) {
	if got := Detect("gemini-cli"); got.Installed {
		t.Fatalf("unknown cli must be not-installed, got %+v", got)
	}
}

// TestDetect_HomeUnset: os.UserHomeDir failure falls back to the passwd home so
// a binary in a passwd-home well-known dir is still found (FR-016 / SC-011).
func TestDetect_HomeUnset(t *testing.T) {
	passwdHome := evalTempDir(t)
	want := writeExec(t, filepath.Join(passwdHome, ".local", "bin"), "opencode")
	d := &detector{
		lookPath:     alwaysMiss,
		evalSymlinks: filepath.EvalSymlinks,
		stat:         os.Stat,
		userHomeDir:  func() (string, error) { return "", errors.New("$HOME unset") },
		currentUser:  func() (*user.User, error) { return &user.User{HomeDir: passwdHome}, nil },
		getenv:       os.Getenv,
		goos:         "linux",
	}
	got := d.detect("opencode")
	if !got.Installed {
		t.Fatalf("expected passwd-home fallback to find binary, got %+v", got)
	}
	if got.Path != want {
		t.Errorf("path = %q, want %q", got.Path, want)
	}
}

// TestDetect_HomeUnset_NoFallbackDoesNotError: when BOTH os.UserHomeDir and the
// passwd lookup fail, detection must degrade to system dirs without erroring or
// panicking (FR-016) — a home-only binary is simply not found.
func TestDetect_HomeUnset_NoFallbackDoesNotError(t *testing.T) {
	d := &detector{
		lookPath:     alwaysMiss,
		evalSymlinks: filepath.EvalSymlinks,
		stat:         os.Stat,
		userHomeDir:  func() (string, error) { return "", errors.New("$HOME unset") },
		currentUser:  func() (*user.User, error) { return nil, errors.New("no passwd entry") },
		getenv:       os.Getenv,
		goos:         "linux",
	}
	got := d.detect("claude-code") // must not panic; simply not found
	if got.Installed {
		t.Fatalf("expected not-installed with no home, got %+v", got)
	}
}

// TestDetect_WindowsPathext: on Windows an npm `.cmd` shim in %APPDATA%\npm is
// found via PATHEXT. Runs on Linux CI by forcing goos="windows" and pointing
// APPDATA at a temp dir (spec is table-driven so Windows cases run cross-OS).
func TestDetect_WindowsPathext(t *testing.T) {
	appData := evalTempDir(t)
	npmDir := filepath.Join(appData, "npm")
	want := writeExec(t, npmDir, "claude.cmd")
	getenv := func(k string) string {
		switch k {
		case "APPDATA":
			return appData
		case "PATHEXT":
			// Lowercase ".cmd" so the generated candidate matches the npm shim
			// filename on case-sensitive Linux CI (real Windows FS is
			// case-insensitive, so the extension case is irrelevant there).
			return ".EXE;.cmd"
		default:
			return ""
		}
	}
	d := &detector{
		lookPath:     alwaysMiss,
		evalSymlinks: filepath.EvalSymlinks,
		stat:         os.Stat,
		userHomeDir:  func() (string, error) { return "", errors.New("no home") },
		currentUser:  func() (*user.User, error) { return nil, errors.New("none") },
		getenv:       getenv,
		goos:         "windows",
	}
	got := d.detect("claude-code")
	if !got.Installed {
		t.Fatalf("expected .cmd shim found via PATHEXT, got %+v", got)
	}
	if got.Path != want {
		t.Errorf("path = %q, want %q", got.Path, want)
	}
	if got.Source != SourceWellKnown {
		t.Errorf("source = %q, want %q", got.Source, SourceWellKnown)
	}
}

// TestCandidateFilenames_WindowsPathext validates the pure PATHEXT expansion.
func TestCandidateFilenames_WindowsPathext(t *testing.T) {
	d := &detector{goos: "windows", getenv: func(k string) string {
		if k == "PATHEXT" {
			return ".EXE;CMD" // second entry lacks a leading dot on purpose
		}
		return ""
	}}
	got := d.candidateFilenames("claude")
	want := []string{"claude.EXE", "claude.CMD"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Non-Windows returns the bare name.
	d2 := &detector{goos: "linux"}
	if names := d2.candidateFilenames("claude"); len(names) != 1 || names[0] != "claude" {
		t.Errorf("non-windows filenames = %v, want [claude]", names)
	}
}

// TestNewestNvmBin selects the highest node version numerically (not lexically).
func TestNewestNvmBin(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"v8.17.0", "v18.16.0", "v20.11.1", "v20.9.0"} {
		if err := os.MkdirAll(filepath.Join(home, ".nvm", "versions", "node", v, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	d := &detector{}
	got := d.newestNvmBin(home)
	want := filepath.Join(home, ".nvm", "versions", "node", "v20.11.1", "bin")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("newestNvmBin = %v, want [%s] (numeric, not lexical, ordering)", got, want)
	}
}

// TestCompareNodeVersion covers the numeric comparator directly.
func TestCompareNodeVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int // sign
	}{
		{"v20.11.1", "v8.17.0", 1},  // 20 > 8 numerically (lexical would say "2" < "8")
		{"v18.16.0", "v18.16.0", 0}, // equal
		{"v20.9.0", "v20.11.1", -1}, // 9 < 11 numerically
		{"v1.0.0", "v1.0", 1},       // longer with a trailing non-zero wins
	}
	for _, c := range cases {
		got := compareNodeVersion(c.a, c.b)
		if (got > 0) != (c.want > 0) || (got < 0) != (c.want < 0) || (got == 0) != (c.want == 0) {
			t.Errorf("compareNodeVersion(%q,%q) sign = %d, want sign %d", c.a, c.b, got, c.want)
		}
	}
}

// TestCandidateDirs_Darwin asserts the macOS candidate ordering: the Homebrew
// prefix (/opt/homebrew/bin on Apple Silicon) is present AND precedes the
// home-rooted dirs, so a brew-installed CLI is preferred over a per-user install.
func TestCandidateDirs_Darwin(t *testing.T) {
	home := t.TempDir()
	d := baseDetector("darwin", home, alwaysMiss)
	dirs := d.candidateDirs("claude")

	pos := func(want string) int {
		for i, dir := range dirs {
			if dir == want {
				return i
			}
		}
		return -1
	}

	brew := pos("/opt/homebrew/bin")
	if brew < 0 {
		t.Fatalf("darwin candidateDirs must include /opt/homebrew/bin, got %v", dirs)
	}
	homeLocal := pos(filepath.Join(home, ".local", "bin"))
	if homeLocal < 0 {
		t.Fatalf("darwin candidateDirs must include the home ~/.local/bin, got %v", dirs)
	}
	if brew >= homeLocal {
		t.Errorf("/opt/homebrew/bin (idx %d) must precede home dirs (idx %d): %v", brew, homeLocal, dirs)
	}
}

// TestDetectAll_KeysAndNoSpawn asserts DetectAll returns all three cli keys and
// (implicitly) spawns nothing — it only ever calls lookPath/stat, never exec.
func TestDetectAll_KeysAndNoSpawn(t *testing.T) {
	all := DetectAll()
	for _, k := range []string{"claude-code", "codex", "opencode"} {
		if _, ok := all[k]; !ok {
			t.Errorf("DetectAll missing key %q", k)
		}
	}
}
