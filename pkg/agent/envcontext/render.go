package envcontext

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/dapicom-ai/omnipus/pkg/audit"
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

	sb.WriteString("### Paths you cannot use\n")
	sb.WriteString("- Everything outside the workspace above is denied unless explicitly allow-listed.\n")
	sb.WriteString("- `/dev/tty` and other TTY devices are blocked.\n")
	sb.WriteString("- System paths (`/etc`, `/usr`, `/root`, `$HOME` outside workspace) are denied.\n\n")

	sb.WriteString("### Sandbox & network\n")
	fmt.Fprintf(&sb, "- Sandbox: %s\n", sandboxMode)
	fmt.Fprintf(&sb, "- Network: %s\n", networkStr)

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
