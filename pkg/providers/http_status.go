package providers

import (
	"errors"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"

	"github.com/elicify-ai/omnipus/pkg/providers/bedrock"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// HTTPStatusOf returns the HTTP status a provider call failed with, read from
// the typed boundary errors of every adapter (raw-HTTP ProviderError, Bedrock
// HTTPError, the OpenAI and Anthropic SDK errors), with the rendered message
// as the last resort. 0 when the failure carries no HTTP status (transport
// error, cancellation).
func HTTPStatusOf(err error) int {
	if err == nil {
		return 0
	}
	var pe *common.ProviderError
	if errors.As(err, &pe) && pe.Status > 0 {
		return pe.Status
	}
	var be *bedrock.HTTPError
	if errors.As(err, &be) && be.StatusCode > 0 {
		return be.StatusCode
	}
	var oe *openai.Error
	if errors.As(err, &oe) && oe.StatusCode > 0 {
		return oe.StatusCode
	}
	var ae *anthropicsdk.Error
	if errors.As(err, &ae) && ae.StatusCode > 0 {
		return ae.StatusCode
	}
	return extractHTTPStatus(err.Error())
}
