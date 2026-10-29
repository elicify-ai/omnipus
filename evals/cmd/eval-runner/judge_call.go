package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ── Judge call ────────────────────────────────────────────────────────────────
//
// The judge speaks the protocol of its provider: Anthropic messages for an
// Anthropic-protocol provider (the MiniMax plan), OpenAI chat completions for
// an OpenAI-compatible one (OpenRouter). It tries the primary judge model
// first and, when the primary is unavailable, the fallback, recording which
// model answered (JudgeCallResult.JudgeModelUsed → judge_model_used in the
// JSONL result).

// JudgeCallResult is the outcome of one judge call.
type JudgeCallResult struct {
	Text           string `json:"text"`
	Tokens         int    `json:"tokens"`
	JudgeModelUsed string `json:"judge_model_used"`
}

// JudgeEndpoints are the full URLs callJudgeWithFallback posts to.
type JudgeEndpoints struct {
	PrimaryMessagesURL         string // full URL, POST Anthropic messages here
	FallbackChatCompletionsURL string // full URL, POST OpenAI chat completions here
}

// judgeTarget is one side of a judge call: the model, the protocol it is
// spoken in and the exact URL.
type judgeTarget struct {
	Side     E2EModelSide
	Protocol catalog.Protocol
	URL      string
}

// callJudgeWithFallback calls the primary judge over Anthropic messages and,
// when the primary is unavailable, the fallback over OpenAI chat completions.
func callJudgeWithFallback(ctx context.Context, primary, fallback E2EModelSide, prompt string, endpoints JudgeEndpoints) (JudgeCallResult, error) {
	fb := &judgeTarget{Side: fallback, Protocol: catalog.ProtocolOpenAICompatible, URL: endpoints.FallbackChatCompletionsURL}
	return callJudgeTargets(ctx,
		judgeTarget{Side: primary, Protocol: catalog.ProtocolAnthropic, URL: endpoints.PrimaryMessagesURL},
		fb, prompt)
}

// judgeTargetFor derives a side's protocol and URL from the embedded catalog.
func judgeTargetFor(side E2EModelSide) (judgeTarget, error) {
	p, ok := catalogProvider(side.Provider)
	if !ok || strings.TrimSpace(p.API) == "" {
		return judgeTarget{}, fmt.Errorf("judge provider %q has no API base in the embedded catalog", side.Provider)
	}
	base := strings.TrimRight(p.API, "/")
	switch p.Protocol {
	case catalog.ProtocolAnthropic:
		return judgeTarget{Side: side, Protocol: p.Protocol, URL: base + "/messages"}, nil
	case catalog.ProtocolOpenAICompatible:
		return judgeTarget{Side: side, Protocol: p.Protocol, URL: base + "/chat/completions"}, nil
	default:
		return judgeTarget{}, fmt.Errorf("judge provider %q speaks %q; the eval judge supports only anthropic and openai-compatible", side.Provider, p.Protocol)
	}
}

// callJudge resolves the judge targets for the configured slugs and calls
// them. fallbackSlug may be empty (no fallback); a fallback equal to the
// primary is ignored.
func callJudge(ctx context.Context, primarySlug, fallbackSlug, prompt string) (JudgeCallResult, error) {
	primarySide, err := sideFromSlug(primarySlug)
	if err != nil {
		return JudgeCallResult{}, fmt.Errorf("judge model: %w", err)
	}
	primary, err := judgeTargetFor(primarySide)
	if err != nil {
		return JudgeCallResult{}, err
	}
	var fallback *judgeTarget
	// A fallback equal to the primary (a job already moved onto the fallback)
	// would only repeat the failed call.
	if fb := strings.TrimSpace(fallbackSlug); fb != "" && fb != strings.TrimSpace(primarySlug) {
		fbSide, err := sideFromSlug(fallbackSlug)
		if err != nil {
			return JudgeCallResult{}, fmt.Errorf("judge fallback model: %w", err)
		}
		t, err := judgeTargetFor(fbSide)
		if err != nil {
			return JudgeCallResult{}, err
		}
		fallback = &t
	}
	return callJudgeTargets(ctx, primary, fallback, prompt)
}

// judgeHTTPError is a non-2xx judge answer. It carries the status and the
// provider's error type only — never the body, which can echo the key.
type judgeHTTPError struct {
	Status    int
	ErrorType string
}

func (e *judgeHTTPError) Error() string {
	if e.ErrorType != "" {
		return fmt.Sprintf("HTTP %d (%s)", e.Status, e.ErrorType)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// judgeShouldFallBack mirrors the job-start check's outage rule: rate limit,
// timeout, server error, auth failure or a transport error move to the
// fallback; any other answer is a request problem the fallback would not fix.
func judgeShouldFallBack(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var he *judgeHTTPError
	if errors.As(err, &he) {
		switch {
		case he.Status == http.StatusTooManyRequests,
			he.Status == http.StatusRequestTimeout,
			he.Status == http.StatusUnauthorized,
			he.Status == http.StatusForbidden,
			he.Status >= 500:
			return true
		default:
			return false
		}
	}
	// Transport errors (refused, reset, deadline) and unusable 2xx bodies.
	return true
}

func callJudgeTargets(ctx context.Context, primary judgeTarget, fallback *judgeTarget, prompt string) (JudgeCallResult, error) {
	res, perr := callJudgeTarget(ctx, primary, prompt, "judge")
	if perr == nil {
		return res, nil
	}
	if fallback == nil {
		return JudgeCallResult{}, fmt.Errorf("judge %s failed: %w (no fallback configured)", primary.Side.Model, perr)
	}
	if !judgeShouldFallBack(perr) {
		return JudgeCallResult{}, fmt.Errorf("judge %s failed: %w (not an outage, fallback %s not tried)",
			primary.Side.Model, perr, fallback.Side.Model)
	}
	res, ferr := callJudgeTarget(ctx, *fallback, prompt, "judge fallback")
	if ferr != nil {
		return JudgeCallResult{}, fmt.Errorf("judge primary %s failed: %w; judge fallback %s failed: %w",
			primary.Side.Model, perr, fallback.Side.Model, ferr)
	}
	return res, nil
}

func callJudgeTarget(ctx context.Context, t judgeTarget, prompt, role string) (JudgeCallResult, error) {
	key, err := lookupEvalKey(t.Side.Provider, role)
	if err != nil {
		return JudgeCallResult{}, err
	}
	var (
		text   string
		tokens int
	)
	switch t.Protocol {
	case catalog.ProtocolAnthropic:
		text, tokens, err = postAnthropicMessages(ctx, t.URL, key, t.Side.Model, prompt)
	case catalog.ProtocolOpenAICompatible:
		text, tokens, err = postChatCompletions(ctx, t.URL, key, t.Side.Model, prompt)
	default:
		err = fmt.Errorf("unsupported judge protocol %q", t.Protocol)
	}
	if err != nil {
		return JudgeCallResult{}, err
	}
	return JudgeCallResult{Text: text, Tokens: tokens, JudgeModelUsed: t.Side.Slug()}, nil
}

// judgeMaxTokens bounds the judge's answer (a short JSON score block).
const judgeMaxTokens = 2048

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func postJudge(ctx context.Context, url string, body any, headers map[string]string) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal judge request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("build judge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("judge HTTP call: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read judge response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &judgeHTTPError{Status: resp.StatusCode, ErrorType: providerErrorType(respBytes)}
	}
	return respBytes, nil
}

// knownProviderErrorTypes is the fixed allowlist of provider error
// identifiers the judge may surface (Anthropic error.type values and the
// common OpenAI-compatible error.code values). Anything else becomes
// "other": a provider field is untrusted text and could carry a token or a
// key fragment into logs and the committed JSONL results.
var knownProviderErrorTypes = map[string]bool{
	// Anthropic messages API.
	"authentication_error":  true,
	"permission_error":      true,
	"not_found_error":       true,
	"rate_limit_error":      true,
	"overloaded_error":      true,
	"api_error":             true,
	"invalid_request_error": true,
	"request_too_large":     true,
	// OpenAI-compatible chat completions (OpenRouter).
	"invalid_api_key":         true,
	"insufficient_quota":      true,
	"rate_limit_exceeded":     true,
	"model_not_found":         true,
	"server_error":            true,
	"context_length_exceeded": true,
}

// providerErrorType returns the provider's error identifier (error.type, or
// a string error.code) only when it is on knownProviderErrorTypes; any other
// present value is reported as "other", an absent one as "". Free text is
// never surfaced: a body can echo the API key.
func providerErrorType(body []byte) string {
	var env struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil || len(env.Error) == 0 {
		return ""
	}
	var obj struct {
		Type string `json:"type"`
		Code any    `json:"code"`
	}
	if json.Unmarshal(env.Error, &obj) != nil {
		return "other"
	}
	t := obj.Type
	if t == "" {
		if s, ok := obj.Code.(string); ok {
			t = s
		}
	}
	if t == "" {
		return "other"
	}
	if knownProviderErrorTypes[t] {
		return t
	}
	return "other"
}

func postAnthropicMessages(ctx context.Context, url, key, model, prompt string) (string, int, error) {
	body := map[string]any{
		"model":      model,
		"max_tokens": judgeMaxTokens,
		"messages":   []chatMessage{{Role: "user", Content: prompt}},
	}
	raw, err := postJudge(ctx, url, body, map[string]string{
		"x-api-key":         key,
		"anthropic-version": "2023-06-01",
	})
	if err != nil {
		return "", 0, err
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", 0, fmt.Errorf("parse judge response: %w", err)
	}
	var sb strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	if sb.Len() == 0 {
		return "", 0, fmt.Errorf("judge returned no text content")
	}
	return sb.String(), parsed.Usage.InputTokens + parsed.Usage.OutputTokens, nil
}

func postChatCompletions(ctx context.Context, url, key, model, prompt string) (string, int, error) {
	body := map[string]any{
		"model":    model,
		"messages": []chatMessage{{Role: "user", Content: prompt}},
	}
	raw, err := postJudge(ctx, url, body, map[string]string{
		"Authorization": "Bearer " + key,
		"Http-Referer":  "https://omnipus.ai",
		"X-Title":       "omnipus-evals",
	})
	if err != nil {
		return "", 0, err
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", 0, fmt.Errorf("parse judge response: %w", err)
	}
	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		return "", 0, fmt.Errorf("judge returned empty choices")
	}
	return parsed.Choices[0].Message.Content, parsed.Usage.PromptTokens + parsed.Usage.CompletionTokens, nil
}
