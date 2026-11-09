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

// uploadPDFPageImages renders pages 1–10 of an uploaded PDF through the
// shared preview service (same admission, limits, cache and sandbox as
// preview_document) and resizes them to the model's image limits. The
// resolver has no turn context, so the call is bounded by the preview call
// timeout only.
func uploadPDFPageImages(localPath, name string, limits catalog.ResizeLimits) uploadPages {
	svc := preview.Shared()
	if svc == nil {
		return uploadPages{problem: "document preview is not running"}
	}
	data, err := readUploadPDF(localPath)
	if err != nil {
		return uploadPages{problem: "the PDF could not be read (" + err.Error() + ")"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), preview.CallTimeout)
	defer cancel()
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
	used := 0
	for _, page := range res.Pages {
		decoded, _, decErr := image.Decode(bytes.NewReader(page.PNG))
		if decErr != nil {
			logger.WarnCF("agent", "uploaded PDF page image invalid", map[string]any{"page": page.Number, "error": decErr.Error()})
			break
		}
		normalized, rsErr := resize.ResizeToFit(decoded, limits)
		if rsErr != nil || used+len(normalized.Data) > previewRequestBudget {
			break
		}
		if len(out.images) == 0 {
			out.first = page.Number
		}
		out.last = page.Number
		out.images = append(out.images, "data:"+normalized.Mime+";base64,"+base64.StdEncoding.EncodeToString(normalized.Data))
		used += len(normalized.Data)
	}
	if len(out.images) == 0 {
		out.problem = "the page images do not fit this model's image limits"
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
