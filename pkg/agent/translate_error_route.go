package agent

import "github.com/elicify-ai/omnipus/pkg/providers"

func translateModelRouteUnavailable(route *providers.ModelRouteUnavailableError) LLMError {
	llm := LLMError{
		Code:     CodeModelRouteUnavailable,
		Message:  defaultUserMessage(CodeModelRouteUnavailable),
		Provider: route.Provider, Model: route.Model,
	}
	if sentence := providerMessageFor(llm.Code, llm.Provider, llm.Model); sentence != "" {
		llm.Message = sentence
		llm.ProviderMessage = true
	}
	if route.Cause != nil {
		llm.Detail = boundWireDetail(buildDetail(nil, route.Cause.Error()))
	}
	return llm
}
