// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// nativePDFPart locates one uploaded PDF the resolver attached natively.
type nativePDFPart struct {
	msg, part  int
	path, mime string
	name       string
	size       int64
	turn       bool // one of this turn's own uploads
}

// limitNativePDFParts keeps uploaded PDFs native within two separate
// guards and sends the rest as their extracted text with a note naming the
// limit (office-preview spec AD-6, architect rule G5):
//
//   - count: the provider's documented per-request document count covers
//     every native PDF in the request, earlier turns included — the newest
//     maxDocs stay native (maxDocs <= 0 means no documented limit);
//   - bytes: this turn's own uploaded PDFs share the request's 32 MiB
//     document media budget, newest first; earlier turns' PDFs are outside
//     it and keep only their per-file size limit.
//
// parts is in request order. It returns the decoded bytes of this turn's
// PDFs that stay native.
func limitNativePDFParts(messages []providers.Message, parts []nativePDFPart, maxDocs int) int {
	drop := map[int]map[int]string{}
	mark := func(p nativePDFPart, note string) {
		if drop[p.msg] == nil {
			drop[p.msg] = map[int]string{}
		}
		drop[p.msg][p.part] = note
	}
	kept := parts
	if maxDocs > 0 && len(parts) > maxDocs {
		kept = parts[len(parts)-maxDocs:]
		for _, p := range parts[:len(parts)-maxDocs] {
			mark(p, fmt.Sprintf("this model accepts at most %d PDF documents per request; the newest ones were sent as documents", maxDocs))
		}
	}
	turnBytes := 0
	for i := len(kept) - 1; i >= 0; i-- {
		p := kept[i]
		if !p.turn {
			continue
		}
		if turnBytes+int(p.size) > previewRequestBudget {
			mark(p, fmt.Sprintf("this turn's attached PDFs exceed the request's %d MiB media budget; the newest ones were sent as documents", previewRequestBudget>>20))
			continue
		}
		turnBytes += int(p.size)
	}
	applyNativePDFDrops(messages, parts, drop)
	return turnBytes
}

// applyNativePDFDrops replaces each dropped native PDF with its extracted
// text and the note saying why.
func applyNativePDFDrops(messages []providers.Message, parts []nativePDFPart, drop map[int]map[int]string) {
	byPos := map[[2]int]nativePDFPart{}
	for _, p := range parts {
		byPos[[2]int{p.msg, p.part}] = p
	}
	for msg, gone := range drop {
		kept := make([]string, 0, len(messages[msg].Media))
		var injections []string
		for i, ref := range messages[msg].Media {
			note, dropped := gone[i]
			if !dropped {
				kept = append(kept, ref)
				continue
			}
			p := byPos[[2]int{msg, i}]
			injections = append(injections, fmt.Sprintf(
				"\n\n[PDF %s sent as text: %s]",
				sanitizeInjectedName(fallbackName(p.name, p.path)), note),
				buildDocumentInjection(p.path, p.mime, p.name, p.size))
		}
		messages[msg].Media = kept
		messages[msg].Content = injectDocumentContent(messages[msg].Content, injections)
	}
}
