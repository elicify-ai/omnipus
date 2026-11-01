package channels

import "context"

// OutboundTargetResult is the outcome of one T-9 target look-up. Code is
// empty when the target exists and can receive a started message;
// otherwise TargetErrNotFound, TargetErrUnsupportedChannelKind or
// TargetErrLookupUnavailable.
type OutboundTargetResult struct {
	Code TargetErrorCode
	// Reason is the T-9 refusal-specific reason when the refusal has one (a
	// DM or group-DM channel, a channel the bot cannot see); empty means the
	// code's catalog reason. In-process only: the writer copies it into the
	// contract's target_errors[].reason after safe-echo sanitizing.
	Reason string
}

// OutboundTargetResolver is implemented by a running adapter that can check
// a canonical target before it is saved (channels-spec-r8 T-9, Discord). The
// outbound writer calls it outside the config lock for every added target;
// an adapter that does not implement it cannot validate additions, and the
// writer refuses them as lookup_unavailable (fail closed, nothing saved).
type OutboundTargetResolver interface {
	ResolveOutboundTarget(ctx context.Context, canonical string) OutboundTargetResult
}
