// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media/resize"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

var errNoOffloadSink = errors.New("this turn has no workspace folder to copy the file into")

// uploadPages is the page-image form of one uploaded PDF.
type uploadPages struct {
	images      []string
	first, last int
	pageCount   int
	problem     string
}

// note is the content line naming what the model received and how to see
// later pages: the workspace copy preview_document accepts (§4.1), or why
// there is none (a materialization failure is stated, never silent).
func (p uploadPages) note(dest string, offErr error) string {
	if len(p.images) == 0 {
		return "\n\n[page images unavailable: " + p.problem + "]"
	}
	line := fmt.Sprintf("\n\n[model received: page images %d–%d of %d", p.first, p.last, p.pageCount)
	if p.last < p.pageCount {
		next := fmt.Sprintf("pages \"%d-%d\"", p.last+1, min(p.last+preview.DefaultPages, p.pageCount))
		if offErr == nil {
			line += fmt.Sprintf("; for more, call preview_document with file %q and %s", dest, next)
		} else {
			line += "; later pages cannot be previewed: the PDF could not be copied into the workspace (" + offErr.Error() + ")"
		}
	}
	return line + "]"
}

// uploadRenderTimeout is the page-rendering budget of one uploaded PDF,
// admission wait included (AD-6 "Upload rendering: 20 s per PDF, then text
// fallback"); the PDF's text form is sent either way.
const uploadRenderTimeout = 20 * time.Second

// errUploadRenderTimeout marks the upload budget, not the turn, as the
// reason the render stopped.
var errUploadRenderTimeout = errors.New("upload page rendering budget spent")

// uploadPagesPerRequest is how many automatic upload page images one
// request carries in total (AD-6 "≤ 20 auto upload pages per request,
// newest uploads first").
const uploadPagesPerRequest = 20

// uploadPageAllowance is what is left of one request's automatic upload
// page images: a page count and decoded bytes (the 32 MiB request budget).
type uploadPageAllowance struct {
	pages, bytes int
}

func newUploadPageAllowance() *uploadPageAllowance {
	return &uploadPageAllowance{pages: uploadPagesPerRequest, bytes: previewRequestBudget}
}

// uploadPDFPageImagesFrom renders pages 1–10 of an uploaded PDF's bytes
// through the shared preview service (same admission, limits, cache and
// sandbox as preview_document) and resizes them to the model's image
// limits. It runs under the turn's context, so Stop ends it, and within
// uploadRenderTimeout (AD-6), not the 120 s whole-call budget of
// preview_document. The request allowance is applied by within.
func uploadPDFPageImagesFrom(turnCtx context.Context, data []byte, name string, limits catalog.ResizeLimits) uploadPages {
	ctx, cancel := context.WithTimeoutCause(turnCtx, uploadRenderTimeout, errUploadRenderTimeout)
	defer cancel()
	pages := renderUploadPages(ctx, data, name, limits)
	if len(pages.images) == 0 && errors.Is(context.Cause(ctx), errUploadRenderTimeout) {
		pages.problem = fmt.Sprintf("page rendering did not finish within %d s", int(uploadRenderTimeout/time.Second))
	}
	return pages
}

// within takes the rendered images from the request's allowance, in page
// order, and returns what this request sends.
func (p uploadPages) within(allowance *uploadPageAllowance) uploadPages {
	if len(p.images) == 0 {
		return p
	}
	if allowance.pages <= 0 || allowance.bytes <= 0 {
		return uploadPages{problem: fmt.Sprintf("newer uploads used this request's %d page images or %d MiB", uploadPagesPerRequest, previewRequestBudget>>20)}
	}
	out := uploadPages{first: p.first, pageCount: p.pageCount}
	for k, img := range p.images {
		size := dataURLDecodedSize(img)
		if allowance.pages == 0 || size > allowance.bytes {
			break
		}
		out.images = append(out.images, img)
		out.last = p.first + k
		allowance.pages--
		allowance.bytes -= size
	}
	if len(out.images) == 0 {
		out.problem = "the page images do not fit this model's image limits or this request's upload page allowance"
	}
	return out
}

func renderUploadPages(ctx context.Context, data []byte, name string, limits catalog.ResizeLimits) uploadPages {
	svc := preview.Shared()
	if svc == nil {
		return uploadPages{problem: "document preview is not running"}
	}
	if len(data) > preview.MaxInputBytes {
		return uploadPages{problem: fmt.Sprintf("the PDF could not be read (larger than %d bytes)", preview.MaxInputBytes)}
	}
	adm, err := svc.Admit(ctx, false)
	if err != nil {
		return uploadPages{problem: err.Error()}
	}
	defer adm.Release()
	res, err := svc.Preview(ctx, adm, preview.Request{
		Name: filepath.Base(name), Ext: ".pdf", Data: data,
		First: 1, Last: preview.DefaultPages, Default: true,
	})
	if err != nil {
		return uploadPages{problem: err.Error()}
	}
	if len(res.Pages) == 0 {
		problem := res.RenderDetail
		if problem == "" {
			problem = "the pages were not rendered"
		}
		return uploadPages{problem: problem}
	}
	out := uploadPages{pageCount: res.PageCount}
	for _, page := range res.Pages {
		decoded, _, decErr := image.Decode(bytes.NewReader(page.PNG))
		if decErr != nil {
			logger.WarnCF("agent", "uploaded PDF page image invalid", map[string]any{"page": page.Number, "error": decErr.Error()})
			break
		}
		normalized, rsErr := resize.ResizeToFit(decoded, limits)
		if rsErr != nil {
			logger.WarnCF("agent", "uploaded PDF page image cannot fit the model's image limits", map[string]any{"page": page.Number, "error": rsErr.Error()})
			break
		}
		if len(out.images) == 0 {
			out.first = page.Number
		}
		out.last = page.Number
		out.images = append(out.images, "data:"+normalized.Mime+";base64,"+base64.StdEncoding.EncodeToString(normalized.Data))
	}
	if len(out.images) == 0 {
		out.problem = "the page images do not fit this model's image limits or this request's upload page allowance"
	}
	return out
}
