// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package doctor implements the `omnipus doctor` command, which performs
// pre-flight configuration safety checks per US-15.
package doctor

import (
	"fmt"
	"os"

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
    host) — degrades silently to JPEG screenshots when unavailable`,
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
