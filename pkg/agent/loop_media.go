// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/h2non/filetype"

	"github.com/elicify-ai/omnipus/pkg/docextract"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// resolveMediaRefs resolves media:// refs in messages.
// Images are base64-encoded into the Media array for multimodal LLMs.
// PDFs are routed to the Media array as data:application/pdf;base64,… for
// PDF-capable models (native document blocks); for other models the text is
// extracted and injected into Content.
// Other document types (docx, txt, etc.) have their text extracted and injected
// into Content as a clearly delimited block. Binary files that cannot be
// extracted produce a descriptive notice in Content instead.
//
// Non-media:// strings are passed through unchanged (they may be pre-resolved
// data URLs or external URLs already handled by the caller).
// Refs that cannot be resolved produce a visible "[attachment unavailable: …]"
// marker in Content.
//
// Returns a new slice; original messages are not mutated.
func resolveMediaRefs(
	messages []providers.Message,
	store media.MediaStore,
	maxSize int,
	model string,
) []providers.Message {
	if store == nil {
		return messages
	}

	result := make([]providers.Message, len(messages))
	copy(result, messages)

	for i, m := range result {
		if len(m.Media) == 0 {
			continue
		}

		resolved := make([]string, 0, len(m.Media))
		var contentInjections []string

		for _, ref := range m.Media {
			// Pass through non-media:// strings (pre-resolved data URLs, HTTP URLs).
			if !strings.HasPrefix(ref, "media://") {
				resolved = append(resolved, ref)
				continue
			}

			localPath, meta, err := store.ResolveWithMeta(ref)
			if err != nil {
				name := ref
				if meta.Filename != "" {
					name = meta.Filename
				}
				logger.WarnCF("agent", "Failed to resolve media ref — attachment unavailable", map[string]any{
					"ref":   ref,
					"error": err.Error(),
				})
				contentInjections = append(contentInjections, "[attachment unavailable: "+name+"]")
				continue
			}

			info, err := os.Stat(localPath)
			if err != nil {
				name := meta.Filename
				if name == "" {
					name = ref
				}
				logger.WarnCF("agent", "Failed to stat media file — attachment unavailable", map[string]any{
					"path":  localPath,
					"error": err.Error(),
				})
				contentInjections = append(contentInjections, "[attachment unavailable: "+name+"]")
				continue
			}

			mime := detectMIME(localPath, meta)

			if strings.HasPrefix(mime, "image/") {
				dataURL := encodeImageToDataURL(localPath, mime, info, maxSize)
				if dataURL != "" {
					resolved = append(resolved, dataURL)
				} else {
					name := meta.Filename
					if name == "" {
						name = localPath
					}
					contentInjections = append(contentInjections,
						"[attachment unavailable: "+name+" (too large or unreadable)]")
				}
				continue
			}

			// PDF: route to native document blocks for capable models, else extract text.
			if isPDF(mime, meta.Filename) {
				if pdfCapableModel(model) {
					dataURL := encodePDFToDataURL(localPath, info, maxSize)
					if dataURL != "" {
						resolved = append(resolved, dataURL)
					} else {
						// File too large or unreadable — fall back to text extraction.
						inj := buildDocumentInjection(localPath, mime, meta.Filename, info.Size())
						contentInjections = append(contentInjections, inj)
					}
				} else {
					inj := buildDocumentInjection(localPath, mime, meta.Filename, info.Size())
					contentInjections = append(contentInjections, inj)
				}
				continue
			}

			// All other non-image files: extract text and inject into Content.
			inj := buildDocumentInjection(localPath, mime, meta.Filename, info.Size())
			contentInjections = append(contentInjections, inj)
		}

		result[i].Media = resolved
		if len(contentInjections) > 0 {
			result[i].Content = injectDocumentContent(result[i].Content, contentInjections)
		}
	}

	return result
}

// pdfCapableModel returns true for models known to support native PDF document
// blocks (base64 application/pdf content). Any model that returns false gets the
// text-extraction fallback, which works for every model.
//
// The allow-list is deliberately CONSERVATIVE. The failure modes are asymmetric:
// a false negative merely sends extracted text (slightly less rich, but always
// works), whereas a false positive sends a native PDF block to a model whose
// upstream provider rejects it with a hard HTTP 400 — which aborts the whole
// turn with an empty reply. When in doubt, prefer text.
//
// Known exclusions (matched first, before the allow-list):
//
//   - Claude *Haiku* — OpenRouter routes Haiku via Amazon Bedrock, which returns
//     "'claude-3-5-haiku-...' does not support PDF input." (verified 2026-06-01).
//     Only Claude Sonnet/Opus (3.5+) and Claude 4 reliably accept PDF.
//
// Allow-list substrings (case-insensitive, matched against the full model id):
//
//	Anthropic: claude-3.5-sonnet / claude-3-7-sonnet / sonnet-4 / opus-4 / claude-4
//	OpenAI:    gpt-4o, gpt-4.1, o1-, o3-
//	Google:    gemini-1.5, gemini-2
func pdfCapableModel(model string) bool {
	lower := strings.ToLower(model)

	// Exclusions take precedence: these match an allow-list substring but their
	// upstream provider rejects PDF input, so they MUST fall back to text.
	for _, deny := range []string{"haiku"} {
		if strings.Contains(lower, deny) {
			return false
		}
	}

	capableSubstrings := []string{
		// Anthropic — only Sonnet/Opus 3.5+ and Claude 4 accept PDF. Bare
		// "claude-3" is intentionally NOT listed (it matched Haiku and the
		// original Claude 3 models that lack PDF support).
		"claude-3.5-sonnet", "claude-3-5-sonnet",
		"claude-3.7-sonnet", "claude-3-7-sonnet",
		"claude-sonnet-4", "claude-opus-4", "claude-4",
		"sonnet-4", "opus-4",
		// OpenAI GPT-4o and 4.1 family
		"gpt-4o",
		"gpt-4.1",
		"openai/gpt-4o",
		"openai/gpt-4.1",
		// OpenAI o1/o3 reasoning models
		"o1-",
		"o3-",
		"openai/o1",
		"openai/o3",
		// Google Gemini 1.5+
		"gemini-1.5",
		"gemini-2",
		"google/gemini-1.5",
		"google/gemini-2",
	}
	for _, sub := range capableSubstrings {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

// downgradePDFMediaToText converts any native PDF data URL in each message's
// Media array back into extracted text injected into Content. It is the recovery
// path for providers that reject native PDF blocks at request time: text input
// works for every model, so the caller downgrades and retries once instead of
// failing the turn. Mutates messages in place; returns true if at least one PDF
// block was downgraded.
func downgradePDFMediaToText(messages []providers.Message) bool {
	const pdfPrefix = "data:application/pdf;base64,"
	const fallbackName = "attachment.pdf"
	anyChanged := false

	for i := range messages {
		if len(messages[i].Media) == 0 {
			continue
		}
		kept := make([]string, 0, len(messages[i].Media))
		var injections []string
		msgChanged := false

		for _, ref := range messages[i].Media {
			if !strings.HasPrefix(ref, pdfPrefix) {
				kept = append(kept, ref)
				continue
			}
			msgChanged = true
			data, decErr := base64.StdEncoding.DecodeString(ref[len(pdfPrefix):])
			if decErr != nil {
				injections = append(injections,
					"\n\n[Attached PDF could not be decoded for text fallback]")
				continue
			}
			text, ok, reason := docextract.ExtractBytes(data, "application/pdf", fallbackName)
			if ok {
				injections = append(injections, fmt.Sprintf(
					"\n\n[Attached file %q]\n%s\n[End of attached file]", fallbackName, text))
			} else {
				injections = append(injections, fmt.Sprintf(
					"\n\n[Attached file %q (application/pdf, %d bytes) — content could not be extracted: %s]",
					fallbackName, len(data), reason))
			}
		}

		if msgChanged {
			messages[i].Media = kept
			if len(injections) > 0 {
				messages[i].Content = injectDocumentContent(messages[i].Content, injections)
			}
			anyChanged = true
		}
	}
	return anyChanged
}

// isImageInputRejection reports whether err is a provider rejecting image input
// because the model has no vision capability (e.g. OpenRouter→Bedrock:
// "'claude-3-5-haiku-...' does not support image input."). Unlike PDFs, images
// have no text fallback, so the caller turns this into a friendly "switch to a
// vision-capable model" message rather than surfacing a raw 400.
//
// The match is intentionally NARROW. It requires the error to express both the
// image-input domain and a capability-absence phrase — "does not support image",
// "image input is not supported", "no image support", etc. Errors that describe
// a corrupt/oversized image, a content-moderation block, or generic provider
// failures that happen to mention "image" are NOT matched. Specifically:
//
//   - "invalid image data", "corrupt image", "too large" → NOT matched
//   - "content policy", "moderation", "safety" → NOT matched
//   - "unable to process image" → NOT matched (ambiguous root cause)
//
// Capability-absence phrases that ARE matched (case-insensitive):
//
//	"does not support image"   – provider states the model lacks image support
//	"image input"              – the specific Bedrock phrase ("does not support image input")
//	"no image support"         – generic capability-absent form
//	"not support image"        – grammatical variant ("model does not support image…")
//	"image not supported"      – reversed subject/predicate form
//	"image input not supported"– variant of the Bedrock phrase
func isImageInputRejection(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())

	// Quick pre-filter: must mention "image" at all.
	if !strings.Contains(lower, "image") {
		return false
	}

	// Explicit exclusions — these are NOT vision-capability errors.
	for _, exclude := range []string{
		"invalid image",
		"corrupt",
		"too large",
		"moderation",
		"content policy",
		"safety",
		"unable to process",
	} {
		if strings.Contains(lower, exclude) {
			return false
		}
	}

	// Capability-absence phrases. Each requires both the "image" domain and an
	// explicit statement that the model/provider lacks vision support.
	capabilityPhrases := []string{
		"image input", // also covers "does not support image input", "image input not supported"
		"no image support",
		"not support image", // also covers "does not support image"
		"image not supported",
	}
	for _, phrase := range capabilityPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// isPDF returns true for files detected as PDF by MIME type or filename extension.
func isPDF(mime, filename string) bool {
	if mime == "application/pdf" {
		return true
	}
	return strings.HasSuffix(strings.ToLower(filename), ".pdf")
}

// encodePDFToDataURL base64-encodes a PDF file as a data:application/pdf;base64,…
// data URL for native document blocks. Returns empty string if the file exceeds
// maxSize or cannot be read.
func encodePDFToDataURL(localPath string, info os.FileInfo, maxSize int) string {
	if info.Size() > int64(maxSize) {
		logger.WarnCF("agent", "PDF file too large for native block, falling back to text extraction", map[string]any{
			"path":     localPath,
			"size":     info.Size(),
			"max_size": maxSize,
		})
		return ""
	}

	f, err := os.Open(localPath)
	if err != nil {
		logger.WarnCF("agent", "Failed to open PDF file", map[string]any{
			"path":  localPath,
			"error": err.Error(),
		})
		return ""
	}
	defer f.Close()

	prefix := "data:application/pdf;base64,"
	encodedLen := base64.StdEncoding.EncodedLen(int(info.Size()))
	var buf bytes.Buffer
	buf.Grow(len(prefix) + encodedLen)
	buf.WriteString(prefix)

	encoder := base64.NewEncoder(base64.StdEncoding, &buf)
	if _, err := io.Copy(encoder, f); err != nil {
		logger.WarnCF("agent", "Failed to encode PDF file", map[string]any{
			"path":  localPath,
			"error": err.Error(),
		})
		return ""
	}
	encoder.Close()

	return buf.String()
}

// buildDocumentInjection extracts text from a document file and returns a
// formatted injection string for the message Content. If extraction fails
// it returns an honest failure notice with file metadata but NO local path.
func buildDocumentInjection(localPath, mime, filename string, size int64) string {
	if filename == "" {
		parts := strings.Split(localPath, "/")
		filename = parts[len(parts)-1]
	}

	text, ok, reason := docextract.Extract(localPath, mime, filename)
	if !ok {
		return fmt.Sprintf(
			"\n\n[Attached file %q (%s, %d bytes) — content could not be extracted: %s]",
			filename, mime, size, reason,
		)
	}
	return fmt.Sprintf(
		"\n\n[Attached file %q]\n%s\n[End of attached file]",
		filename, text,
	)
}

// injectDocumentContent appends document content injections to the message content.
func injectDocumentContent(content string, injections []string) string {
	var sb strings.Builder
	sb.WriteString(content)
	for _, inj := range injections {
		sb.WriteString(inj)
	}
	return sb.String()
}

func buildArtifactTags(store media.MediaStore, refs []string) []string {
	if store == nil || len(refs) == 0 {
		return nil
	}

	tags := make([]string, 0, len(refs))
	for _, ref := range refs {
		localPath, meta, err := store.ResolveWithMeta(ref)
		if err != nil {
			logger.WarnCF("agent", "buildArtifactTags: resolve failed",
				map[string]any{"ref": ref, "error": err.Error()})
			continue
		}
		mime := detectMIME(localPath, meta)
		tags = append(tags, buildPathTag(mime, localPath))
	}

	return tags
}

// detectMIME determines the MIME type from metadata or magic-bytes detection.
// Returns empty string if detection fails.
func detectMIME(localPath string, meta media.MediaMeta) string {
	if meta.ContentType != "" {
		return meta.ContentType
	}
	kind, err := filetype.MatchFile(localPath)
	if err != nil || kind == filetype.Unknown {
		return ""
	}
	return kind.MIME.Value
}

// encodeImageToDataURL base64-encodes an image file into a data URL.
// Returns empty string if the file exceeds maxSize or encoding fails.
func encodeImageToDataURL(localPath, mime string, info os.FileInfo, maxSize int) string {
	if info.Size() > int64(maxSize) {
		logger.WarnCF("agent", "Media file too large, skipping", map[string]any{
			"path":     localPath,
			"size":     info.Size(),
			"max_size": maxSize,
		})
		return ""
	}

	f, err := os.Open(localPath)
	if err != nil {
		logger.WarnCF("agent", "Failed to open media file", map[string]any{
			"path":  localPath,
			"error": err.Error(),
		})
		return ""
	}
	defer f.Close()

	prefix := "data:" + mime + ";base64,"
	encodedLen := base64.StdEncoding.EncodedLen(int(info.Size()))
	var buf bytes.Buffer
	buf.Grow(len(prefix) + encodedLen)
	buf.WriteString(prefix)

	encoder := base64.NewEncoder(base64.StdEncoding, &buf)
	if _, err := io.Copy(encoder, f); err != nil {
		logger.WarnCF("agent", "Failed to encode media file", map[string]any{
			"path":  localPath,
			"error": err.Error(),
		})
		return ""
	}
	encoder.Close()

	return buf.String()
}

// buildPathTag creates a structured tag exposing the local file path.
// Tag type is derived from MIME: [audio:/path], [video:/path], or [file:/path].
// This is retained for buildArtifactTags (agent-generated files) and must not
// be used for user-uploaded files whose paths are outside the sandbox workspace.
func buildPathTag(mime, localPath string) string {
	switch {
	case strings.HasPrefix(mime, "audio/"):
		return "[audio:" + localPath + "]"
	case strings.HasPrefix(mime, "video/"):
		return "[video:" + localPath + "]"
	default:
		return "[file:" + localPath + "]"
	}
}
