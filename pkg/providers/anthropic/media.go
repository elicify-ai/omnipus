package anthropicprovider

import (
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// anthropicUserContent preserves the image/PDF parts supported by the former
// connector, including caption-less media. Other media is resolved upstream.
func anthropicUserContent(message Message) []anthropic.ContentBlockParamUnion {
	content := make([]anthropic.ContentBlockParamUnion, 0, 1+len(message.Media))
	if message.Content != "" {
		content = append(content, anthropic.NewTextBlock(message.Content))
	}
	for _, dataURL := range message.Media {
		meta, data, ok := strings.Cut(strings.TrimPrefix(dataURL, "data:"), ",")
		if !ok || !strings.HasSuffix(meta, ";base64") {
			continue
		}
		mediaType := strings.TrimSuffix(meta, ";base64")
		if strings.HasPrefix(mediaType, "image/") {
			content = append(content, anthropic.ContentBlockParamUnion{OfImage: &anthropic.ImageBlockParam{Source: anthropic.ImageBlockParamSourceUnion{OfBase64: &anthropic.Base64ImageSourceParam{Data: data, MediaType: anthropic.Base64ImageSourceMediaType(mediaType)}}}})
		} else if mediaType == "application/pdf" {
			content = append(content, anthropic.ContentBlockParamUnion{OfDocument: &anthropic.DocumentBlockParam{Source: anthropic.DocumentBlockParamSourceUnion{OfBase64: &anthropic.Base64PDFSourceParam{Data: data}}}})
		}
	}
	if len(content) == 0 {
		content = append(content, anthropic.NewTextBlock(message.Content))
	}
	return content
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
