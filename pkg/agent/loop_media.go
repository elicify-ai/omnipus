// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/h2non/filetype"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/elicify-ai/omnipus/pkg/docextract"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/media/resize"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
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
// When a non-nil offload sink is supplied, attachments that no provider path
// can present (e.g. AVIF/HEIC with no decoder) are offloaded at step 5: copied
// into the workspace work/ dir under a safe-derived name and surfaced to the
// model as a filesystem path + format-aware guidance line (FR-020/020a/021).
// A nil sink disables offload — the chain degrades to the step-7 honest marker.
//
// A non-nil capability catalog applies the step-1 gate (FR-010): if the model
// lacks the image modality, native image send (step 2) is skipped and the file
// routes directly to step 5 offload. A nil catalog is optimistic (FR-026) — the
// gate always passes and a wrong guess costs one outcome-based step-4 retry.
//
// A non-nil refcounter tracks manifest refcounts for workspace library refs
// (FR-007a): each workspace ref processed here increments the manifest
// refcount (per-session deduped); the matching decrement fires at session
// cleanup. A nil refcounter disables tracking (legacy/no-workspace turns).
//
// Returns a new slice; original messages are not mutated.
func resolveMediaRefs(
	messages []providers.Message,
	store media.MediaStore,
	maxSize int,
	model string,
) []providers.Message {
	return resolveMediaRefsWithOffload(messages, store, maxSize, "", model, nil, nil, nil, "")
}

// resolveMediaRefsWithOffload is the full presentation path: the legacy
// resolveMediaRefs behavior plus step-1 capability gate (catalog),
// step-5 offload (sink, FR-020/020a/021/022) and manifest refcount
// tracking (rc, FR-007a) when the respective arguments are non-nil. The
// four-argument resolveMediaRefs is a thin wrapper that calls this with
// nil sink/catalog/rc so existing call sites preserve their exact behavior.
//
// callerWorkspace is the turn's workspace ID (FR-028a). It MUST be set for
// media://workspace/<ws>/<id> refs; empty is the legacy posture (global
// registry only). Without it, workspace-library attachments fail closed
// with ErrWorkspaceContextRequired and surface as [attachment unavailable].
func resolveMediaRefsWithOffload(
	messages []providers.Message,
	store media.MediaStore,
	maxSize int,
	provider, model string,
	sink *offloadSink,
	cat *catalog.Catalog,
	rc workspaceRefcounter,
	callerWorkspace string,
) []providers.Message {
	if store == nil {
		return messages
	}

	resolveOpts := media.ResolveOpts{}
	if callerWorkspace != "" {
		resolveOpts = media.WithCallerWorkspace(callerWorkspace)
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

			localPath, meta, err := store.ResolveWithMetaOpts(ref, resolveOpts)
			if err != nil {
				name := sanitizeInjectedName(fallbackName(meta.Filename, ref))
				logger.WarnCF("agent", "Failed to resolve media ref — attachment unavailable", map[string]any{
					"ref":   ref,
					"error": err.Error(),
				})
				contentInjections = append(contentInjections, "[attachment unavailable: "+name+"]")
				continue
			}

			// FR-002: sha256-on-read verification for workspace refs. The path-
			// returning resolver (ResolvePathWithCaller) intentionally skips the
			// integrity check for transport-only consumers; the decode-bound agent
			// loop path MUST verify before opening the file. A mismatch is terminal
			// for this ref — route to step-7 honest marker.
			if media.IsWorkspaceRef(ref) && meta.SHA256 != "" {
				if verifyErr := verifyFileIntegrity(localPath, meta.SHA256); verifyErr != nil {
					name := sanitizeInjectedName(fallbackName(meta.Filename, ref))
					logger.WarnCF("agent", "SHA-256 integrity check failed for workspace ref", map[string]any{
						"ref":   ref,
						"path":  localPath,
						"error": verifyErr.Error(),
					})
					contentInjections = append(
						contentInjections,
						"[attachment unavailable: "+name+" (integrity check failed)]",
					)
					continue
				}
			}

			info, err := os.Stat(localPath)
			if err != nil {
				name := sanitizeInjectedName(fallbackName(meta.Filename, ref))
				logger.WarnCF("agent", "Failed to stat media file — attachment unavailable", map[string]any{
					"path":  localPath,
					"error": err.Error(),
				})
				contentInjections = append(contentInjections, "[attachment unavailable: "+name+"]")
				continue
			}

			// FR-007a: record that this turn references the workspace
			// library file (per-session-deduped increment). Legacy
			// media://<uuid> and agent-inline refs are no-ops (no manifest
			// entry). Errors are logged-not-fatal inside the helper.
			incrementWorkspaceRef(rc, ref)

			mime := detectMIME(localPath, meta)

			// D1b (library-spec, 2026-07-29 UAT): announce every successfully
			// resolved workspace-library attachment with its real,
			// agent-actionable path BEFORE any type-specific handling below —
			// the model must be told a file arrived and WHERE, in text it can
			// act on (pass straight to read_file/library_read), rather than
			// guessing a filename the way Ray did in the UAT. Applies to every
			// branch below (image, PDF, document, offload) uniformly; a
			// non-workspace ref (legacy media://<uuid>, channel-native
			// attachments) has no "workspace-relative path" to announce, so
			// buildUploadAnnouncement returns "" for those and nothing is
			// injected.
			if announcement := buildUploadAnnouncement(ref, meta); announcement != "" {
				contentInjections = append(contentInjections, announcement)
			}

			if strings.HasPrefix(mime, "image/") {
				// Step 1 — capability gate (FR-010). If the catalog says
				// the model lacks the image modality, skip native send
				// (step 2 normalize/resize + step 3 native block + step 4
				// outcome retry) and route directly to the step 5/6/7
				// fallback — the same path encodeImageToDataURL's failure
				// takes. A nil catalog is optimistic (FR-026): the gate
				// always passes and a wrong guess self-corrects via the
				// outcome-based step-4 retry.
				var dataURL string
				if modelSupportsImage(cat, provider, model) {
					dataURL = encodeImageToDataURLCached(
						localPath,
						mime,
						info,
						maxSize,
						model,
						resizeBudgetForModel(cat, provider, model, maxSize),
					)
				}
				if dataURL != "" {
					resolved = append(resolved, dataURL)
				} else if mime == "image/svg+xml" {
					// Option B (ADR-051 RD1 extension): rasterization failed,
					// but an SVG is XML text — inject the markup into Content
					// (step 6) so ANY model (including text-only ones) can
					// reason about the image from its code. Never drop it
					// silently. FR-022: when a step-5 offload sink is
					// available, the guidance line + file path (step 5) prefix
					// the markup — steps 5 and 6 compose, never either/or.
					textInj := buildDocumentInjection(localPath, mime, meta.Filename, info.Size())
					if offInj, ok := sink.offload(localPath, mime, meta.Filename, model); ok {
						contentInjections = append(contentInjections, offInj+textInj)
					} else {
						contentInjections = append(contentInjections, textInj)
					}
				} else {
					// Step-5 offload (FR-020/020a/021, H1-M2): the image could
					// not be normalized — unsupported format (AVIF/HEIC/ICO),
					// a decode bomb, oversize, or resize-ladder floor. Copy the
					// file into the workspace work/ dir under a safe-derived
					// name and inject the format-aware guidance + filesystem
					// path. Non-image files are not text-extractable, so step 6
					// does not fire (FR-022 negative case). With no sink
					// (legacy/empty-workspace turn) this degrades to the step-7
					// honest marker — previously these attachments all landed
					// here with the misleading "too large or unreadable" reason
					// regardless of cause (H1-M2).
					if offInj, ok := sink.offload(localPath, mime, meta.Filename, model); ok {
						contentInjections = append(contentInjections, offInj)
					} else {
						name := sanitizeInjectedName(fallbackName(meta.Filename, localPath))
						contentInjections = append(contentInjections,
							"[attachment unavailable: "+name+" (too large or unreadable)]")
					}
				}
				continue
			}

			// PDF: route to native document blocks for capable models, else
			// extract text. When the capability catalog says the model lacks
			// the pdf modality (step-1 gate), skip native send and route to
			// step-5 offload with document-class guidance, falling back to
			// step-6 text extraction (FR-022) and step-7 honest marker.
			if isPDF(mime, meta.Filename) {
				if modelSupportsPDF(cat, provider, model) && pdfCapableModel(model) {
					dataURL := encodePDFToDataURL(localPath, info, maxSize)
					if dataURL != "" {
						resolved = append(resolved, dataURL)
					} else {
						inj := buildDocumentInjection(localPath, mime, meta.Filename, info.Size())
						contentInjections = append(contentInjections, inj)
					}
				} else if !modelSupportsPDF(cat, provider, model) {
					// Step-1 gate: text-only model + PDF → offload with
					// document-class guidance + text extraction (step 5 + 6).
					textInj := buildDocumentInjection(localPath, mime, meta.Filename, info.Size())
					if offInj, ok := sink.offload(localPath, mime, meta.Filename, model); ok {
						contentInjections = append(contentInjections, offInj+textInj)
					} else {
						contentInjections = append(contentInjections, textInj)
					}
				} else {
					// Model not in the PDF-capable allow-list but capability
					// gate passed (or catalog is nil/text-only model wasn't
					// recognized) — extract text (step 6).
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

// buildUploadAnnouncement returns the D1b "[user uploaded: ...]" content
// injection for a successfully-resolved media ref, or "" for a ref this
// function has nothing useful to announce for: a non-workspace ref (legacy
// media://<uuid>, channel-native attachments) has no "workspace-relative
// path" the way a chat-upload's workspace/work/.library/ dual-write does
// (D-1), so nothing is announced for those.
//
// The returned path is NEVER run through sanitizeInjectedName's
// truncate-to-128-runes cap: both of its possible sources — the exact
// recorded write-time path (LookupUploadWorkPath) and the fallback formula
// (FallbackAnnouncedUploadPath) — route through SanitizeUploadFilename
// first, which already guarantees no control characters and no path
// separators beyond the deliberate ".library/" prefix this file's own
// upload handler adds. Truncating an otherwise-valid up-to-256-character
// filename to 128 runes here would corrupt the exact string the model is
// meant to pass straight to read_file/library_read — the one thing this
// fix cannot afford to get wrong.
func buildUploadAnnouncement(ref string, meta media.MediaMeta) string {
	if !media.IsWorkspaceRef(ref) {
		return ""
	}
	workPath, ok := LookupUploadWorkPath(ref)
	if !ok {
		workPath = FallbackAnnouncedUploadPath(meta.Filename)
	}
	if workPath == "" {
		return ""
	}
	return "\n\n[user uploaded: " + workPath + "]"
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

// isImageInputRejection and its tests were retired in the Wave 1 fix pass
// (ADR-051 §RD2 / FR-012). The shared classifier in translate_error.go
// owns the media-class decision now — it covers both capability-absence
// and format-rejection shapes (incl. the xAI/Grok incident string) and
// uses status/body when available, not a string substring alone. The
// media-downgrade helper (media_downgrade.go::TryMediaDowngrade) consumes
// the classifier and applies the per-turn guard.

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
// The filename is sanitized (FR-023a) before insertion so a user-controlled
// name cannot carry control characters, newlines, or an over-long
// prompt-injection payload into LLM-visible content.
func buildDocumentInjection(localPath, mime, filename string, size int64) string {
	if filename == "" {
		parts := strings.Split(localPath, "/")
		filename = parts[len(parts)-1]
	}
	filename = sanitizeInjectedName(filename)

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
		localPath, meta, err := store.ResolveWithMetaOpts(ref, media.ResolveOpts{})
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

// attachToolResultMedia mutates msg.Media by appending a base64 data URL
// for each ref in refs that resolves to an image the model can see.
//
// The behavior is the outbound counterpart of resolveMediaRefs's image
// path (FR-016 / ADR-051 Rev 4 Gap 4):
//
//   - Non-image refs (audio/video/PDF/document) are skipped — the
//     tool-result Media channel is reserved for inline vision input,
//     and PDF already routes through native document blocks in
//     resolveMediaRefs. The artifact tag built by buildArtifactTags
//     (loop.go:8246) covers the path/filename signal for non-image
//     refs so the caller still has a hook to the file on disk.
//   - Universal image MIMEs (PNG/JPEG/WebP/GIF/BMP/TIFF) get the
//     verbatim "data:<mime>;base64,<bytes>" URL — same as the pre-fix
//     loop.go path. Backward compatible (Constraint #6).
//   - Non-universal image MIMEs (SVG / AVIF / HEIC / HEIF / ICO)
//     NEVER reach the wire as their raw data URL — most vision
//     providers 400 on image/svg+xml and the pure-Go decoder set
//     cannot normalize AVIF/HEIC/ICO. SVG is rasterized to PNG via
//     encodeSVGToDataURL (oxsvg/rasterx, no new deps); the others
//     route through encodeImageToDataURL (which already returns ""
//     for undecodable formats) and fall back to "skip inline attach,
//     keep artifact tag". In either case the URL attached to msg
//     (and persisted into session history at loop.go:8442-8443) is
//     always a format the provider can consume — the next turn of
//     the same session cannot replay a poison image block.
//
// maxSize is the byte budget for the encoded data URL — both
// encodeSVGToDataURL and encodeImageToDataURL enforce it and return
// "" on oversize, which the helper treats as "skip inline attach".
//
// Errors during resolve / read / rasterize are logged-not-fatal at
// WARN level and silently skipped (matching the pre-fix behavior at
// loop.go:8332-8335). The artifact tag remains the user/agent's
// fallback hook to the file on disk.
func attachToolResultMedia(msg *providers.Message, refs []string, store media.MediaStore, maxSize int) {
	if msg == nil || len(refs) == 0 || store == nil {
		return
	}
	for _, ref := range refs {
		localPath, meta, err := store.ResolveWithMetaOpts(ref, media.ResolveOpts{})
		if err != nil {
			logger.WarnCF("agent", "attachToolResultMedia: resolve failed", map[string]any{
				"ref":   ref,
				"error": err.Error(),
			})
			continue
		}
		mime := meta.ContentType
		if mime == "" {
			// Detect from bytes if the store did not capture the type.
			mime = detectMIME(localPath, meta)
		}
		if !strings.HasPrefix(mime, "image/") {
			continue
		}

		if isNonUniversalImageMIME(mime) {
			// FR-016 / Gap 4: SVG rasterizes to PNG via encodeSVGToDataURL;
			// AVIF/HEIC/ICO cannot be decoded by the pure-Go set, so
			// encodeImageToDataURL returns "" and the inline attach is
			// skipped. Either way the data URL that ends up in session
			// history is something the provider can see.
			var dataURL string
			switch mime {
			case "image/svg+xml", "image/svg":
				info, statErr := os.Stat(localPath)
				if statErr != nil {
					logger.WarnCF("agent", "attachToolResultMedia: svg stat failed", map[string]any{
						"path":  localPath,
						"error": statErr.Error(),
					})
					continue
				}
				dataURL = encodeSVGToDataURL(localPath, info, maxSize)
			default:
				info, statErr := os.Stat(localPath)
				if statErr != nil {
					logger.WarnCF("agent", "attachToolResultMedia: stat failed", map[string]any{
						"path":  localPath,
						"mime":  mime,
						"error": statErr.Error(),
					})
					continue
				}
				dataURL = encodeImageToDataURL(localPath, mime, info, maxSize)
			}
			if dataURL == "" {
				// Rasterize or decode failed / oversize / format
				// unsupported. The artifact tag (built above this
				// site at loop.go:8246) still names the file path —
				// the model can read it via tools.file_read or the
				// next tool invocation.
				logger.WarnCF("agent",
					"attachToolResultMedia: non-universal image normalize failed, skipping inline attach",
					map[string]any{
						"ref":  ref,
						"path": localPath,
						"mime": mime,
					})
				continue
			}
			msg.Media = append(msg.Media, dataURL)
			continue
		}

		// Universal image MIME: build the data URL verbatim. Same
		// shape as the pre-fix loop.go:8332-8337 site, so PNG/JPEG/
		// WebP/GIF/BMP/TIFF behavior is preserved exactly.
		data, err := os.ReadFile(localPath)
		if err != nil {
			logger.WarnCF("agent", "attachToolResultMedia: read failed", map[string]any{
				"path":  localPath,
				"mime":  mime,
				"error": err.Error(),
			})
			continue
		}
		msg.Media = append(msg.Media, "data:"+mime+";base64,"+base64.StdEncoding.EncodeToString(data))
	}
}

const maxImagePixels = 16 * 1024 * 1024

// encodeImageToDataURL normalizes a decodable image to the provider-friendly
// canonical PNG and returns it as a data URL. SVG is rasterized to PNG via
// the pure-Go oksvg/rasterx path (Option A, retained). All other formats
// route through the resize pipeline (pkg/media/resize, FR-011/014/015):
// PNG when it fits (canonical output), JPEG via the q90→q40 ladder when
// PNG does not fit (FR-015). The pixel budget is checked before full
// decode to bound memory use (FR-013).
//
// FR-004 (ADR-051 Rev 4 Gap 1): the normalized PNG/JPEG artifact is
// cached by sha256 of the raw source bytes plus the cache-affecting
// budget inputs (model slot, MaxBytes, LongEdgePx). Repeated
// presentations of the same attachment skip the decode → resize →
// encode pipeline and return the cached data URL. The sha256 is the
// source-of-truth identity (NOT the sha256 of the normalized output).
//
// FR-016 (ADR-051 Rev 4): the prior D2 passthrough for AVIF/HEIC/HEIF/ICO
// is DELETED. These formats are not in the stdlib + x/image decoder set;
// image.DecodeConfig fails on them, the function returns "", and the caller
// routes the attachment to step 5 offload (workspace work/ + guidance).
//
// Returns an empty string when the input exceeds maxSize (or budget.MaxBytes),
// the file is unreadable, normalization fails, or the resize ladder reaches its floor
// (ErrLadderFloor) — in every case the caller falls through to the
// "[attachment unavailable: …]" marker or step 5 offload.
//
// budget is optional: when absent, defaults to 7680px long edge (the catalog
// default) and maxSize for MaxBytes. Passing a non-zero budget overrides the
// long-edge default and blends MaxBytes with the maxSize cap (the smaller wins).
//
// encodeImageToDataURL is the backward-compatible entry point used by the
// regression tests; it delegates to encodeImageToDataURLCached with an
// empty model slot. The production orchestrator calls the cached variant
// directly so the model slot is part of the cache key (two different
// models may have different budgets and therefore different normalized
// artifacts for the same raw bytes).
func encodeImageToDataURL(
	localPath, mime string,
	info os.FileInfo,
	maxSize int,
	budget ...catalog.ResizeLimits,
) string {
	return encodeImageToDataURLCached(localPath, mime, info, maxSize, "", budget...)
}

// encodeImageToDataURLCached is the cache-aware implementation. See
// encodeImageToDataURL for the full contract. modelSlot is part of the
// cache key (FR-004) — empty in the wrapper path, populated by the
// production orchestrator.
func encodeImageToDataURLCached(
	localPath, mime string,
	info os.FileInfo,
	maxSize int,
	modelSlot string,
	budget ...catalog.ResizeLimits,
) string {
	if info.Size() > int64(maxSize) {
		logImageNormalizationFailure(localPath, mime, "input-oversize", nil, map[string]any{
			"size":     info.Size(),
			"max_size": maxSize,
		})
		return ""
	}

	// ADR-051 RD1 extension (Option A): SVG is XML, not a raster format, so
	// DecodeConfig below can never read it. Rasterize to canonical PNG via
	// the pure-Go oksvg/rasterx path instead — the provider receives an
	// image it can actually see. The rasterized-SVG path is NOT cached
	// here — its inputs (the SVG markup) are not the same shape as the
	// raster decode pipeline, and FR-004 covers the latter.
	if mime == "image/svg+xml" {
		return encodeSVGToDataURL(localPath, info, maxSize)
	}

	// FR-011/014/015: resolve the effective budget BEFORE reading bytes
	// so the cache key carries the resolved budget, not the variadic
	// shell. The cache key requires numeric budget fields (MaxBytes,
	// LongEdgePx) — the variadic may be empty (legacy call path), in
	// which case the defaults below apply.
	longEdgePx := 7680
	maxBytes := int64(maxSize)
	if len(budget) > 0 && budget[0].LongEdgePx > 0 {
		longEdgePx = budget[0].LongEdgePx
		if budget[0].MaxBytes > 0 && budget[0].MaxBytes < maxBytes {
			maxBytes = budget[0].MaxBytes
		}
	}

	f, err := os.Open(localPath)
	if err != nil {
		logImageNormalizationFailure(localPath, mime, "open-failed", err, nil)
		return ""
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			logImageNormalizationFailure(localPath, mime, "close-failed", closeErr, nil)
		}
	}()

	// FR-004: hash the raw source bytes for the cache key. Read once
	// (io.ReadAll) so we can both hash AND feed the same bytes to
	// image.DecodeConfig + image.Decode via bytes.NewReader. The sha256
	// is the source-of-truth identity — NOT the sha256 of the
	// normalized output, which would defeat the purpose.
	rawBytes, err := io.ReadAll(f)
	if err != nil {
		logImageNormalizationFailure(localPath, mime, "read-failed", err, nil)
		return ""
	}

	cacheKey := library.NormalizeCacheKey{
		Sha256:         library.HashRawBytes(rawBytes),
		ModelSlot:      modelSlot,
		BudgetMaxBytes: maxBytes,
		BudgetMaxEdge:  longEdgePx,
	}
	if cached, ok := library.GlobalNormalizeCache().Get(cacheKey); ok {
		// FR-004 hit: skip the entire decode → resize → encode
		// pipeline. The cached value is the data URL string bytes
		// (caller-converted).
		return string(cached)
	}

	// FR-013: DecodeConfig pre-flight pixel guard.
	config, format, err := image.DecodeConfig(bytes.NewReader(rawBytes))
	if err != nil {
		logImageNormalizationFailure(localPath, mime, "decode-config-failed", err, nil)
		return ""
	}
	if config.Width <= 0 || config.Height <= 0 {
		logImageNormalizationFailure(localPath, mime, "invalid-dimensions", nil, map[string]any{
			"format": format,
			"width":  config.Width,
			"height": config.Height,
		})
		return ""
	}
	pixels := uint64(config.Width) * uint64(config.Height)
	if pixels > maxImagePixels {
		logImageNormalizationFailure(localPath, mime, "pixel-budget-exceeded", nil, map[string]any{
			"format":     format,
			"width":      config.Width,
			"height":     config.Height,
			"max_pixels": maxImagePixels,
		})
		return ""
	}

	decoded, decodedFormat, err := image.Decode(bytes.NewReader(rawBytes))
	if err != nil {
		logImageNormalizationFailure(localPath, mime, "decode-failed", err, map[string]any{
			"format": format,
		})
		return ""
	}

	result, err := resize.ResizeToFit(decoded, catalog.ResizeLimits{
		LongEdgePx: longEdgePx,
		MaxBytes:   maxBytes,
	})
	if err != nil {
		if errors.Is(err, resize.ErrLadderFloor) {
			logImageNormalizationFailure(localPath, mime, "resize-ladder-floor", err, map[string]any{
				"format":       decodedFormat,
				"long_edge":    maxInt(config.Width, config.Height),
				"budget_bytes": maxSize,
			})
		} else {
			logImageNormalizationFailure(localPath, mime, "resize-failed", err, map[string]any{
				"format": decodedFormat,
			})
		}
		return ""
	}

	dataURL := "data:" + result.Mime + ";base64," + base64.StdEncoding.EncodeToString(result.Data)

	// FR-004: populate the cache for the next presentation. The cache
	// value is the data URL bytes; the mime is recoverable from the
	// data-URL prefix so we don't need a separate mime field.
	library.GlobalNormalizeCache().Put(cacheKey, []byte(dataURL))

	return dataURL
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func logImageNormalizationFailure(localPath, mime, reason string, err error, fields map[string]any) {
	details := map[string]any{
		"path":   localPath,
		"mime":   mime,
		"reason": reason,
	}
	if err != nil {
		details["error"] = err.Error()
	}
	for key, value := range fields {
		details[key] = value
	}
	logger.WarnCF("agent", "Image normalization failed, skipping attachment", details)
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

// maxInjectedNameRunes caps a sanitized user-controlled filename before it is
// inserted into LLM-visible content (FR-023a). 128 runes is generous for any
// legitimate filename while bounding a prompt-injection payload's reach.
const maxInjectedNameRunes = 128

// sanitizeInjectedName strips control characters (including newlines, tabs,
// NUL) and caps the length to maxInjectedNameRunes runes (FR-023a). It is the
// content-injection counterpart to FR-020a's copy-name sanitization: every
// human-readable name that originates from a user-controlled filename and
// reaches LLM content (the step-7 "[attachment unavailable: <name>]" marker,
// the step-5 guidance, and the document-injection wrapper) is routed through
// it. A sanitized name can still be a prompt-injection string of printable
// characters, but it can no longer smuggle line-breaks that break out of a
// delimiter, control chars that confuse terminals/log parsers, or an
// unbounded payload.
func sanitizeInjectedName(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	count := 0
	for _, r := range name {
		if unicode.IsControl(r) {
			continue
		}
		if count >= maxInjectedNameRunes {
			break
		}
		b.WriteRune(r)
		count++
	}
	return b.String()
}

// fallbackName returns filename when non-empty, else alt. Centralizes the
// "prefer the manifest filename, fall back to the ref/path" pattern used at
// every content-injection marker site.
func fallbackName(filename, alt string) string {
	if filename != "" {
		return filename
	}
	return alt
}

// offloadSink carries the validated workspace work/ directory (the
// Landlock-allowed target produced by workspace.SafeWorkDir) into which step-5
// offload copies files that no provider presentation path could handle. A nil
// sink means no offload target is available (legacy/empty-workspace turn); the
// presentation chain then degrades to the step-7 honest marker instead of
// failing the turn.
type offloadSink struct {
	workDir string
}

// offload performs step-5 offload (ADR-051 Rev 4): it copies the file into the
// workspace work/ dir under a safe-derived name (FR-020a) and returns the
// content injection carrying the format-aware guidance line (FR-021) and the
// filesystem path (FR-020 — never a media:// ref). A nil receiver yields
// ("", false) so callers can write `if inj, ok := sink.offload(...); ok {…}`
// uniformly across the sink-present and degraded paths.
//
// Returns ok=false (empty injection) when there is no sink or the copy fails;
// the caller then produces the step-7 honest marker (edge case: "step-5 offload
// cannot copy the file → step 7 honest marker with reason offload-failed").
func (s *offloadSink) offload(localPath, mime, filename, model string) (string, bool) {
	if s == nil {
		return "", false
	}
	safeName, err := deriveSafeOffloadName(localPath, filename)
	if err != nil {
		logger.WarnCF("agent", "step-5 offload: safe-name derivation failed", map[string]any{
			"path":  localPath,
			"error": err.Error(),
		})
		return "", false
	}
	dest, err := copyToWorkDir(localPath, s.workDir, safeName)
	if err != nil {
		logger.WarnCF("agent", "step-5 offload: copy into work/ failed", map[string]any{
			"path":     localPath,
			"work_dir": s.workDir,
			"error":    err.Error(),
		})
		return "", false
	}
	return buildOffloadInjection(dest, detectFileClass(mime, filename), model), true
}

// sha256PrefixBytes is the number of sha256 digest bytes (16 hex chars) used
// as the safe-derived offload copy-name prefix. 16 hex chars give 64 bits of
// collision resistance — ample for filename disambiguation within a workspace
// work/ dir, and short enough to stay readable in agent tool output.
const sha256PrefixBytes = 8

// offloadExtMaxLen bounds a preserved (sanitized) file extension on the offload
// copy name. Long enough for ".docx"/".sheet"/double extensions, short enough
// to rule out an extension-borne payload.
const offloadExtMaxLen = 9

// deriveSafeOffloadName builds a SAFE-DERIVED copy name for step-5 offload
// (FR-020a): the sha256 hex prefix of the file's bytes, plus a sanitized
// extension preserved only so the agent's read_file/docextract tools can still
// recognize the type. The raw user filename is NEVER used as the copy name —
// it is both a path-traversal and a prompt-injection vector. The returned name
// contains no path separators and never starts with ".", so on its own it
// cannot escape the work/ dir; copyToWorkDir re-checks containment as
// defense-in-depth.
func deriveSafeOffloadName(srcPath, originalFilename string) (string, error) {
	h := sha256.New()
	f, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	prefix := hex.EncodeToString(h.Sum(nil)[:sha256PrefixBytes])
	return prefix + sanitizeExtension(filepath.Ext(originalFilename)), nil
}

// sanitizeExtension lower-cases a ".ext" suffix and keeps only [a-z0-9] after
// the dot, capped to offloadExtMaxLen runes total. Returns "" for anything that
// is not a usable extension (no dot, empty, or all-stripped), so it contributes
// nothing to the copy name rather than a misleading fragment.
func sanitizeExtension(ext string) string {
	ext = strings.ToLower(ext)
	if len(ext) < 2 || ext[0] != '.' {
		return ""
	}
	var b strings.Builder
	b.WriteByte('.')
	for _, r := range ext[1:] {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) < 2 {
		return ""
	}
	if len(out) > offloadExtMaxLen {
		out = out[:offloadExtMaxLen]
	}
	return out
}

// copyToWorkDir copies srcPath into workDir under safeName using
// filepath.Clean(filepath.Join(...)) and a containment check that verifies the
// joined result still falls under workDir before any write (FR-020a, mirroring
// the safeID traversal-guard pattern in pkg/workspace/instructions.go). The
// copy streams via io.Copy to bound memory (files up to maxUploadFileSize / 100
// MB never sit wholly in RAM). The destination is written 0600 and the work dir
// is created (0700) if absent.
func copyToWorkDir(srcPath, workDir, safeName string) (dest string, err error) {
	cleanDir := filepath.Clean(workDir)
	dest = filepath.Clean(filepath.Join(cleanDir, safeName))
	if !isWithinWorkDir(dest, cleanDir) {
		return "", fmt.Errorf("offload copy escapes work dir: %q", safeName)
	}
	if err = os.MkdirAll(cleanDir, 0o700); err != nil {
		return "", err
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	// Named-return + deferred Close so a failed flush on a writable handle is
	// never silently dropped (github-code-quality: writable file handle closed
	// without error handling). On copy failure, prefer the copy error but still
	// surface a close error if copy succeeded and only close failed.
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return "", err
	}
	return dest, nil
}

// isWithinWorkDir reports whether path resolves strictly inside dir (not equal
// to dir, not escaping via ".."). It is the FR-020a containment predicate.
func isWithinWorkDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return false
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// DetectFileClass is the exported wrapper around detectFileClass: the
// PRESENTATION-NOUN class ("image", "document", "file") used to phrase the
// step-5 offload guidance line so a PDF reads "this document" and an AVIF
// reads "this image".
//
// DO NOT use this for a wire-format field. It is a different taxonomy from
// the Attachment/MediaPart contract enum — see DetectAttachmentType below,
// which is what a persisted or transmitted attachment must use. Conflating
// the two shipped a BLOCKER: "document" is not in the contract enum, the
// SPA's strict Zod schema rejected the whole payload, and chat history
// failed to load entirely with "Backend response failed validation" after
// any document upload (found in live UAT, 2026-07-29).
func DetectFileClass(mime, filename string) string {
	return detectFileClass(mime, filename)
}

// DetectAttachmentType classifies a file into the MEDIA-CATEGORY taxonomy the
// wire contract defines: exactly "image" | "audio" | "video" | "file" and
// nothing else (contracts/components/schemas/Attachment.yaml, kept aligned
// with MediaPart.yaml).
//
// This is the ONLY classifier valid for session.TranscriptEntry.Attachments[].Type
// or any other field crossing the gateway/SPA boundary. It delegates to
// inferMediaType, the same function the channel-message path already uses, so
// there is one media-category implementation rather than two that can drift.
//
// The distinction from DetectFileClass above is deliberate and load-bearing:
// a PDF is presentation-class "document" but media-category "file". Both are
// correct in their own vocabulary; only one is legal on the wire.
func DetectAttachmentType(mime, filename string) string {
	return inferMediaType(filename, mime)
}

// detectFileClass returns the FR-021 presentation noun class for a file:
// "image", "document", or "file" (generic). The guidance line's noun derives
// entirely from this, so a PDF reads "this document" while an AVIF reads "this
// image" and a binary reads "this file".
func detectFileClass(mime, filename string) string {
	if strings.HasPrefix(mime, "image/") {
		return "image"
	}
	if isDocumentMime(mime, filename) {
		return "document"
	}
	return "file"
}

// isDocumentMime reports whether mime/filename indicate a document class
// (text, PDF, Office). Used only to pick the FR-021 guidance noun; it has no
// bearing on routing, which is decided before offload is reached.
func isDocumentMime(mime, filename string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch mime {
	case "application/pdf",
		"application/msword",
		"application/vnd.ms-excel",
		"application/vnd.ms-powerpoint",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return true
	}
	fn := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(fn, ".pdf"), strings.HasSuffix(fn, ".doc"),
		strings.HasSuffix(fn, ".docx"), strings.HasSuffix(fn, ".xls"),
		strings.HasSuffix(fn, ".xlsx"), strings.HasSuffix(fn, ".ppt"),
		strings.HasSuffix(fn, ".pptx"), strings.HasSuffix(fn, ".txt"),
		strings.HasSuffix(fn, ".csv"), strings.HasSuffix(fn, ".md"),
		strings.HasSuffix(fn, ".json"), strings.HasSuffix(fn, ".html"),
		strings.HasSuffix(fn, ".htm"):
		return true
	}
	return false
}

// buildOffloadGuidance returns the FR-021 format-aware guidance line for a file
// class and model: image → "...vision-capable model.", document →
// "...document-capable model.", else → "...capable model.". The model name is
// interpolated so the user/agent knows which model could not read the file.
func buildOffloadGuidance(class, model string) string {
	noun, capability := offloadNoun(class)
	return fmt.Sprintf("Cannot read this %s with %s; switch to a %s model.", noun, model, capability)
}

// offloadNoun returns the (noun, capability-qualifier) pair for a class.
func offloadNoun(class string) (noun, capability string) {
	switch class {
	case "image":
		return "image", "vision-capable"
	case "document":
		return "document", "document-capable"
	default:
		return "file", "capable"
	}
}

// buildOffloadInjection composes the step-5 content injection: the FR-021
// guidance line and the FR-020 filesystem path to the work/ copy (never a
// media:// ref). When step 6 also fires (FR-022), the caller concatenates the
// text injection after this string so the guidance prefixes the extracted
// text/markup.
func buildOffloadInjection(workPath, class, model string) string {
	return "\n\n[" + buildOffloadGuidance(class, model) + " File available at: " + workPath + "]"
}

// verifyFileIntegrity computes the sha256 digest of the file at path and
// compares it against the expected hex-encoded digest. Returns nil on match,
// or an error describing the mismatch (FR-002).
func verifyFileIntegrity(path, expectedSHA256 string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("integrity check: open: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("integrity check: read: %w", err)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expectedSHA256 {
		return fmt.Errorf("integrity check: sha256 mismatch: expected %s, actual %s", expectedSHA256, actual)
	}
	return nil
}

// resizeBudgetForModel returns the resize budget the media pipeline must
// honour for one (provider, model) pair (FR-004). A hit carries the
// provider's own resize_limits; a miss — and a nil catalog — carries the
// document-level default, which ParseDocument guarantees is non-zero. The
// maxSize parameter is the additional operator bound
// (agents.defaults.max_media_size) applied downstream by
// encodeImageToDataURLCached, which takes the smaller of the two byte
// ceilings; it is used here only for the no-catalog fallback.
func resizeBudgetForModel(cat *catalog.Catalog, provider, model string, maxSize int) catalog.ResizeLimits {
	if cat != nil {
		if budget := cat.Resolve(provider, model).Budget(); budget.LongEdgePx > 0 {
			return budget
		}
	}
	// No catalog at all: the package default long edge with the operator's
	// byte cap.
	return catalog.ResizeLimits{
		LongEdgePx: catalog.DefaultResizeLimits.LongEdgePx,
		MaxBytes:   int64(maxSize),
	}
}
