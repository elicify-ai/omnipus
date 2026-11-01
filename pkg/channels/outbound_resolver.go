package channels

import "context"

// OutboundTargetResult is the outcome of one T-9 target look-up. Code is
// empty when the target exists and can receive a started message;
// otherwise TargetErrNotFound, TargetErrUnsupportedChannelKind or
// TargetErrLookupUnavailable.
type OutboundTargetResult struct {
	Code TargetErrorCode
}

// OutboundTargetResolver is implemented by a running adapter that can check
// a canonical target before it is saved (channels-spec-r8 T-9, Discord). The
// outbound writer calls it outside the config lock for every added target;
// an adapter that does not implement it cannot validate additions, and the
// writer refuses them as lookup_unavailable (fail closed, nothing saved).
type OutboundTargetResolver interface {
	ResolveOutboundTarget(ctx context.Context, canonical string) OutboundTargetResult
}
