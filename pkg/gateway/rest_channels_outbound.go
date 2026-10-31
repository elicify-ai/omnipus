package gateway

// Outbound writer WP-0 shell (channels-spec-r8, FR-020, FR-027, T-6b, T-9).
// Signature only: no route, tool or start path calls it yet. WP-2 implements
// it and wires GET/PUT /api/v1/channels/{id}/outbound and the
// set_channel_outbound tool to it.

import (
	"context"
	"errors"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// errOutboundWriterNotImplemented is the WP-0 shell's answer.
//
//nolint:unused // WP-0 shell: returned by applyOutboundChange, whose users arrive with the tests and WP-2.
var errOutboundWriterNotImplemented = errors.New("channel outbound writer is not implemented yet")

// outboundRejectionError is applyOutboundChange's refusal before anything was
// written: Status is the HTTP status of the contract (400, 404, 409, 422,
// 502) and Body the contract ChannelOutboundRejection (already sanitized to
// the safe-echo set). Any other non-nil error from applyOutboundChange is a
// save failure: before the file write → 500 "could not save the setting;
// nothing was changed".
//
//nolint:unused // WP-0 shell: WP-2 returns it from applyOutboundChange and maps it to the HTTP status.
type outboundRejectionError struct {
	Body   gen.ChannelOutboundRejection
	Status int
}

//nolint:unused // WP-0 shell: see outboundRejectionError.
func (e *outboundRejectionError) Error() string {
	return string(e.Body.Code) + ": " + e.Body.Error
}

// applyOutboundChange is the one outbound writer (FR-020), shared by PUT
// /api/v1/channels/{id}/outbound and the set_channel_outbound tool:
// (1) without a lock, read the live configuration, fast-fail on
// expectedRevision, canonicalize targets and run the bounded Discord
// look-ups for added Discord targets (ctx carries the request's
// cancellation; each look-up adds its own 5-second deadline); (2) take
// configMu; (3) recheck the live revision (409 stale_revision); (3b)
// classify extending vs non-extending over the saved and live snapshots
// (409 saved_live_mismatch); (4) save through the common save step with
// GuardOutbound(dedicated = true); (5) on a live-refresh failure publish the
// guard-accepted outbound value alone and set Warning; (6) return the
// contract ChannelOutboundWriteResponse. actor names the writer for logs
// ("rest" or the calling agent id); it grants nothing.
//
// Errors: *outboundRejectionError for every contract refusal; any other
// error is a save failure before the file was written.
//
// The spec gives the parameter list (id, policy, targets, expectedRevision,
// actor); ctx and the generated policy type are this shell's choices.
//
// WP-0 shell: returns errOutboundWriterNotImplemented.
//
//nolint:unused // WP-0 shell: the test author's tests and WP-2's route and tool are its users.
func (a *restAPI) applyOutboundChange(
	ctx context.Context,
	id string,
	policy gen.ChannelOutboundUpdateRequestPolicy,
	targets []string,
	expectedRevision, actor string,
) (gen.ChannelOutboundWriteResponse, error) {
	return gen.ChannelOutboundWriteResponse{}, errOutboundWriterNotImplemented
}
