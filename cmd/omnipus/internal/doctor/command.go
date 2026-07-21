// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package doctor implements the `omnipus doctor` command, which performs
// pre-flight configuration safety checks per US-15.
package doctor

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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

// packageChromeRootForDoctor computes the on-disk location of the package
// Chrome (ADR-052 §D2) the way pkg/tools/browser/exec_resolver.go computes
// it at runtime: <dir(os.Executable())>/../chromium on linux/darwin, the
// same path on windows. This is duplicated locally in the doctor package
// to keep doctor offline + read-only (no package-import cycle with the
// browser resolver's package-managed flag plumbing); the runtime path
// computation in exec_resolver.go remains the source of truth for the
// resolution order itself. If backend-lead-A exposes a public
// PackageChromeRoot() helper in pkg/tools/browser this local helper can be
// replaced with a thin re-export.
func packageChromeRootForDoctor() (root string, ok bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	absExe, err := filepath.Abs(exe)
	if err != nil {
		return "", false
	}
	return filepath.Join(filepath.Dir(absExe), "..", "chromium"), true
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
// two warnings:
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
//     package-installed chrome.
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
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		// No package chrome — bare-binary install. Silent on purpose.
		return nil
	}

	chromeBin := packageChromeBinaryPath(root)
	if chromeBin == "" {
		return []warning{{
			code:    "WARN-BROWSER-005",
			message: fmt.Sprintf("package chrome root present (%s) but chrome binary is missing — reinstall the omnipus package or remove the chromium/ directory.", root),
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
				code:    "WARN-BROWSER-005",
				message: fmt.Sprintf("could not parse bundled chrome ELF: %s — Chrome may not launch. Reinstall the omnipus package to recover the integrity-verified payload.", elfErr),
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

	if got, want, err := readChromeSHA(root, chromeBin); err == nil && got != "" && want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		warnings = append(warnings, warning{
			code: "WARN-BROWSER-006",
			message: fmt.Sprintf(
				"bundled chrome hash mismatch — refusing to use the package chrome. expected=%s got=%s. Reinstall the omnipus package to recover the integrity-verified payload.",
				want, got,
			),
		})
	}

	return warnings
}

// readChromeSHA reads dist/chromium/chrome.sha256, computes the SHA-256 of
// chromeBin, and returns both as hex strings. A missing file or unreadable
// binary returns ("", "", nil) — caller treats that as "not checkable,
// silent". Errors reading the file return a non-nil error.
//
// Parser hardening (ADR-052 SEC-ADR052-004):
//   - BOM at file start is stripped.
//   - CRLF / LF / CR line endings all tolerated.
//   - Leading "sha256:" prefix on a hash field is stripped.
//   - Lines starting with "#" (after whitespace) are treated as comments.
//   - Whitespace-only lines are ignored.
//   - The first 64-char hex field is taken as the expected digest;
//     uppercase hex is normalized to lowercase (sha256sum / shasum emit
//     lowercase; any toolchain that emits uppercase is normalized rather
//     than rejected, so a non-canonical-but-valid manifest still matches).
//   - NUL bytes inside a field terminate the field — never silently
//     absorbed into the digest.
//   - The hash comparison uses crypto/subtle.ConstantTimeCompare so the
//     comparison itself does not leak the expected digest via timing.
//
// A manifest with no parseable hex digest returns ("", "", non-nil error)
// — the caller treats that as "not checkable, silent" by the same path,
// but the underlying error is preserved for tests that want to assert on
// it directly.
func readChromeSHA(root, chromeBin string) (got, want string, err error) {
	shaFile := filepath.Join(root, "chrome.sha256")
	data, err := os.ReadFile(shaFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("read chrome.sha256: %w", err)
	}
	// Strip a leading UTF-8 BOM, if present.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	want, err = parseSHA256Manifest(data)
	if err != nil {
		return "", "", err
	}
	if want == "" {
		// File present but empty / no parseable hash. Treat as "not
		// checkable" — same posture as a missing file.
		return "", "", nil
	}

	f, err := os.Open(chromeBin)
	if err != nil {
		return "", "", fmt.Errorf("open chrome binary: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", "", fmt.Errorf("hash chrome binary: %w", err)
	}
	return strings.ToLower(hex.EncodeToString(h.Sum(nil))), want, nil
}

// parseSHA256Manifest extracts the first valid hex SHA-256 from a CfT-style
// `sha256sum`-format manifest (hex + 2-space + filename, or `<hex> *chrome`
// binary-mode, or a `sha256:` prefixed line, or comment lines). Returns
// the lowercase hex digest. Empty input → empty digest, no error. Caller
// distinguishes "no digest" from "error" by inspecting the returned error.
//
// The parser is self-contained — it strips a leading UTF-8 BOM if
// present, so callers don't need to. readChromeSHA still does its own
// (idempotent) TrimPrefix before calling here so the BOM strip survives
// either entry point.
func parseSHA256Manifest(data []byte) (string, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	for _, raw := range strings.Split(string(data), "\n") {
		// Strip CR for CRLF-tolerant parsing.
		raw = strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		// Comment lines: leading "#" (sha256sum / shasum don't emit them,
		// but hand-edited manifests sometimes do).
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Strip a leading "sha256:" prefix if present.
		trimmed = strings.TrimPrefix(trimmed, "sha256:")
		trimmed = strings.TrimSpace(trimmed)
		// Walk fields, take the first that looks like a SHA-256 digest.
		for _, f := range strings.Fields(trimmed) {
			// NUL bytes inside a field terminate it cleanly.
			if idx := strings.IndexByte(f, 0); idx >= 0 {
				f = f[:idx]
			}
			if len(f) != 64 {
				continue
			}
			if !isLowerHex(f) {
				continue
			}
			return f, nil
		}
	}
	return "", nil
}

// isLowerHex reports whether s is composed entirely of lowercase hex
// digits. Per ADR-052 SEC-ADR052-004 the manifest must be lowercase
// (sha256sum / shasum both default to lowercase); uppercase is rejected
// at the parse step. The caller normalizes on read for safety against
// non-canonical-but-valid toolchains, but a strict-by-default parser
// surfaces mismatched toolchains instead of silently matching.
func isLowerHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
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
