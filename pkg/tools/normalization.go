package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/media"
)

const (
	largeBase64OmittedMessage = "[Tool returned a large base64-like payload; omitted from model context.]"
	inlineMediaOmittedMessage = "[Tool returned inline media content; omitted from model context.]"
	inlineMediaStoredMessage  = "[Tool returned inline media content (%s); omitted from model context and registered as a media attachment.]"
)

var (
	inlineMarkdownDataURLRe = regexp.MustCompile(`!\[[^\]]*\]\((data:[^)]+)\)`)
	inlineRawDataURLRe      = regexp.MustCompile(`data:[^;\s]+;base64,[A-Za-z0-9+/=\r\n]+`)
)

func normalizeToolResult(
	ctx context.Context,
	result *ToolResult,
	toolName string,
	store media.MediaStore,
	channel string,
	chatID string,
) *ToolResult {
	if result == nil {
		return nil
	}

	notes := make([]string, 0, 2)
	seen := make(map[string]struct{})

	if store == nil || channel == "" || chatID == "" {
		slog.Warn("normalizeToolResult: media extraction skipped — missing context",
			"tool", toolName,
			"has_store", store != nil,
			"channel", channel,
			"chat_id", chatID,
			"has_media_payload", strings.Contains(result.ForLLM, "data:"),
		)
	}
	if store != nil && channel != "" && chatID != "" {
		var refs []string
		var extractedNotes []string

		result.ForLLM, refs, extractedNotes = extractInlineMediaRefs(
			ctx,
			result.ForLLM,
			toolName,
			store,
			channel,
			chatID,
			seen,
		)
		result.Media = append(result.Media, refs...)
		notes = append(notes, extractedNotes...)

		result.ForUser, refs, extractedNotes = extractInlineMediaRefs(
			ctx,
			result.ForUser,
			toolName,
			store,
			channel,
			chatID,
			seen,
		)
		result.Media = append(result.Media, refs...)
		notes = append(notes, extractedNotes...)
	}

	result.ForLLM = sanitizeToolLLMContent(result.ForLLM)

	if len(result.Media) > 0 && len(notes) > 0 {
		if strings.TrimSpace(result.ForLLM) == "" {
			result.ForLLM = strings.Join(notes, "\n")
		} else {
			result.ForLLM = strings.TrimSpace(result.ForLLM) + "\n" + strings.Join(notes, "\n")
		}
	}
	if len(result.Media) > 0 && strings.TrimSpace(result.ForLLM) == "" {
		result.ForLLM = "[Tool returned media content; omitted from model context and registered as a media attachment.]"
	}

	return result
}

func sanitizeToolLLMContent(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text
	}
	if inlineMarkdownDataURLRe.MatchString(trimmed) || inlineRawDataURLRe.MatchString(trimmed) {
		cleaned := inlineMarkdownDataURLRe.ReplaceAllString(trimmed, "")
		cleaned = inlineRawDataURLRe.ReplaceAllString(cleaned, "")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			return inlineMediaOmittedMessage
		}
		return cleaned + "\n" + inlineMediaOmittedMessage
	}
	if looksLikeLargeBase64Payload(trimmed) {
		return largeBase64OmittedMessage
	}
	return text
}

func looksLikeLargeBase64Payload(text string) bool {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 1024 {
		return false
	}

	nonSpace := 0
	base64Like := 0
	spaceCount := 0

	for _, r := range trimmed {
		if unicode.IsSpace(r) {
			spaceCount++
			continue
		}
		nonSpace++
		if (r >= 'A' && r <= 'Z') ||
			(r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			r == '+' || r == '/' || r == '=' {
			base64Like++
		}
	}

	if nonSpace == 0 {
		return false
	}

	ratio := float64(base64Like) / float64(nonSpace)
	return ratio >= 0.97 && spaceCount <= len(trimmed)/128
}

func extractInlineMediaRefs(
	ctx context.Context,
	text string,
	toolName string,
	store media.MediaStore,
	channel string,
	chatID string,
	seen map[string]struct{},
) (cleaned string, refs []string, notes []string) {
	cleaned = text

	matches := inlineMarkdownDataURLRe.FindAllStringSubmatch(cleaned, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		dataURL := match[1]
		ref, note := storeInlineDataURL(ctx, toolName, store, channel, chatID, dataURL, seen)
		if ref != "" {
			refs = append(refs, ref)
		}
		if note != "" {
			notes = append(notes, note)
		}
		cleaned = strings.ReplaceAll(cleaned, match[0], "")
	}

	rawMatches := inlineRawDataURLRe.FindAllString(cleaned, -1)
	for _, dataURL := range rawMatches {
		ref, note := storeInlineDataURL(ctx, toolName, store, channel, chatID, dataURL, seen)
		if ref != "" {
			refs = append(refs, ref)
		}
		if note != "" {
			notes = append(notes, note)
		}
		cleaned = strings.ReplaceAll(cleaned, dataURL, "")
	}

	return strings.TrimSpace(cleaned), refs, notes
}

func storeInlineDataURL(
	ctx context.Context,
	toolName string,
	store media.MediaStore,
	channel string,
	chatID string,
	dataURL string,
	seen map[string]struct{},
) (ref string, note string) {
	dataURL = strings.TrimSpace(dataURL)
	if _, ok := seen[dataURL]; ok {
		return "", ""
	}
	seen[dataURL] = struct{}{}

	if !strings.HasPrefix(strings.ToLower(dataURL), "data:") {
		return "", ""
	}

	comma := strings.IndexByte(dataURL, ',')
	if comma <= 5 {
		return "", "[Tool returned inline media content that could not be parsed.]"
	}

	metaPart := dataURL[:comma]
	payload := dataURL[comma+1:]
	if !strings.Contains(strings.ToLower(metaPart), ";base64") {
		return "", "[Tool returned inline media content that was not base64-encoded.]"
	}

	mimeType := strings.TrimSpace(strings.TrimPrefix(metaPart, "data:"))
	if semi := strings.IndexByte(mimeType, ';'); semi >= 0 {
		mimeType = mimeType[:semi]
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	payload = strings.NewReplacer("\n", "", "\r", "", "\t", "", " ", "").Replace(payload)
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Sprintf("[Tool returned inline media content (%s) that could not be decoded.]", mimeType)
	}

	ext := extensionForMIMEType(mimeType)
	safeTool := sanitizeIdentifierComponent(toolName)

	// Determine the storage destination and cleanup policy.
	// When a persisted session ID is in context, write the file under the
	// session's uploads directory (identical to user-uploaded files) so it
	// is (a) immune to the TTL media cleaner and (b) cascade-deleted with
	// the session via the existing uploadsRoot() logic in pkg/session.
	// When there is no session (channel media, repair path, etc.) fall back
	// to the ephemeral TempDir with the default delete_on_cleanup policy.
	sessionID := ToolTranscriptSessionID(ctx)
	uploadsDir, hasSession := media.SessionUploadsDir(sessionID)

	var dir string
	var cleanupPolicy media.CleanupPolicy
	if hasSession {
		dir = uploadsDir
		cleanupPolicy = media.CleanupPolicyForgetOnly
	} else {
		dir = media.TempDir()
		cleanupPolicy = media.CleanupPolicyDeleteOnCleanup
	}

	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Sprintf("[Tool returned inline media content (%s) but it could not be stored.]", mimeType)
	}

	tmpFile, err := os.CreateTemp(dir, "tool-"+safeTool+"-*"+ext)
	if err != nil {
		return "", fmt.Sprintf("[Tool returned inline media content (%s) but it could not be stored.]", mimeType)
	}
	tmpPath := tmpFile.Name()
	if _, err = tmpFile.Write(decoded); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Sprintf("[Tool returned inline media content (%s) but it could not be stored.]", mimeType)
	}
	if err = tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Sprintf("[Tool returned inline media content (%s) but it could not be stored.]", mimeType)
	}

	filename := safeTool + ext

	// Scope selection:
	// - Session-bound media: media.SessionInlineScopePrefix+"<sessionID>" — stable
	//   and derivable from the sessionID alone, so deleteSession can call
	//   ReleaseAll(media.SessionInlineScopePrefix+"<sessionID>") to free all refs
	//   for that session in one call. This scope is also what CleanExpired pins
	//   (never TTL-expired) so the media survives in chat history.
	// - Ephemeral (no session): per-call scope with UnixNano suffix to avoid
	//   collisions between concurrent tool calls sharing a chatID.
	var scope string
	if hasSession {
		scope = media.SessionInlineScopePrefix + sessionID
	} else {
		scope = fmt.Sprintf(
			"tool:inline:%s:%s:%s:%d",
			safeTool,
			channel,
			chatID,
			time.Now().UnixNano(),
		)
	}

	ref, err = store.Store(tmpPath, media.MediaMeta{
		Filename:      filename,
		ContentType:   mimeType,
		Source:        fmt.Sprintf("tool:inline:%s", safeTool),
		CleanupPolicy: cleanupPolicy,
	}, scope)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Sprintf("[Tool returned inline media content (%s) but it could not be registered.]", mimeType)
	}

	return ref, fmt.Sprintf(inlineMediaStoredMessage, mimeType)
}

func extensionForMIMEType(mimeType string) string {
	if mimeType == "" {
		return ".bin"
	}
	// Prefer well-known extensions — mime.ExtensionsByType returns the OS mime DB
	// ordering, which on many systems puts .jfif before .jpg for image/jpeg.
	switch strings.ToLower(mimeType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "video/mp4":
		return ".mp4"
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return filepath.Ext(mimeType)
}
