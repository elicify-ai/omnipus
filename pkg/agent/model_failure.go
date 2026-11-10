package agent

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// permanentModelFailure is the shared recovery refusal: an unavailable route
// never sent a request, and a confirmed missing model cannot be fixed by
// stripping attachments or changing a request format.
func permanentModelFailure(err error) bool {
	var route *providers.ModelRouteUnavailableError
	if errors.As(err, &route) {
		return true
	}
	var fe *providers.FailoverError
	if errors.As(err, &fe) && fe.Reason == providers.FailoverBadModel {
		return true
	}
	return permanentModelProviderError(errorToProviderError(err))
}

func permanentModelProviderError(pe *ProviderError) bool {
	if pe == nil {
		return false
	}
	var fe *providers.FailoverError
	if errors.As(pe.Err, &fe) && fe.Reason == providers.FailoverBadModel {
		return true
	}
	return providers.IsBadModelError(pe.Status, pe.Body, pe.Model)
}
