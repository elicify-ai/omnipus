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
	"io"
	"os"
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

// uploadPageJob is one uploaded PDF whose page images go into message msg:
// the images at Media index mediaAt, the note inside injection injAt at
// byte noteAt (between the offload line and the text).
type uploadPageJob struct {
	msg, mediaAt, injAt, noteAt int
	localPath, name, dest       string
	offErr                      error
	limits                      catalog.ResizeLimits
	turn                        bool // one of this turn's own uploads
}

// renderUploadPageJobs renders the jobs newest first within one request
// allowance and places each one's images and note. Jobs are in request
// order, so placing them in reverse keeps every earlier job's indices valid.
// It returns the decoded bytes of the page images of this turn's uploads.
func renderUploadPageJobs(ctx context.Context, jobs []uploadPageJob, media, injections [][]string) int {
	allowance := newUploadPageAllowance()
	turnBytes := 0
	for j := len(jobs) - 1; j >= 0; j-- {
		job := jobs[j]
		pages := uploadPDFPageImagesWithin(ctx, allowance, job.localPath, job.name, job.limits)
		m := media[job.msg]
		media[job.msg] = append(m[:job.mediaAt:job.mediaAt], append(pages.images, m[job.mediaAt:]...)...)
		inj := injections[job.msg][job.injAt]
		injections[job.msg][job.injAt] = inj[:job.noteAt] + pages.note(job.dest, job.offErr) + inj[job.noteAt:]
		if job.turn {
			for _, img := range pages.images {
				turnBytes += dataURLDecodedSize(img)
			}
		}
	}
	return turnBytes
}

// uploadPDFPageImages renders an uploaded PDF's first pages without a turn
// (no Stop to follow) and with a request allowance of its own.
func uploadPDFPageImages(localPath, name string, limits catalog.ResizeLimits) uploadPages {
	return uploadPDFPageImagesWithin(context.Background(), newUploadPageAllowance(), localPath, name, limits)
}

// uploadPDFPageImagesWithin renders pages 1–10 of an uploaded PDF through
// the shared preview service (same admission, limits, cache and sandbox as
// preview_document) and resizes them to the model's image limits, taking
// the images from the request's allowance. It runs under the turn's
// context, so Stop ends it, and within uploadRenderTimeout (AD-6), not the
// 120 s whole-call budget of preview_document.
func uploadPDFPageImagesWithin(turnCtx context.Context, allowance *uploadPageAllowance, localPath, name string, limits catalog.ResizeLimits) uploadPages {
	if allowance.pages <= 0 || allowance.bytes <= 0 {
		return uploadPages{problem: fmt.Sprintf("newer uploads used this request's %d page images or %d MiB", uploadPagesPerRequest, previewRequestBudget>>20)}
	}
	ctx, cancel := context.WithTimeoutCause(turnCtx, uploadRenderTimeout, errUploadRenderTimeout)
	defer cancel()
	pages := renderUploadPages(ctx, allowance, localPath, name, limits)
	if len(pages.images) == 0 && errors.Is(context.Cause(ctx), errUploadRenderTimeout) {
		pages.problem = fmt.Sprintf("page rendering did not finish within %d s", int(uploadRenderTimeout/time.Second))
	}
	return pages
}

func renderUploadPages(ctx context.Context, allowance *uploadPageAllowance, localPath, name string, limits catalog.ResizeLimits) uploadPages {
	svc := preview.Shared()
	if svc == nil {
		return uploadPages{problem: "document preview is not running"}
	}
	data, err := readUploadPDF(localPath)
	if err != nil {
		return uploadPages{problem: "the PDF could not be read (" + err.Error() + ")"}
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
		if allowance.pages == 0 {
			break
		}
		decoded, _, decErr := image.Decode(bytes.NewReader(page.PNG))
		if decErr != nil {
			logger.WarnCF("agent", "uploaded PDF page image invalid", map[string]any{"page": page.Number, "error": decErr.Error()})
			break
		}
		normalized, rsErr := resize.ResizeToFit(decoded, limits)
		if rsErr != nil || len(normalized.Data) > allowance.bytes {
			break
		}
		if len(out.images) == 0 {
			out.first = page.Number
		}
		out.last = page.Number
		out.images = append(out.images, "data:"+normalized.Mime+";base64,"+base64.StdEncoding.EncodeToString(normalized.Data))
		allowance.pages--
		allowance.bytes -= len(normalized.Data)
	}
	if len(out.images) == 0 {
		out.problem = "the page images do not fit this model's image limits or this request's upload page allowance"
	}
	return out
}

func readUploadPDF(localPath string) ([]byte, error) {
	f, err := os.Open(localPath) // a resolved media-store path (already authorized)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, preview.MaxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > preview.MaxInputBytes {
		return nil, fmt.Errorf("larger than %d bytes", preview.MaxInputBytes)
	}
	return data, nil
}
