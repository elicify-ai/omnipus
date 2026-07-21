// Package doctor implements the `omnipus doctor` command, which performs
// pre-flight configuration safety checks per US-15.
//
// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package doctor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	runtimepkg "runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/elicify-ai/omnipus/cmd/omnipus/internal"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/chromeintegrity"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// NewDoctorCommand returns the cobra command for `omnipus doctor`.
func NewDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check configuration for common security and safety issues",
		Long: `doctor runs pre-flight checks on your omnipus configuration and
reports warnings about potentially unsafe settings.

Currently checks:
  - Channels with no allow_from restriction (US-15)
  - Exec tool enabled without an HTTP egress proxy (SEC-29 / FR-030)
  - Binary built without version metadata, e.g. via a plain "go build"
    instead of "make build" or an official release
  - Browser live-view WebRTC video/audio capture availability (disabled in
    config, compiled out in a lite build, or otherwise not capable on this
    host) — degrades silently to JPEG screenshots when unavailable
  - Package-bundled Chrome (ADR-052): missing required host shared libraries
    (Linux only) and a SHA-256 mismatch between the bundled chrome binary
    and its chrome.sha256 manifest. Bare-binary installs (no chromium/
    sibling) are silent — they fall back to the runtime-download path and
    are not doctor-checked here.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor()
		},
	}
}

// warning holds a single doctor finding.
type warning struct {
	code    string
	message string
}

func runDoctor() error {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return fmt.Errorf("doctor: load config: %w", err)
	}

	warnings := checkConfig(cfg)

	if len(warnings) == 0 {
		fmt.Println("omnipus doctor: all checks passed.")
		return nil
	}

	fmt.Fprintf(os.Stderr, "omnipus doctor: %d warning(s) found:\n\n", len(warnings))
	for i, w := range warnings {
		fmt.Fprintf(os.Stderr, "  [%d] %s: %s\n", i+1, w.code, w.message)
	}
	fmt.Fprintln(os.Stderr)
	// Exit with a non-zero status to make `omnipus doctor` scriptable
	os.Exit(1)
	return nil
}

// checkConfig runs all doctor checks against cfg and returns any warnings found.
func checkConfig(cfg *config.Config) []warning {
	warnings := make([]warning, 0, 8)
	warnings = append(warnings, checkDMPolicies(cfg)...)
	warnings = append(warnings, checkExecEgress(cfg)...)
	warnings = append(warnings, checkBuildIntegrity()...)
	warnings = append(warnings, checkBrowserVideoCapability(cfg)...)
	warnings = append(warnings, checkBrowserPackageChrome()...)
	return warnings
}

// checkBuildIntegrity warns when the running binary was built without the
// version metadata the Makefile injects via ldflags into pkg/config
// (Version, GitCommit, BuildTime, GoVersion — see LDFLAGS in Makefile and
// pkg/config/version.go). config.Version defaults to the literal "dev" and
// is only ever overwritten by the -X ldflags "make build" (or an official
// goreleaser release) applies, so an unset value here means the binary was
// produced by a bare "go build ./cmd/omnipus/" (or equivalent), bypassing
// that pipeline. This has shipped to production before: two binaries from
// the same repo measured 102MB (via make) vs 150MB (via plain go build) —
// the plain build also misses the -s -w strip flags — and the oversized,
// unversioned one is what was actually deployed.
func checkBuildIntegrity() []warning {
	if config.Version != "dev" {
		return nil
	}
	return []warning{
		{
			code: "WARN-BUILD-001",
			message: "This binary has no version metadata (unset — build system default \"dev\"), meaning it was " +
				"likely built with a plain `go build` instead of `make build` (or an official release/goreleaser " +
				"artifact). Rebuild with `make build`, or install an official release, so the running binary can " +
				"be traced back to a commit. An unversioned build is also missing the -s -w strip flags and can be " +
				"tens of MB larger than a proper release build.",
		},
	}
}

// checkBrowserVideoCapability warns when the WebRTC live-browser video/audio
// capture path (ADR-047/ADR-048) cannot work, so the live-view panel is
// silently degrading to JPEG screenshots instead of streaming real video —
// today the only signal is a WARN buried in the gateway log
// (pkg/gateway/browser_webrtc.go's webrtcUnavailableReason). This mirrors
// that function's WebRTCEnabled -> lite-build -> capture-capable gate
// ladder, plus the capture_shared_context precondition (ADR-048 condition
// 2) that ladder only checks via a live BrowserManager. doctor is
// offline/read-only: it never launches a browser, probes $PATH, or hits the
// network — it only classifies the already-on-disk config against the pure
// browser.ClassifyVideoCapabilityWithExec/InstallRootForProfileDir
// functions, exactly as instructed. Returns at most one warning (first
// failing precondition wins), matching webrtcUnavailableReason's own
// first-match-wins ladder.
func checkBrowserVideoCapability(cfg *config.Config) []warning {
	b := cfg.Tools.Browser

	if !b.WebRTCEnabled {
		return []warning{
			{
				code: "WARN-BROWSER-001",
				message: "Browser live-view WebRTC video/audio capture is disabled " +
					"(tools.browser.webrtc_enabled=false). The live-view panel will silently fall back to JPEG " +
					"screenshots. Set webrtc_enabled=true if you want live video/audio.",
			},
		}
	}

	if !webrtc.Available {
		return []warning{
			{
				code: "WARN-BROWSER-002",
				message: "This is a lite build: the WebRTC stack is compiled out entirely, so live-browser " +
					"video/audio can never work in this binary regardless of configuration. This is a BUILD " +
					"choice, not a config mistake — rebuild without -tags lite (e.g. `make build`) if you need " +
					"video capture.",
			},
		}
	}

	installRoot := browser.InstallRootForProfileDir(b.ProfileDir)
	videoCap := browser.ClassifyVideoCapabilityWithExec(b.ExecPath, installRoot)
	if !videoCap.Capable {
		return []warning{
			{
				code: "WARN-BROWSER-003",
				message: fmt.Sprintf(
					"Browser live-view video/audio capture is not available: %s. The live-view panel will fall "+
						"back to JPEG screenshots.",
					videoCap.Reason,
				),
			},
		}
	}

	if !b.CaptureSharedContext {
		return []warning{
			{
				code: "WARN-BROWSER-004",
				message: "Browser live-view video/audio capture requires tools.browser.capture_shared_context=true " +
					"(ADR-048): the WebRTC capture extension cannot capture a tab living in an isolated per-agent " +
					"browser context. With this disabled, the live-view panel will fall back to JPEG screenshots.",
			},
		}
	}

	return nil
}

// checkExecEgress warns when the exec tool is enabled but no HTTP proxy or
// network egress control is configured, per SEC-29 / FR-030.
func checkExecEgress(cfg *config.Config) []warning {
	exec := cfg.Tools.Exec
	if !exec.Enabled {
		return nil
	}
	if exec.EnableProxy {
		return nil
	}
	return []warning{
		{
			code:    "WARN-EXEC-001",
			message: "Exec tool is enabled without an HTTP egress proxy. Child processes can make unrestricted outbound HTTP requests. Set tools.exec.enable_proxy=true to enforce SSRF controls (SEC-29).",
		},
	}
}

// dmChannel pairs a channel's enabled/allowFrom state with its warning metadata.
type dmChannel struct {
	name      string
	code      string
	enabled   bool
	allowFrom []string
}

// checkDMPolicies checks each enabled DM-capable channel for an empty allow_from.
// Per US-15: warns when any DM channel accepts messages from anyone.
func checkDMPolicies(cfg *config.Config) []warning {
	instOf := func(key string) config.ChannelInstanceConfig {
		return cfg.Channels[key]
	}

	channels := []dmChannel{
		{"Telegram", "WARN-DM-001", instOf("telegram").Enabled, instOf("telegram").AllowFrom},
		{"Discord", "WARN-DM-002", instOf("discord").Enabled, instOf("discord").AllowFrom},
		{"WhatsApp", "WARN-DM-003", instOf("whatsapp").Enabled, instOf("whatsapp").AllowFrom},
		{"Slack", "WARN-DM-004", instOf("slack").Enabled, instOf("slack").AllowFrom},
		{"LINE", "WARN-DM-005", instOf("line").Enabled, instOf("line").AllowFrom},
		{"WeCom", "WARN-DM-007", instOf("wecom").Enabled, instOf("wecom").AllowFrom},
		{"Feishu", "WARN-DM-008", instOf("feishu").Enabled, instOf("feishu").AllowFrom},
	}

	var warnings []warning
	for _, c := range channels {
		if c.enabled && len(c.allowFrom) == 0 {
			warnings = append(warnings, warning{
				code: c.code,
				message: fmt.Sprintf(
					"%s channel accepts messages from anyone. Set allow_from to restrict access.",
					c.name,
				),
			})
		}
	}
	return warnings
}

// packageChromeRootOverride is a test seam (STYLE-007): when non-empty,
// packageChromeRootForDoctor returns it verbatim instead of resolving via
// os.Executable(). Tests set this to point at a t.TempDir()-synthesized
// chromium/ tree and restore via t.Cleanup. Mirrors the resolver's
// packageChromeRootForTest pattern (pkg/tools/browser/exec_resolver.go).
//
// Production code MUST leave this empty. There is no env-var or runtime
// knob for it — doctor stays offline and read-only.
var packageChromeRootOverride string

// packageChromeRootForDoctor computes the on-disk location of the package
// Chrome (ADR-052 §D2). It is a thin shim around the runtime's
// packageChromeRoot (pkg/tools/browser/package_chrome.go), which probes the
// install-path multi-root candidate list (<dir(exe)>/../chromium,
// ../share/omnipus/chromium, ../libexec/omnipus/chromium) and returns the
// first existing root. We keep the local helper because:
//   - the runtime function is package-private (lowercase
//     packageChromeRoot) and is being exported as
//     browser.PackageChromeRoot() by the parallel FIX-W5-1 commit
//     (CORR2-001).
//   - doctor must stay offline + read-only (no import of the
//     package-managed flag plumbing from the resolver).
//
// TODO(FIX-W5-1): once browser.PackageChromeRoot() lands, replace this
// function's body with `return browser.PackageChromeRoot(), true`. Until
// then we inline the same multi-root probe here so the doctor recognizes
// an install.sh-laid-down chromium/ at ../share/omnipus/chromium — the
// single-candidate <dir>/../chromium lookup the previous version did
// silently never fired WARN-BROWSER-005/006 on real install.sh installs.
//
// TODO(SEC-NEW2-003 follow-up): once findPackageChrome exposes a
// isHeadlessShell distinction (planned by the parallel review), have the
// doctor surface a distinct NotCapable-style reason when the package
// Chrome is a headless-shell binary (not video-capable) so operators can
// tell apart "no Chrome installed" from "Chrome installed but it's the
// headless-shell fallback, install full Chrome for video capture". Until
// then the headless-shell case reads as the same generic
// "full-Chrome build not installed yet" reason the no-chrome case uses.
//
// When packageChromeRootOverride is set (test seam only), returns it
// unconditionally with ok=true — the test owns the existence/invariants.
func packageChromeRootForDoctor() (root string, ok bool) {
	if packageChromeRootOverride != "" {
		return packageChromeRootOverride, true
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	absExe, err := filepath.Abs(exe)
	if err != nil {
		return "", false
	}
	exeDir := filepath.Dir(absExe)
	// Mirror packageChromeRootCandidates() in pkg/tools/browser/package_chrome.go.
	candidates := []string{
		filepath.Join(exeDir, "..", "chromium"),
		filepath.Join(exeDir, "..", "share", "omnipus", "chromium"),
		filepath.Join(exeDir, "..", "libexec", "omnipus", "chromium"),
	}
	for _, candidate := range candidates {
		info, err := os.Lstat(candidate)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !info.IsDir() {
			continue
		}
		if info.Mode()&0o002 != 0 {
			continue
		}
		return candidate, true
	}
	return "", false
}

// packageChromeBinaryPath returns the absolute path of the bundled chrome
// executable inside packageChromeRoot. The layout mirrors CfT's
// chrome-linux64/chrome (linux) and the macOS .app bundle
// (darwin — chrome-mac-arm64/Google Chrome for Testing.app/...). Returns
// "" if the expected binary is not present.
func packageChromeBinaryPath(root string) string {
	linuxPath := filepath.Join(root, "chrome-linux64", "chrome")
	if _, err := os.Stat(linuxPath); err == nil {
		return linuxPath
	}
	// macOS .app bundle: scan the root for the canonical binary name. The
	// matcher keeps the loop bounded — chromium/ is shallow.
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(
			root,
			e.Name(),
			"Google Chrome for Testing.app",
			"Contents",
			"MacOS",
			"Google Chrome for Testing",
		)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// checkBrowserPackageChrome (ADR-052 §D2/M2 + §1 C2 + SEC-ADR052-007) emits
// up to three diagnostic warnings, applied independently so an operator
// sees every applicable problem in a single doctor run:
//
//   - WARN-BROWSER-005 (Linux only): in-process ELF parsing of the bundled
//     chrome binary's DT_NEEDED entries (no shelling out — HC #2) against
//     the canonical library search paths. A DT_NEEDED that resolves
//     nowhere is exactly the runtime failure mode of a missing host
//     prerequisite on a minimal Linux server. Skipped on non-Linux
//     (Chrome's macOS .app bundle ships its dylibs; Windows Chrome is
//     gated to Phase 4). Implementation: command_libs_linux.go.
//
//   - WARN-BROWSER-006: hashes the bundled chrome binary, parses
//     chrome.sha256 alongside it, and warns on a mismatch — the
//     equivalent of the runtime's verifyGoogHashMD5 check applied to the
//     package-installed chrome. The parser + verifier are the shared
//     pkg/tools/browser/chromeintegrity helpers so the runtime and the
//     doctor cannot drift on tolerance rules (SEC-ADR052-004).
//
//   - WARN-BROWSER-008: package-chrome layout/path anomaly — a symlinked
//     chromium/ root (SEC-NEW-003), or a chromium/ root with the chrome
//     binary missing (the chrome payload itself is gone, distinct from
//     the "missing host libs" WARN-BROWSER-005).
//
// Primary defense (SEC-ADR052-008): WARN-BROWSER-005 is the durable
// runtime-correctness gate. The hard-coded host-libs list in install.sh
// (and apt-get / dnf invocations) is best-effort and may drift as CfT
// bumps; the ELF walk catches whatever install.sh missed, regardless of
// the install list's freshness.
//
// A bare-binary install (no chromium/ sibling next to the executable)
// returns nil — bare-binary users have no package Chrome to verify, the
// runtime falls through to download, and they get no warnings here.
func checkBrowserPackageChrome() []warning {
	root, ok := packageChromeRootForDoctor()
	if !ok {
		return nil
	}
	// SECURITY (SEC-NEW-003): use Lstat, not Stat, so a symlinked root does
	// not silently pass the directory check by resolving to the target. The
	// runtime resolver (findPackageChrome in pkg/tools/browser/exec_resolver.go)
	// uses Lstat for the same reason — refusing symlinks is the documented
	// posture. Without Lstat here, a `chrome` symlink to an attacker-controlled
	// /tmp/chrome would resolve to that directory's contents and the WARN-
	// BROWSER-006 hash check (below) would silently pass against the symlink
	// target's contents, giving a false-negative "hash OK" on a tampered
	// payload. Lstat + the symlink bit check below make that impossible: a
	// symlinked root produces a WARN-BROWSER-008 instead of a clean
	// WARN-BROWSER-006.
	//
	// Order matters: Lstat's FileInfo for a symlink-to-directory has
	// IsDir()==false (Go reports the link's mode, NOT the target's), so the
	// symlink-bit check MUST come BEFORE the IsDir check. The inverse order
	// would silently pass on a symlink.
	info, err := os.Lstat(root)
	if err != nil {
		// No package chrome — bare-binary install. Silent on purpose.
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return []warning{{
			code: "WARN-BROWSER-008",
			message: fmt.Sprintf(
				"package chrome root is a symlink (%s) — refusing to verify against a symlinked path. Replace the symlink with the real chromium/ directory.",
				root,
			),
		}}
	}
	if !info.IsDir() {
		// No package chrome — bare-binary install. Silent on purpose.
		return nil
	}

	chromeBin := packageChromeBinaryPath(root)
	if chromeBin == "" {
		// Binary absent — reserved WARN code for "package chrome binary
		// absent", separate from WARN-BROWSER-005 which is the ldd-style
		// "missing host shared libraries" diagnostic. Same severity, distinct
		// remediation: WARN-BROWSER-005 = install host libs; WARN-BROWSER-008
		// = reinstall the omnipus package (the chrome payload itself is gone).
		return []warning{{
			code: "WARN-BROWSER-008",
			message: fmt.Sprintf(
				"package chrome root present (%s) but chrome binary is missing — reinstall the omnipus package or remove the chromium/ directory.",
				root,
			),
		}}
	}

	var warnings []warning

	if runtimepkg.GOOS == "linux" {
		missing, elfErr := missingChromeLibsELF(chromeBin)
		if elfErr != nil {
			// Structural error reading the ELF — not the same as a clean
			// "all libs present" result. Surface a WARN so the operator
			// knows doctor couldn't run the check rather than seeing
			// silence.
			warnings = append(warnings, warning{
				code: "WARN-BROWSER-005",
				message: fmt.Sprintf(
					"could not parse bundled chrome ELF: %s — Chrome may not launch. Reinstall the omnipus package to recover the integrity-verified payload.",
					elfErr,
				),
			})
		} else if len(missing) > 0 {
			warnings = append(warnings, warning{
				code: "WARN-BROWSER-005",
				message: fmt.Sprintf(
					"bundled chrome is missing host shared libraries: %s. Install via `apt-get install %s` (Debian/Ubuntu) or `dnf install %s` (RHEL/Fedora) — see ADR-052 C2. The in-process ELF check is the primary defense; install.sh's host-libs list is best-effort and may need regenerating per Chrome release (ADR-052 SEC-ADR052-008).",
					strings.Join(missing, ", "),
					strings.Join(debianMissingLibs(missing), " "),
					strings.Join(rpmMissingLibs(missing), " "),
				),
			})
		}
	}

	if mismatch, got, want := readChromeSHA(root, chromeBin); mismatch {
		warnings = append(warnings, warning{
			code: "WARN-BROWSER-006",
			message: fmt.Sprintf(
				"bundled chrome hash mismatch — refusing to use the package chrome. expected=%s got=%s. Reinstall the omnipus package to recover the integrity-verified payload.",
				want,
				got,
			),
		})
	}

	return warnings
}

// readChromeSHA delegates the manifest parse to the shared
// pkg/tools/browser/chromeintegrity helpers so the doctor cannot drift
// from the runtime's tolerance rules (SEC-ADR052-004 — BOM, CRLF,
// sha256:/SHA-256: prefixes, comment lines, two-field sha256sum form,
// uppercase-hex rejection, NUL truncation, 64-char digest length).
//
// Returns (mismatch=true, got, want) when the binary's actual SHA-256
// differs from the manifest's declared digest. Returns (false, "", "")
// for any "not checkable" condition — a missing manifest
// (chromeintegrity.ErrSHA256ManifestMissing), an unparseable manifest,
// a missing binary. The caller treats these as silent (bare-binary
// users have no package Chrome to verify; the runtime falls through to
// download). Real verifier errors (parse failures, hash mismatches) are
// surfaced as WARN-BROWSER-006 with got/want both populated when
// possible.
//
// SECURITY (SEC-NEW-003): chromeintegrity.VerifyChromeSHA256 does its
// own Lstat on both the manifest and the binary, refusing a symlink at
// the leaf. The caller's Lstat on `root` is the directory-level defense;
// the verifier adds the per-file defense so a tampered manifest at the
// leaf cannot pass.
func readChromeSHA(root, chromeBin string) (mismatch bool, got, want string) {
	shaFile := filepath.Join(root, "chrome.sha256")
	err := chromeintegrity.VerifyChromeSHA256(chromeBin, shaFile)
	if err == nil {
		return false, "", ""
	}
	if errors.Is(err, chromeintegrity.ErrSHA256ManifestMissing) {
		// Manifest missing or unreadable — bare-binary fallback posture.
		return false, "", ""
	}
	// Real mismatch / parse failure. Surface got/want so the operator sees
	// both digests verbatim. Best-effort: any of these I/O calls failing
	// leaves the corresponding field blank rather than failing closed.
	want = parseManifestDigestBestEffort(shaFile)
	if g := hashChromeBinaryBestEffort(chromeBin); g != "" {
		got = g
	}
	return true, got, want
}

// parseManifestDigestBestEffort returns the manifest's declared digest
// (lowercase hex) or "" if the manifest is missing/unparseable. Best-effort:
// callers must not fail closed on this returning "".
func parseManifestDigestBestEffort(shaFile string) string {
	data, err := os.ReadFile(shaFile)
	if err != nil {
		return ""
	}
	digest, err := chromeintegrity.ParseChromeSHA256Manifest(data)
	if err != nil {
		return ""
	}
	return digest
}

// hashChromeBinaryBestEffort streams the chrome binary through
// crypto/sha256 and returns the lowercase-hex digest. Returns "" on any
// I/O error — callers must not fail closed on this.
func hashChromeBinaryBestEffort(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// debianMissingLibs maps the set of missing ldd basenames (e.g.
// "libnss3.so", "libgbm.so.1") to the apt package names install.sh would
// use. Best-effort: anything not in the table is omitted from the hint.
func debianMissingLibs(missing []string) []string {
	pkgByLib := map[string]string{
		"libnss3.so":           "libnss3",
		"libnspr4.so":          "libnspr4",
		"libatk-1.0.so.0":      "libatk1.0-0",
		"libatk-bridge-2.0.so": "libatk-bridge2.0-0",
		"libcups.so.2":         "libcups2",
		"libdrm.so.2":          "libdrm2",
		"libgbm.so.1":          "libgbm1",
		"libxkbcommon.so.0":    "libxkbcommon0",
		"libXcomposite.so.1":   "libxcomposite1",
		"libXdamage.so.1":      "libxdamage1",
		"libXrandr.so.2":       "libxrandr2",
		"libxshmfence.so.1":    "libxshmfence1",
		"libasound.so.2":       "libasound2",
		"libpango-1.0.so.0":    "libpango-1.0-0",
		"libcairo.so.2":        "libcairo2",
	}
	seen := map[string]bool{}
	var out []string
	for _, lib := range missing {
		if pkg, ok := pkgByLib[lib]; ok && !seen[pkg] {
			out = append(out, pkg)
			seen[pkg] = true
		}
	}
	return out
}

// rpmMissingLibs mirrors debianMissingLibs for the dnf / rpm package set.
func rpmMissingLibs(missing []string) []string {
	pkgByLib := map[string]string{
		"libnss3.so":           "nss",
		"libnspr4.so":          "nspr",
		"libatk-1.0.so.0":      "atk",
		"libatk-bridge-2.0.so": "at-spi2-atk",
		"libcups.so.2":         "cups-libs",
		"libdrm.so.2":          "libdrm",
		"libgbm.so.1":          "mesa-libgbm",
		"libxkbcommon.so.0":    "libxkbcommon",
		"libXcomposite.so.1":   "libXcomposite",
		"libXdamage.so.1":      "libXdamage",
		"libXrandr.so.2":       "libXrandr",
		"libxshmfence.so.1":    "libxshmfence",
		"libasound.so.2":       "alsa-lib",
		"libpango-1.0.so.0":    "pango",
		"libcairo.so.2":        "cairo",
	}
	seen := map[string]bool{}
	var out []string
	for _, lib := range missing {
		if pkg, ok := pkgByLib[lib]; ok && !seen[pkg] {
			out = append(out, pkg)
			seen[pkg] = true
		}
	}
	return out
}
