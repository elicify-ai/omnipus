// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package extracttext is extract-text, the hidden mode of the omnipus binary
// that reads a PDF from stdin and answers protocol v1 info and text requests
// with the existing pure-Go extractor (office-preview spec AD-2, AD-15). It
// is used when the PDF renderer is absent, damaged or failed. It runs as its
// own process under run-limited, so a hostile PDF that exhausts the
// extractor ends this process, never the gateway.
//
// It opens no file and no socket: the request and the PDF arrive on stdin,
// frames leave on stdout (MV-6).
package extracttext

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/ledongthuc/pdf"

	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
)

// ModeArg is the hidden argv[1] that selects extract-text.
const ModeArg = "__omnipus-extract-text"

// BuildID identifies this program in META frames and cache keys.
const BuildID = "omnipus-extract-text/1"

// Main runs extract-text on the process's stdin/stdout and returns the exit
// code.
func Main() int {
	return Run(os.Stdin, os.Stdout)
}

// Run serves one request read from in and written to out.
func Run(in io.Reader, out io.Writer) int {
	req, inputLen, err := protocol.ReadRequest(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "extract-text:", err)
		return protocol.ExitTruncatedInput
	}
	if req.Op == protocol.OpRender {
		fw := protocol.NewFrameWriter(out)
		_ = fw.Error("unsupported_op", "extract-text does not render pages")
		return 2
	}
	data := make([]byte, inputLen)
	if _, err := io.ReadFull(in, data); err != nil {
		fmt.Fprintln(os.Stderr, "extract-text: truncated input")
		return protocol.ExitTruncatedInput
	}
	// stdin EOF after the request means the parent is gone: stop at the next
	// page boundary (AD-2).
	var parentGone atomic.Bool
	go func() {
		var b [1]byte
		for {
			if _, err := in.Read(b[:]); err != nil {
				parentGone.Store(true)
				return
			}
		}
	}()
	return serve(req, data, out, &parentGone)
}

// ExitParentGone mirrors protocol.ExitParentGone for readers of this file.
const ExitParentGone = protocol.ExitParentGone

func serve(req protocol.Request, data []byte, out io.Writer, parentGone *atomic.Bool) (code int) {
	fw := protocol.NewFrameWriter(out)
	defer func() {
		// The extractor panics on some malformed PDFs; that is a damaged
		// document, reported as an ERROR frame, not a crash.
		if r := recover(); r != nil {
			_ = fw.Error("open_failed", "the PDF could not be opened (damaged or unsupported)")
			code = 4
		}
	}()
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if errors.Is(err, pdf.ErrInvalidPassword) {
		if metaErr := fw.Meta(protocol.Meta{ProtocolVersion: protocol.Version, BuildID: BuildID, PageCount: 0, Encrypted: true}); metaErr != nil {
			return 1
		}
		_ = fw.Error("encrypted", "password-protected documents cannot be previewed")
		return 4
	}
	if err != nil {
		_ = fw.Error("open_failed", "the PDF could not be opened (damaged or unsupported)")
		return 4
	}
	pages := reader.NumPage()
	if err := fw.Meta(protocol.Meta{ProtocolVersion: protocol.Version, BuildID: BuildID, PageCount: pages}); err != nil {
		return 1
	}
	if req.Op == protocol.OpText {
		last := min(int(req.LastPage), pages)
		for n := int(req.FirstPage); n <= last; n++ {
			if parentGone.Load() {
				return protocol.ExitParentGone
			}
			text, textErr := pageText(reader, n)
			if textErr != nil {
				text = ""
			}
			cut, writeErr := fw.Text(uint32(n), text)
			if writeErr != nil {
				return 1
			}
			if cut {
				break
			}
		}
	}
	if err := fw.Done(); err != nil {
		return 1
	}
	return 0
}

func pageText(r *pdf.Reader, n int) (string, error) {
	page := r.Page(n)
	if page.V.IsNull() {
		return "", errors.New("missing page")
	}
	text, err := page.GetPlainText(nil)
	if err != nil {
		return "", err
	}
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 {
			return r
		}
		return -1
	}, text), nil
}
