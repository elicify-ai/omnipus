package envcontext

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// renderSandboxMode converts a sandbox.Status into the human-readable string
// that appears in the env preamble. The mapping is pinned in spec v7 and must
// not be changed without a corresponding spec update.
//
//   - none / unavailable                                       → "off"
//   - linux backend, kernel-level, ABI version > 0              → "landlock-abi-<n>"
//   - linux backend, no kernel-level enforcement                → "fallback"
//   - fallback backend                                          → "fallback"
//   - seatbelt backend, kernel-level AND policy actually applied → "seatbelt"
//   - seatbelt backend, kernel-level but NOT applied             → "fallback"
//   - anything else                                             → "unknown"
//
// The "seatbelt" row was added by ADR-052 Phase-3 AC-6, together with the
// matching spec update. Before darwin had a kernel backend it always reported
// "fallback"; the moment selectBackendPlatform started returning "seatbelt"
// this switch fell through to "unknown", telling every macOS agent its sandbox
// state was unknown while it was actually kernel-confined. Any future backend
// name must add a row here AND to the spec, or it inherits the same bug.
//
// The seatbelt row also requires status.PolicyApplied, not just
// status.Backend == "seatbelt". Seatbelt has no audit-only mode (see
// pkg/gateway/sandbox_apply.go), so under mode=permissive the gateway refuses
// to install a profile — children run completely unwrapped — yet
// SeatbeltBackend.ConfinesChildren() (and therefore status.KernelLevel) stays
// true because it only reflects "/usr/bin/sandbox-exec exists", not whether a
// profile was ever installed. Reporting "seatbelt" from Backend/KernelLevel
// alone told the agent it was kernel-confined while every child it spawned was
// not. status.PolicyApplied is the one field that reflects whether Apply()
// actually ran on this process (see SeatbeltBackend.PolicyApplied), mirroring
// the honesty check the Linux row already gets for free from ABIVersion > 0
// only being meaningful once the kernel has been probed. When Seatbelt is
// selected but not applied, this degrades to "fallback" — the same string an
// operator sees for every other unenforced state — rather than "unknown",
// because the failure mode is a known, named one (permissive mode or mode=off
// on a Seatbelt-capable host), not a new backend the mapping has never seen.
//
// There is no "seatbelt-abi-<n>" form: Seatbelt exposes no versioned ABI to
// probe, so ABIVersion stays 0 on darwin and carries no information.
func renderSandboxMode(status sandbox.Status) string {
	switch {
	case status.Backend == "none" && !status.Available:
		return "off"
	case status.Backend == "linux" && status.KernelLevel && status.ABIVersion > 0:
		return fmt.Sprintf("landlock-abi-%d", status.ABIVersion)
	case status.Backend == "seatbelt" && status.KernelLevel && status.PolicyApplied:
		return "seatbelt"
	case status.Backend == "fallback",
		status.Backend == "linux" && !status.KernelLevel,
		status.Backend == "seatbelt" && !status.PolicyApplied:
		return "fallback"
	default:
		return "unknown"
	}
}
