// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package doctor implements the `omnipus doctor` command, which performs
// pre-flight configuration safety checks per US-15.
package doctor

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// NewDoctorCommand returns the cobra command for `omnipus doctor`.
func NewDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check configuration for common security and safety issues",
		Long: `doctor runs pre-flight checks on your omnipus configuration and
reports warnings about potentially unsafe settings.

Currently checks:
  - Channels with no allow_from restriction (US-15)`,
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
	warnings = append(warnings, checkPreviewPort(cfg)...)
	return warnings
}

// checkPreviewPort implements the three FR-026 doctor checks:
// (a) warn if preview_port < 1024 (privileged port, needs root),
// (b) warn if preview_port collides with a known channel port,
// (c) info when host=0.0.0.0 and public_url is unset (frame-ancestors fallback).
func checkPreviewPort(cfg *config.Config) []warning {
	var warnings []warning

	// Only check when the preview listener is enabled.
	if !cfg.Gateway.IsPreviewListenerEnabled() {
		return nil
	}

	// Resolve effective preview port (may not be computed yet if ValidateAndApplyPreviewDefaults
	// hasn't been called — doctor runs pre-boot, so derive it here for display purposes).
	previewPort := int(cfg.Gateway.PreviewPort)
	if previewPort == 0 {
		previewPort = cfg.Gateway.Port + 1
	}

	// (a) Privileged port check.
	if previewPort > 0 && previewPort < 1024 {
		// Only warn if we're not running as root. On Windows and Android, skip.
		isRoot := false
		if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
			isRoot = (os.Getuid() == 0)
		}
		if !isRoot {
			warnings = append(warnings, warning{
				code: "WARN-PREVIEW-001",
				message: fmt.Sprintf(
					"gateway.preview_port %d is a privileged port (<1024). Binding it requires root on Linux/macOS. "+
						"Set gateway.preview_port to a port ≥1024 or run as root.",
					previewPort,
				),
			})
		}
	}

	// (b) Channel port collision check.
	type channelPort struct {
		name string
		port int
	}
	knownPorts := []channelPort{
		{"channels.line.webhook_port", cfg.Channels.LINE.WebhookPort},
	}
	for _, cp := range knownPorts {
		if cp.port > 0 && cp.port == previewPort {
			warnings = append(warnings, warning{
				code: "WARN-PREVIEW-002",
				message: fmt.Sprintf(
					"gateway.preview_port %d collides with %s=%d. "+
						"Set gateway.preview_port to a different port to avoid binding conflict.",
					previewPort, cp.name, cp.port,
				),
			})
		}
	}

	// (c) Public URL advisory — frame-ancestors fallback.
	h := cfg.Gateway.Host
	if (h == "0.0.0.0" || h == "::" || h == "[::]") && cfg.Gateway.PublicURL == "" {
		warnings = append(warnings, warning{
			code: "INFO-PREVIEW-003",
			message: "gateway.host = " + h + " and gateway.public_url is unset. " +
				"frame-ancestors will fall back to '*' on /serve/ and /dev/. " +
				"Set gateway.public_url for strict embedding control.",
		})
	}

	return warnings
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
	ch := cfg.Channels

	channels := []dmChannel{
		{"Telegram", "WARN-DM-001", ch.Telegram.Enabled, ch.Telegram.AllowFrom},
		{"Discord", "WARN-DM-002", ch.Discord.Enabled, ch.Discord.AllowFrom},
		{"WhatsApp", "WARN-DM-003", ch.WhatsApp.Enabled, ch.WhatsApp.AllowFrom},
		{"Slack", "WARN-DM-004", ch.Slack.Enabled, ch.Slack.AllowFrom},
		{"LINE", "WARN-DM-005", ch.LINE.Enabled, ch.LINE.AllowFrom},
		{"OneBot", "WARN-DM-006", ch.OneBot.Enabled, ch.OneBot.AllowFrom},
		{"WeCom", "WARN-DM-007", ch.WeCom.Enabled, ch.WeCom.AllowFrom},
		{"Feishu", "WARN-DM-008", ch.Feishu.Enabled, ch.Feishu.AllowFrom},
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
