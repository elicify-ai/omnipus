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
}

// capNativePDFParts applies the provider's documented per-request document
// count (catalog pdf_limits.max_documents_per_request, office-preview spec
// AD-6): the newest maxDocs uploaded PDFs stay native documents; each older
// one goes as its extracted text (the text path a model without native PDF
// gets) with a note naming the limit. maxDocs <= 0 means no documented
// limit. parts is in request order.
func capNativePDFParts(messages []providers.Message, parts []nativePDFPart, maxDocs int) {
	if maxDocs <= 0 || len(parts) <= maxDocs {
		return
	}
	drop := map[int]map[int]nativePDFPart{}
	for _, p := range parts[:len(parts)-maxDocs] {
		if drop[p.msg] == nil {
			drop[p.msg] = map[int]nativePDFPart{}
		}
		drop[p.msg][p.part] = p
	}
	for msg, gone := range drop {
		kept := make([]string, 0, len(messages[msg].Media))
		var injections []string
		for i, ref := range messages[msg].Media {
			p, dropped := gone[i]
			if !dropped {
				kept = append(kept, ref)
				continue
			}
			injections = append(injections, fmt.Sprintf(
				"\n\n[PDF %s sent as text: this model accepts at most %d PDF documents per request; the newest ones were sent as documents]",
				sanitizeInjectedName(fallbackName(p.name, p.path)), maxDocs),
				buildDocumentInjection(p.path, p.mime, p.name, p.size))
		}
		messages[msg].Media = kept
		messages[msg].Content = injectDocumentContent(messages[msg].Content, injections)
	}
}
