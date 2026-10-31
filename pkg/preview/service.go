// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package preview runs office preview's out-of-process document programs for
// the preview_document tool (docs/internal/specs/office-preview-spec.md):
// admission (AD-6), LibreOffice conversion (AD-2, AD-7), the PDF renderer
// with its extract-text fallback (AD-2, AD-15), the rendered-page cache
// (AD-9), per-call scratch with owner records and startup recovery (AD-8),
// and component trust (AD-4, pkg/preview/component).
//
// Nothing here is loaded into the gateway: LibreOffice, pdfium and the PDF
// text extractor always run as separate, resource-limited, sandboxed child
// processes started through run-limited (pkg/preview/launcher).
package preview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/preview/extracttext"
	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// Admission budgets (AD-6).
const (
	maxRendererRuns = 2
	maxOfficeRuns   = 1
	// DefaultPages is the default batch size (AD-1).
	DefaultPages = 10
	// MaxLongEdge is the longest page side in pixels (AD-2).
	MaxLongEdge = protocol.MaxLongEdgePx
	// MaxInputBytes is the preview input limit (AD-5).
	MaxInputBytes = protocol.MaxInputPDF
	// CallTimeout is the whole-call budget including queue wait (AD-6).
	CallTimeout = 120 * time.Second
)

// Rendering outcome reasons (contract enum MediaFrame.preview.unavailable_reason).
const (
	ReasonRendererMissing = "renderer_missing"
	ReasonRendererDamaged = "renderer_damaged"
	ReasonRendererFailed  = "renderer_failed"
)

// Config wires a Service.
type Config struct {
	// Home is $OMNIPUS_HOME.
	Home string
	// Self is the running omnipus executable (it hosts run-limited and
	// extract-text).
	Self string
	// Resolver finds the components.
	Resolver *component.Resolver
	// WriteRoots returns every agent write root known from configuration
	// (writer exclusion, AD-4). Nil means none known.
	WriteRoots func() []component.WriteRoot
}

// Service is safe for concurrent use.
type Service struct {
	cfg          Config
	rendererRuns chan struct{}
	officeRuns   chan struct{}
	cache        *pageCache
	gatewayPID   int
	gatewayStart string

	officeMu sync.Mutex
	office   *component.Component
}

// New builds a Service. It does not touch any component yet.
func New(cfg Config) (*Service, error) {
	if cfg.Home == "" || cfg.Self == "" || cfg.Resolver == nil {
		return nil, errors.New("preview: home, executable and resolver are required")
	}
	start, err := processStart(os.Getpid())
	if err != nil {
		return nil, fmt.Errorf("preview: identify gateway: %w", err)
	}
	return &Service{
		cfg:          cfg,
		rendererRuns: make(chan struct{}, maxRendererRuns),
		officeRuns:   make(chan struct{}, maxOfficeRuns),
		cache:        newPageCache(pageCacheBytes),
		gatewayPID:   os.Getpid(),
		gatewayStart: start,
	}, nil
}

// Recover runs startup recovery over the scratch folders (AD-8).
func (s *Service) Recover() RecoveryReport {
	return Recover(s.cfg.Home, s.gatewayPID, s.gatewayStart)
}

// Admission is a held renderer slot (and LibreOffice slot for Office input).
type Admission struct {
	once    sync.Once
	release func()
}

// Release returns the slots. Safe to call more than once.
func (a *Admission) Release() {
	if a == nil {
		return
	}
	a.once.Do(a.release)
}

// Admit takes the call's slots before the source is read or copied (AD-6,
// T-39). It waits until ctx ends ("stopped" / "timed out after 120 s").
func (s *Service) Admit(ctx context.Context, office bool) (*Admission, error) {
	var held []chan struct{}
	release := func() {
		for _, ch := range held {
			<-ch
		}
	}
	take := func(ch chan struct{}) error {
		select {
		case ch <- struct{}{}:
			held = append(held, ch)
			return nil
		case <-ctx.Done():
			release()
			return stopFailure(ctx)
		}
	}
	if office {
		if err := take(s.officeRuns); err != nil {
			return nil, err
		}
	}
	if err := take(s.rendererRuns); err != nil {
		return nil, err
	}
	return &Admission{release: release}, nil
}

// Request is one preview.
type Request struct {
	// Name is the source's file name (for the person and the model).
	Name string
	// Ext is ".pdf", ".docx", ".xlsx" or ".pptx" (already validated).
	Ext string
	// Data is the authorized source content (≤ MaxInputBytes).
	Data []byte
	// First/Last is the requested range; Default marks "pages omitted".
	First, Last int
	Default     bool
	// ReadModel is the calling turn's kernel policy (LibreOffice reads per
	// the turn's read model, AD-7). Nil when no kernel policy applies.
	ReadModel *sandbox.SandboxPolicy
	// CallerWriteRoots are the calling turn's write roots (per-call writer
	// exclusion, AD-4).
	CallerWriteRoots []component.WriteRoot
}

// Page is a rendered page.
type Page struct {
	Number int
	Width  int
	Height int
	PNG    []byte
}

// Result is a successful preview. Rendering may still be unavailable
// (AD-15): Pages is then empty and RenderReason says why.
type Result struct {
	// SourceVersion is the first 12 hex of the source's SHA-256.
	SourceVersion string
	// PDF is the PDF the model may receive natively (the source, or the
	// converted PDF for Office input).
	PDF       []byte
	PageCount int
	First     int
	Last      int
	// Clamped is set when the requested range was cut to the document.
	Clamped bool
	// RequestedFirst/RequestedLast echo the request (for the clamp note).
	RequestedFirst, RequestedLast int
	Pages                         []Page
	// Text is the extracted text of First..Last (≤ 4 MiB).
	Text []protocol.TextPage
	// TextTruncated reports that the 4 MiB text budget cut the text.
	TextTruncated bool
	// RenderReason is "" when pages were rendered, else a Reason* value;
	// RenderDetail is the human reason.
	RenderReason string
	RenderDetail string
	// Validity is "rendered", "structural" (Office without a renderer) or
	// "not checked" (PDF without a renderer).
	Validity string
	// Unconfined is the "converted without sandbox" reason, if any.
	Unconfined string
	// RendererBuild identifies the renderer for the cache key, if used.
	RendererBuild string
}

// IsOffice reports whether ext needs LibreOffice.
func IsOffice(ext string) bool { return ext == ".docx" || ext == ".xlsx" || ext == ".pptx" }

// Preview runs one preview under adm. Errors are MV-1 failures whose
// Error() is the text after "preview_document: ".
func (s *Service) Preview(ctx context.Context, adm *Admission, req Request) (*Result, error) {
	if adm == nil {
		return nil, errors.New("preview: admission required")
	}
	sum := sha256.Sum256(req.Data)
	res := &Result{SourceVersion: hex.EncodeToString(sum[:])[:12], RequestedFirst: req.First, RequestedLast: req.Last}
	c, err := newCall(s.cfg.Home, s.gatewayPID, s.gatewayStart)
	if err != nil {
		return nil, programFailure("preview scratch unavailable")
	}
	defer c.finish()

	pdf := req.Data
	maxInput := int64(protocol.MaxInputPDF)
	if IsOffice(req.Ext) {
		office, problem := s.officeComponent(req.CallerWriteRoots)
		if problem != nil {
			return nil, problem
		}
		conv := officeConversion{
			self:       s.cfg.Self,
			executable: office.Executable,
			root:       office.Root,
			registry:   office.Registry,
			extraExes:  officeHelpers(office),
			readModel:  confinement{readModel: req.ReadModel},
			call:       c,
		}
		converted, unconfined, convErr := conv.run(ctx, req.Data, req.Ext)
		c.noteUnconfined(unconfined)
		if convErr != nil {
			return nil, convErr
		}
		pdf = converted
		maxInput = protocol.MaxInputConverted
	}
	res.PDF = pdf

	renderer, rendererReason, rendererDetail := s.rendererComponent(req.CallerWriteRoots)
	extract := protocolProgram{self: s.cfg.Self, target: s.cfg.Self, args: []string{extracttext.ModeArg},
		confine: confinement{readExec: []string{filepath.Dir(s.cfg.Self)}}, call: c, maxInput: maxInput}
	var render *protocolProgram
	if renderer != nil {
		render = &protocolProgram{self: s.cfg.Self, target: renderer.Executable,
			// run-limited (the omnipus executable) runs first and becomes the
			// renderer by execve: both folders need read + execute.
			confine: confinement{readExec: []string{renderer.Root, filepath.Dir(s.cfg.Self)}}, call: c, maxInput: maxInput}
		res.RendererBuild = renderer.Manifest.BuildID + "/p" + fmt.Sprint(renderer.Manifest.ProtocolVersion)
	}

	// info: page count (and encryption) before any page work (AD-5 step 2).
	meta, infoErr := s.info(ctx, render, extract, pdf)
	if infoErr != nil {
		var f *failure
		if render != nil && errors.As(infoErr, &f) && f.Kind == failProgram {
			// The renderer failed; fall back to extract-text (AD-15).
			render = nil
			rendererReason, rendererDetail = ReasonRendererFailed, f.Message
			meta, infoErr = s.info(ctx, nil, extract, pdf)
		}
		if infoErr != nil {
			return nil, infoErr
		}
	}
	if err := clamp(res, meta.PageCount, req); err != nil {
		return nil, err
	}
	if render != nil {
		if renderErr := s.renderPages(ctx, render, pdf, sum, res); renderErr != nil {
			var f *failure
			if errors.As(renderErr, &f) && f.Kind == failStopped {
				return nil, renderErr
			}
			if errors.As(renderErr, &f) && f.Kind == failDocument {
				return nil, renderErr
			}
			render = nil
			res.Pages = nil
			rendererReason, rendererDetail = ReasonRendererFailed, renderErr.Error()
		}
	}
	if render != nil {
		res.Validity = "rendered"
	} else {
		res.RenderReason, res.RenderDetail = rendererReason, rendererDetail
		res.Validity = "not checked"
		if IsOffice(req.Ext) {
			res.Validity = "structural"
		}
	}
	textProg := extract
	if render != nil {
		textProg = *render
	}
	text, textErr := s.text(ctx, textProg, pdf, res.First, res.Last)
	if textErr != nil && render != nil {
		text, textErr = s.text(ctx, extract, pdf, res.First, res.Last)
	}
	if textErr != nil {
		var f *failure
		if errors.As(textErr, &f) && f.Kind == failStopped {
			return nil, textErr
		}
		// Text is a fallback form; its failure does not undo a render.
		res.Text = nil
	} else {
		res.Text = text
		total := 0
		for _, t := range text {
			total += len(t.Text)
		}
		res.TextTruncated = total >= protocol.MaxTextTotalBytes
	}
	res.Unconfined = c.unconfinedReason()
	return res, nil
}

func (s *Service) info(ctx context.Context, render *protocolProgram, extract protocolProgram, pdf []byte) (protocol.Meta, error) {
	prog := extract
	if render != nil {
		prog = *render
	}
	resp, _, err := prog.run(ctx, protocol.Request{Op: protocol.OpInfo}, pdf, nil)
	if err != nil {
		return protocol.Meta{}, err
	}
	if resp.Meta.Encrypted {
		return protocol.Meta{}, docFailure("password-protected documents cannot be previewed")
	}
	if resp.Meta.PageCount < 1 {
		return protocol.Meta{}, docFailure("the PDF could not be opened (damaged or unsupported)")
	}
	return resp.Meta, nil
}

// clamp applies AD-5 step 2 to the requested range.
func clamp(res *Result, pageCount int, req Request) error {
	res.PageCount = pageCount
	if req.Default {
		res.First, res.Last = 1, min(DefaultPages, pageCount)
		return nil
	}
	if req.First > pageCount {
		return docFailure(fmt.Sprintf("the document has %d pages", pageCount))
	}
	res.First, res.Last = req.First, req.Last
	if req.Last > pageCount {
		res.Last = pageCount
		res.Clamped = true
	}
	return nil
}

func (s *Service) renderPages(ctx context.Context, render *protocolProgram, pdf []byte, sum [32]byte, res *Result) error {
	key := func(page int) cacheKey {
		return cacheKey{source: sum, page: page, settings: fmt.Sprintf("2x/%d/forms", MaxLongEdge), renderer: res.RendererBuild}
	}
	missing := false
	cached := make(map[int]Page, res.Last-res.First+1)
	for p := res.First; p <= res.Last; p++ {
		if pg, ok := s.cache.get(key(p)); ok {
			cached[p] = pg
		} else {
			missing = true
		}
	}
	if missing {
		req := protocol.Request{Op: protocol.OpRender, FirstPage: uint32(res.First), LastPage: uint32(res.Last),
			MaxLongEdge: MaxLongEdge, Flags: protocol.FlagRenderForms}
		_, _, err := render.run(ctx, req, pdf, func(p protocol.Page) error {
			pg := Page{Number: int(p.Number), Width: int(p.Width), Height: int(p.Height), PNG: append([]byte(nil), p.PNG...)}
			cached[pg.Number] = pg
			s.cache.put(key(pg.Number), pg)
			return nil
		})
		if err != nil {
			return err
		}
	}
	for p := res.First; p <= res.Last; p++ {
		if pg, ok := cached[p]; ok {
			res.Pages = append(res.Pages, pg)
		}
	}
	if len(res.Pages) != res.Last-res.First+1 {
		return programFailure("PDF renderer failed (pages missing)")
	}
	return nil
}

func (s *Service) text(ctx context.Context, prog protocolProgram, pdf []byte, first, last int) ([]protocol.TextPage, error) {
	req := protocol.Request{Op: protocol.OpText, FirstPage: uint32(first), LastPage: uint32(last)}
	resp, _, err := prog.run(ctx, req, pdf, nil)
	if err != nil {
		return nil, err
	}
	return resp.Text, nil
}

func (s *Service) writeRoots(caller []component.WriteRoot) []component.WriteRoot {
	var roots []component.WriteRoot
	if s.cfg.WriteRoots != nil {
		roots = append(roots, s.cfg.WriteRoots()...)
	}
	return append(roots, caller...)
}

// rendererComponent resolves and re-hashes the renderer before every launch
// (AD-4). A nil component comes with the unavailable reason.
func (s *Service) rendererComponent(caller []component.WriteRoot) (*component.Component, string, string) {
	comp, problem := s.cfg.Resolver.Resolve(component.KindPDFRenderer)
	if problem != nil {
		if problem.Status == component.StatusMissing {
			return nil, ReasonRendererMissing, "PDF renderer not installed"
		}
		return nil, ReasonRendererDamaged, "PDF renderer damaged (" + problem.Reason + ")"
	}
	if overlap := component.FirstOverlap(comp.Root, s.writeRoots(caller)); overlap != nil {
		return nil, ReasonRendererDamaged, fmt.Sprintf("PDF renderer is in a folder an agent can write (%s); disabled until that access is removed", overlap.Grant)
	}
	return comp, "", ""
}

// officeComponent resolves LibreOffice once and re-verifies it when a
// fingerprinted file's size or modification time changed (AD-4).
func (s *Service) officeComponent(caller []component.WriteRoot) (*component.Component, *failure) {
	s.officeMu.Lock()
	defer s.officeMu.Unlock()
	if s.office != nil {
		if problem := s.office.VerifyIfChanged(); problem != nil {
			s.office = nil
			return nil, officeProblem(problem)
		}
	} else {
		comp, problem := s.cfg.Resolver.Resolve(component.KindOffice)
		if problem != nil {
			return nil, officeProblem(problem)
		}
		s.office = comp
	}
	if overlap := component.FirstOverlap(s.office.Root, s.writeRoots(caller)); overlap != nil {
		return nil, docFailure(fmt.Sprintf("Office preview is in a folder an agent can write (%s); disabled until that access is removed.", overlap.Grant))
	}
	return s.office, nil
}

func officeProblem(p *component.ComponentError) *failure {
	if p.Status == component.StatusMissing {
		return docFailure(`Office preview is not installed. Re-run the Omnipus installer with the Office preview component, or register LibreOffice with "omnipus office register" (PDF preview works without it).`)
	}
	return docFailure("Office preview is damaged (" + p.Reason + "). Reinstall or re-register the Office preview component.")
}

// officeHelpers lists the extra executables LibreOffice may show under the
// launcher PID (recovery identity).
func officeHelpers(c *component.Component) []string {
	out := make([]string, 0, len(c.Manifest.Files))
	for name := range c.Manifest.Files {
		out = append(out, filepath.Join(c.Root, filepath.FromSlash(name)))
	}
	return out
}

// Status describes the components for doctor and the tool.
type Status struct {
	Renderer string
	Office   string
}

// ComponentStatus reports both components without launching anything.
func (s *Service) ComponentStatus() Status {
	var st Status
	if _, reason, detail := s.rendererComponent(nil); reason == "" {
		st.Renderer = "ready"
	} else {
		st.Renderer = detail
	}
	if _, f := s.officeComponent(nil); f == nil {
		st.Office = "ready"
	} else {
		st.Office = f.Message
	}
	return st
}
