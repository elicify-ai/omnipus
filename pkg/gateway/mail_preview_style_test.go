package gateway

// RED pack (w4 spec §9.1 row 8; US-4; artefact §7 P1–P9 at the assembled
// pipeline): mailSanitizePreviewHTML + the rewrite pipeline. The unit-level
// P1–P9 obligations live in pkg/email/mailhtml/sanitize_test.go; this file
// proves the ASSEMBLED pipeline a real mail rides — and it is the
// integration-level reproduction of finding F1 (the <style>-block pass is
// dead end-to-end: bluemonday drops the placeholder element, so every
// sanitized block is discarded and styled mail renders unstyled).

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/email"
)

func normalizeHTML(t *testing.T, s string) string {
	t.Helper()
	for _, c := range []string{" ", "\t", "\n", "\r"} {
		s = strings.ReplaceAll(s, c, "")
	}
	return s
}

// P9 at the pipeline level — the founder's probe mail, all six categories.
// RED against the landed code (finding F1): the style block never survives.
func TestMailSanitizePreviewHTMLFounderProbe(t *testing.T) {
	in := `<div style="color: red">a</div>` +
		`<p style="font-size: 14px">b</p>` +
		`<span style="font-family: Arial">c</span>` +
		`<table><tr><td style="background-color: #efefef" bgcolor="#efefef" class="cell">d</td></tr></table>` +
		`<font color="#333333" face="Arial" size="3">e</font>` +
		`<style>p{color: blue}</style>`
	out := normalizeHTML(t, mailSanitizePreviewHTML(in, nil, nil))
	for _, want := range []string{
		"color:red", "font-size:14px", "background-color:#efefef",
		`bgcolor="#efefef"`, `class="cell"`, "<font",
		// The style BLOCK half — artefact P9: the probe's style block survives.
		"<style>", "color:blue",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("probe category lost (%q): %q (artefact P9 at the pipeline level; a missing <style>/color:blue is finding F1)", want, out)
		}
	}
}

// P7 at the pipeline level: a safe @media rule renders.
func TestMailSanitizePreviewHTMLMediaQueryBlock(t *testing.T) {
	in := `<style>@media (max-width:600px){h1{font-size:20px;color:blue;position:fixed}}</style><h1>hi</h1>`
	out := normalizeHTML(t, mailSanitizePreviewHTML(in, nil, nil))
	if !strings.Contains(out, "<style>") || !strings.Contains(out, "max-width:600px") || !strings.Contains(out, "color:blue") {
		t.Fatalf("safe @media block lost at the pipeline level: %q (US-4.AC-1; finding F1)", out)
	}
	if strings.Contains(out, "position") {
		t.Fatalf("denied declaration survived inside the block: %q", out)
	}
}

// P2/P6 at the pipeline level: @import and @font-face never reach the served artifact.
func TestMailSanitizePreviewHTMLDangerousAtRulesStripped(t *testing.T) {
	in := `<style>@import url("https://evil/x.css"); @font-face{font-family:E;src:url(data:font/woff2;base64,AAAA)} p{color:red}</style><p>x</p>`
	out := normalizeHTML(t, mailSanitizePreviewHTML(in, nil, nil))
	for _, banned := range []string{"@import", "@font-face", "evil/x.css", "data:font"} {
		if strings.Contains(out, banned) {
			t.Fatalf("%s survived the pipeline (artefact P2/P6; the data: variant is the sanitizer-only gate, US-4.AC-3): %q", banned, out)
		}
	}
}

// P1/P4 spot checks at the pipeline level: the URL policy and the escape
// decode order hold in assembled output.
func TestMailSanitizePreviewHTMLURLPolicyAndEscapes(t *testing.T) {
	in := `<div style="background-image:url('http://evil/x')">a</div>` +
		`<div style="col\6fr:red">b</div>`
	out := normalizeHTML(t, mailSanitizePreviewHTML(in, nil, nil))
	if strings.Contains(out, "background-image") || strings.Contains(out, "evil/x") {
		t.Fatalf("non-conforming url() survived the pipeline: %q (artefact P1)", out)
	}
	if !strings.Contains(out, "color:red") {
		t.Fatalf("decoded-safe property dropped by the pipeline: %q (artefact P4: col\\6fr:red survives as color)", out)
	}
}

// CSS url() values are pinned and rewritten onto the token-scoped path —
// never the raw https string (US-4.AC-2; artefact §5).
func TestMailSanitizePreviewHTMLRewritesCSSURLs(t *testing.T) {
	in := `<div style="background-image:url('https://good/img.png')">x</div>`
	remote := []string{"https://good/img.png"}
	out := normalizeHTML(t, mailSanitizePreviewHTML(in, nil, remote))
	if !strings.Contains(out, "/mail-preview/img/") {
		t.Fatalf("CSS url() not rewritten onto the token-scoped proxy path: %q (US-4.AC-2 — without the rewrite an allowed background silently never renders)", out)
	}
	if strings.Contains(out, "https://good/img.png") {
		t.Fatalf("the raw https URL survived the rewrite: %q", out)
	}
}

// The CSP is unchanged by the styling work (artefact §4: no CSP change is
// needed or requested; style-src 'unsafe-inline' is exactly right).
func TestMailIsolationPolicyUnchangedByStyling(t *testing.T) {
	csp := mailIsolationPolicy("https://gateway.example")
	for _, want := range []string{
		"script-src 'none'",
		"style-src 'unsafe-inline'",
		"default-src 'none'",
		"connect-src 'none'",
		"sandbox allow-popups allow-popups-to-escape-sandbox",
	} {
		if !strings.Contains(csp, want) {
			t.Fatalf("Mail CSP lost %q — the styling work must not change the container (artefact §4): %q", want, csp)
		}
	}
	if !strings.Contains(csp, "img-src") || !strings.Contains(csp, "data:") {
		t.Fatalf("img-src data: branch missing from the CSP: %q (the sanitizer-only gates rely on its exact shape)", csp)
	}
}

// The pipeline composes the real inlines list without choking on cid: parts
// (structure smoke over the assembled signature, US-4 regression surface).
func TestMailSanitizePreviewHTMLComposesWithInlineParts(t *testing.T) {
	in := `<p>body</p><img src="cid:img1@inline.test">` +
		`<style>p{color:red}</style>`
	out := mailSanitizePreviewHTML(in, []email.MailPart{}, nil)
	if out == "" {
		t.Fatalf("pipeline produced no output for a cid: mail")
	}
}
