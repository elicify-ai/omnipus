// Package clidetect resolves the on-disk location of the external-executor CLI
// binaries (claude-code / codex / opencode) used by subagent_3p agents.
//
// Detection is a PURE filesystem probe — it NEVER spawns a subprocess or shells
// out (spec FR-004). It searches the gateway process's $PATH first (via
// exec.LookPath, which honors PATHEXT on Windows), and on a miss scans a
// curated per-OS list of well-known install directories (npm-global, Homebrew,
// ~/.local/bin, the newest nvm node, bun, deno, cargo, snap). The first hit
// wins and a $PATH hit always takes precedence over a well-known hit (FR-002).
// The scan is bounded: a fixed candidate-dir list and a single-level os.Stat of
// the exact binary filename in each — no recursive walk.
//
// Security / robustness posture:
//   - No caller-supplied input reaches the filesystem here. The scanned
//     directories are a fixed per-OS list and the probed filename is the fixed
//     binary name for the requested cli, so there is no path-traversal surface
//     and detection is safe to run unauthenticated-adjacent and unaudited (it
//     executes nothing — auditing is reserved for cli-validate, which spawns).
//   - os.UserHomeDir() failure (systemd units with HOME unset) is TOLERATED: we
//     fall back to the passwd database (pure-Go when CGO is disabled) and, if
//     that also fails, skip the ~-rooted candidates rather than erroring out
//     (FR-016). Detection degrades to the system dirs; it never fails the call.
package clidetect

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Source classifies how a binary was located.
const (
	// SourcePath means the binary was resolved via the gateway process's $PATH.
	SourcePath = "path"
	// SourceWellKnown means the binary was found only by scanning the curated
	// per-OS well-known install directories.
	SourceWellKnown = "well-known"
)

// Result is the detection outcome for a single CLI. When Installed is false,
// Path and Source are the empty string.
type Result struct {
	Installed bool
	// Path is the absolute, symlink-resolved path to the binary when Installed.
	Path string
	// Source is SourcePath or SourceWellKnown when Installed, "" otherwise.
	Source string
}

// cliBinaries maps the executor `cli` value to its binary name. Keys match
// config.ExecutorConfig.CLI and gen.CliValidateRequestCli
// ("claude-code" / "codex" / "opencode").
var cliBinaries = map[string]string{
	"claude-code": "claude",
	"codex":       "codex",
	"opencode":    "opencode",
}

// SupportedCLIs returns the recognized cli identifiers in deterministic order.
func SupportedCLIs() []string {
	out := make([]string, 0, len(cliBinaries))
	for k := range cliBinaries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Detect resolves a single cli. An unknown cli returns the zero Result
// (Installed=false).
func Detect(cli string) Result {
	return newDetector().detect(cli)
}

// DetectAll resolves every supported cli, keyed by cli identifier. The map
// always contains all three keys (claude-code / codex / opencode).
func DetectAll() map[string]Result {
	d := newDetector()
	out := make(map[string]Result, len(cliBinaries))
	for cli := range cliBinaries {
		out[cli] = d.detect(cli)
	}
	return out
}

// detector holds the injectable OS seams so the scan logic is unit-testable
// without mutating global state or requiring the real host layout. The default
// detector (newDetector) wires the real os/exec/os functions.
type detector struct {
	lookPath     func(string) (string, error)
	evalSymlinks func(string) (string, error)
	stat         func(string) (os.FileInfo, error)
	userHomeDir  func() (string, error)
	currentUser  func() (*user.User, error)
	getenv       func(string) string
	goos         string
}

func newDetector() *detector {
	return &detector{
		lookPath:     exec.LookPath,
		evalSymlinks: filepath.EvalSymlinks,
		stat:         os.Stat,
		userHomeDir:  os.UserHomeDir,
		currentUser:  user.Current,
		getenv:       os.Getenv,
		goos:         runtime.GOOS,
	}
}

func (d *detector) detect(cli string) Result {
	binary, ok := cliBinaries[cli]
	if !ok {
		return Result{}
	}

	// 1. $PATH first. LookPath only returns success for a file it considers
	//    executable (and honors PATHEXT on Windows), so a hit is authoritative.
	if p, err := d.lookPath(binary); err == nil && p != "" {
		return d.resolve(p, SourcePath)
	}

	// 2. Well-known install locations. Single-level os.Stat of the exact
	//    filename in each candidate dir — bounded, no recursive walk. First hit
	//    wins (candidate dirs are ordered by precedence).
	for _, dir := range d.candidateDirs(binary) {
		for _, name := range d.candidateFilenames(binary) {
			full := filepath.Join(dir, name)
			fi, err := d.stat(full)
			if err != nil {
				continue
			}
			if !isExecutableFile(fi, d.goos) {
				continue
			}
			return d.resolve(full, SourceWellKnown)
		}
	}

	return Result{}
}

// resolve turns a located path into an absolute, symlink-resolved Result.
// EvalSymlinks is best-effort: on error (e.g. a dangling shim) we fall back to
// filepath.Abs of the raw hit and STILL report installed — the binary was found
// and is executable; only the canonicalization failed.
func (d *detector) resolve(raw, source string) Result {
	resolved := raw
	if ev, err := d.evalSymlinks(raw); err == nil && ev != "" {
		resolved = ev
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		// filepath.Abs effectively never fails, but never drop a real hit.
		abs = resolved
	}
	return Result{Installed: true, Path: abs, Source: source}
}

// isExecutableFile reports whether fi is a regular file the OS would execute.
// On Windows executability is by extension (already filtered by
// candidateFilenames via PATHEXT), so a regular file is sufficient; elsewhere at
// least one execute bit must be set. Directories, sockets, and devices are
// rejected.
func isExecutableFile(fi os.FileInfo, goos string) bool {
	if fi == nil || fi.IsDir() || !fi.Mode().IsRegular() {
		return false
	}
	if goos == "windows" {
		return true
	}
	return fi.Mode().Perm()&0o111 != 0
}

// candidateFilenames returns the filenames to probe for a binary in a candidate
// directory. On non-Windows this is just the bare name; on Windows we append
// each PATHEXT extension (defaulting to the standard set) so an npm `.cmd` shim
// or an `.exe` is found — mirroring how LookPath resolves $PATH entries.
func (d *detector) candidateFilenames(binary string) []string {
	if d.goos != "windows" {
		return []string{binary}
	}
	exts := d.pathExt()
	names := make([]string, 0, len(exts))
	for _, ext := range exts {
		names = append(names, binary+ext)
	}
	return names
}

// pathExt parses %PATHEXT% into a normalized list of extensions (each with a
// leading dot), falling back to the Windows default when unset.
func (d *detector) pathExt() []string {
	raw := d.getenv("PATHEXT")
	if strings.TrimSpace(raw) == "" {
		raw = ".COM;.EXE;.BAT;.CMD"
	}
	var out []string
	for _, e := range strings.Split(raw, ";") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out = append(out, e)
	}
	return out
}

// candidateDirs returns the ordered per-OS well-known install directories to
// scan. Order encodes precedence (first hit wins). Home-rooted candidates are
// included only when a home directory is resolvable (FR-016). `binary`
// parameterizes the Windows per-tool %LOCALAPPDATA%\Programs\<tool> directory.
func (d *detector) candidateDirs(binary string) []string {
	home, haveHome := d.homeDir()

	switch d.goos {
	case "windows":
		dirs := make([]string, 0, 3)
		if appData := d.getenv("APPDATA"); appData != "" {
			dirs = append(dirs, filepath.Join(appData, "npm"))
		}
		if local := d.getenv("LOCALAPPDATA"); local != "" {
			dirs = append(dirs, filepath.Join(local, "Programs", binary))
		}
		if pf := d.getenv("ProgramFiles"); pf != "" {
			dirs = append(dirs, filepath.Join(pf, binary))
		}
		return dirs

	case "darwin":
		dirs := []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"}
		if haveHome {
			dirs = append(dirs, d.homeBinDirs(home)...)
		}
		return dirs

	default: // linux and other unixes
		dirs := []string{"/usr/local/bin", "/usr/bin"}
		if haveHome {
			dirs = append(dirs, d.homeBinDirs(home)...)
		}
		dirs = append(dirs, "/snap/bin")
		return dirs
	}
}

// homeBinDirs returns the ~-rooted well-known bin dirs in precedence order:
// ~/.local/bin, ~/.npm-global/bin, the newest nvm node bin, then bun/deno/cargo.
func (d *detector) homeBinDirs(home string) []string {
	dirs := make([]string, 0, 6) // 2 base + up to 1 nvm + 3 tail
	dirs = append(dirs,
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
	)
	dirs = append(dirs, d.newestNvmBin(home)...)
	dirs = append(dirs,
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".deno", "bin"),
		filepath.Join(home, ".cargo", "bin"),
	)
	return dirs
}

// homeDir resolves the user's home directory, tolerating os.UserHomeDir failure
// (HOME unset under systemd). It falls back to the passwd database (pure-Go when
// CGO is disabled) and finally reports ("", false) so callers skip ~-rooted
// candidates rather than erroring (FR-016).
func (d *detector) homeDir() (string, bool) {
	if h, err := d.userHomeDir(); err == nil && strings.TrimSpace(h) != "" {
		return h, true
	}
	if d.currentUser != nil {
		if u, err := d.currentUser(); err == nil && u != nil && strings.TrimSpace(u.HomeDir) != "" {
			return u.HomeDir, true
		}
	}
	return "", false
}

// newestNvmBin returns the bin dir of the highest-versioned nvm-installed node
// (~/.nvm/versions/node/<version>/bin), or nil when nvm is absent. Versions are
// compared numerically without an external semver dependency.
func (d *detector) newestNvmBin(home string) []string {
	root := filepath.Join(home, ".nvm", "versions", "node")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	best := ""
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if best == "" || compareNodeVersion(e.Name(), best) > 0 {
			best = e.Name()
		}
	}
	if best == "" {
		return nil
	}
	return []string{filepath.Join(root, best, "bin")}
}

// compareNodeVersion compares two nvm directory names ("v18.16.0", "v20.11.1")
// numerically, component-by-component. Returns >0 when a>b, <0 when a<b, and a
// stable raw-name tie-break at 0 so selection is deterministic.
func compareNodeVersion(a, b string) int {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(as) {
			av, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(bs[i])
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return strings.Compare(a, b)
}
