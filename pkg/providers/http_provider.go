// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/openai_compat"
)

type HTTPProvider struct {
	delegate *openai_compat.Provider
}

func NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
	apiKey, apiBase, proxy, maxTokensField string,
	requestTimeoutSeconds int,
	extraBody map[string]any,
) (*HTTPProvider, error) {
	p, err := openai_compat.NewProvider(
		apiKey,
		apiBase,
		proxy,
		openai_compat.WithMaxTokensField(maxTokensField),
		openai_compat.WithRequestTimeout(time.Duration(requestTimeoutSeconds)*time.Second),
		openai_compat.WithExtraBody(extraBody),
	)
	if err != nil {
		return nil, err
	}
	return &HTTPProvider{delegate: p}, nil
}

func (p *HTTPProvider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	return p.delegate.Chat(ctx, messages, tools, model, options)
}

// ChatStream implements providers.StreamingProvider by delegating to the
// OpenAI-compatible streaming endpoint (SSE with stream: true).
func (p *HTTPProvider) ChatStream(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	onChunk func(accumulated string),
	onProgress OnToolCallProgress,
) (*LLMResponse, error) {
	return p.delegate.ChatStream(ctx, messages, tools, model, options, onChunk, onProgress)
}

func (p *HTTPProvider) GetDefaultModel() string {
	return ""
}

func (p *HTTPProvider) SupportsNativeSearch() bool {
	return p.delegate.SupportsNativeSearch()
}

// APIBase returns the resolved base URL this transport posts to. It exists so
// the factory's dispatch tests can assert the URL a catalog row produced
// (ADR-067 DS-3) without reaching into the openai_compat package's
// unexported state.
func (p *HTTPProvider) APIBase() string {
	if p == nil || p.delegate == nil {
		return ""
	}
	return p.delegate.APIBase()
}
