package agent

// In-test fixtures for spec #1968 (read-file native PDF), loop level.
//
// The harness shape follows effort_red_bedrock_loop_test.go: the fixture
// catalogue is installed as the PROCESS catalogue (providers.SetCatalog,
// restored at cleanup) so the fixture provider ids resolve, each routing
// pair gets a cfg.Providers row pointed at a recording httptest peer
// (protocol openai-compatible), and every assertion reads the raw request
// bodies the peer saw. Nothing here is committed to disk (§6.1): the PDF
// fixtures are assembled in memory from numbered objects with a
// byte-accurate xref table. Catalogue fixtures carry capabilities only;
// limit fixtures populate config.PDFLimitsConfig, never catalogue members.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/docextract"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/mediatest"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/azure"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ---------------------------------------------------------------------------
// PDF fixtures.
// ---------------------------------------------------------------------------

type pdfObj struct {
	num    int
	body   string
	offset int
}

// pdf1968AssemblePDF lays out header + objects + xref/trailer/startxref and
// returns the complete PDF bytes. rootNum is the object number the trailer's
// /Root points at (the /Type /Catalog object); highest is derived from the
// object numbers actually present, so builders that derive their numbering
// stay collision-free at any page count (F3).
func pdf1968AssemblePDF(header string, objs []pdfObj, rootNum int, trailerExtra string) []byte {
	highest := 0
	byNum := make(map[int]*pdfObj, len(objs))
	for i := range objs {
		byNum[objs[i].num] = &objs[i]
		if objs[i].num > highest {
			highest = objs[i].num
		}
	}

	var buf strings.Builder
	offset := 0
	write := func(s string) {
		offset += len(s)
		buf.WriteString(s)
	}
	write(header + "\n")
	for i := range objs {
		objs[i].offset = offset
		write(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", objs[i].num, objs[i].body))
	}
	xrefStart := offset
	write(fmt.Sprintf("xref\n0 %d\n", highest+1))
	write("0000000000 65535 f \n")
	for n := 1; n <= highest; n++ {
		if o, ok := byNum[n]; ok {
			write(fmt.Sprintf("%010d 00000 n \n", o.offset))
		} else {
			write("0000000000 65535 f \n")
		}
	}
	trailer := fmt.Sprintf("/Size %d /Root %d 0 R", highest+1, rootNum)
	if trailerExtra != "" {
		trailer += " " + trailerExtra
	}
	write(fmt.Sprintf("trailer\n<< %s >>\nstartxref\n%d\n%%%%EOF\n", trailer, xrefStart))
	return []byte(buf.String())
}

// pdf1968Layout derives every object number from the page count so no two
// objects ever share a number, at any N (F3: the old fixed 888/999 collided
// with the derived ranges at 700 pages). font=1, contents=2..N+1,
// pages=N+2..2N+1, tree=2N+2, catalog=2N+3.
type pdf1968Layout struct {
	font     int
	contents []int
	pages    []int
	tree     int
	catalog  int
}

func pdf1968LayoutFor(pages int) pdf1968Layout {
	l := pdf1968Layout{
		font:     1,
		contents: make([]int, pages),
		pages:    make([]int, pages),
		tree:     2*pages + 2,
		catalog:  2*pages + 3,
	}
	for i := 0; i < pages; i++ {
		l.contents[i] = 2 + i
		l.pages[i] = pages + 2 + i
	}
	return l
}

func pdf1968PageDict(l pdf1968Layout, i int) string {
	return fmt.Sprintf(
		`<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>`,
		l.tree, l.font, l.contents[i])
}

func pdf1968TreeDict(l pdf1968Layout, withCount bool, count int) string {
	countDict := ""
	if withCount {
		countDict = fmt.Sprintf("/Count %d ", count)
	}
	kids := ""
	for i, id := range l.pages {
		if i > 0 {
			kids += " "
		}
		kids += fmt.Sprintf("%d 0 R", id)
	}
	return fmt.Sprintf(`<< %s/Type /Pages /Kids [%s] >>`, countDict, kids)
}

// pdf1968TextPDF builds a valid N-page PDF whose page i draws "PAGE i text"
// (uncompressed content stream). withCount controls the pages tree's /Count
// entry — omitting it makes the page count unknown to the extractor.
func pdf1968TextPDF(t *testing.T, pages int, withCount bool) []byte {
	t.Helper()
	l := pdf1968LayoutFor(pages)
	contentFor := func(page int) string {
		return fmt.Sprintf("BT /F1 12 Tf 72 720 Td (PAGE %d text) Tj ET", page+1)
	}

	objs := []pdfObj{{num: l.font, body: `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`}}
	for i := 0; i < pages; i++ {
		objs = append(objs, pdfObj{num: l.contents[i], body: fmt.Sprintf(`<< /Length %d >>`+"\n"+
			"stream\n%s\nendstream", len(contentFor(i)), contentFor(i))})
	}
	for i := 0; i < pages; i++ {
		objs = append(objs, pdfObj{num: l.pages[i], body: pdf1968PageDict(l, i)})
	}
	objs = append(objs,
		pdfObj{num: l.tree, body: pdf1968TreeDict(l, withCount, pages)},
		pdfObj{num: l.catalog, body: fmt.Sprintf(`<< /Type /Catalog /Pages %d 0 R >>`, l.tree)})
	return pdf1968AssemblePDF("%PDF-1.4", objs, l.catalog, "")
}

// pdf1968NoTextPDF builds a valid N-page PDF whose content streams are
// empty: real structure, no extractable text (the scanned shape).
func pdf1968NoTextPDF(t *testing.T, pages int, withCount bool) []byte {
	t.Helper()
	l := pdf1968LayoutFor(pages)
	objs := []pdfObj{{num: l.font, body: `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`}}
	for i := 0; i < pages; i++ {
		objs = append(objs, pdfObj{num: l.contents[i], body: "<< /Length 0 >>\nstream\n\nendstream"})
	}
	for i := 0; i < pages; i++ {
		objs = append(objs, pdfObj{num: l.pages[i], body: pdf1968PageDict(l, i)})
	}
	objs = append(objs,
		pdfObj{num: l.tree, body: pdf1968TreeDict(l, withCount, pages)},
		pdfObj{num: l.catalog, body: fmt.Sprintf(`<< /Type /Catalog /Pages %d 0 R >>`, l.tree)})
	return pdf1968AssemblePDF("%PDF-1.4", objs, l.catalog, "")
}

// pdf1968PadTo pads a PDF to exactly size bytes with a tail the ledongthuc
// reader still accepts (F3): '%' comment bytes followed by a complete
// startxref line repeating the REAL xref offset and a final %%EOF, so the
// reader's last-100-byte window keeps a complete `startxref` line and an
// %%EOF terminator. Padding inside the first page's content stream would
// change /Length; after-EOF padding with no trailer re-parse breaks
// NewReaderEncrypted's window — this scheme is the one both accept.
func pdf1968PadTo(t *testing.T, data []byte, size int) []byte {
	t.Helper()
	if len(data) >= size {
		t.Fatalf("fixture broken: base PDF is %d bytes, larger than requested size %d", len(data), size)
	}
	// The real xref offset is the number on the line after the document's
	// last complete "startxref" line (the line ends at its newline — the
	// trailer continues with %%EOF, which is not part of the number).
	raw := string(data)
	idx := strings.LastIndex(raw, "\nstartxref\n")
	if idx < 0 {
		t.Fatalf("fixture broken: no startxref line to re-emit")
	}
	rest := raw[idx+len("\nstartxref\n"):]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	off := strings.TrimSpace(rest)
	if _, err := strconv.Atoi(off); err != nil {
		t.Fatalf("fixture broken: startxref value %q is not a number", off)
	}
	tail := "\nstartxref\n" + off + "\n%%EOF\n"
	pad := size - len(data) - len(tail)
	if pad < 0 {
		t.Fatalf("fixture broken: %d-byte base cannot be padded to exactly %d (tail %d bytes)", len(data), size, len(tail))
	}
	out := make([]byte, 0, size)
	out = append(out, data...)
	out = append(out, bytes_Repeat(pad)...)
	out = append(out, tail...)
	return out
}

// pdf1968PDFOfSize builds a one-page text PDF padded to exactly size bytes.
func pdf1968PDFOfSize(t *testing.T, size int) []byte {
	t.Helper()
	return pdf1968PadTo(t, pdf1968TextPDF(t, 1, true), size)
}

// pdf1968UnknownPagePDFOfSize builds a text PDF whose page count is NOT
// stated (no /Count) padded to exactly size bytes (F4): unknown-page
// accounting must consume bytes while adding zero pages, so the fixture
// needs both properties at once.
func pdf1968UnknownPagePDFOfSize(t *testing.T, size int) []byte {
	t.Helper()
	return pdf1968PadTo(t, pdf1968TextPDF(t, 1, false), size)
}

func bytes_Repeat(n int) []byte {
	pad := make([]byte, n)
	for i := range pad {
		pad[i] = '%'
	}
	return pad
}

// pdf1968GuardTextFree asserts data is a structurally valid PDF whose
// extraction is text-free. Today's extractor reports a parsed but text-free
// document as ok=false with the "no extractable text" reason (returned only
// after pdf open and the plain-text walk succeeded); either that or a clean
// empty-text outcome proves the fixture valid — an open/extraction/read
// failure means the fixture itself is broken.
func pdf1968GuardTextFree(t *testing.T, label string, data []byte, pdfMIME string) {
	t.Helper()
	text, ok, reason := docextract.ExtractBytes(data, pdfMIME, label+".pdf")
	switch {
	case ok:
		if strings.TrimSpace(text) != "" {
			t.Fatalf("%s must have no extractable text, got %q", label, clipText(text))
		}
	case strings.Contains(reason, "no extractable text"):
		// parsed and text-free — today's outcome for a text-free document
	case strings.Contains(reason, "pdf open failed"),
		strings.Contains(reason, "pdf text extraction failed"),
		strings.Contains(reason, "pdf read failed"):
		t.Fatalf("%s is structurally invalid: %s", label, reason)
	default:
		t.Fatalf("%s failed extraction unexpectedly: %s", label, reason)
	}
}

// TestPDF1968FixtureBuilders_Parse (G, F3) — the fixture builders must
// produce documents the production extractor accepts: exact size, page
// counts, unknown pages, and padding that keeps the file parseable. The
// builder itself is the oracle for every loop-level test above, so its
// output is validated directly (any collision, bad xref or broken padding
// fails here first instead of masquerading as a behavioural failure).
func TestPDF1968FixtureBuilders_Parse(t *testing.T) {
	pdfMIME := "application/pdf"

	t.Run("text pdf extracts its page text", func(t *testing.T) {
		for _, pages := range []int{1, 2, 7, 700} {
			data := pdf1968TextPDF(t, pages, true)
			text, ok, reason := docextract.ExtractBytes(data, pdfMIME, "t.pdf")
			if !ok {
				t.Fatalf("%d-page text fixture must extract (reason %q)", pages, reason)
			}
			if !strings.Contains(text, "PAGE 1 text") || (pages > 1 && !strings.Contains(text, fmt.Sprintf("PAGE %d text", pages))) {
				t.Fatalf("%d-page text fixture text incomplete: %q", pages, clipText(text))
			}
		}
	})

	t.Run("no-text pdf extracts nothing", func(t *testing.T) {
		for _, pages := range []int{1, 700} {
			pdf1968GuardTextFree(t, fmt.Sprintf("%d-page no-text", pages), pdf1968NoTextPDF(t, pages, true), pdfMIME)
		}
	})

	t.Run("no /Count means the page count is unstated", func(t *testing.T) {
		data := pdf1968TextPDF(t, 3, false)
		if strings.Contains(string(data), "/Count") {
			t.Fatal("withCount=false fixture must not carry a /Count entry anywhere")
		}
		pdf1968GuardTextFree(t, "no-count", data, pdfMIME)
	})

	t.Run("object numbers never collide", func(t *testing.T) {
		re := regexp.MustCompile(`(?m)^(\d+) 0 obj$`)
		for _, pages := range []int{1, 700} {
			seen := map[int]bool{}
			for _, m := range re.FindAllStringSubmatch(string(pdf1968TextPDF(t, pages, true)), -1) {
				n, err := strconv.Atoi(m[1])
				if err != nil {
					t.Fatalf("bad object number %q", m[1])
				}
				if seen[n] {
					t.Fatalf("%d-page fixture reuses object number %d", pages, n)
				}
				seen[n] = true
			}
			if len(seen) != 2*pages+3 {
				t.Fatalf("%d-page fixture has %d objects, want %d (font + N contents + N pages + tree + catalog)", pages, len(seen), 2*pages+3)
			}
		}
	})

	t.Run("exact-size padding stays parseable", func(t *testing.T) {
		for _, size := range []int{1024, 4096, 2048 * 6, 30000000} {
			data := pdf1968PDFOfSize(t, size)
			if len(data) != size {
				t.Fatalf("pdf1968PDFOfSize(%d) produced %d bytes", size, len(data))
			}
			text, ok, reason := docextract.ExtractBytes(data, pdfMIME, "t.pdf")
			if ok {
				if !strings.Contains(text, "PAGE 1 text") {
					t.Fatalf("padded %d-byte fixture lost its page text: %q", size, clipText(text))
				}
				continue
			}
			// Fixtures above today's extractor's own document-size limit
			// (25 MiB) are refused before any parse happens — that outcome
			// says nothing about fixture validity; the under-limit sizes in
			// this sweep prove the padding scheme parseable.
			if strings.Contains(reason, "too large to extract") {
				continue
			}
			t.Fatalf("padded %d-byte fixture must stay a parseable text PDF (ok=%v reason=%q text=%q)", size, ok, reason, clipText(text))
		}
	})
}

// clipText bounds an extractor excerpt in failure messages.
func clipText(s string) string {
	if len(s) <= 200 {
		return s
	}
	return s[:200] + "…[clipped]"
}

// ---------------------------------------------------------------------------
// Catalogue capabilities and engine-configured PDF limit fixtures.
// ---------------------------------------------------------------------------

// pdf1968ModelRow carries capability metadata and optional CONFIG limits.
type pdf1968ModelRow struct {
	id         string
	modalities []string
	limits     map[string]any
}

// pdf1968ProviderRow carries provider metadata and optional CONFIG limits.
// protocol/region/regions feed the loop-level Bedrock and Anthropic recipes
// (T11/T15/F9): protocol selects the client the fixture provider id builds,
// region names the Bedrock region, regions seeds the region group list.
type pdf1968ProviderRow struct {
	id       string
	limits   map[string]any
	models   []pdf1968ModelRow
	protocol string // empty → openai-compatible
	region   string
	regions  []map[string]any
}

// pdf1968DocJSON builds an ADR-067 catalogue with capabilities, never limits.
func pdf1968DocJSON(t *testing.T, provs []pdf1968ProviderRow) []byte {
	t.Helper()
	doc := map[string]any{
		"schema_version":        "2.0.0",
		"version":               "v2026.7.23",
		"updated_at":            "2026-07-23T00:00:00Z",
		"source":                "test",
		"default_resize_limits": map[string]any{"long_edge_px": 7680, "max_bytes": 10485760},
	}
	providerList := make([]map[string]any, 0, len(provs))
	for _, p := range provs {
		protocol := p.protocol
		if protocol == "" {
			protocol = "openai-compatible"
		}
		pj := map[string]any{
			"id":            p.id,
			"name":          p.id,
			"api":           "https://api.example.test/v1",
			"protocol":      protocol,
			"tier":          "standard",
			"auth_methods":  []string{"api_key"},
			"resize_limits": map[string]any{"long_edge_px": 7680, "max_bytes": 10485760},
		}
		if p.region != "" {
			pj["region"] = p.region
		}
		if p.regions != nil {
			pj["regions"] = p.regions
		}
		models := make([]map[string]any, 0, len(p.models))
		for _, m := range p.models {
			mj := map[string]any{
				"id":               m.id,
				"name":             m.id,
				"context_window":   128000,
				"input_modalities": m.modalities,
				"tool_call":        true,
				"status":           "active",
			}
			models = append(models, mj)
		}
		pj["models"] = models
		providerList = append(providerList, pj)
	}
	doc["providers"] = providerList
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	return data
}

func pdf1968Catalog(t *testing.T, provs []pdf1968ProviderRow) *catalog.Catalog {
	t.Helper()
	c, err := catalog.NewCatalog(pdf1968DocJSON(t, provs))
	require.NoError(t, err)
	return c
}

// pdf1968LimitTable translates fixture rows into the real config decoder.
// Tests of otherwise-unlimited routes explicitly neutralize shipped defaults;
// this preserves their single-limit premises without relying on old feeds.
func pdf1968LimitTable(t *testing.T, defaults map[string]any, provs []pdf1968ProviderRow) *config.PDFLimitsConfig {
	t.Helper()
	base := map[string]any{
		"max_file_bytes": 0, "max_total_file_bytes": 0, "max_request_bytes": 0,
		"max_pages_per_file": 0, "max_pages_per_request": 0,
		"max_documents_per_message": 0, "max_documents_per_request": 0,
	}
	for name, value := range defaults {
		base[name] = value
	}
	var overrides []map[string]any
	for _, p := range provs {
		if p.limits != nil {
			row := map[string]any{"provider": p.id}
			for name, value := range p.limits {
				row[name] = value
			}
			overrides = append(overrides, row)
		}
		for _, m := range p.models {
			if m.limits == nil {
				continue
			}
			row := map[string]any{"provider": p.id, "model": m.id}
			for name, value := range m.limits {
				row[name] = value
			}
			overrides = append(overrides, row)
		}
	}
	data, err := json.Marshal(map[string]any{"default": base, "overrides": overrides})
	require.NoError(t, err)
	var table config.PDFLimitsConfig
	require.NoError(t, json.Unmarshal(data, &table))
	return &table
}

func pdf1968SetLimits(t *testing.T, al *AgentLoop, defaults map[string]any, provs []pdf1968ProviderRow) {
	t.Helper()
	require.NoError(t, al.MutateConfig(func(c *config.Config) error {
		c.PDFLimits = pdf1968LimitTable(t, defaults, provs)
		return nil
	}))
}

// ---------------------------------------------------------------------------
// Recording peer: an OpenAI-compatible httptest server with scripted
// responses (content, tool calls, or raw HTTP errors) that records every
// raw request body.
// ---------------------------------------------------------------------------

// pdf1968Step is one scripted peer response. shape selects the response
// dialect: "" or "openai" answers the Chat Completions shape, "bedrock" the
// Converse shape, "anthropic" the Messages shape (T11/T15 protocol arms).
// headers sets response headers — the Retry-After the fallback chain's
// rate-limit parse layer reads (T15's rate-limit exclusion arm).
type pdf1968Step struct {
	content   string
	toolCalls []map[string]any // OpenAI chat tool_calls entries (finish tool_calls)
	status    int              // non-zero: respond with this HTTP status + errBody
	errBody   string
	headers   map[string]string
	onRequest func() // invoked after recording this exact request, before responding
	shape     string // "", "openai", "bedrock", "anthropic", "responses"
}

type pdf1968Peer struct {
	mu     sync.Mutex
	steps  []pdf1968Step
	calls  int
	raws   []string
	paths  []string
	server *httptest.Server
}

func newPdf1968Peer(t *testing.T, steps ...pdf1968Step) *pdf1968Peer {
	t.Helper()
	p := &pdf1968Peer{steps: steps}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "peer read failed", http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		idx := p.calls
		p.calls++
		p.raws = append(p.raws, string(body))
		p.paths = append(p.paths, r.URL.Path)
		var step pdf1968Step
		have := idx < len(p.steps)
		if have {
			step = p.steps[idx]
		}
		p.mu.Unlock()
		if step.onRequest != nil {
			step.onRequest()
		}

		w.Header().Set("Content-Type", "application/json")
		if len(step.headers) > 0 {
			for k, v := range step.headers {
				w.Header().Set(k, v)
			}
		}
		if !have {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"pdf1968Peer: unscripted call"}}`))
			return
		}
		if step.status != 0 {
			w.WriteHeader(step.status)
			_, _ = w.Write([]byte(step.errBody))
			return
		}
		switch step.shape {
		case "bedrock":
			// Converse shape (effort_red_bedrock_loop_test.go recipe). The
			// request path is /model/<id>/converse; the peer answers the same
			// way for both IDs.
			output := map[string]any{}
			stop := "end_turn"
			if len(step.toolCalls) > 0 {
				blocks := make([]any, 0, len(step.toolCalls))
				for _, tc := range step.toolCalls {
					fn, _ := tc["function"].(map[string]any)
					name, _ := fn["name"].(string)
					args := fn["arguments"]
					var input any
					if s, ok := args.(string); ok {
						input = json.RawMessage(s)
					} else {
						input = args
					}
					blocks = append(blocks, map[string]any{
						"toolUse": map[string]any{
							"toolUseId": tc["id"],
							"name":      name,
							"input":     input,
						},
					})
				}
				output["message"] = map[string]any{"role": "assistant", "content": blocks}
				stop = "tool_use"
			} else {
				output["message"] = map[string]any{"role": "assistant",
					"content": []any{map[string]any{"text": step.content}}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output":     output,
				"stopReason": stop,
				"usage":      map[string]any{"inputTokens": 1, "outputTokens": 1, "totalTokens": 2},
			})
		case "responses":
			output := make([]any, 0, len(step.toolCalls)+1)
			for _, tc := range step.toolCalls {
				fn, ok := tc["function"].(map[string]any)
				if !ok {
					t.Errorf("Responses fixture function must be an object: %#v", tc["function"])
					http.Error(w, "invalid tool-call fixture", http.StatusInternalServerError)
					return
				}
				id, ok := tc["id"].(string)
				if !ok || id == "" {
					t.Errorf("Responses fixture call id must be a nonempty string: %#v", tc["id"])
					http.Error(w, "invalid tool-call fixture", http.StatusInternalServerError)
					return
				}
				output = append(output, map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": id, "name": fn["name"], "arguments": fn["arguments"], "status": "completed"})
			}
			if len(step.toolCalls) == 0 {
				output = append(output, map[string]any{"type": "message", "role": "assistant", "id": "msg_pdf1968", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": step.content, "annotations": []any{}}}})
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"id": "resp_pdf1968", "object": "response", "created_at": 1, "status": "completed", "model": "fx/req-model", "output": output, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}); err != nil {
				t.Errorf("encode Responses peer reply: %v", err)
			}
		case "anthropic":
			// Messages shape: content blocks with tool_use entries.
			blocks := make([]any, 0, 2)
			stop := "end_turn"
			if len(step.toolCalls) > 0 {
				for _, tc := range step.toolCalls {
					fn, _ := tc["function"].(map[string]any)
					name, _ := fn["name"].(string)
					args := fn["arguments"]
					var input any
					if s, ok := args.(string); ok {
						input = json.RawMessage(s)
					} else {
						input = args
					}
					blocks = append(blocks, map[string]any{
						"type": "tool_use", "id": tc["id"], "name": name, "input": input,
					})
				}
				stop = "tool_use"
			} else {
				blocks = append(blocks, map[string]any{"type": "text", "text": step.content})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "msg_pdf1968", "type": "message", "role": "assistant", "model": "pdf1968",
				"stop_reason": stop, "content": blocks,
				"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
			})
		default:
			msg := map[string]any{"role": "assistant"}
			stop := "stop"
			if len(step.toolCalls) > 0 {
				msg["tool_calls"] = step.toolCalls
				msg["content"] = ""
				stop = "tool_calls"
			} else {
				msg["content"] = step.content
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "chatcmpl-pdf1968", "object": "chat.completion", "created": 1, "model": "pdf1968",
				"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": stop}},
				"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			})
		}
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *pdf1968Peer) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// Paths returns the request path of each call, in call order.
func (p *pdf1968Peer) Paths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.paths...)
}

// Bodies returns a copy of every raw request body the peer has seen, in
// call order.
func (p *pdf1968Peer) Bodies() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.raws...)
}

// ---------------------------------------------------------------------------
// Harness: process-catalogue install + provider rows + loop + media store.
// ---------------------------------------------------------------------------

// pdf1968Pair is a routing pair (catalog provider id, catalog model id).
type pdf1968Pair struct {
	provider string
	model    string
}

const pdf1968APIKeyEnv = "PDF1968_TEST_KEY"

// pdf1968NewPeerHarnessAt is pdf1968NewPeerHarness with a caller-chosen
// home directory (T14: the canonical store is reloaded from a known path).
func pdf1968NewPeerHarnessAt(t *testing.T, peer *pdf1968Peer, procCat, loopDoc *catalog.Catalog,
	home string, tune func(*config.Config), pairs ...pdf1968Pair,
) (al *AgentLoop, agents map[string]*AgentInstance, store *session.UnifiedStore, mediaStore media.MediaStore) {
	t.Helper()
	require.NotEmpty(t, pairs, "at least one routing pair required")

	if procCat != nil {
		old := providers.ProviderCatalog()
		providers.SetCatalog(procCat)
		t.Cleanup(func() { providers.SetCatalog(old) })
		// The context-window gate (ADR-066) resolves through a second
		// process-level catalogue (SetWindowCatalog); without it the loopback
		// peer URL in cfg.Providers classifies as a local endpoint and the
		// turn is refused with context_window_unknown.
		prevWin := windowCatalog()
		SetWindowCatalog(procCat)
		t.Cleanup(func() { SetWindowCatalog(prevWin) })
	}
	t.Setenv(pdf1968APIKeyEnv, "test-placeholder")

	workspaceDir := filepath.Join(home, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	cfg := &config.Config{
		PDFLimits: pdf1968LimitTable(t, nil, nil),
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Provider: pairs[0].provider, Model: pairs[0].model},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: make([]config.AgentConfig, 0, len(pairs)),
		},
		Providers: make([]*config.ModelConfig, 0, len(pairs)),
	}
	for i, p := range pairs {
		id := "pdf1968-agent"
		if i > 0 {
			id = fmt.Sprintf("pdf1968-agent-%d", i+1)
		}
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
			ID:    id,
			Name:  id,
			Type:  config.AgentTypeCustom,
			Model: &config.AgentModelConfig{Primary: p.model, Provider: p.provider},
		})
		row := &config.ModelConfig{
			Provider:  p.provider,
			Model:     p.model,
			APIBase:   peer.server.URL,
			APIKeyRef: pdf1968APIKeyEnv,
		}
		cfg.Providers = append(cfg.Providers, row)
	}
	if tune != nil {
		tune(cfg)
	}

	// The loop-injected default provider: the first pair's own client (the
	// agents route through their keyed pool rows; the injected default is
	// only the loop-level fallback and must be non-nil).
	p0, _, err := providers.CreateProviderFromConfig(cfg.Providers[0])
	require.NoError(t, err, "SETUP: the first pair's provider client must build")

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al = mustNewAgentLoop(t, cfg, msgBus, p0)
	t.Cleanup(al.Close)

	agents = make(map[string]*AgentInstance, len(pairs))
	for i := range pairs {
		id := "pdf1968-agent"
		if i > 0 {
			id = fmt.Sprintf("pdf1968-agent-%d", i+1)
		}
		inst, ok := al.GetRegistry().GetAgent(id)
		require.True(t, ok, "SETUP: agent %s must be registered", id)
		agents[id] = inst
	}

	if len(peer.steps) > 0 && peer.steps[0].shape == "responses" {
		// F9/F10: custom-row factory restrictions do not require mocking the
		// adapter. Install the REAL Azure Responses transport via the existing
		// pool setter; only its network peer is a test double.
		realResponses, err := azure.NewProvider("test-key", peer.server.URL, "")
		require.NoError(t, err)
		for i, pair := range pairs {
			id := "pdf1968-agent"
			if i > 0 {
				id = fmt.Sprintf("pdf1968-agent-%d", i+1)
			}
			agents[id].StoreProviderPool(map[string]providers.LLMProvider{pair.provider: realResponses})
		}
	}
	store = al.GetSessionStore()
	require.NotNil(t, store, "SETUP: shared session store must be non-nil")

	mediaStore = mediatest.NewFileMediaStore(t)
	al.SetMediaStore(mediaStore)
	if loopDoc != nil {
		al.SetCapabilityCatalog(loopDoc)
	}
	return al, agents, store, mediaStore
}

// pdf1968NewPeerHarness wires a minimal AgentLoop with one agent per pair,
// each routed through cfg.Providers rows at the recording peer. procCat,
// when non-nil, is installed as the PROCESS catalogue (so the pair resolves
// and the provider client builds — buildProviderPool consults it at agent
// construction). loopDoc, when non-nil, is set as the loop's catalogue
// document (SetCapabilityCatalog); T3 passes nil to keep it absent, the
// FR-11 refusal premise. Optional tune hook adjusts operator settings
// (e.g. Agents.Defaults.MaxMediaSize).
func pdf1968NewPeerHarness(t *testing.T, peer *pdf1968Peer, procCat, loopDoc *catalog.Catalog,
	tune func(*config.Config), pairs ...pdf1968Pair,
) (al *AgentLoop, agents map[string]*AgentInstance, store *session.UnifiedStore, mediaStore media.MediaStore) {
	t.Helper()
	return pdf1968NewPeerHarnessAt(t, peer, procCat, loopDoc, t.TempDir(), tune, pairs...)
}

// A base-green guard proves that the pool seam still executes the real
// Responses marshaller and decoder, rather than a fake Chat implementation.
func TestPDF1968ResponsesHarness(t *testing.T) {
	peer := newPdf1968Peer(t, pdf1968Step{shape: "responses", content: "responses adapter reached"})
	cat := pdf1968Catalog(t, []pdf1968ProviderRow{pdf1968TextOnlyRow("fx/responses", "fx/text-model", nil)})
	al, agents, store, _ := pdf1968NewPeerHarness(t, peer, cat, cat, nil, pdf1968Pair{provider: "fx/responses", model: "fx/text-model"})
	sid := pdf1968NewSession(t, store, "pdf1968-agent")
	answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
	require.NoError(t, err)
	require.Equal(t, "responses adapter reached", answer)
	require.Equal(t, []string{"/openai/v1/responses"}, peer.Paths())
	var request map[string]any
	require.NoError(t, json.Unmarshal([]byte(peer.Bodies()[0]), &request))
	require.Contains(t, request, "input")
	require.Contains(t, request, "instructions")
	require.NotContains(t, request, "messages")
}

// The same enqueue seam serves the PDF recovery scenarios and its base-green
// delivery guard. No prefix is legal: runTurn and the inbox use plain sid.
func pdf1968QueueSteering(al *AgentLoop, sid, content string, media ...string) (string, error) {
	return al.EnqueueSteeringMessage(sid, "pdf1968-agent", providers.Message{Role: "user", Content: content, Media: media}, "")
}

func TestPDF1968SteeringHarness(t *testing.T) {
	var alRef *AgentLoop
	var sidRef string
	peer := newPdf1968Peer(t, pdf1968Step{content: "first", onRequest: func() {
		id, err := pdf1968QueueSteering(alRef, sidRef, "steering reached the active turn")
		if err != nil || id == "" {
			t.Errorf("enqueue steering: %q %v", id, err)
		}
	}}, pdf1968Step{content: "finished"})
	cat := pdf1968Catalog(t, []pdf1968ProviderRow{pdf1968TextOnlyRow("fx/steer", "fx/plain", nil)})
	al, agents, store, _ := pdf1968NewPeerHarness(t, peer, cat, cat, nil, pdf1968Pair{provider: "fx/steer", model: "fx/plain"})
	sid := pdf1968NewSession(t, store, "pdf1968-agent")
	alRef, sidRef = al, sid
	_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
	require.NoError(t, err)
	bodies := peer.Bodies()
	require.Len(t, bodies, 2, "steering must be delivered inside the same runTurn call")
	require.Contains(t, bodies[1], "steering reached the active turn")
}

// pdf1968NewSession creates a chat session for the agent id.
func pdf1968NewSession(t *testing.T, store *session.UnifiedStore, agentID string) string {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "web", agentID)
	require.NoError(t, err)
	return meta.ID
}

// pdf1968RunTurn runs one agent turn carrying the given media refs.
func pdf1968RunTurn(t *testing.T, al *AgentLoop, inst *AgentInstance, store *session.UnifiedStore,
	sessionID string, refs ...string,
) (string, error) {
	t.Helper()
	return pdf1968RunTurnMsg(t, al, inst, store, sessionID, "summarise the attachment", refs...)
}

// pdf1968RunTurnMsg runs one agent turn with an explicit user message
// (T16's retry and steering arms script different user prompts).
func pdf1968RunTurnMsg(t *testing.T, al *AgentLoop, inst *AgentInstance, store *session.UnifiedStore,
	sessionID, userMsg string, refs ...string,
) (string, error) {
	t.Helper()
	return al.runAgentLoop(context.Background(), inst, processOptions{
		// SessionKey is the plain session id (N6): production keys the model
		// window — and the store's on-disk layout — by the owning session id
		// (shared_session_store.go::sharedSessionStoreFor), so the store
		// paths the T14 non-retention sweep reads must be the ones
		// production actually writes, not a namespaced variant.
		SessionKey:          sessionID,
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         userMsg,
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
		Media:               refs,
	})
}

// pdf1968StorePDF registers PDF bytes in the media store and returns the ref.
func pdf1968StorePDF(t *testing.T, store media.MediaStore, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	ref, err := store.Store(path, media.MediaMeta{Filename: name, ContentType: "application/pdf"}, "test")
	require.NoError(t, err)
	return ref
}

// pdf1968StorePNG registers PNG bytes in the media store and returns the ref.
func pdf1968StorePNG(t *testing.T, store media.MediaStore, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	ref, err := store.Store(path, media.MediaMeta{Filename: name, ContentType: "image/png"}, "test")
	require.NoError(t, err)
	return ref
}

// pdf1968TinyPNG encodes a real 2x2 PNG (the media conductor only attaches
// images it can decode).
func pdf1968TinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// Raw-body helpers: the wire oracle. A native PDF crosses the wire as
// base64 in one of the shapes the four transports actually emit (FR-18).
// ---------------------------------------------------------------------------

// pdf1968BodyPDFs decodes every native PDF carried in a raw request body,
// in wire order, recognising every shape the suite can meet (N1 — a shared
// detector must cover every tested wire format):
//
//   - Chat Completions / Responses: a file part whose file_data value is a
//     "data:application/pdf;base64,<b64>" data URL;
//   - Anthropic (Messages API, incl. inside tool_result): a source object
//     {"type":"base64","media_type":"application/pdf","data":"<b64>"};
//   - Bedrock (Converse, incl. inside toolResult): a document object
//     {"format":"pdf", ..., "source":{"bytes":"<b64>"}}.
//
// Objects are decoded structurally; child traversal retains JSON wire order.
// Only file_data/document sources count, never an incidental data URL in text.
// Parse/base64 errors are errors, not an empty (and falsely green) PDF set.
func pdf1968BodyPDFs(t *testing.T, raw string) [][]byte {
	t.Helper()
	out, err := pdf1968DecodeBodyPDFs(raw)
	require.NoError(t, err, "the native-PDF wire oracle must decode every supported format")
	return out
}

func pdf1968DecodeBodyPDFs(raw string) ([][]byte, error) {
	var root json.RawMessage
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return nil, fmt.Errorf("invalid wire JSON: %w", err)
	}
	var out [][]byte
	emit := func(encoded string) error {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("invalid native PDF base64 %q: %w", encoded, err)
		}
		if len(decoded) == 0 {
			return fmt.Errorf("invalid native PDF base64 %q: decoded payload is empty", encoded)
		}
		out = append(out, decoded)
		return nil
	}
	var walk func(json.RawMessage) error
	walk = func(value json.RawMessage) error {
		value = bytes.TrimSpace(value)
		switch value[0] {
		case '[':
			var children []json.RawMessage
			if err := json.Unmarshal(value, &children); err != nil {
				return err
			}
			for _, child := range children {
				if err := walk(child); err != nil {
					return err
				}
			}
		case '{':
			var object struct {
				Type   string `json:"type"`
				Format string `json:"format"`
				Source struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
					Bytes     string `json:"bytes"`
				} `json:"source"`
			}
			if err := json.Unmarshal(value, &object); err != nil {
				return err
			}
			if object.Type == "document" && object.Source.Type == "base64" && object.Source.MediaType == "application/pdf" {
				return emit(object.Source.Data)
			}
			if object.Format == "pdf" {
				return emit(object.Source.Bytes)
			}
			dec := json.NewDecoder(bytes.NewReader(value))
			if _, err := dec.Token(); err != nil {
				return err
			}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				var child json.RawMessage
				if err := dec.Decode(&child); err != nil {
					return err
				}
				if key == "file_data" {
					var url string
					if err := json.Unmarshal(child, &url); err != nil {
						return err
					}
					if strings.HasPrefix(url, "data:application/pdf;base64,") {
						if err := emit(strings.TrimPrefix(url, "data:application/pdf;base64,")); err != nil {
							return err
						}
					}
				} else if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return out, nil
}

// This guard validates the shared instrument before T11/T12 rely on it (N1).
func TestPDF1968BodyPDFs_AllWireFormats(t *testing.T) {
	a, b := []byte("%PDF-1.4 first"), []byte("%PDF-1.4 second")
	ea, eb := base64.StdEncoding.EncodeToString(a), base64.StdEncoding.EncodeToString(b)
	cases := map[string]string{
		"chat":                         `{"messages":[{"content":[{"type":"file","file":{"filename":"a.pdf","file_data":"data:application/pdf;base64,` + ea + `"}},{"type":"file","file":{"file_data":"data:application/pdf;base64,` + eb + `"}}]}]}`,
		"responses nested output":      `{"input":[{"type":"function_call_output","call_id":"p1","output":[{"type":"input_file","file_data":"data:application/pdf;base64,` + ea + `"},{"type":"input_file","file_data":"data:application/pdf;base64,` + eb + `"}]}]}`,
		"anthropic nested tool_result": `{"messages":[{"content":[{"type":"tool_result","content":[{"type":"document","source":{"data":"` + ea + `","media_type":"application/pdf","type":"base64"}},{"source":{"type":"base64","data":"` + eb + `","media_type":"application/pdf"},"type":"document"}]}]}]}`,
		"bedrock nested toolResult":    `{"messages":[{"content":[{"toolResult":{"content":[{"document":{"source":{"bytes":"` + ea + `"},"format":"pdf"}},{"document":{"format":"pdf","source":{"bytes":"` + eb + `"}}}]}}]}]}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) { require.Equal(t, [][]byte{a, b}, pdf1968BodyPDFs(t, raw)) })
	}
	t.Run("ignore images and text", func(t *testing.T) {
		raw := `{"text":"data:application/pdf;base64,` + ea + `","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + ea + `"}},{"image":{"format":"png","source":{"bytes":"` + eb + `"}}}]}`
		require.Empty(t, pdf1968BodyPDFs(t, raw))
	})
	for _, raw := range []string{`{`, `{} {}`, `{"file_data":"data:application/pdf;base64,%%%"}`, `{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"%%%"}}`, `{"document":{"format":"pdf","source":{"bytes":"%%%"}}}`} {
		t.Run("malformed "+raw, func(t *testing.T) {
			_, err := pdf1968DecodeBodyPDFs(raw)
			require.Error(t, err, "malformed wire data must not look like zero native PDFs")
		})
	}
}

// pdf1968HasPDF reports whether the raw body carries any native PDF, by the
// same detector pdf1968BodyPDFs uses (never by a substring probe, which
// only knows the data-URL shape).
func pdf1968HasPDF(t *testing.T, raw string) bool {
	t.Helper()
	return len(pdf1968BodyPDFs(t, raw)) > 0
}

// ---------------------------------------------------------------------------
// Event capture: error events fired during a turn.
// ---------------------------------------------------------------------------

type pdf1968Events struct {
	mu   sync.Mutex
	errs []ErrorPayload
}

// pdf1968Capture installs the event tap on al and returns the capture.
func pdf1968Capture(al *AgentLoop) *pdf1968Events {
	capture := &pdf1968Events{}
	al.SetEventSyncTap(func(e Event) {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		if p, ok := e.Payload.(ErrorPayload); ok {
			capture.errs = append(capture.errs, p)
		} else if p, ok := e.Payload.(*ErrorPayload); ok {
			capture.errs = append(capture.errs, *p)
		}
	})
	return capture
}

// modelListErrors returns every captured error whose Code is
// model_list_unavailable.
func (c *pdf1968Events) modelListErrors() []ErrorPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []ErrorPayload
	for _, e := range c.errs {
		if e.Code == "model_list_unavailable" {
			out = append(out, e)
		}
	}
	return out
}

func (c *pdf1968Events) all() []ErrorPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ErrorPayload(nil), c.errs...)
}

// ---------------------------------------------------------------------------
// Fixture provider/model rows.
// ---------------------------------------------------------------------------

// pdf1968CapableRow is the fixture provider row whose model is flagged as
// reading PDFs (pdf input modality, no inline_pdf:false).
func pdf1968CapableRow(providerID, modelID string, limits map[string]any) pdf1968ProviderRow {
	return pdf1968ProviderRow{
		id:     providerID,
		limits: limits,
		models: []pdf1968ModelRow{{id: modelID, modalities: []string{"text", "pdf"}}},
	}
}

// pdf1968BedrockRow is the loop-level Bedrock recipe's fixture row
// (effort_red_bedrock_loop_test.go precedent): protocol bedrock, one region.
func pdf1968BedrockRow(providerID, modelID string, limits map[string]any) pdf1968ProviderRow {
	return pdf1968ProviderRow{
		id:       providerID,
		limits:   limits,
		protocol: "bedrock",
		region:   "us-east-1",
		regions:  []map[string]any{{"id": "us-east-1", "group": "us"}},
		models:   []pdf1968ModelRow{{id: modelID, modalities: []string{"text", "pdf"}}},
	}
}

// pdf1968TextOnlyRow is the fixture provider row whose model is NOT flagged
// (text-only modality).
func pdf1968TextOnlyRow(providerID, modelID string, limits map[string]any) pdf1968ProviderRow {
	return pdf1968ProviderRow{
		id:     providerID,
		limits: limits,
		models: []pdf1968ModelRow{{id: modelID, modalities: []string{"text"}}},
	}
}

// pdf1968VisionRow is the fixture provider row for an image-capable model
// (the modality the image presentation gate reads today).
func pdf1968VisionRow(providerID, modelID string) pdf1968ProviderRow {
	return pdf1968ProviderRow{
		models: []pdf1968ModelRow{{id: modelID, modalities: []string{"text", "image"}}},
		id:     providerID,
	}
}

// ---------------------------------------------------------------------------
// Fixed notice content (§5.6) — the distinctive substrings the assertions
// pin. Wording of the surrounding notice is the implementer's; these
// sentences are fixed by the spec.
// ---------------------------------------------------------------------------

const (
	pdf1968L2Sentence = "PDF sent as text"
	pdf1968P1Sentence = "native PDF rejected by the provider"
	pdf1968N1Sentence = "no extractable text (likely scanned)"
	pdf1968N2Sentence = "does not accept PDF input"
	// pdf1968M1Marker is the distinctive fragment of the §5.6 M1 tool notice
	// ("[document: <name> | native PDF attached | ... | not retained]").
	pdf1968M1Marker = "native PDF attached"
)

// Validate one complete L2 notice, not unrelated JSON model/config fields.
// Human-readable limit names are legal; MB and MiB are different quantities.
func pdf1968AssertL2(t *testing.T, label, body, limitName string, who []string, value int) {
	t.Helper()
	assert.NoError(t, pdf1968L2Error(body, limitName, who, value), "%s: complete L2 notice", label)
}

func pdf1968L2Error(body, limitName string, who []string, value int) error {
	var root any
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return fmt.Errorf("invalid request JSON: %w", err)
	}
	aliases := map[string][]string{
		"max_file_bytes":            {"max_file_bytes", "file bytes", "bytes per file", "file size", "per-file size"},
		"max_total_file_bytes":      {"max_total_file_bytes", "total file bytes", "total bytes", "total file size"},
		"max_request_bytes":         {"max_request_bytes", "request bytes", "request size", "serialized body"},
		"max_pages_per_file":        {"max_pages_per_file", "pages per file", "pages/file", "per-file page"},
		"max_pages_per_request":     {"max_pages_per_request", "pages per request", "pages/request", "request page"},
		"max_documents_per_message": {"max_documents_per_message", "documents per message", "documents/message", "per-message document"},
		"max_documents_per_request": {"max_documents_per_request", "documents per request", "documents/request", "per-request document"},
	}
	var notices []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			notices = append(notices, regexp.MustCompile(`\[PDF sent as text:[^\]]*\]`).FindAllString(x, -1)...)
		case []any:
			for _, child := range x {
				walk(child)
			}
		case map[string]any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(root)
	for _, notice := range notices {
		owner, named := false, false
		for _, id := range who {
			owner = owner || strings.Contains(notice, id)
		}
		for _, alias := range aliases[limitName] {
			named = named || strings.Contains(strings.ToLower(notice), alias)
		}
		if owner && named && pdf1968NoticeValue(notice, value, strings.HasSuffix(limitName, "bytes")) {
			return nil
		}
	}
	return fmt.Errorf("no single L2 notice names owner %v, %s and value %d; notices: %q", who, limitName, value, notices)
}

func pdf1968NoticeValue(notice string, want int, byteLimit bool) bool {
	// Capture the unit with the number; do not match 20 from "20 MiB" as
	// if it were the unitless value 20 or the decimal quantity 20 MB.
	matches := regexp.MustCompile(`\b([0-9]+(?:[.,_ ][0-9]+)*)(?:\s*(bytes?|[kKmMgG](?:i)?[bB]|B))?\b`).FindAllStringSubmatch(notice, -1)
	for _, match := range matches {
		number, unit := match[1], strings.ToLower(match[2])
		if unit == "" || unit == "b" || strings.HasPrefix(unit, "byte") {
			number = strings.NewReplacer(",", "", ".", "", " ", "", "_", "").Replace(number)
			if number == strconv.Itoa(want) {
				return true
			}
			continue
		}
		if !byteLimit {
			continue
		}
		multiplier := map[string]int64{"kb": 1000, "mb": 1000000, "gb": 1000000000, "kib": 1024, "mib": 1048576, "gib": 1073741824}[unit]
		quantity, ok := new(big.Rat).SetString(number)
		if !ok || multiplier == 0 {
			continue
		}
		quantity.Mul(quantity, big.NewRat(multiplier, 1))
		if quantity.Cmp(big.NewRat(int64(want), 1)) == 0 {
			return true
		}
	}
	return false
}

func pdf1968AssertC1(t *testing.T, notice, model string) {
	t.Helper()
	assert.NoError(t, pdf1968C1Error(notice, model), "C1 must explain the model and the recovery steps")
}

func pdf1968AssertStoredC1(t *testing.T, store *session.UnifiedStore, sid, model string) {
	t.Helper()
	fresh, err := session.NewUnifiedStoreWithHome(store.BaseDir(), filepath.Dir(store.BaseDir()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fresh.Close()) })
	for label, source := range map[string]*session.UnifiedStore{"live": store, "reopened": fresh} {
		entries, err := source.ReadTranscript(sid)
		require.NoError(t, err)
		require.NotEmpty(t, entries, "%s transcript must be nonempty", label)
		found := 0
		for _, e := range entries {
			if e.ErrorCode == "model_list_unavailable" {
				found++
				assert.Equal(t, "error", e.Status)
				pdf1968AssertC1(t, e.Content, model)
			}
		}
		require.Equal(t, 1, found, "%s: exactly one persisted catalogue refusal", label)
	}
}

func pdf1968C1Error(notice, model string) error {
	lower := strings.ToLower(notice)
	for _, part := range []string{"pdf not sent", "model list", "not loaded", "cannot check", model, "pdf", "restart", "omnipus", "persists", "catalog", "error", "log"} {
		if !strings.Contains(lower, strings.ToLower(part)) {
			return fmt.Errorf("C1 lacks %q: %s", part, notice)
		}
	}
	return nil
}

func TestPDF1968NoticeOracles(t *testing.T) {
	owner := []string{"fx/gateway", "fx/model"}
	body := func(s string) string {
		b, err := json.Marshal(map[string]any{"model": "fx/model", "max_file_bytes": 20000000, "messages": []map[string]string{{"content": s}}})
		require.NoError(t, err)
		return string(b)
	}
	for _, notice := range []string{"[PDF sent as text: over fx/model's file bytes of 20 MB]", "[PDF sent as text: over fx/gateway's max_file_bytes of 20,000,000]"} {
		require.NoError(t, pdf1968L2Error(body(notice), "max_file_bytes", owner, 20000000))
	}
	for _, notice := range []string{"[PDF sent as text: over file bytes of 20000000]", "[PDF sent as text: over fx/model's file bytes]", "[PDF sent as text: over fx/model's limit of 20000000]", "[PDF sent as text: over fx/model's file bytes of 20 MiB]", "[PDF sent as text: over fx/model's file bytes of 200000001]"} {
		require.Error(t, pdf1968L2Error(body(notice), "max_file_bytes", owner, 20000000), notice)
	}
	require.NoError(t, pdf1968L2Error(body("[PDF sent as text: over fx/model's file bytes of 20 MiB]"), "max_file_bytes", owner, 20*1048576))
	// §5.6 fixes C1's content, not its wording. The catalogue supplies test
	// INPUT here; pdf1968C1Error's required concepts remain spec-derived and
	// independent. This is not an expected-value call to the unit under test.
	c1 := strings.Replace(UserMessageForCode(LLMErrorCode("model_list_unavailable")), "this model", "fx/model", 1)
	require.NoError(t, pdf1968C1Error(c1, "fx/model"))
	for _, part := range []string{"fx/model", "Restart Omnipus", "catalog", "log"} {
		require.Error(t, pdf1968C1Error(strings.ReplaceAll(c1, part, ""), "fx/model"), "missing %s", part)
	}
}

// Inspect decoded string leaves so JSON escaping cannot hide raw bytes.
func pdf1968PrivatePDFError(records any, payloads [][]byte) error {
	blob, err := json.Marshal(records)
	if err != nil {
		return err
	}
	var decoded any
	if err := json.Unmarshal(blob, &decoded); err != nil {
		return err
	}
	var walk func(any) error
	walk = func(v any) error {
		switch x := v.(type) {
		case string:
			for i, data := range payloads {
				if strings.Contains(x, string(data)) || strings.Contains(x, base64.StdEncoding.EncodeToString(data)) {
					return fmt.Errorf("native PDF %d leaked into canonical records", i)
				}
			}
		case []any:
			for _, child := range x {
				if err := walk(child); err != nil {
					return err
				}
			}
		case map[string]any:
			for _, child := range x {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(decoded)
}

// N10: exact ordered payloads, so dropping the chat candidate cannot pass.
func pdf1968PayloadsError(got, want [][]byte) error {
	if len(got) != len(want) {
		return fmt.Errorf("got %d payloads, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			return fmt.Errorf("payload %d differs", i)
		}
	}
	return nil
}

// N5: each item is checked separately across all admitting attempts.
func pdf1968ReauthorizationError(counts []int32, attempts int32) error {
	for i, count := range counts {
		if count != attempts {
			return fmt.Errorf("item %d authorized %d times, want %d", i, count, attempts)
		}
	}
	return nil
}

func TestPDF1968AttemptOracles(t *testing.T) {
	chat, tool := []byte("%PDF-chat"), []byte("%PDF-tool")
	want := [][]byte{chat, tool}
	require.NoError(t, pdf1968PayloadsError(want, want))
	for _, wrong := range [][][]byte{{chat}, {tool}, {tool, chat}} {
		require.Error(t, pdf1968PayloadsError(wrong, want), "no candidate may disappear or change order")
	}
	require.NoError(t, pdf1968ReauthorizationError([]int32{2, 2, 2}, 2))
	for _, wrong := range [][]int32{{1, 1, 1}, {2, 2, 1}, {6, 0, 0}} {
		require.Error(t, pdf1968ReauthorizationError(wrong, 2), "cached or aggregate-only authorization must fail")
	}
}

// N12's identity oracle must reject newest-first demotion, not merely count
// one surviving PDF. Used by both the real request-size case and this probe.
func pdf1968NewestPDFError(got [][]byte, newest []byte) error {
	if len(got) != 1 {
		return fmt.Errorf("got %d survivors, want exactly one", len(got))
	}
	if !bytes.Equal(got[0], newest) {
		return fmt.Errorf("survivor is not the newest candidate")
	}
	return nil
}

func TestPDF1968NewestSurvivorOracle(t *testing.T) {
	oldest, newest := []byte("%PDF-oldest"), []byte("%PDF-newest")
	require.NoError(t, pdf1968NewestPDFError([][]byte{newest}, newest))
	for _, wrong := range [][][]byte{nil, {oldest}, {oldest, newest}} {
		require.Error(t, pdf1968NewestPDFError(wrong, newest), "wrong survivor count or identity must fail")
	}
}

func TestPDF1968PrivacyOracle(t *testing.T) {
	data := []byte("%PDF-1.4\nraw \"quoted\" bytes")
	require.NoError(t, pdf1968PrivatePDFError([]providers.Message{{Role: "tool", Content: "persisted text only"}}, [][]byte{data}))
	for _, leak := range []string{string(data), base64.StdEncoding.EncodeToString(data)} {
		require.Error(t, pdf1968PrivatePDFError([]providers.Message{{Role: "tool", Content: leak}}, [][]byte{data}), "content leak must fail even when JSON-escaped")
		require.Error(t, pdf1968PrivatePDFError([]providers.Message{{Role: "tool", Media: []string{leak}}}, [][]byte{data}), "media leak must fail too")
	}
}
