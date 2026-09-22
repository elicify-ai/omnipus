// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package bedrock implements the AWS Bedrock Runtime Converse API over plain
// HTTPS. Authentication is an AWS Bedrock API key sent as a Bearer token; AWS
// credential-chain authentication belongs to roadmap issue #801.
package bedrock

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

type (
	ToolCall               = protocoltypes.ToolCall
	FunctionCall           = protocoltypes.FunctionCall
	LLMResponse            = protocoltypes.LLMResponse
	UsageInfo              = protocoltypes.UsageInfo
	Message                = protocoltypes.Message
	ToolDefinition         = protocoltypes.ToolDefinition
	ToolFunctionDefinition = protocoltypes.ToolFunctionDefinition
)

const (
	defaultRegion          = "us-east-1"
	maxImageSize           = 10 * 1024 * 1024
	maxResponseBody        = 8 * 1024 * 1024
	bedrockContentTypeJSON = "application/json"
)

// Provider implements the LLM provider interface for AWS Bedrock.
type Provider struct {
	apiKey         string
	endpoint       string
	region         string
	httpClient     *http.Client
	requestTimeout time.Duration
}

// Option configures the Bedrock provider.
type Option func(*providerConfig)

type providerConfig struct {
	region         string
	baseEndpoint   string
	httpClient     *http.Client
	requestTimeout time.Duration
}

// WithRegion selects the regional Bedrock Runtime endpoint.
func WithRegion(region string) Option {
	return func(c *providerConfig) {
		c.region = strings.TrimSpace(region)
		c.baseEndpoint = ""
	}
}

// WithBaseEndpoint overrides the Bedrock Runtime endpoint. Production catalog
// rows use regional HTTPS endpoints; tests use this seam with httptest.Server.
func WithBaseEndpoint(endpoint string) Option {
	return func(c *providerConfig) {
		c.baseEndpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	}
}

// WithHTTPClient supplies the HTTP client used for Converse requests.
func WithHTTPClient(client *http.Client) Option {
	return func(c *providerConfig) { c.httpClient = client }
}

// WithRequestTimeout sets the timeout for Bedrock Runtime requests.
func WithRequestTimeout(timeout time.Duration) Option {
	return func(c *providerConfig) { c.requestTimeout = timeout }
}

// NewProvider creates an API-key-authenticated Bedrock provider without
// loading AWS profiles, roles, instance metadata, SSO, or any AWS SDK code.
func NewProvider(apiKey string, opts ...Option) (*Provider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("bedrock api key is required")
	}
	pc := providerConfig{region: defaultRegion, httpClient: http.DefaultClient}
	for _, opt := range opts {
		opt(&pc)
	}
	if pc.httpClient == nil {
		pc.httpClient = http.DefaultClient
	}
	if pc.region == "" && pc.baseEndpoint == "" {
		return nil, errors.New("bedrock region or endpoint is required")
	}
	endpoint := pc.baseEndpoint
	if endpoint == "" {
		if err := ValidateRegion(pc.region); err != nil {
			return nil, err
		}
		endpoint = RegionalEndpoint(pc.region)
	}
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	return &Provider{
		apiKey:         apiKey,
		endpoint:       endpoint,
		region:         pc.region,
		httpClient:     pc.httpClient,
		requestTimeout: pc.requestTimeout,
	}, nil
}

// RegionalEndpoint derives the Bedrock RUNTIME (data-plane) host for a
// region — distinct from inference_profiles.go's ControlPlaneEndpoint, which
// derives the CONTROL-plane host ListInferenceProfiles uses. Exported
// (orchestrator review round 2, issue #800 D1) so a caller that needs the
// probe/validate endpoint BEFORE constructing a Provider — the onboarding
// probe and the PUT-triggered save-time key check, both in pkg/gateway —
// can derive the same host this package's own NewProvider builds via
// WithRegion, rather than hand-rolling a second copy of the URL template.
func RegionalEndpoint(region string) string {
	return "https://bedrock-runtime." + strings.TrimSpace(region) + ".amazonaws.com"
}

func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("invalid bedrock runtime endpoint %q", endpoint)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("invalid bedrock runtime endpoint %q", endpoint)
	}
	return nil
}

// HTTPError is a non-success response from the Bedrock Runtime API.
type HTTPError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("bedrock runtime returned HTTP %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	if e.Message != "" {
		return fmt.Sprintf("bedrock runtime returned HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("bedrock runtime returned HTTP %d", e.StatusCode)
}

// Chat sends messages to AWS Bedrock using the Converse API.
func (p *Provider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("bedrock model is required")
	}
	effectiveTimeout := p.requestTimeout
	if effectiveTimeout <= 0 {
		effectiveTimeout = common.DefaultRequestTimeout
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, effectiveTimeout)
		defer cancel()
	}

	payload, err := json.Marshal(buildConverseRequest(messages, tools, options))
	if err != nil {
		return nil, fmt.Errorf("bedrock converse request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.converseURL(model), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("bedrock converse request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", bedrockContentTypeJSON)
	req.Header.Set("Accept", bedrockContentTypeJSON)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bedrock converse: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return nil, fmt.Errorf("bedrock converse response: %w", err)
	}
	if len(body) > maxResponseBody {
		return nil, errors.New("bedrock converse response exceeds 8 MiB")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, parseHTTPError(resp, body)
	}
	var output converseResponse
	if err := json.Unmarshal(body, &output); err != nil {
		return nil, fmt.Errorf("bedrock converse response: %w", err)
	}
	return parseResponse(&output)
}

func (p *Provider) converseURL(model string) string {
	return p.endpoint + "/model/" + url.PathEscape(strings.TrimSpace(model)) + "/converse"
}

func parseHTTPError(resp *http.Response, body []byte) error {
	var payload struct { // not-wire-format: upstream Bedrock error envelope
		Message string `json:"message"`
		Code    string `json:"code"`
		Type    string `json:"__type"`
	}
	_ = json.Unmarshal(body, &payload)
	code := strings.TrimSpace(resp.Header.Get("X-Amzn-Errortype"))
	if code == "" {
		code = payload.Code
	}
	if code == "" {
		code = payload.Type
	}
	if before, _, found := strings.Cut(code, ":"); found {
		code = before
	}
	return &HTTPError{StatusCode: resp.StatusCode, Code: code, Message: payload.Message}
}

// GetDefaultModel returns an empty string because Bedrock models are selected
// from the catalog.
func (p *Provider) GetDefaultModel() string { return "" }

// Region returns the configured region. A custom endpoint retains the default
// region unless WithRegion was also selected.
func (p *Provider) Region() string { return p.region }

// Endpoint returns the Bedrock Runtime base URL.
func (p *Provider) Endpoint() string { return p.endpoint }

type converseRequest struct {
	Messages        []bedrockMessage     `json:"messages"`
	System          []systemContentBlock `json:"system,omitempty"`
	InferenceConfig *inferenceConfig     `json:"inferenceConfig,omitempty"`
	ToolConfig      *toolConfiguration   `json:"toolConfig,omitempty"`
}

type bedrockMessage struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type systemContentBlock struct {
	Text string `json:"text"`
}

type contentBlock struct {
	Text       *string          `json:"text,omitempty"`
	Image      *imageBlock      `json:"image,omitempty"`
	ToolUse    *toolUseBlock    `json:"toolUse,omitempty"`
	ToolResult *toolResultBlock `json:"toolResult,omitempty"`
}

type imageBlock struct {
	Format string      `json:"format"`
	Source imageSource `json:"source"`
}

type imageSource struct {
	Bytes []byte `json:"bytes"`
}

type toolUseBlock struct {
	ToolUseID string         `json:"toolUseId"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
}

type toolResultBlock struct {
	ToolUseID string         `json:"toolUseId"`
	Content   []contentBlock `json:"content"`
}

type toolConfiguration struct {
	Tools []toolBlock `json:"tools"`
}

type toolBlock struct {
	ToolSpec toolSpecification `json:"toolSpec"`
}

type toolSpecification struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema toolInputSchema `json:"inputSchema"`
}

type toolInputSchema struct {
	JSON map[string]any `json:"json"`
}

type inferenceConfig struct {
	MaxTokens   *int32   `json:"maxTokens,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`
}

func buildConverseRequest(messages []Message, tools []ToolDefinition, options map[string]any) converseRequest {
	converted, system := convertMessages(messages)
	request := converseRequest{Messages: converted, System: system}
	if maxTokens, ok := common.AsInt(options["max_tokens"]); ok && maxTokens > 0 {
		if maxTokens > math.MaxInt32 {
			maxTokens = math.MaxInt32
		}
		value := int32(maxTokens)
		request.InferenceConfig = &inferenceConfig{MaxTokens: &value}
	}
	if temperature, ok := common.AsFloat(options["temperature"]); ok {
		if request.InferenceConfig == nil {
			request.InferenceConfig = &inferenceConfig{}
		}
		value := float32(temperature)
		request.InferenceConfig.Temperature = &value
	}
	if convertedTools := convertTools(tools); convertedTools != nil && len(convertedTools.Tools) > 0 {
		request.ToolConfig = convertedTools
	}
	return request
}

// convertMessages preserves the existing Converse mapping: system prompts are
// separated, consecutive tool results are merged into one user message, and
// assistant tool calls retain their correlation ids and arguments.
func convertMessages(messages []Message) ([]bedrockMessage, []systemContentBlock) {
	var converted []bedrockMessage
	var system []systemContentBlock
	for i := 0; i < len(messages); {
		msg := messages[i]
		switch {
		case msg.Role == "system":
			system = append(system, systemContentBlock{Text: msg.Content})
			i++
		case isToolResult(msg):
			blocks := make([]contentBlock, 0, 1)
			for i < len(messages) && isToolResult(messages[i]) {
				blocks = append(blocks, makeToolResultBlock(messages[i]))
				i++
			}
			converted = append(converted, bedrockMessage{Role: "user", Content: blocks})
		case msg.Role == "assistant":
			converted = append(converted, bedrockMessage{Role: "assistant", Content: buildAssistantContent(msg)})
			i++
		case msg.Role == "user" || msg.Role == "tool":
			converted = append(converted, bedrockMessage{Role: "user", Content: buildUserContent(msg)})
			i++
		default:
			i++
		}
	}
	return converted, system
}

func isToolResult(msg Message) bool {
	return (msg.Role == "tool" || msg.Role == "user") && msg.ToolCallID != ""
}

func makeToolResultBlock(msg Message) contentBlock {
	content := []contentBlock{{Text: stringPointer(msg.Content)}}
	rejected := map[string]int{}
	for _, mediaURL := range msg.Media {
		if image, reason := bedrockImageBlock(mediaURL); reason == "" {
			content = append(content, contentBlock{Image: &image})
		} else {
			rejected[reason]++
		}
	}
	if len(rejected) > 0 {
		warning := toolMediaWarning(rejected)
		content = append(content, contentBlock{Text: &warning})
		logger.WarnCF("bedrock", "tool-result media omitted", map[string]any{
			"unsupported_image_format_count": rejected["unsupported image format"],
			"malformed_image_data_count":     rejected["malformed image data"],
			"image_oversize_count":           rejected["image exceeds 10 MiB"],
			"unsupported_media_type_count":   rejected["unsupported media type"],
		})
	}
	return contentBlock{ToolResult: &toolResultBlock{ToolUseID: msg.ToolCallID, Content: content}}
}

func toolMediaWarning(rejected map[string]int) string {
	parts := make([]string, 0, 4)
	for _, reason := range []string{"unsupported image format", "malformed image data", "image exceeds 10 MiB", "unsupported media type"} {
		if count := rejected[reason]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s (%d)", reason, count))
		}
	}
	return "[Tool-result media omitted for Bedrock: " + strings.Join(parts, ", ") + ". Re-read the attachment in a supported image format.]"
}

func bedrockImageBlock(mediaURL string) (imageBlock, string) {
	if !strings.HasPrefix(mediaURL, "data:image/") {
		return imageBlock{}, "unsupported media type"
	}
	parts := strings.SplitN(mediaURL, ",", 2)
	if len(parts) != 2 || !strings.Contains(parts[0], ";base64") {
		return imageBlock{}, "malformed image data"
	}
	format := strings.TrimSuffix(strings.TrimPrefix(parts[0], "data:image/"), ";base64")
	if format == "jpg" {
		format = "jpeg"
	}
	switch format {
	case "jpeg", "png", "gif", "webp":
	default:
		return imageBlock{}, "unsupported image format"
	}
	if base64.StdEncoding.DecodedLen(len(parts[1])) > maxImageSize {
		return imageBlock{}, "image exceeds 10 MiB"
	}
	data, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return imageBlock{}, "malformed image data"
	}
	if len(data) > maxImageSize {
		return imageBlock{}, "image exceeds 10 MiB"
	}
	return imageBlock{Format: format, Source: imageSource{Bytes: data}}, ""
}

func buildUserContent(msg Message) []contentBlock {
	content := make([]contentBlock, 0, 1+len(msg.Media))
	if msg.Content != "" {
		content = append(content, contentBlock{Text: stringPointer(msg.Content)})
	}
	for _, mediaURL := range msg.Media {
		image, reason := bedrockImageBlock(mediaURL)
		if reason != "" {
			logger.WarnCF("bedrock", "skipping user image", map[string]any{"reason": reason})
			continue
		}
		content = append(content, contentBlock{Image: &image})
	}
	if len(content) == 0 {
		content = append(content, contentBlock{Text: stringPointer("")})
	}
	return content
}

func buildAssistantContent(msg Message) []contentBlock {
	content := make([]contentBlock, 0, 1+len(msg.ToolCalls))
	if msg.Content != "" {
		content = append(content, contentBlock{Text: stringPointer(msg.Content)})
	}
	for _, tc := range msg.ToolCalls {
		if strings.TrimSpace(tc.ID) == "" {
			logger.WarnCF("bedrock", "skipping tool call with empty ID", map[string]any{"tool": tc.Name})
			continue
		}
		name, args := toolCallParts(tc)
		if strings.TrimSpace(name) == "" {
			continue
		}
		content = append(content, contentBlock{ToolUse: &toolUseBlock{
			ToolUseID: tc.ID,
			Name:      name,
			Input:     args,
		}})
	}
	if len(content) == 0 {
		content = append(content, contentBlock{Text: stringPointer("")})
	}
	return content
}

func toolCallParts(tc ToolCall) (string, map[string]any) {
	name := tc.Name
	if name == "" && tc.Function != nil {
		name = tc.Function.Name
	}
	args := tc.Arguments
	if args == nil && tc.Function != nil && tc.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			logger.WarnCF("bedrock", "failed to parse Function.Arguments", map[string]any{
				"tool": name, "error": err.Error(),
			})
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	return name, args
}

func convertTools(tools []ToolDefinition) *toolConfiguration {
	converted := make([]toolBlock, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}
		parameters := tool.Function.Parameters
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		converted = append(converted, toolBlock{ToolSpec: toolSpecification{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: toolInputSchema{JSON: parameters},
		}})
	}
	return &toolConfiguration{Tools: converted}
}

type converseResponse struct {
	Output     converseOutput `json:"output"`
	StopReason string         `json:"stopReason"`
	Usage      *tokenUsage    `json:"usage,omitempty"`
}

type converseOutput struct {
	Message bedrockResponseMessage `json:"message"`
}

type bedrockResponseMessage struct {
	Role    string                 `json:"role"`
	Content []responseContentBlock `json:"content"`
}

type responseContentBlock struct {
	Text    *string               `json:"text,omitempty"`
	ToolUse *responseToolUseBlock `json:"toolUse,omitempty"`
}

type responseToolUseBlock struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type tokenUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

func parseResponse(output *converseResponse) (*LLMResponse, error) {
	if output == nil {
		return nil, errors.New("bedrock converse response is nil")
	}
	usage := responseUsage(output.Usage)
	var content strings.Builder
	toolCalls := make([]ToolCall, 0)
	for _, block := range output.Output.Message.Content {
		if block.Text != nil {
			content.WriteString(*block.Text)
		}
		if block.ToolUse == nil {
			continue
		}
		args, err := decodeToolInput(block.ToolUse.Input)
		if err != nil {
			cause := fmt.Errorf("%w: tool %q (id %q): %w",
				common.ErrToolArgumentsUndecodable, block.ToolUse.Name, block.ToolUse.ToolUseID, err)
			tae := common.NewToolArgumentsError(block.ToolUse.Name, cause, output.StopReason == "max_tokens")
			return nil, common.AttachToolArgumentsEvidence(tae, output.StopReason, usage)
		}
		argsJSON, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("bedrock tool arguments for %q: %w", block.ToolUse.Name, err)
		}
		toolCalls = append(toolCalls, ToolCall{
			ID:        block.ToolUse.ToolUseID,
			Name:      block.ToolUse.Name,
			Arguments: args,
			Function:  &FunctionCall{Name: block.ToolUse.Name, Arguments: string(argsJSON)},
		})
	}
	return &LLMResponse{
		Content:      content.String(),
		ToolCalls:    toolCalls,
		FinishReason: mapStopReason(output.StopReason),
		Usage:        usage,
	}, nil
}

func decodeToolInput(raw json.RawMessage) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var args map[string]any
	if err := decoder.Decode(&args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

func responseUsage(usage *tokenUsage) *UsageInfo {
	if usage == nil {
		return nil
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	return &UsageInfo{
		PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens, TotalTokens: total,
	}
}

func mapStopReason(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "content_filtered", "guardrail_intervened":
		return "content_filter"
	default:
		return "stop"
	}
}

func stringPointer(value string) *string { return &value }
