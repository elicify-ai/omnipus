// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package protocol implements protocol v1, the private framed protocol
// between the gateway and the out-of-process document programs
// (omnipus-pdfium and the binary's own extract-text mode) — office-preview
// spec AD-2 "Protocol v1".
//
// Every length the peer sends is checked against the limit for its frame
// type before a single byte of payload is allocated, and every structural
// rule (META first and exactly once, pages strictly increasing inside the
// requested range, DONE last with a matching count, nothing after DONE,
// total output cap) is enforced by the reader. Any violation is a
// *ProtocolError; the caller kills the program's process group and reports
// "PDF renderer failed (protocol)".
package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"unicode/utf8"
)

// Version is the protocol version this build speaks.
const Version = 1

// Magic opens every request.
const Magic = "OPDF"

// Op selects the operation of a request.
type Op uint16

// Operations (AD-2).
const (
	OpInfo   Op = 1
	OpRender Op = 2
	OpText   Op = 3
)

// FlagRenderForms asks the renderer to draw form fields (request flags bit 0).
const FlagRenderForms uint32 = 1

// Frame types (AD-2 table).
const (
	FrameMeta  byte = 1
	FramePage  byte = 2
	FrameText  byte = 3
	FrameError byte = 4
	FrameDone  byte = 5
)

// Limits from the AD-2 table and AD-6.
const (
	MaxInputPDF       = 50 << 20  // a PDF the person or agent supplied
	MaxInputConverted = 200 << 20 // a PDF LibreOffice produced
	MaxPagesPerCall   = 20        // last - first < 20
	MaxLongEdgePx     = 2000
	MaxMetaBytes      = 4 << 10
	MaxPageFrameBytes = 4 << 20
	MaxTextFrameBytes = 1 << 20
	MaxTextTotalBytes = 4 << 20
	MaxErrorBytes     = 4 << 10
	MaxStdoutBytes    = 80 << 20
	doneFrameBytes    = 4
	pageHeaderBytes   = 12
	textHeaderBytes   = 4
	frameHeaderBytes  = 5
	requestHeaderSize = 32
)

// Exit codes the programs use (documentation for callers; the reader never
// trusts an exit code to decide success — DONE does, FR-011).
const (
	ExitTruncatedInput = 3
	ExitParentGone     = 5
)

// Request is one protocol v1 request.
type Request struct {
	Op          Op
	FirstPage   uint32
	LastPage    uint32
	MaxLongEdge uint32
	Flags       uint32
	// MaxInput is the input-length ceiling for this request: MaxInputPDF
	// for a supplied PDF, MaxInputConverted for LibreOffice output.
	MaxInput int64
}

// Validate checks a request before any byte is sent.
func (r Request) Validate(inputLen int64) error {
	switch r.Op {
	case OpInfo, OpRender, OpText:
	default:
		return fmt.Errorf("protocol: unknown op %d", r.Op)
	}
	if r.MaxInput != MaxInputPDF && r.MaxInput != MaxInputConverted {
		return fmt.Errorf("protocol: input ceiling %d is neither the PDF nor the converted-PDF limit", r.MaxInput)
	}
	if inputLen <= 0 || inputLen > r.MaxInput {
		return fmt.Errorf("protocol: input length %d outside 1..%d", inputLen, r.MaxInput)
	}
	if r.Op != OpInfo {
		if r.FirstPage < 1 || r.LastPage < r.FirstPage || r.LastPage-r.FirstPage >= MaxPagesPerCall {
			return fmt.Errorf("protocol: page range %d-%d invalid (at most %d pages)", r.FirstPage, r.LastPage, MaxPagesPerCall)
		}
	}
	if r.Op == OpRender && (r.MaxLongEdge < 1 || r.MaxLongEdge > MaxLongEdgePx) {
		return fmt.Errorf("protocol: max long edge %d outside 1..%d", r.MaxLongEdge, MaxLongEdgePx)
	}
	return nil
}

// WriteRequest writes the request header followed by exactly inputLen bytes
// of input. The caller keeps stdin open afterwards: closing it tells the
// program its parent is gone.
func WriteRequest(w io.Writer, r Request, input io.Reader, inputLen int64) error {
	if err := r.Validate(inputLen); err != nil {
		return err
	}
	var h [requestHeaderSize]byte
	copy(h[0:4], Magic)
	binary.LittleEndian.PutUint16(h[4:6], Version)
	binary.LittleEndian.PutUint16(h[6:8], uint16(r.Op))
	binary.LittleEndian.PutUint32(h[8:12], r.FirstPage)
	binary.LittleEndian.PutUint32(h[12:16], r.LastPage)
	binary.LittleEndian.PutUint32(h[16:20], r.MaxLongEdge)
	binary.LittleEndian.PutUint32(h[20:24], r.Flags)
	binary.LittleEndian.PutUint64(h[24:32], uint64(inputLen))
	if _, err := w.Write(h[:]); err != nil {
		return fmt.Errorf("protocol: write request header: %w", err)
	}
	n, err := io.CopyN(w, input, inputLen)
	if err != nil {
		return fmt.Errorf("protocol: write request input (%d of %d bytes): %w", n, inputLen, err)
	}
	return nil
}

// ReadRequest parses a request header on the program side (extract-text
// mode). It returns the request and the declared input length; the caller
// reads exactly that many bytes next. The declared length is bounded by the
// converted-PDF ceiling, the larger of the two input limits.
func ReadRequest(rd io.Reader) (Request, int64, error) {
	var h [requestHeaderSize]byte
	if _, err := io.ReadFull(rd, h[:]); err != nil {
		return Request{}, 0, fmt.Errorf("protocol: read request header: %w", err)
	}
	if string(h[0:4]) != Magic {
		return Request{}, 0, errors.New("protocol: bad magic")
	}
	if v := binary.LittleEndian.Uint16(h[4:6]); v != Version {
		return Request{}, 0, fmt.Errorf("protocol: unsupported version %d", v)
	}
	r := Request{
		Op:          Op(binary.LittleEndian.Uint16(h[6:8])),
		FirstPage:   binary.LittleEndian.Uint32(h[8:12]),
		LastPage:    binary.LittleEndian.Uint32(h[12:16]),
		MaxLongEdge: binary.LittleEndian.Uint32(h[16:20]),
		Flags:       binary.LittleEndian.Uint32(h[20:24]),
		MaxInput:    MaxInputConverted,
	}
	declared := binary.LittleEndian.Uint64(h[24:32])
	if declared == 0 || declared > MaxInputConverted {
		return Request{}, 0, fmt.Errorf("protocol: input length %d outside 1..%d", declared, MaxInputConverted)
	}
	inputLen := int64(declared)
	if err := r.Validate(inputLen); err != nil {
		return Request{}, 0, err
	}
	return r, inputLen, nil
}

// Meta is the META frame payload.
type Meta struct {
	ProtocolVersion int    `json:"protocol_version"`
	BuildID         string `json:"build_id"`
	PageCount       int    `json:"page_count"`
	Encrypted       bool   `json:"encrypted"`
}

// Page is one rendered page.
type Page struct {
	Number uint32
	Width  uint32
	Height uint32
	PNG    []byte
}

// TextPage is one page of extracted text.
type TextPage struct {
	Number uint32
	Text   string
}

// PeerError is the program's terminal error report.
type PeerError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error makes a received ERROR frame usable as an error.
func (e *PeerError) Error() string {
	return fmt.Sprintf("program error %s: %s", e.Code, e.Message)
}

// Response is everything a successful exchange produced.
type Response struct {
	Meta  Meta
	Pages []Page
	Text  []TextPage
}

// ProtocolError is a peer that broke a protocol rule. The caller must kill
// the program; nothing the peer sent after the violation is used.
type ProtocolError struct {
	Reason string
}

func (e *ProtocolError) Error() string { return "protocol error: " + e.Reason }

func violation(format string, args ...any) error {
	return &ProtocolError{Reason: fmt.Sprintf(format, args...)}
}

// IsProtocolError reports whether err is a protocol violation.
func IsProtocolError(err error) bool {
	var pe *ProtocolError
	return errors.As(err, &pe)
}

// PageSink receives each rendered page as soon as it has been validated, so
// the gateway holds at most one frame per running program (AD-6 in-flight
// budget). A sink error aborts the exchange.
type PageSink func(Page) error

// ReadResponse reads and validates the complete response to req. Pages are
// delivered to sink when it is non-nil (and not retained in Response.Pages);
// with a nil sink they are collected. An ERROR frame is returned as
// *PeerError. Any rule violation is a *ProtocolError.
func ReadResponse(rd io.Reader, req Request, sink PageSink) (*Response, error) {
	lr := &countingReader{r: rd, limit: MaxStdoutBytes}
	resp := &Response{}
	var frames uint32
	var lastPage uint32
	var textTotal int
	sawMeta := false
	for {
		typ, payload, err := readFrame(lr)
		if err != nil {
			return nil, err
		}
		frames++
		if !sawMeta && typ != FrameMeta && typ != FrameError {
			return nil, violation("first frame is type %d, want META", typ)
		}
		switch typ {
		case FrameMeta:
			if sawMeta {
				return nil, violation("second META frame")
			}
			sawMeta = true
			if err := decodeStrictJSON(payload, &resp.Meta); err != nil {
				return nil, violation("META is not valid JSON: %v", err)
			}
			if resp.Meta.ProtocolVersion != Version {
				return nil, violation("META protocol_version %d, want %d", resp.Meta.ProtocolVersion, Version)
			}
			if resp.Meta.BuildID == "" || resp.Meta.PageCount < 0 {
				return nil, violation("META missing build_id or negative page_count")
			}
		case FramePage:
			if req.Op != OpRender {
				return nil, violation("PAGE frame in a non-render response")
			}
			page, err := parsePage(payload, req, resp.Meta.PageCount, lastPage)
			if err != nil {
				return nil, err
			}
			lastPage = page.Number
			if sink != nil {
				if err := sink(page); err != nil {
					return nil, err
				}
			} else {
				resp.Pages = append(resp.Pages, page)
			}
		case FrameText:
			if req.Op != OpText {
				return nil, violation("TEXT frame in a non-text response")
			}
			tp, err := parseText(payload, req, resp.Meta.PageCount, lastPage)
			if err != nil {
				return nil, err
			}
			textTotal += len(tp.Text)
			if textTotal > MaxTextTotalBytes {
				return nil, violation("text total exceeds %d bytes", MaxTextTotalBytes)
			}
			lastPage = tp.Number
			resp.Text = append(resp.Text, tp)
		case FrameError:
			var ef PeerError
			if err := decodeStrictJSON(payload, &ef); err != nil || ef.Code == "" {
				return nil, violation("ERROR frame is not a valid {code,message} object")
			}
			if err := expectEOF(lr); err != nil {
				return nil, err
			}
			return nil, &ef
		case FrameDone:
			if !sawMeta {
				return nil, violation("DONE before META")
			}
			count := binary.LittleEndian.Uint32(payload)
			if count != frames {
				return nil, violation("DONE counts %d frames, received %d", count, frames)
			}
			if err := expectEOF(lr); err != nil {
				return nil, err
			}
			return resp, nil
		}
	}
}

// maxPayload is the per-type ceiling checked before allocation.
func maxPayload(typ byte) (uint32, bool) {
	switch typ {
	case FrameMeta:
		return MaxMetaBytes, true
	case FramePage:
		return MaxPageFrameBytes, true
	case FrameText:
		return MaxTextFrameBytes, true
	case FrameError:
		return MaxErrorBytes, true
	case FrameDone:
		return doneFrameBytes, true
	default:
		return 0, false
	}
}

func readFrame(lr *countingReader) (byte, []byte, error) {
	var h [frameHeaderBytes]byte
	if _, err := io.ReadFull(lr, h[:]); err != nil {
		if lr.over {
			return 0, nil, violation("output exceeds %d bytes", MaxStdoutBytes)
		}
		if errors.Is(err, io.EOF) {
			return 0, nil, violation("stream ended before DONE")
		}
		return 0, nil, violation("truncated frame header: %v", err)
	}
	typ := h[0]
	length := binary.LittleEndian.Uint32(h[1:5])
	limit, known := maxPayload(typ)
	if !known {
		return 0, nil, violation("unknown frame type %d", typ)
	}
	if length > limit {
		return 0, nil, violation("frame type %d declares %d bytes, limit %d", typ, length, limit)
	}
	if typ == FrameDone && length != doneFrameBytes {
		return 0, nil, violation("DONE frame has %d bytes, want %d", length, doneFrameBytes)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(lr, payload); err != nil {
		if lr.over {
			return 0, nil, violation("output exceeds %d bytes", MaxStdoutBytes)
		}
		return 0, nil, violation("truncated frame payload (type %d, %d bytes): %v", typ, length, err)
	}
	return typ, payload, nil
}

func parsePage(payload []byte, req Request, pageCount int, lastPage uint32) (Page, error) {
	if len(payload) <= pageHeaderBytes {
		return Page{}, violation("PAGE frame too short")
	}
	p := Page{
		Number: binary.LittleEndian.Uint32(payload[0:4]),
		Width:  binary.LittleEndian.Uint32(payload[4:8]),
		Height: binary.LittleEndian.Uint32(payload[8:12]),
		PNG:    payload[pageHeaderBytes:],
	}
	if err := checkPageNumber(p.Number, req, pageCount, lastPage); err != nil {
		return Page{}, err
	}
	if p.Width < 1 || p.Height < 1 || p.Width > req.MaxLongEdge || p.Height > req.MaxLongEdge {
		return Page{}, violation("page %d is %dx%d, limit %d", p.Number, p.Width, p.Height, req.MaxLongEdge)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(p.PNG))
	if err != nil {
		return Page{}, violation("page %d is not a PNG: %v", p.Number, err)
	}
	if uint32(cfg.Width) != p.Width || uint32(cfg.Height) != p.Height {
		return Page{}, violation("page %d PNG is %dx%d, frame says %dx%d", p.Number, cfg.Width, cfg.Height, p.Width, p.Height)
	}
	return p, nil
}

func parseText(payload []byte, req Request, pageCount int, lastPage uint32) (TextPage, error) {
	if len(payload) < textHeaderBytes {
		return TextPage{}, violation("TEXT frame too short")
	}
	tp := TextPage{Number: binary.LittleEndian.Uint32(payload[0:4])}
	if err := checkPageNumber(tp.Number, req, pageCount, lastPage); err != nil {
		return TextPage{}, err
	}
	body := payload[textHeaderBytes:]
	if !utf8.Valid(body) {
		return TextPage{}, violation("page %d text is not UTF-8", tp.Number)
	}
	tp.Text = string(body)
	return tp, nil
}

func checkPageNumber(n uint32, req Request, pageCount int, lastPage uint32) error {
	if n < req.FirstPage || n > req.LastPage {
		return violation("page %d outside requested %d-%d", n, req.FirstPage, req.LastPage)
	}
	if pageCount >= 0 && int64(n) > int64(pageCount) {
		return violation("page %d beyond page count %d", n, pageCount)
	}
	if n <= lastPage {
		return violation("page %d not after page %d", n, lastPage)
	}
	return nil
}

func expectEOF(lr *countingReader) error {
	var b [1]byte
	n, err := lr.Read(b[:])
	if n > 0 {
		return violation("output after the terminal frame")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return violation("reading after the terminal frame: %v", err)
	}
	if err == nil {
		// A reader may return (0, nil); one more read decides.
		n, err = lr.Read(b[:])
		if n > 0 {
			return violation("output after the terminal frame")
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return violation("reading after the terminal frame: %v", err)
		}
	}
	return nil
}

func decodeStrictJSON(payload []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after the JSON object")
	}
	return nil
}

// countingReader enforces the total-output cap.
type countingReader struct {
	r     io.Reader
	n     int64
	limit int64
	over  bool
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.n >= c.limit {
		// Probe one byte: reaching the limit exactly is fine, more is not.
		var b [1]byte
		n, err := c.r.Read(b[:])
		if n > 0 {
			c.over = true
			return 0, violation("output exceeds %d bytes", c.limit)
		}
		return 0, err
	}
	if remaining := c.limit - c.n; int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
