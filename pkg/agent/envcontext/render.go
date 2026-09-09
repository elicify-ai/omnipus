package envcontext

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// maxPreambleRunes is the hard cap on rendered preamble length (FR-050).
const maxPreambleRunes = 2000

// truncationSuffix is appended when the rendered preamble exceeds the cap.
const truncationSuffix = "\n\n[env context truncated]"

// defaultRedactor is a package-level Redactor used by render(). Constructed
// once at package init; panic on bad pattern is acceptable since patterns are
// hardcoded.
var defaultRedactor = func() *audit.Redactor {
	r, err := audit.NewRedactor(nil)
	if err != nil {
		panic(fmt.Sprintf("envcontext: failed to build default redactor: %v", err))
	}
	return r
}()

// render is the lane-E implementation. It builds the ## Environment preamble
// from the Provider's runtime state. Field-level errors degrade the affected
// field to "<unknown>" without aborting the render (FR-054, CRIT-005).
// The result is redacted (FR-055) and capped at 2000 runes (FR-050).
func render(p Provider, workspaceOverride string) string {
	// Pentest override: emit no preamble at all so the LLM is not steered by
	// any path/sandbox/network guidance. Kernel-level enforcement is unchanged.
	if os.Getenv("OMNIPUS_PENTEST_HIDE_SANDBOX") == "1" {
		return ""
	}

	// Resolve workspace: override wins, then provider's own value.
	workspace := workspaceOverride
	if workspace == "" {
		workspace = p.WorkspacePath()
	}
	omnipusHome := p.OmnipusHome()

	// Sandbox mode.
	sandboxMode, err := p.SandboxMode()
	if err != nil {
		slog.Debug("envcontext: field unreadable", "field", "sandbox_mode", "err", err)
		sandboxMode = "<unknown>"
	}

	// Network policy.
	netPolicy := p.NetworkPolicy()
	networkStr := "outbound-denied"
	if netPolicy.OutboundAllowed {
		networkStr = "outbound-allowed"
	}

	// Active warnings (may be empty).
	warnings := p.ActiveWarnings()

	var sb strings.Builder

	sb.WriteString("## Environment\n\n")
	sb.WriteString(
		"You are running inside the Omnipus agent harness. Read this once; it tells you where you can work and where you cannot.\n\n",
	)

	sb.WriteString("### Paths you can use\n")
	fmt.Fprintf(&sb, "- Workspace (your working directory): %s\n", workspace)
	fmt.Fprintf(&sb, "- Omnipus home (framework data; read-only unless specified): %s\n\n", omnipusHome)

	sb.WriteString(pathsCannotUseSection(sandboxMode))

	sb.WriteString("### Sandbox & network\n")
	fmt.Fprintf(&sb, "- Sandbox: %s\n", sandboxMode)
	fmt.Fprintf(&sb, "- Network: %s\n", networkStr)

	// Public URL: only rendered when the provider can derive one (e.g. a
	// wildcard bind with no gateway.public_url configured yields ""). Told
	// apart from Workspace/Omnipus-home above because it is the one path
	// item that's a URL, not a filesystem path — it's where pages the agent
	// serves (e.g. via serve_web) are reachable from OUTSIDE the sandbox.
	if publicURL := strings.TrimSpace(p.PublicURL()); publicURL != "" {
		fmt.Fprintf(&sb,
			"- Public URL (externally reachable base for pages you serve, e.g. via serve_web): %s\n",
			publicURL)
	}

	if len(warnings) > 0 {
		sb.WriteString("\n### Active warnings\n")
		for _, w := range warnings {
			fmt.Fprintf(&sb, "- %s\n", w)
		}
	}

	result := sb.String()

	// Redact any accidental secret-looking content (FR-055, MIN-002).
	result = defaultRedactor.Redact(result)

	// Cap at 2000 runes. If the result exceeds the cap, truncate cleanly and
	// append the truncation marker. The marker may push the total slightly
	// beyond 2000 runes, but the rune cap applies to the content before the
	// marker (FR-050).
	if utf8.RuneCountInString(result) > maxPreambleRunes {
		runes := []rune(result)
		result = string(runes[:maxPreambleRunes]) + truncationSuffix
	}

	return result
}

// pathsCannotUseSection renders the "### Paths you cannot use" block with
// wording calibrated to the resolved sandbox mode (finding 6, context-audit
// 2026-08). Previously this stated an absolute denial unconditionally,
// regardless of whether the sandbox was kernel-enforced, running in
// application-level fallback, or off entirely (sandbox.mode: off is an
// explicitly supported operator choice) — telling the agent "denied" is
// actively misleading when there is no kernel actually stopping the access.
//
// mode is the exact string renderSandboxMode/DefaultProvider.SandboxMode
// produce: "off", "landlock-abi-<n>", "seatbelt" (all kernel-enforced or
// genuinely absent), "fallback" (application-level only), or "unknown"/
// "<unknown>" (SandboxMode() errored — treated conservatively, same as
// fallback).
func pathsCannotUseSection(mode string) string {
	var sb strings.Builder
	sb.WriteString("### Paths you cannot use\n")

	switch mode {
	case "off":
		sb.WriteString("- No sandbox is active (sandbox.mode = off) — nothing below is kernel-enforced. Treat the workspace boundary as a rule the operator expects you to follow, not a guarantee the system will stop you from breaking it.\n")
		sb.WriteString("- Stay inside the workspace above. Avoid `/dev/tty` and other TTY devices, and avoid system paths (`/etc`, `/usr`, `/root`, `$HOME` outside workspace) unless the operator has explicitly asked you to touch them.\n\n")
	case "fallback", "unknown", "<unknown>":
		sb.WriteString("- Enforcement here is APPLICATION-level, not kernel-level — the items below are rules to follow, not guarantees the system will stop a violation.\n")
		sb.WriteString("- Everything outside the workspace above should be treated as denied unless explicitly allow-listed.\n")
		sb.WriteString("- Treat `/dev/tty` and other TTY devices as off-limits.\n")
		sb.WriteString("- Treat system paths (`/etc`, `/usr`, `/root`, `$HOME` outside workspace) as denied.\n\n")
	default: // kernel-enforced: landlock-abi-<n>, seatbelt
		sb.WriteString("- Everything outside the workspace above is denied unless explicitly allow-listed.\n")
		sb.WriteString("- `/dev/tty` and other TTY devices are blocked.\n")
		sb.WriteString("- System paths (`/etc`, `/usr`, `/root`, `$HOME` outside workspace) are denied.\n\n")
	}

	return sb.String()
}
