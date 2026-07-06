package envcontext

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// renderSandboxMode converts a sandbox.Status into the human-readable string
// that appears in the env preamble. The mapping is pinned in spec v7 and must
// not be changed without a corresponding spec update.
//
//   - none / unavailable          → "off"
//   - linux backend, kernel-level, ABI version > 0 → "landlock-abi-<n>"
//   - linux backend, no kernel-level enforcement    → "fallback"
//   - fallback backend                              → "fallback"
//   - anything else                                 → "unknown"
func renderSandboxMode(status sandbox.Status) string {
	switch {
	case status.Backend == "none" && !status.Available:
		return "off"
	case status.Backend == "linux" && status.KernelLevel && status.ABIVersion > 0:
		return fmt.Sprintf("landlock-abi-%d", status.ABIVersion)
	case status.Backend == "fallback",
		status.Backend == "linux" && !status.KernelLevel:
		return "fallback"
	default:
		return "unknown"
	}
}
