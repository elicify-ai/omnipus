package mailhtml_test

// RED pack (w4 spec §9.1 row 7): the parsed CSS policy, against the CSS
// authority's proof obligations P1–P9 (receipts/css-allowlist.md §7) and the
// spec's US-4. Every expected value cites the artefact/spec — never the
// implementation. Assertions are made on whitespace-normalized output so the
// parser's emission formatting cannot weaken them; the semantic content
// asserted is exact.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/microcosm-cc/bluemonday"

	"github.com/elicify-ai/omnipus/pkg/email/mailhtml"
)

// testPolicy builds the caller-owned baseline the artefact describes: the
// structural element list is the caller's (MC-10); mailhtml adds the
// presentation layer. The img src shape is the artefact §2 img row's value
// shape (token-scoped proxy path, raster data:, https) — the same shape the
// gateway pipeline enforces.
func testPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "div", "span", "table", "td", "tr", "th", "h1", "font")
	// The <style> ELEMENT stays allowed (artefact §6: add style to the
	// element allow-list and route its text through the stylesheet pass) —
	// the placeholder-then-substitute pattern needs the tag to survive
	// bluemonday with its placeholder content.
	p.AllowElements("style")
	p.AllowAttrs("src").Matching(regexp.MustCompile(`^(?:/mail-preview/|data:image/(?:png|gif|jpe?g|webp);base64,|https://)`)).OnElements("img")
	mailhtml.ApplyStylePolicy(p)
	mailhtml.AllowPresentationMarkup(p)
	return p
}

// sanitizeMailHTML composes the pipeline pieces in the documented order
// (decode escapes → strip oversized attrs → lift style blocks → bluemonday →
// sanitize blocks → re-insert).
func sanitizeMailHTML(t *testing.T, html string) string {
	t.Helper()
	decoded := mailhtml.DecodeStyleAttrProperties(html)
	stripped := mailhtml.StripOversizedStyleAttrs(decoded)
	body, blocks := mailhtml.ExtractStyleBlocks(stripped, mailhtml.StyleMaxBlockBytes)
	sanitizedBody := testPolicy().Sanitize(body)
	sanitizedBlocks := make([]string, len(blocks))
	for i, b := range blocks {
		sanitizedBlocks[i] = mailhtml.SanitizeStylesheet(b, mailhtml.StyleMaxBlockBytes)
	}
	return mailhtml.ReplaceStyleBlocks(sanitizedBody, sanitizedBlocks)
}

// normalize removes all whitespace so assertions are exact about semantic
// content and insensitive to emission formatting.
func normalize(t *testing.T, s string) string {
	t.Helper()
	for _, c := range []string{" ", "\t", "\n", "\r"} {
		s = strings.ReplaceAll(s, c, "")
	}
	return s
}

// P1 — non-conforming url() values' declarations dropped, conforming kept.
func TestStyleURLPolicyDropsNonConformingKeepsConforming(t *testing.T) {
	cases := []struct {
		url  string
		keep bool
		note string
	}{
		{`url('http://evil/x')`, false, "plain http"},
		{`url(//evil/x)`, false, "protocol-relative"},
		{`url(/rel)`, false, "relative"},
		{`url(data:image/svg+xml;base64,PHN2Zy8+)`, false, "svg data URI (artefact §3 row 8: sanitizer-only gate)"},
		{`url(data:text/html;base64,PHNjcmlwdD4=)`, false, "html data URI"},
		// security F3 (commit 690e40cbb): pin-then-rewrite runs BEFORE
		// sanitisation, so a remote URL still raw at check time is by
		// definition unpinned (LoadRemote off, or past the pin cap) and its
		// declaration drops — never served verbatim (a served artifact that
		// phones home on re-host). The pre-F3 "https absolute → keep" row
		// pinned the removed admit; the fix commit's NOTE for qa-lead
		// declares the flip.
		{`url('https://good/img.png')`, false, "raw https absolute (security F3: unpinned remote drops)"},
		// The conforming remote shape is the post-rewrite token-scoped
		// path — the same shape mailImageSrcRe admits for <img>.
		{`url('/mail-preview/img/tok/0')`, true, "token-scoped post-rewrite path"},
		{`url(data:image/png;base64,iVBORw0KGgo=)`, true, "raster data URI"},
	}
	for _, tc := range cases {
		in := fmt.Sprintf(`<div style="background-image:%s">x</div>`, tc.url)
		out := normalize(t, sanitizeMailHTML(t, in))
		if tc.keep && !strings.Contains(out, `background-image:url`) {
			t.Fatalf("%s: expected the conforming declaration to survive, got %q", tc.note, out)
		}
		if !tc.keep && strings.Contains(out, `background-image`) {
			t.Fatalf("%s: the non-conforming declaration survived: %q (artefact P1: declaration dropped)", tc.note, out)
		}
	}
}

// P2 — @import gone, the sibling safe rule kept.
func TestStyleBlockImportStrippedRuleKept(t *testing.T) {
	out := mailhtml.SanitizeStylesheet(`@import url("https://evil/x.css"); p{color:red}`, 16384)
	if out == "" {
		t.Fatalf("whole stylesheet refused; artefact P2 requires strip-the-rule keep-the-rest")
	}
	n := normalize(t, out)
	if strings.Contains(n, "@import") {
		t.Fatalf("@import survived: %q (artefact P2)", out)
	}
	if !strings.Contains(n, "p{color:red") && !strings.Contains(n, "color:red") {
		t.Fatalf("safe sibling rule lost: %q (artefact P2)", out)
	}
}

// P3 — position/top/left/z-index dropped, color kept — attribute path.
func TestPositionDeniedInStyleAttribute(t *testing.T) {
	in := `<div style="position:fixed;top:0;left:0;z-index:9999;color:red">x</div>`
	out := normalize(t, sanitizeMailHTML(t, in))
	if strings.Contains(out, "position:fixed") || strings.Contains(out, "top:0") || strings.Contains(out, "left:0") || strings.Contains(out, "z-index:9999") {
		t.Fatalf("denied declarations survived: %q (artefact P3)", out)
	}
	if !strings.Contains(out, "color:red") {
		t.Fatalf("safe sibling color lost: %q (artefact P3: denied dropped WITHOUT dropping unrelated safe content)", out)
	}
}

// P3's block half + P7 — @media retained recursively sanitised.
func TestMediaQueryRetainedQueryIntactDeniedDropped(t *testing.T) {
	out := mailhtml.SanitizeStylesheet(`@media (max-width:600px){h1{font-size:20px;color:blue;position:fixed}}`, 16384)
	if out == "" {
		t.Fatalf("@media block refused wholesale; artefact P7 requires the at-rule retained")
	}
	n := normalize(t, out)
	if !strings.Contains(n, "@media") || !strings.Contains(n, "max-width:600px") {
		t.Fatalf("query not intact: %q (artefact P7)", out)
	}
	if !strings.Contains(n, "font-size:20px") || !strings.Contains(n, "color:blue") {
		t.Fatalf("allowed declarations lost inside @media: %q (artefact P7)", out)
	}
	if strings.Contains(n, "position") {
		t.Fatalf("denied declaration survived inside @media: %q (artefact P7: recursive sanitisation)", out)
	}
}

// P4 — the anti-regex escape set: decode-then-check ordering.
func TestEscapeDecodeOrdering(t *testing.T) {
	// decoded-safe property SURVIVES (a regex would have failed it).
	out := normalize(t, sanitizeMailHTML(t, `<div style="col\6fr:red">x</div>`))
	if !strings.Contains(out, "color:red") {
		t.Fatalf(`col\6fr:red did not survive as color: %q (artefact P4: the parser-decoded truth decides)`, out)
	}
	// decoded-dangerous URL DROPS.
	out = normalize(t, sanitizeMailHTML(t, `<div style="background-image:url('ht\74 tps://evil/p')">x</div>`))
	if strings.Contains(out, "background-image") || strings.Contains(out, "evil") {
		t.Fatalf(`escaped URL scheme survived: %q (artefact P4: decoded URL fails the scheme check)`, out)
	}
	// escaped denied property drops (block path decodes before lookup).
	if got := mailhtml.SanitizeStylesheet(`p{position:fix\65 d}`, 16384); strings.Contains(normalize(t, got), "position") {
		t.Fatalf(`escaped position survived: %q (artefact P4)`, got)
	}
	// comment-spliced denied property drops.
	if got := mailhtml.SanitizeStylesheet(`p{/**/position:fixed}`, 16384); strings.Contains(normalize(t, got), "position") {
		t.Fatalf(`comment-spliced position survived: %q (artefact P4)`, got)
	}
	// and the block path's decoded-safe property survives too. The emitter
	// may spell the property decoded (color) or keep the browser-equivalent
	// escaped form (col\6fr — any conformant browser reads it as color);
	// what P4 requires is that the declaration SURVIVES — the policy judged
	// the decoded truth instead of dropping the escape.
	if got := mailhtml.SanitizeStylesheet(`p{col\6fr:red}`, 16384); got == "" ||
		(!strings.Contains(normalize(t, got), "color:red") && !strings.Contains(got, `col\6fr`)) {
		t.Fatalf(`block-path col\6fr:red did not survive as color: %q (artefact P4)`, got)
	}
}

// P5 — style kept, handler gone, on the same element.
func TestEventHandlerStrippedWhileStyleKept(t *testing.T) {
	in := `<img src="https://good/a.png" style="width:10px" onerror="fetch('//evil')">`
	out := normalize(t, sanitizeMailHTML(t, in))
	if strings.Contains(out, "onerror") || strings.Contains(out, "fetch") {
		t.Fatalf("event handler survived: %q (artefact P5)", out)
	}
	if !strings.Contains(out, "width:10px") {
		t.Fatalf("style value lost when the handler was stripped: %q (artefact P5: style support must not re-open handler injection)", out)
	}
}

// P6 — @font-face gone in BOTH variants; the data: variant is the path the
// CSP would have admitted (sanitizer is the only gate — US-4.AC-3).
func TestFontFaceDroppedInBothVariants(t *testing.T) {
	for _, css := range []string{
		`@font-face{font-family:E;src:url(https://evil/f.woff)}`,
		`@font-face{font-family:E;src:url(data:font/woff2;base64,AAAA)}`,
	} {
		if got := mailhtml.SanitizeStylesheet(css, 16384); got != "" {
			t.Fatalf("at-rule survived (%s...): %q (artefact P6: entire at-rule gone)", css[:20], got)
		}
	}
}

// P8 — bounded work; fail-closed on parse error; oversize dropped entirely.
func TestBoundedWorkUnderHostileCSS(t *testing.T) {
	// An over-cap style attribute is removed entirely (before any parse).
	big := strings.Repeat("a", mailhtml.StyleMaxAttrBytes+1)
	stripped := mailhtml.StripOversizedStyleAttrs(`<div style="color:red;width:` + big + `">x</div>`)
	if strings.Contains(stripped, "style=") {
		t.Fatalf("over-cap style attribute survived StripOversizedStyleAttrs (E-5: 4 KB attr cap)")
	}
	// A block over the 16 KB cap is refused whole.
	if got := mailhtml.SanitizeStylesheet(strings.Repeat("p{color:red}", 1700), mailhtml.StyleMaxBlockBytes); got != "" {
		t.Fatalf("over-cap block not refused whole (E-5: 16 KB block cap): got %d bytes", len(got))
	}
	// 100 nested @media blocks: bounded input, no panic, bounded output.
	deep := strings.Repeat(`@media (max-width:600px){`, 100) + "p{color:red}" + strings.Repeat("}", 100)
	got := mailhtml.SanitizeStylesheet(deep, mailhtml.StyleMaxBlockBytes)
	if len(got) > mailhtml.StyleMaxBlockBytes {
		t.Fatalf("output exceeded the block cap: %d bytes (P8: bounded output)", len(got))
	}
	// A malformed style attribute is dropped ENTIRELY (fail-closed,
	// bluemonday's own behaviour — artefact §6).
	out := normalize(t, sanitizeMailHTML(t, `<div style="background-image:url(https://good/x.png">x</div>`))
	if strings.Contains(out, "background-image") || strings.Contains(out, "good/x.png") {
		t.Fatalf("parse-failed style attribute partially survived: %q (P8: fail-closed, dropped entirely)", out)
	}
}

// P9 — the founder's probe mail: all six categories survive sanitisation.
// This is the acceptance test for the feature's purpose; ordinary colour and
// layout SURVIVE.
func TestFounderProbeMailSurvivesSanitisation(t *testing.T) {
	in := `<div style="color: red">a</div>` +
		`<p style="font-size: 14px">b</p>` +
		`<span style="font-family: Arial, sans-serif">c</span>` +
		`<table><tr><td style="background-color: #efefef" bgcolor="#efefef" class="cell">d</td></tr></table>` +
		`<font color="#333333" face="Arial" size="3">e</font>` +
		`<style>p{color: blue}</style>`
	out := normalize(t, sanitizeMailHTML(t, in))
	for _, want := range []string{
		"color:red", "font-size:14px", "font-family:Arial,sans-serif",
		"background-color:#efefef", `bgcolor="#efefef"`, `class="cell"`,
		"<font", `color="#333333"`, `face="Arial"`, "color:blue",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("probe category lost (%q): %q (artefact P9: all six categories survive)", want, out)
		}
	}
}

// Selector surface: simple type/class/descendant only (artefact §2).
func TestSelectorSurfaceRestricted(t *testing.T) {
	kept := mailhtml.SanitizeStylesheet(`div p{color:red}`, 16384)
	if !strings.Contains(normalize(t, kept), "color:red") {
		t.Fatalf("descendant selector refused: %q (artefact §2: descendant selectors allowed)", kept)
	}
	for _, css := range []string{`*{color:red}`, `a[href="x"]{color:red}`, `div>p{color:red}`, `#hero{color:red}`} {
		if got := mailhtml.SanitizeStylesheet(css, 16384); got != "" {
			t.Fatalf("non-simple selector survived (%q): %q (artefact §2: no *, no attribute selectors, no >-chains, no ids in v1)", css, got)
		}
	}
}

// display is enum-restricted: flex in, grid out (artefact §2.B).
func TestDisplayEnumRestricted(t *testing.T) {
	kept := mailhtml.SanitizeStylesheet(`p{display:flex}`, 16384)
	if !strings.Contains(normalize(t, kept), "display:flex") {
		t.Fatalf("display:flex refused: %q (artefact §2.B: flex allowed)", kept)
	}
	if got := mailhtml.SanitizeStylesheet(`p{display:grid}`, 16384); got != "" {
		t.Fatalf("display:grid survived: %q (artefact §2.B: enum-restricted, no grid)", got)
	}
}

// The pin-then-rewrite extension: eligible https url() bodies are extracted
// first-seen, deduplicated; the rewrite yields the token-scoped path and
// never the raw https string (SPEC US-4.AC-2; artefact §5).
func TestCSSImageExtractionAndRewrite(t *testing.T) {
	raw := `<div style="background-image:url('https://good/1.png')">` +
		`<div style="background: #fff url(https://good/1.png) center/cover">` +
		`<div style="background-image:url('http://bad/3.png')">` +
		`<div style="background-image:url('data:image/svg+xml;base64,PHN2Zy8+')">`
	urls := mailhtml.ExtractCSSImageURLs(raw, 10)
	if len(urls) != 1 || urls[0] != "https://good/1.png" {
		t.Fatalf("extraction = %v, want [https://good/1.png] (first-seen, deduplicated, https-only)", urls)
	}
	rewritten := mailhtml.RewriteCSSImageURLs(raw, urls, "/mail-preview/img/tok/")
	n := normalize(t, rewritten)
	if !strings.Contains(n, `url("/mail-preview/img/tok/0")`) && !strings.Contains(n, `url('/mail-preview/img/tok/0')`) {
		t.Fatalf("rewrite did not produce the token-scoped path: %q (SPEC US-4.AC-2)", rewritten)
	}
	if strings.Contains(rewritten, "https://good/1.png") {
		t.Fatalf("raw https url survived the rewrite: %q (SPEC US-4.AC-2: never the raw URL)", rewritten)
	}
}
