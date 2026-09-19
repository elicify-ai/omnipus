// Package browser implements the `omnipus browser` subcommand tree.
//
// Squad K (founder ruling 2026-09-19, contract — installer route only):
// the gateway's live-view capture browser must come via the managed
// installer (EnsureChromium / EnsureChromiumBuild), never the
// Playwright cache. The CI provisioning step needs an explicit
// NON-INTERACTIVE entry point onto that install path so a fresh runner
// can materialise the gateway's managed Chrome before any test runs
// without having to drive a full gateway boot through Preprovision.
//
// This package provides that entry point. `omnipus browser provision`
// invokes the same code path the gateway's Preprovision invokes for
// the managed install (browser.EnsureChromiumFullBuild) — explicitly
// requesting the full "chrome" CfT build, which is the WebRTC
// tabCapture-capable default on linux per capability.go:35-48 — prints
// the resolved binary path on success, and exits non-zero with the
// LOUD error the install path now returns on missing manifest /
// missing platform / integrity failure. The step never prints secret
// material: only the resolved binary path, the version, and the
// install root.
//
// It is intentionally a subcommand tree (`omnipus browser <verb>`)
// rather than a flat command so future per-verb commands — `inspect`,
// `diagnose`, `clean` — can land in the same package without
// reshuffling cobra registration. The subcommand tree is registered
// by NewBrowserCommand; each leaf is its own constructor following
// the pattern set by cmd/omnipus/internal/{doctor,onboard,run,stop}.
package browser

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/elicify-ai/omnipus/pkg/app/internal"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// NewBrowserCommand returns the cobra command tree for `omnipus browser`.
// It is registered into the main omnipus command by cmd/omnipus/main.go.
func NewBrowserCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "browser",
		Short: "Operate on the managed browser install (provision, inspect, …)",
		Long: `browser is a subcommand tree for offline maintenance of the
managed Chromium install the gateway's live-view pipeline depends on.

The 'provision' leaf is what CI runs before any Playwright E2E: it
calls the SAME managed install path the gateway's Preprovision calls
(browser.EnsureChromiumFullBuild) and prints the resolved binary on
success. It is intentionally minimal — no interactivity, no doctor
output, no side effects beyond the install. See the leaf's own
Long for the full contract.`,
	}

	cmd.AddCommand(newProvisionCommand())
	return cmd
}

// newProvisionCommand constructs the `omnipus browser provision` leaf.
// Squad K: this leaf is what `.github/workflows/pr.yml` (Playwright E2E
// job) calls to materialise the gateway's managed Chrome BEFORE any
// test runs. The fix shape the prior round proposed (calling
// `omnipus doctor` to drive the install) was wrong: doctor never
// installs, exits 1 on any warning, and is wiped by the next
// pre-existing step that runs `rm -rf /tmp/omnipus-e2e`. This leaf
// is the actual installer entry point the brief's "cmd/ entry points
// the installer needs" carve-out allows.
func newProvisionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "provision",
		Short: "Ensure the gateway's managed full-Chrome build is installed",
		Long: `provision ensures the managed "full" Chrome-for-Testing build is
present under the gateway's managed install root, downloading it
from chrome-for-testing if necessary.

This is the OFFLINE / NON-INTERACTIVE installer entry point CI uses
before any Playwright E2E runs, so the gateway's Preprovision does
not pay the download cost on the first spec. It is deliberately
the SAME install path the gateway uses at runtime
(browser.EnsureChromiumFullBuild, which WebRTC tabCapture requires —
capability.go:35-48) so a successful run here guarantees a successful
gateway launch on the same install root.

Exit code:
  0 — full Chrome is installed; resolved binary path printed to stdout.
  1 — install failed (no network, manifest missing the requested
      build, integrity mismatch, disk error). The error is printed
      to stderr verbatim.

It never prints secret material. A 64-hex placeholder master key is
acceptable — the command only needs an OMNIPUS_HOME; the install path
itself does not decrypt anything.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runProvision(cmd.Context())
		},
	}
}

// runProvision is the body of `omnipus browser provision`. It loads
// the gateway config to determine ProfileDir, derives the managed
// install root the same way the gateway's BrowserManager does
// (browser.InstallRootForProfileDir), and calls
// browser.EnsureChromiumFullBuild — the same call Preprovision
// would make for the install on linux. On success it prints
// "exec_path=<abs path>" so callers (and humans) can verify the
// install without parsing stderr.
//
// The CWD the command runs from does not matter: the install root is
// derived from OMNIPUS_HOME (or DefaultConfig()'s home fallback), not
// from any path-relative computation. This is what makes the leaf
// safe to call from a CI runner that may have a non-empty
// /tmp/omnipus-e2e from a prior run.
func runProvision(ctx context.Context) error {
	// LoadConfig is the single source of truth for ProfileDir — same
	// path the gateway reads at boot. The command's own install root
	// is therefore guaranteed to match the install root the gateway
	// will inspect once it boots. If ProfileDir is empty in the
	// config, LoadConfig's defaults (env > home) take over and the
	// gateway's DefaultConfig() default kicks in identically at boot.
	cfg, err := internal.LoadConfig()
	if err != nil {
		return fmt.Errorf("browser provision: load config: %w", err)
	}

	// Derive the install root the gateway's BrowserManager.InstallRoot()
	// would compute — explicit, no relative-path ambiguity, no
	// dependency on the caller's CWD.
	installRoot, err := browser.EffectiveInstallRoot(cfg.Tools.Browser.ProfileDir)
	if err != nil {
		return fmt.Errorf("browser provision: resolve install root: %w", err)
	}

	// EnsureChromiumFullBuild is the same call the gateway makes
	// during Preprovision (pkg/tools/browser/exec_resolver.go:728
	// via EnsureChromium / selectDownloadBuild's linux→fullChrome
	// path). It is also the EXPLICIT "give me the full build"
	// entry point — the per-build seam Squad E's brief explicitly
	// carved out for offline installers, so CI's invocation is the
	// direct, no-fallback version. A failure here is loud (the
	// install path's "manifest missing" / "no platform zip" branches
	// now return errors, not silent headless-shell swaps), and the
	// loud error is what propagates as the leaf's exit 1.
	binPath, err := browser.EnsureChromiumFullBuild(ctx, installRoot)
	if err != nil {
		return fmt.Errorf("browser provision: install full Chrome build under %s: %w", installRoot, err)
	}

	// Print the resolved path. The "exec_path=" prefix is grep-friendly
	// for callers that want to capture the path without parsing
	// arbitrary stderr noise. No secret material, no env dump, no
	// URL — exactly the two facts a downstream step needs.
	if _, err := fmt.Fprintf(os.Stdout, "exec_path=%s\n", binPath); err != nil {
		// stdout-write failures are not the install's problem; the
		// install succeeded, surface the path on stderr as well so
		// the caller can still observe it via whichever stream is
		// being captured.
		fmt.Fprintf(os.Stderr, "exec_path=%s\n", binPath)
	}
	return nil
}

// errBrowserProvisionFailed is exported so tests can assert on the
// exit-code path without string-matching the error message. Reserved
// for the case where EnsureChromiumFullBuild's underlying error
// type is wrapped by a future caller — currently unused (we surface
// the install error verbatim) but kept as the documented hook for a
// future "exit 2 on platform-unsupported" split. Lint-safe no-op
// when unused.
var _ = errBrowserProvisionFailed

var errBrowserProvisionFailed = errors.New("browser provision: failed")
