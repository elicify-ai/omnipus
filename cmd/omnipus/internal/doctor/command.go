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

	// WARN-BROWSER-004 is RETIRED with ADR-075 FR-031. It warned that
	// tools.browser.capture_shared_context was false; that key no longer
	// exists, and there is no configuration left under which the capture
	// extension cannot reach the tab. Do not reintroduce the code without a
	// condition that can actually be true.

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
// install-path multi-root candidate list and returns the first existing root.
// We keep the local helper (rather than calling browser.PackageChromeRoot)
// because the runtime's exported helper folds payload validation into the
// probe (packageChromeRootCandidateStatus requires findPackageChrome to
// resolve), whereas doctor splits validation in two: this helper returns the
// first usable ROOT dir, then checkBrowserPackageChrome separately probes for
// the binary inside it to decide between WARN-BROWSER-005/006/008. Folding
// payload validation in here would make the "root exists but the chrome
// binary is missing" case (WARN-BROWSER-008) indistinguishable from "no
// root at all" and silently skip the warning.
//
// The candidate list mirrors packageChromeRootCandidates()
// (pkg/tools/browser/package_chrome.go) per GOOS so doctor probes exactly
// where the runtime looks:
//   - linux: <exeDir>/../chromium, ../share/omnipus/chromium,
//     ../libexec/omnipus/chromium (goreleaser / install.sh FHS / nfpm).
//   - darwin: <exeDir>/../chromium (non-.app parity) and
//     <exeDir>/../../../chromium (C3 option (ii): the Google-signed sibling
//     beside the gateway .app — three levels up from Contents/MacOS/).
//
// doctor must stay offline + read-only (no import of the package-managed
// flag plumbing from the resolver), so the candidate list is inlined here
// rather than imported.
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
	var candidates []string
	if runtimepkg.GOOS == "darwin" {
		candidates = []string{
			filepath.Join(exeDir, "..", "chromium"),
			filepath.Join(exeDir, "..", "..", "..", "chromium"),
		}
	} else {
		candidates = []string{
			filepath.Join(exeDir, "..", "chromium"),
			filepath.Join(exeDir, "..", "share", "omnipus", "chromium"),
			filepath.Join(exeDir, "..", "libexec", "omnipus", "chromium"),
		}
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
// executable inside packageChromeRoot. The candidate list MUST mirror
// pkg/tools/browser/package_chrome.go's binaryLayoutsForRoot — that is the
// function the real runtime resolver (findPackageChrome, reached via
// exec_resolver.go step 3 and capability.go) uses to decide whether a
// package Chrome is usable. Before this fix this helper only recognized
// CfT's chrome-linux64/chrome extraction-subdir layout, so a package that
// ships Chrome flat at <root>/chrome (e.g. Alpine's apk chromium package,
// as staged by docker/Dockerfile.heavy's B2a layout, or install.sh's flat
// fallback) resolved and verified cleanly at runtime but doctor reported a
// FALSE WARN-BROWSER-008 ("chrome binary is missing") — the binary was
// right there, just not at the one path this helper checked. Returns "" if
// no candidate layout is present.
func packageChromeBinaryPath(root string) string {
	candidates := []string{
		filepath.Join(root, "chrome-linux64", "chrome"),
		filepath.Join(root, "chrome-headless-shell-linux64", "chrome-headless-shell"),
		filepath.Join(root, "chrome"),
		filepath.Join(root, "chrome-headless-shell"),
		filepath.Join(root, "chrome.exe"),
		filepath.Join(root, "chrome-headless-shell.exe"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
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

	// WARN-BROWSER-005: in-process dependency walker. The implementation
	// is OS-specific (ELF on Linux, Mach-O on macOS) but the WARN code
	// and the diagnostic posture are the same: surface every dependency
	// that does not resolve on the host so the operator can fix it
	// before Chrome fails to launch at runtime.
	switch runtimepkg.GOOS {
	case "linux":
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
	case "darwin":
		missing, machoErr := missingChromeLibsMachO(chromeBin)
		if w := darwinMachOWarning(missing, machoErr); w != nil {
			warnings = append(warnings, *w)
		}
	}

	if w := readChromeSHA(root, chromeBin); w != nil {
		warnings = append(warnings, *w)
	}

	return warnings
}

// darwinMachOWarning builds the WARN-BROWSER-005 diagnostic (or nil) for
// the darwin Mach-O dependency walker's result (missingChromeLibsMachO).
// Factored out of checkBrowserPackageChrome's switch, taking the parser's
// OUTPUT directly rather than the chrome binary path, so the FIX-HIGH-002
// behavior is unit-testable on any host: missingChromeLibsMachO itself is
// darwin-build-tagged (the real implementation lives in
// command_libs_darwin.go; command_libs_linux.go's copy is a compile-only
// no-op stub — see that file's doc comment), so a Linux CI host can never
// exercise the real parser end-to-end. This function has no such
// restriction — tests feed it synthetic (missing, machoErr) pairs
// directly.
//
// Three distinct outcomes, each with its own accurate message:
//
//   - machoErr != nil: a structural parse error (truncated header, bad
//     magic bytes below the recognized set, implausible offsets) — the
//     check could not run at all.
//   - missing == [notAMachOBinary]: the parser ran cleanly but the file
//     isn't a valid Mach-O — e.g. zeroed out by a failed/partial
//     extraction, a shell script staged for testing, or wrong-format
//     garbage. Before this fix this case was silently skipped (reasoning
//     the missing-libraries text would mislead), which means a corrupted
//     chrome binary reported a clean doctor bill of health — the same
//     "no signal" bug this whole fix closes. It is deliberately NOT
//     folded into the missing-libraries message below: that message names
//     real, resolvable dylib dependencies, and reusing it here would send
//     the operator hunting for libraries that don't exist.
//   - missing is a real, non-empty dependency list: the standard
//     "missing required macOS libraries" diagnostic.
func darwinMachOWarning(missing []string, machoErr error) *warning {
	if machoErr != nil {
		return &warning{
			code: "WARN-BROWSER-005",
			message: fmt.Sprintf(
				"could not parse bundled chrome Mach-O: %s — Chrome may not launch. Reinstall the omnipus package to recover the integrity-verified payload.",
				machoErr,
			),
		}
	}
	if len(missing) == 1 && missing[0] == notAMachOBinary {
		return &warning{
			code:    "WARN-BROWSER-005",
			message: "bundled chrome binary is not a valid Mach-O executable (corrupt, wrong format, or zeroed out by a failed/partial install) — Chrome will not launch. Reinstall the omnipus package to recover the integrity-verified payload.",
		}
	}
	if len(missing) > 0 {
		return &warning{
			code: "WARN-BROWSER-005",
			message: fmt.Sprintf(
				"bundled chrome is missing required macOS libraries: %s. System libraries (/usr/lib, /System/Library) ship with macOS — a missing system dylib indicates macOS needs reinstallation. Bundled frameworks ship in the .app's Frameworks directory — a missing @rpath framework indicates the omnipus package is corrupt; reinstall it to recover the integrity-verified payload. The in-process Mach-O check is the primary defense (ADR-052 SEC-ADR052-008).",
				strings.Join(missing, ", "),
			),
		}
	}
	return nil
}

// readChromeSHA delegates the manifest parse + verification to the shared
// pkg/tools/browser/chromeintegrity helpers so the doctor cannot drift
// from the runtime's tolerance rules (SEC-ADR052-004 — BOM, CRLF,
// sha256:/SHA-256: prefixes, comment lines, two-field sha256sum form,
// uppercase-hex rejection, NUL truncation, 64-char digest length).
//
// Returns the WARN-BROWSER-006 diagnostic to surface, or nil when there's
// nothing to report — verification passed, OR the manifest is missing/
// unreadable (chromeintegrity.ErrSHA256ManifestMissing — the back-compat
// bare-binary posture; the runtime falls through to download, so there's
// nothing to verify against and this stays silent).
//
// FIX-HIGH-003: chromeintegrity.VerifyChromeSHA256 can fail for several
// structurally different reasons beyond a missing manifest — a malformed
// manifest, a binary Lstat error, a SYMLINKED binary (a real security
// event per SEC-ADR052-005, not a hash disagreement), a directory where a
// file is expected, or an I/O failure while hashing — plus the genuine
// digest-mismatch case. Before this fix every one of those (other than the
// missing-manifest case) collapsed into the same "bundled chrome hash
// mismatch — expected=X got=Y" message, with the real error dropped and
// expected/got sometimes blank. That text actively misleads for a symlink
// refusal (an operator would look for bit-rot instead of a substituted
// binary) and gives no actionable detail for a parse or I/O failure.
// Classify by sentinel error so each cause gets an accurate message.
//
// SECURITY (SEC-NEW-003): chromeintegrity.VerifyChromeSHA256 does its own
// Lstat on both the manifest and the binary, refusing a symlink at the
// leaf. The caller's Lstat on `root` is the directory-level defense; the
// verifier adds the per-file defense so a tampered manifest at the leaf
// cannot pass.
func readChromeSHA(root, chromeBin string) *warning {
	shaFile := filepath.Join(root, "chrome.sha256")
	err := chromeintegrity.VerifyChromeSHA256(chromeBin, shaFile)
	if err == nil {
		return nil
	}
	if errors.Is(err, chromeintegrity.ErrSHA256ManifestMissing) {
		// Manifest missing or unreadable — bare-binary fallback posture.
		return nil
	}

	switch {
	case errors.Is(err, chromeintegrity.ErrSHA256BinarySymlink):
		return &warning{
			code: "WARN-BROWSER-006",
			message: fmt.Sprintf(
				"bundled chrome binary at %s is a symlink — refusing to verify or use it. This is a security-relevant anomaly (a substituted binary), NOT a checksum mismatch. Replace it with the real binary, or reinstall the omnipus package.",
				chromeBin,
			),
		}
	case errors.Is(err, chromeintegrity.ErrSHA256ManifestMalformed):
		return &warning{
			code: "WARN-BROWSER-006",
			message: fmt.Sprintf(
				"could not parse the chrome.sha256 integrity manifest at %s: %s. Reinstall the omnipus package to recover a valid manifest.",
				shaFile,
				err,
			),
		}
	case errors.Is(err, chromeintegrity.ErrSHA256VerificationFailed):
		return &warning{
			code: "WARN-BROWSER-006",
			message: fmt.Sprintf(
				"could not verify bundled chrome integrity: %s. Reinstall the omnipus package to recover the integrity-verified payload.",
				err,
			),
		}
	default:
		// The genuine digest-disagreement path — chromeintegrity's "sha256
		// mismatch" error carries no sentinel of its own (it IS the base
		// case). Surface both digests verbatim so the operator sees
		// exactly what's on disk vs what's expected.
		want := parseManifestDigestBestEffort(shaFile)
		got := hashChromeBinaryBestEffort(chromeBin)
		return &warning{
			code: "WARN-BROWSER-006",
			message: fmt.Sprintf(
				"bundled chrome hash mismatch — refusing to use the package chrome. expected=%s got=%s. Reinstall the omnipus package to recover the integrity-verified payload.",
				want,
				got,
			),
		}
	}
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
