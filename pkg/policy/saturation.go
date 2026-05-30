// Package policy — saturation-guard mechanics for the approval registry
// (FR-016, FR-078).
//
// The approval registry itself lives in `pkg/gateway/` (A3's scope). This
// file provides:
//
//  1. Boot-time validation of `gateway.tool_approval_max_pending`.
//     Negative → reject (HIGH audit + non-zero exit).
//     Zero     → allow but warn (sentinel "unlimited"; FR-016).
//     Positive → use as the cap.
//  2. A pure cap-evaluation function `ShouldSaturate` that the gateway
//     calls per ask request. The caller owns the pending-approval
//     counter; this function is the boundary that decides "synthesize
//     a deny with reason=saturated, no WS event emitted, transcript
//     system-message appended."
//
// The split (validation here, registry there) keeps the security
// invariants in `pkg/policy/` where the security lane owns them, while
// allowing A3 to provide the concurrent-state machinery (channels,
// timeouts, broadcaster) that has nothing to do with the security
// decision.

package policy

import (
	"context"
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// SaturationConfigKey is the canonical config-key string for the cap.
// Used as the `config_key` field on `gateway.config.invalid_value` and
// `gateway.startup.guard_disabled` audit events so SIEM can attribute the
// event to a specific knob.
const SaturationConfigKey = "gateway.tool_approval_max_pending"

// SaturationDefault is the spec default (FR-016, FR-078). All variants
// (open-source / desktop / cloud) share this value until Cloud bring-up;
// FR-078 explicitly drops the per-variant override.
const SaturationDefault = 64

// SaturationSentinelUnlimited is the operator-facing "unlimited" value.
// FR-016: setting the cap to 0 disables the saturation guard entirely
// and emits `gateway.startup.guard_disabled` WARN at boot.
const SaturationSentinelUnlimited = 0

// ValidateSaturationCap inspects the operator-supplied cap value and
// returns the effective cap to use plus a boolean indicating whether the
// gateway should continue booting.
//
// Behavior matrix (FR-016):
//
//	cap < 0  → emit `gateway.config.invalid_value` HIGH audit, return
//	            (0, false). The caller MUST exit non-zero. In addition,
//	            the boot-abort stderr line (FR-063) is printed via
//	            `audit.EmitBootAbortStderr` so even an audit-subsystem
//	            failure does not swallow the cause.
//	cap == 0 → emit `gateway.startup.guard_disabled` WARN audit, return
//	            (0, true). The caller continues with no cap; ShouldSaturate
//	            will always return false.
//	cap > 0  → no audit, return (cap, true).
//
// `logger` may be nil (audit disabled); the boot-abort stderr fallback
// still fires for the negative-cap case.
//
// FR-016, FR-063.
func ValidateSaturationCap(ctx context.Context, logger *audit.Logger, satCap int) (effective int, ok bool) {
	if satCap < 0 {
		audit.EmitGatewayConfigInvalidValue(ctx, logger, SaturationConfigKey,
			fmt.Sprintf("%d", satCap),
			"negative value; only 0 (unlimited) and positive integers are accepted")
		audit.EmitBootAbortStderr(
			audit.EventGatewayConfigInvalidValue,
			"-", // no agent
			"-", // no path
			fmt.Errorf("invalid %s=%d: must be >= 0", SaturationConfigKey, satCap),
			nil,
		)
		return 0, false
	}
	if satCap == SaturationSentinelUnlimited {
		audit.EmitGatewayStartupGuardDisabled(ctx, logger, SaturationConfigKey)
		return SaturationSentinelUnlimited, true
	}
	return satCap, true
}

// ShouldSaturate reports whether a new ask request must be synthesized
// as `reason=saturated` (FR-016) given the current pending-approval count
// and the effective cap.
//
//	cap == 0 → unlimited, never saturate (always false).
//	cap > 0  → saturate iff currentPending >= cap.
//
// The caller owns the pending counter and increments it AFTER this
// function returns false (i.e. AFTER the slot is reserved). Race-safe
// usage requires the caller to hold a mutex around the
// `ShouldSaturate(currentPending) → maybe-increment` window; this
// function does not synchronize.
//
// FR-016.
func ShouldSaturate(satCap, currentPending int) bool {
	if satCap == SaturationSentinelUnlimited {
		return false
	}
	return currentPending >= satCap
}
