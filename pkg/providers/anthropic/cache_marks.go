package anthropicprovider

import (
	"net/url"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// cacheLifetime is an endpoint dialect, not a feature switch. Unknown and
// automatic-only hosts receive no field. MiniMax M3 is automatic-only.
func (p *Provider) cacheLifetime(model string) string {
	u, err := url.Parse(p.baseURL)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Hostname()) {
	case "api.anthropic.com":
		return "1h"
	case "api.minimax.io", "api.minimax.cn", "api.minimaxi.com":
		if strings.HasPrefix(strings.ToLower(model), "minimax-m3") {
			return ""
		}
		return "5m"
	case "dashscope.aliyuncs.com", "dashscope-intl.aliyuncs.com":
		return "5m"
	case "ai-gateway.vercel.sh":
		if strings.Contains(strings.ToLower(model), "claude") {
			return "5m"
		}
	}
	return ""
}

func (p *Provider) markCache(params *anthropic.MessageNewParams, source []Message, model string) {
	lifetime := p.cacheLifetime(model)
	if lifetime == "" {
		return
	}
	mark := anthropic.NewCacheControlEphemeralParam()
	mark.TTL = anthropic.CacheControlEphemeralTTL(lifetime)
	if lifetime == "1h" {
		if len(params.Tools) > 0 && params.Tools[len(params.Tools)-1].OfTool != nil {
			params.Tools[len(params.Tools)-1].OfTool.CacheControl = mark
		}
		if len(params.System) > 0 {
			params.System[len(params.System)-1].CacheControl = mark
		}
	}
	limit := len(params.Messages)
	if len(source) > 0 {
		last := source[len(source)-1]
		// Direct adapter callers can supply the generated clock block as well
		// as the request assembler's private Volatile marker.
		if last.Volatile || (last.Role == "user" && strings.HasPrefix(last.Content, "## Current Time\n")) {
			limit--
		}
	}
	pending := map[string]bool{}
	var candidate *anthropic.ContentBlockParamUnion
	for i := 0; i < limit; i++ {
		for j := range params.Messages[i].Content {
			block := &params.Messages[i].Content[j]
			if block.OfToolUse != nil {
				pending[block.OfToolUse.ID] = true
			}
			if block.OfToolResult != nil {
				delete(pending, block.OfToolResult.ToolUseID)
			}
			if len(pending) == 0 {
				candidate = block
			}
		}
	}
	if candidate == nil {
		return
	}
	tail := anthropic.NewCacheControlEphemeralParam()
	tail.TTL = "5m"
	switch {
	case candidate.OfText != nil:
		candidate.OfText.CacheControl = tail
	case candidate.OfImage != nil:
		candidate.OfImage.CacheControl = tail
	case candidate.OfDocument != nil:
		candidate.OfDocument.CacheControl = tail
	case candidate.OfToolResult != nil:
		candidate.OfToolResult.CacheControl = tail
	}
}
