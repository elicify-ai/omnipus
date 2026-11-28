package agent

// pdf_present_forms.go — the office-preview forms (page images within the
// 32 MiB request media budget) driven by the #1968 per-attempt PDF decision
// (founder decision 2026-11-28: one mechanism for uploads, read_file and
// preview_document). pdf_present.go decides native or not, from the
// catalogue's PDF yes/no and config.json pdf_limits only; this file supplies
// what a PDF that is not native becomes on an attempt whose model sees
// images. A native upload or tool PDF is bounded by the configured limits,
// not by the 32 MiB media budget (office-preview AD-6 keeps that budget for
// page images and preview forms).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/media/resize"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// attemptStage is the cached part of one attempt's request preparation:
// the messages with inspection images, upload page images and preview forms
// attached, the previews whose native PDF the presenter may still demote,
// and what is left of the request's 32 MiB document media budget.
type attemptStage struct {
	messages  []providers.Message
	previews  map[int]previewOrigin
	docBudget int
	images    bool
	resize    catalog.ResizeLimits
}

// attachUploadPageImages gives an attempt whose model sees images but does
// not read PDFs natively (pdfNativeSupported false) pages 1-10 of each
// uploaded PDF as images, newest uploads first within one request allowance
// (20 page images, 32 MiB; office-preview spec section 4.1, AD-6). The PDF
// entry stays: the presenter shows it as its text with its note. It returns
// the request copy and the decoded page-image bytes of this turn's uploads.
func (rt *agentLoopRunTurn) attachUploadPageImages(
	ctx context.Context, messages []providers.Message, cat *catalog.Catalog, provider, model string, limits catalog.ResizeLimits,
) ([]providers.Message, int) {
	if cat == nil || cat.Document() == nil || !modelSupportsImage(cat, provider, model) {
		return messages, 0
	}
	rt.al.mu.RLock()
	pdfLimits := rt.al.cfg.ResolvePDFLimits(provider, model)
	rt.al.mu.RUnlock()
	if pdfNativeSupported(cat.Resolve(provider, model), pdfLimits) {
		return messages, 0
	}
	turnIdx := turnUserMessageIndex(messages)
	allowance := newUploadPageAllowance()
	var out []providers.Message
	turnBytes := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].ToolCallID != "" {
			continue
		}
		for j := len(messages[i].Media) - 1; j >= 0; j-- {
			dataURL := messages[i].Media[j]
			_, data, ok := pdfDataURLBytes(dataURL)
			if !ok {
				continue
			}
			if out == nil {
				out = append([]providers.Message(nil), messages...)
			}
			entry := rt.chatPDFs.lookup(dataURL)
			pages := rt.uploadPages(ctx, data, entry.name, limits).within(allowance)
			m := out[i]
			media := make([]string, 0, len(m.Media)+len(pages.images))
			media = append(media, m.Media[:j+1]...)
			media = append(media, pages.images...)
			media = append(media, m.Media[j+1:]...)
			m.Media = media
			dest, offErr := "", error(nil)
			if len(pages.images) > 0 && pages.last < pages.pageCount {
				dest, offErr = entry.workspaceCopy()
			}
			m.Content += pages.note(dest, offErr)
			out[i] = m
			if i == turnIdx {
				for _, img := range pages.images {
					turnBytes += dataURLDecodedSize(img)
				}
			}
		}
	}
	if out == nil {
		return messages, 0
	}
	return out, turnBytes
}

// pdfDataURLBytes decodes a well-formed PDF data URL.
func pdfDataURLBytes(mediaURL string) (string, []byte, bool) {
	if _, ok := pdfPayload(mediaURL); !ok {
		return "", nil, false
	}
	return protocoltypes.DecodePDFDataURL(mediaURL)
}

// uploadPageCache keeps one turn's rendered upload pages, keyed by the
// PDF's digest and the model's resize limits, so in-place retries and
// fallback candidates with the same limits do not render again.
type uploadPageCache struct {
	mu      sync.Mutex
	entries map[string]uploadPages
}

// uploadPages renders (once per turn and resize limits) pages 1-10 of an
// uploaded PDF within uploadRenderTimeout, under ctx so Stop ends it.
func (rt *agentLoopRunTurn) uploadPages(ctx context.Context, data []byte, name string, limits catalog.ResizeLimits) uploadPages {
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:]) + fmt.Sprintf("|%d|%d|%d", limits.LongEdgePx, limits.MaxBytes, limits.MaxPatches)
	if rt.uploadPageCache == nil {
		rt.uploadPageCache = &uploadPageCache{}
	}
	c := rt.uploadPageCache
	c.mu.Lock()
	cached, ok := c.entries[key]
	c.mu.Unlock()
	if ok {
		return cached
	}
	pages := uploadPDFPageImagesFrom(ctx, data, name, limits)
	if pages.problem == "" || len(pages.images) > 0 {
		// A cancelled or timed-out render is not kept: a later attempt of
		// the turn may still render.
		c.mu.Lock()
		if c.entries == nil {
			c.entries = map[string]uploadPages{}
		}
		c.entries[key] = pages
		c.mu.Unlock()
	}
	return pages
}

// toolPDFPages is a tool PDF's page-image form (read_file, library_read):
// rendered on first need, once per item.
type toolPDFPages struct {
	once        sync.Once
	pages       []tools.DocumentPreviewPage
	unavailable string
	note        string
}

// renderToolPDFPages returns the item's rendered pages, rendering them on
// the first call for this tool call and item.
func (rt *agentLoopRunTurn) renderToolPDFPages(ctx context.Context, c *pdfCandidate) *toolPDFPages {
	rt.toolPDFPagesMu.Lock()
	if rt.toolPDFPages == nil {
		rt.toolPDFPages = map[string]*toolPDFPages{}
	}
	key := c.item.SHA256 + "|" + c.item.Name
	entry, ok := rt.toolPDFPages[key]
	if !ok {
		entry = &toolPDFPages{}
		rt.toolPDFPages[key] = entry
	}
	rt.toolPDFPagesMu.Unlock()
	entry.once.Do(func() {
		if c.item.RenderPages == nil {
			entry.unavailable = "the pages were not rendered"
			return
		}
		entry.pages, entry.unavailable, entry.note = c.item.RenderPages(ctx)
	})
	return entry
}

// attachToolPDFPages adds a demoted tool PDF's page images (at most
// previewMaxImages) within budget and returns the message, the bytes used
// and the note line naming what the model received.
func attachToolPDFPages(m providers.Message, pages *toolPDFPages, limits catalog.ResizeLimits, budget int) (providers.Message, int, string, error) {
	if len(pages.pages) == 0 {
		return m, 0, "[page images unavailable: " + pages.unavailable + "]", nil
	}
	used, sent := 0, 0
	media := append([]string(nil), m.Media...)
	for _, page := range pages.pages {
		if sent == previewMaxImages {
			break
		}
		decoded, _, err := image.Decode(bytes.NewReader(page.PNG))
		if err != nil {
			return m, 0, "", fmt.Errorf("invalid PDF page image %d: %w", page.Number, err)
		}
		normalized, err := resize.ResizeToFit(decoded, limits)
		if err != nil {
			return m, 0, "", fmt.Errorf("PDF page %d cannot fit model limits: %w", page.Number, err)
		}
		if used+len(normalized.Data) > budget {
			break
		}
		media = append(media, "data:"+normalized.Mime+";base64,"+base64.StdEncoding.EncodeToString(normalized.Data))
		used += len(normalized.Data)
		sent++
	}
	if sent == 0 {
		return m, 0, fmt.Sprintf("[page images not sent: they do not fit the request's %d MiB media budget]", previewRequestBudget>>20), nil
	}
	m.Media = media
	return m, used, fmt.Sprintf("[model received: page images %d–%d%s]", pages.pages[0].Number, pages.pages[sent-1].Number, pages.note), nil
}
