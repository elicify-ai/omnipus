package gateway

// The set_channel_outbound bridge (channels-spec-r8 FR-020, FR-030): the
// tool calls the one outbound writer, applyOutboundChange, and receives its
// refusals in the sysagent tool package's neutral types.

import (
	"context"
	"errors"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// applyOutboundChangeForTool runs applyOutboundChange for the tool and
// translates its errors: *outboundRejectionError → *ChannelOutboundRefusal
// (the writer's code and safe text, per-target detail kept);
// errOutboundNotConfigured → ErrChannelOutboundNotConfigured; any other error
// is returned unchanged (a save failure before the file write).
func (a *restAPI) applyOutboundChangeForTool(ctx context.Context, change systools.ChannelOutboundChange) (systools.ChannelOutboundSaved, error) {
	resp, err := a.applyOutboundChange(ctx, change.ID, gen.ChannelOutboundUpdateRequestPolicy(change.Policy),
		change.Targets, change.ExpectedRevision, change.Actor)
	var rejection *outboundRejectionError
	switch {
	case err == nil:
		saved := systools.ChannelOutboundSaved{
			Policy:   string(resp.Outbound.Policy),
			Targets:  append([]string{}, resp.Outbound.Targets...),
			Revision: resp.Revision,
		}
		if resp.Warning != nil {
			saved.Warning = *resp.Warning
		}
		return saved, nil
	case errors.As(err, &rejection):
		refusal := &systools.ChannelOutboundRefusal{Reason: string(rejection.Body.Code), Message: rejection.Body.Error}
		if rejection.Body.TargetErrors != nil {
			refusal.TargetErrors = *rejection.Body.TargetErrors
		}
		if rejection.Body.OmittedCount != nil {
			refusal.OmittedCount = *rejection.Body.OmittedCount
		}
		return systools.ChannelOutboundSaved{}, refusal
	case errors.Is(err, errOutboundNotConfigured):
		return systools.ChannelOutboundSaved{}, systools.ErrChannelOutboundNotConfigured
	default:
		return systools.ChannelOutboundSaved{}, err
	}
}
