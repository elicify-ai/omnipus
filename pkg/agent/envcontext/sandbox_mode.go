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
//   - seatbelt backend (macOS kernel sandbox)       → "seatbelt"
//   - anything else                                 → "unknown"
//
// The "seatbelt" row was added by ADR-052 Phase-3 AC-6, together with the
// matching spec update. Before darwin had a kernel backend it always reported
// "fallback"; the moment selectBackendPlatform started returning "seatbelt"
// this switch fell through to "unknown", telling every macOS agent its sandbox
// state was unknown while it was actually kernel-confined. Any future backend
// name must add a row here AND to the spec, or it inherits the same bug.
//
// There is no "seatbelt-abi-<n>" form: Seatbelt exposes no versioned ABI to
// probe, so ABIVersion stays 0 on darwin and carries no information.
func renderSandboxMode(status sandbox.Status) string {
	switch {
	case status.Backend == "none" && !status.Available:
		return "off"
	case status.Backend == "linux" && status.KernelLevel && status.ABIVersion > 0:
		return fmt.Sprintf("landlock-abi-%d", status.ABIVersion)
	case status.Backend == "seatbelt":
		return "seatbelt"
	case status.Backend == "fallback",
		status.Backend == "linux" && !status.KernelLevel:
		return "fallback"
	default:
		return "unknown"
	}
}
