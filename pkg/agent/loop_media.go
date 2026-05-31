// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/h2non/filetype"

	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/media"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// resolveMediaRefs resolves media:// refs in messages.
// Images are base64-encoded into the Media array for multimodal LLMs.
// Non-image files (documents, audio, video) have their local path injected
// into Content so the agent can access them via file tools like read_file.
//
// Non-media:// strings are dropped (validation guard — every channel should
// only emit media:// refs, but the pass-through was a bug). Refs that cannot
// be resolved (unknown ref or file missing on disk) produce a visible
// "[attachment unavailable: <name>]" marker in Content so the LLM knows the
// file was referenced but unavailable, and increment droppedCounter when
// non-nil.
//
// Returns a new slice; original messages are not mutated.
func resolveMediaRefs(
	messages []providers.Message,
	store media.MediaStore,
	maxSize int,
	droppedCounter ...*atomic.Int64,
) []providers.Message {
	if store == nil {
		return messages
	}

	var counter *atomic.Int64
	if len(droppedCounter) > 0 {
		counter = droppedCounter[0]
	}

	result := make([]providers.Message, len(messages))
	copy(result, messages)

	for i, m := range result {
		if len(m.Media) == 0 {
			continue
		}

		resolved := make([]string, 0, len(m.Media))
		var pathTags []string
		var unavailableTags []string

		for _, ref := range m.Media {
			// Guard: only accept well-formed media:// refs. Non-matching strings
			// (e.g. raw local paths or HTTP URLs leaked by channel fallback code)
			// are silently dropped here — they were already logged at the channel
			// ingress point and must never reach the LLM content array.
			if _, err := media.ParseMediaRef(ref); err != nil {
				logger.WarnCF("agent", "resolveMediaRefs: dropping non-media:// string", map[string]any{
					"ref": ref,
				})
				continue
			}

			localPath, meta, err := store.ResolveWithMeta(ref)
			if err != nil {
				name := ref // truncated ref as fallback name
				if meta.Filename != "" {
					name = meta.Filename
				}
				logger.WarnCF("agent", "Failed to resolve media ref — attachment unavailable", map[string]any{
					"ref":   ref,
					"error": err.Error(),
				})
				if counter != nil {
					counter.Add(1)
				}
				unavailableTags = append(unavailableTags, "[attachment unavailable: "+name+"]")
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
				if counter != nil {
					counter.Add(1)
				}
				unavailableTags = append(unavailableTags, "[attachment unavailable: "+name+"]")
				continue
			}

			mime := detectMIME(localPath, meta)

			if strings.HasPrefix(mime, "image/") {
				dataURL := encodeImageToDataURL(localPath, mime, info, maxSize)
				if dataURL != "" {
					resolved = append(resolved, dataURL)
				}
				continue
			}

			pathTags = append(pathTags, buildPathTag(mime, localPath))
		}

		result[i].Media = resolved
		if len(pathTags) > 0 {
			result[i].Content = injectPathTags(result[i].Content, pathTags)
		}
		// Inject unavailable-attachment markers into Content so the LLM can
		// report them back to the user rather than silently ignoring the refs.
		for _, tag := range unavailableTags {
			if result[i].Content == "" {
				result[i].Content = tag
			} else {
				result[i].Content += " " + tag
			}
		}
	}

	return result
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

// injectPathTags replaces generic media tags in content with path-bearing versions,
// or appends if no matching generic tag is found.
func injectPathTags(content string, tags []string) string {
	for _, tag := range tags {
		var generic string
		switch {
		case strings.HasPrefix(tag, "[audio:"):
			generic = "[audio]"
		case strings.HasPrefix(tag, "[video:"):
			generic = "[video]"
		case strings.HasPrefix(tag, "[file:"):
			generic = "[file]"
		}

		if generic != "" && strings.Contains(content, generic) {
			content = strings.Replace(content, generic, tag, 1)
		} else if content == "" {
			content = tag
		} else {
			content += " " + tag
		}
	}
	return content
}
