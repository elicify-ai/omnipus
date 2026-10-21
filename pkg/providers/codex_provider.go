package providers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	"github.com/elicify-ai/omnipus/pkg/logger"
	orc "github.com/elicify-ai/omnipus/pkg/providers/openai_responses_common"
)

const (
	codexDefaultModel        = "gpt-5.3-codex"
	codexDefaultInstructions = "You are Codex, a coding assistant."
)

type CodexProvider struct {
	client          *openai.Client
	accountID       string
	tokenSource     func() (string, string, error)
	enableWebSearch bool
}

const defaultCodexInstructions = "You are Codex, a coding assistant."

func NewCodexProvider(token, accountID string) *CodexProvider {
	return newCodexProviderWithBaseURL(token, accountID, "https://chatgpt.com/backend-api/codex")
}

// newCodexProviderWithBaseURL is the construction seam behind
// NewCodexProvider: every shared client option lives here — including D13's
// option.WithMaxRetries(0) — so the retry posture cannot drift between the
// production constructor and a test pointing the provider at a local
// request-counting server. apiBase is the Codex backend base URL verbatim.
func newCodexProviderWithBaseURL(token, accountID, apiBase string) *CodexProvider {
	opts := []option.RequestOption{
		option.WithBaseURL(apiBase),
		option.WithAPIKey(token),
		option.WithHeader("originator", "codex_cli_rs"),
		option.WithHeader("OpenAI-Beta", "responses=experimental"),
		// D13: the chain owns every retry decision (§7.4's in-chain
		// rate-limit loop reads the provider's own retry-after fact). An
		// SDK-internal retry would double-attack a rate-limited candidate
		// and hide the boundary error whose headers carry the retry-after
		// fact — so the SDK's default 2 retries are disabled here and the
		// boundary error reaches the chain verbatim.
		option.WithMaxRetries(0),
	}
	if accountID != "" {
		opts = append(opts, option.WithHeader("Chatgpt-Account-Id", accountID))
	}
	client := openai.NewClient(opts...)
	return &CodexProvider{
		client:          &client,
		accountID:       accountID,
		enableWebSearch: true,
	}
}

func NewCodexProviderWithTokenSource(
	token, accountID string, tokenSource func() (string, string, error),
) *CodexProvider {
	p := NewCodexProvider(token, accountID)
	p.tokenSource = tokenSource
	return p
}

func (p *CodexProvider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	var opts []option.RequestOption
	accountID := p.accountID
	resolvedModel, fallbackReason := resolveCodexModel(model)
	if fallbackReason != "" {
		logger.WarnCF(
			"provider.codex",
			"Requested model is not compatible with Codex backend, using fallback",
			map[string]any{
				"requested_model": model,
				"resolved_model":  resolvedModel,
				"reason":          fallbackReason,
			},
		)
	}
	if p.tokenSource != nil {
		tok, accID, err := p.tokenSource()
		if err != nil {
			return nil, fmt.Errorf("refreshing token: %w", err)
		}
		opts = append(opts, option.WithAPIKey(tok))
		if accID != "" {
			accountID = accID
		}
	}
	if accountID != "" {
		opts = append(opts, option.WithHeader("Chatgpt-Account-Id", accountID))
	} else {
		logger.WarnCF(
			"provider.codex",
			"No account id found for Codex request; backend may reject with 400",
			map[string]any{
				"requested_model": model,
				"resolved_model":  resolvedModel,
			},
		)
	}

	// Respect tools.web.prefer_native: only inject native search when the agent
	// loop passes options["native_search"]=true, so prefer_native=false means no injection.
	useNativeSearch := p.enableWebSearch && (options["native_search"] == true)
	params := buildCodexParams(messages, tools, resolvedModel, options, useNativeSearch)

	stream := p.client.Responses.NewStreaming(ctx, params, opts...)
	defer stream.Close()

	var resp *responses.Response
	var streamed codexStreamedOutput
	for stream.Next() {
		evt := stream.Current()
		streamed.countEvent(evt.Type)
		switch evt.Type {
		case "response.completed", "response.failed", "response.incomplete":
			evtResp := evt.Response
			if evtResp.ID != "" {
				evtRespCopy := evtResp
				resp = &evtRespCopy
			}
		case "response.output_item.done":
			streamed.addItem(evt.OutputIndex, evt.Item)
		}
	}
	err := stream.Err()
	if err != nil {
		fields := map[string]any{
			"requested_model":    model,
			"resolved_model":     resolvedModel,
			"messages_count":     len(messages),
			"tools_count":        len(tools),
			"account_id_present": accountID != "",
			"error":              err.Error(),
		}
		var apiErr *openai.Error
		if errors.As(err, &apiErr) {
			fields["status_code"] = apiErr.StatusCode
			fields["api_type"] = apiErr.Type
			fields["api_code"] = apiErr.Code
			fields["api_param"] = apiErr.Param
			fields["api_message"] = apiErr.Message
			if apiErr.StatusCode == 400 {
				fields["hint"] = "verify account id header and model compatibility for codex backend"
			}
			if apiErr.Response != nil {
				fields["request_id"] = apiErr.Response.Header.Get("x-request-id")
			}
		}
		logger.ErrorCF("provider.codex", "Codex API call failed", fields)
		return nil, fmt.Errorf("codex API call: %w", normalizeSDKError(err))
	}
	if resp == nil {
		fields := map[string]any{
			"requested_model":    model,
			"resolved_model":     resolvedModel,
			"messages_count":     len(messages),
			"tools_count":        len(tools),
			"account_id_present": accountID != "",
		}
		logger.ErrorCF(
			"provider.codex",
			"Codex stream ended without completed response event",
			fields,
		)
		return nil, fmt.Errorf("codex API call: stream ended without completed response")
	}

	source := streamed.fillEmptyOutput(resp, resolvedModel)
	parsed, err := orc.ParseResponseFromStruct(resp)
	if parsed != nil {
		parsed.Diagnostics = streamed.diagnostics(resp, source)
	}
	return parsed, err
}

// codexStreamedOutput collects the output items as the stream delivers them
// (provider hotfix H5). CAPTURED 2026-10-13 from the real
// chatgpt.com/backend-api/codex stream (gpt-6.1-sol): whenever the request
// carries tools, the backend closes with a `response.completed` whose
// `output` is EMPTY, and the answer — a message, or a function_call — exists
// only in the earlier `response.output_item.done` events. Reading only the
// completed event dropped the whole answer ("The model returned an empty
// response"). A no-tools request still carries its output in the completed
// event, which is why a tiny scratch agent kept working.
type codexStreamedOutput struct {
	items   map[int64]responses.ResponseOutputItemUnion
	ordered []int64
	events  map[string]int
}

func (s *codexStreamedOutput) countEvent(eventType string) {
	if s.events == nil {
		s.events = make(map[string]int)
	}
	s.events[eventType]++
}

// diagnostics is the content-free shape summary the agent loop logs when an
// answer comes back empty (provider hotfix H5): event types and counts,
// output item types, where the output came from, status and incomplete
// reason. No text, no arguments, no identifiers beyond item TYPES.
func (s *codexStreamedOutput) diagnostics(resp *responses.Response, source string) *ResponseDiagnostics {
	d := &ResponseDiagnostics{EventCounts: s.events, OutputSource: source}
	if resp != nil {
		for _, item := range resp.Output {
			d.OutputItemTypes = append(d.OutputItemTypes, item.Type)
		}
		d.Status = string(resp.Status)
		d.IncompleteReason = resp.IncompleteDetails.Reason
	}
	return d
}

func (s *codexStreamedOutput) addItem(index int64, item responses.ResponseOutputItemUnion) {
	if s.items == nil {
		s.items = make(map[int64]responses.ResponseOutputItemUnion)
	}
	if _, seen := s.items[index]; !seen {
		s.ordered = append(s.ordered, index)
	}
	s.items[index] = item
}

// fillEmptyOutput gives resp the streamed output items when — and only
// when — the terminal event carried none. A non-empty terminal output stays
// authoritative, so nothing is ever counted twice. Items are placed in
// output_index order.
//
// It returns where the response's output now comes from: "completed",
// "output_item.done", or "none".
func (s *codexStreamedOutput) fillEmptyOutput(resp *responses.Response, model string) string {
	if resp == nil {
		return "none"
	}
	if len(resp.Output) > 0 {
		return "completed"
	}
	if len(s.items) > 0 {
		sort.Slice(s.ordered, func(i, j int) bool { return s.ordered[i] < s.ordered[j] })
		for _, idx := range s.ordered {
			resp.Output = append(resp.Output, s.items[idx])
		}
		logger.DebugCF("provider.codex", "Completed event carried no output; using streamed output items",
			map[string]any{"resolved_model": model, "items": len(s.items)})
		return "output_item.done"
	}
	return "none"
}

func (p *CodexProvider) GetDefaultModel() string {
	return codexDefaultModel
}

func (p *CodexProvider) SupportsNativeSearch() bool {
	return p.enableWebSearch
}

func resolveCodexModel(model string) (string, string) {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return codexDefaultModel, "empty model"
	}

	if after, ok := strings.CutPrefix(m, "openai/"); ok {
		m = after
	} else if strings.Contains(m, "/") {
		return codexDefaultModel, "non-openai model namespace"
	}

	unsupportedPrefixes := []string{
		"glm",
		"claude",
		"anthropic",
		"gemini",
		"google",
		"moonshot",
		"kimi",
		"qwen",
		"deepseek",
		"llama",
		"meta-llama",
		"mistral",
		"grok",
		"xai",
		"zhipu",
	}
	for _, prefix := range unsupportedPrefixes {
		if strings.HasPrefix(m, prefix) {
			return codexDefaultModel, "unsupported model prefix"
		}
	}

	if strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") {
		return m, ""
	}

	return codexDefaultModel, "unsupported model family"
}

func buildCodexParams(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	enableWebSearch bool,
) responses.ResponseNewParams {
	inputItems, instructions := orc.TranslateMessages(messages)

	params := responses.ResponseNewParams{
		Model: model,
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: inputItems,
		},
		Store: openai.Opt(false),
	}

	if instructions != "" {
		params.Instructions = openai.Opt(instructions)
	} else {
		// ChatGPT Codex backend requires instructions to be present.
		params.Instructions = openai.Opt(defaultCodexInstructions)
	}

	// Prompt caching: pass a stable cache key so OpenAI can bucket requests
	// and reuse prefix KV cache across calls with the same key.
	// See: https://platform.openai.com/docs/guides/prompt-caching
	if cacheKey, ok := options["prompt_cache_key"].(string); ok && cacheKey != "" {
		params.PromptCacheKey = openai.Opt(cacheKey)
	}

	if len(tools) > 0 || enableWebSearch {
		params.Tools = orc.TranslateTools(tools, enableWebSearch)
	}

	return params
}
