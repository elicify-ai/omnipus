package anthropicprovider

import (
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// anthropicUserContent preserves the image/PDF parts supported by the former
// connector, including caption-less media. Other media is resolved upstream.
func anthropicUserContent(message Message) []anthropic.ContentBlockParamUnion {
	content := make([]anthropic.ContentBlockParamUnion, 0, 1+len(message.Media))
	if message.Content != "" {
		content = append(content, anthropic.NewTextBlock(message.Content))
	}
	for _, dataURL := range message.Media {
		if protocoltypes.IsPDFDataURL(dataURL) {
			content = append(content, anthropicPDFBlock(dataURL))
			continue
		}
		meta, data, ok := strings.Cut(strings.TrimPrefix(dataURL, "data:"), ",")
		if !ok || !strings.HasSuffix(meta, ";base64") {
			continue
		}
		mediaType := strings.TrimSuffix(meta, ";base64")
		if strings.HasPrefix(mediaType, "image/") {
			content = append(content, anthropic.ContentBlockParamUnion{OfImage: &anthropic.ImageBlockParam{Source: anthropic.ImageBlockParamSourceUnion{OfBase64: &anthropic.Base64ImageSourceParam{Data: data, MediaType: anthropic.Base64ImageSourceMediaType(mediaType)}}}})
		}
	}
	if len(content) == 0 {
		content = append(content, anthropic.NewTextBlock(message.Content))
	}
	return content
}

// anthropicPDFDocument returns the native document block of a PDF data URL
// (exact bytes, base64 source), or nil when the data URL is malformed
// (FR-18).
func anthropicPDFDocument(dataURL string) *anthropic.DocumentBlockParam {
	payload, _, ok := protocoltypes.DecodePDFDataURL(dataURL)
	if !ok {
		return nil
	}
	return &anthropic.DocumentBlockParam{Source: anthropic.DocumentBlockParamSourceUnion{OfBase64: &anthropic.Base64PDFSourceParam{Data: payload}}}
}

// anthropicPDFBlock is the user-message form of a PDF data URL: the
// document block, or the visible malformed-data note (never silence).
func anthropicPDFBlock(dataURL string) anthropic.ContentBlockParamUnion {
	if doc := anthropicPDFDocument(dataURL); doc != nil {
		return anthropic.ContentBlockParamUnion{OfDocument: doc}
	}
	return anthropic.NewTextBlock(protocoltypes.PDFMalformedNote)
}

// Parallel results remain correlated in a single user-role tool-result group.
// A normal user message or volatile text is never merged into that group.
func appendToolResult(messages []anthropic.MessageParam, result anthropic.ContentBlockParamUnion) []anthropic.MessageParam {
	if n := len(messages); n > 0 && messages[n-1].Role == "user" {
		previous := &messages[n-1]
		if len(previous.Content) > 0 && previous.Content[len(previous.Content)-1].OfToolResult != nil {
			previous.Content = append(previous.Content, result)
			return messages
		}
	}
	return append(messages, anthropic.NewUserMessage(result))
}
