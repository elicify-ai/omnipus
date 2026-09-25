package specapi_test

import (
	"io"
	"mime"
	"net/mail"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/email"
	"golang.org/x/net/html"
)

// These tests call the compose/signature API the spec requires and the tree
// does not have yet (email-mail-view-spec.md §15, MC-2, MC-3, FR-003, FR-022,
// D29). A compile error naming the missing symbol is the expected RED.

func TestSignatureSanitizedOnSave(t *testing.T) {
	const raw = `<script>alert(1)</script>` +
		`<img src=x onerror=alert(1)>` +
		`<a href="javascript:alert(1)">x</a>` +
		`<p style="color:navy">Best regards</p>` +
		`<table><tr><td>Ada</td></tr></table>` +
		`<img src="https://logo.example/a.png" alt="logo">` +
		`<img src="data:image/png;base64,AAAA" alt="dot">` +
		`<form action="https://evil.example"></form>` +
		`<iframe src="https://evil.example"></iframe>`
	got, err := email.SanitizeSignatureHTML(raw)
	if err != nil {
		t.Fatalf("FR-003: sanitizing a hostile signature: %v", err)
	}
	assertSignatureKept(t, got)
}

func TestSignatureSanitizedOnSave_TooLong(t *testing.T) {
	// MC-1: 16,384 chars is the maximum. 16,385 is rejected.
	_, err := email.SanitizeSignatureHTML(strings.Repeat("a", 16385))
	if err == nil {
		t.Fatal("MC-1: 16385-character signature was stored, want an error")
	}
	if !strings.Contains(err.Error(), "16384") {
		t.Fatalf("MC-1: error %q does not name the 16384 limit", err)
	}
}

func TestComposeMultipart_SignatureAndSanitize(t *testing.T) {
	// B-2 / B-6 / MC-2 / MC-3: Markdown body plus signature, hostile content
	// gone from both parts, signature present on both.
	const sig = `<p style="color:navy">Best regards</p><table><tr><td>Ada</td></tr></table>` +
		`<img src="https://logo.example/a.png" alt="logo">`
	out, err := email.Compose(email.ComposeInput{
		From:          "ada@box.test",
		To:            []string{"a@x.test"},
		Subject:       "Hello",
		Markdown:      "# Title\n\nSee [docs](https://docs.example/a).\n\n<script>alert(1)</script>\n",
		SignatureHTML: sig,
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	plain, htmlPart := partsOf(t, string(out.Transmitted))
	if strings.Contains(strings.ToLower(htmlPart), "<script") || strings.Contains(strings.ToLower(plain), "<script") {
		t.Fatalf("MC-2: script survived\nplain:\n%s\nhtml:\n%s", plain, htmlPart)
	}
	if !strings.Contains(plain, "docs (https://docs.example/a)") {
		t.Fatalf("MC-30: plain part missing the link form:\n%s", plain)
	}
	if !strings.Contains(plain, "Best regards") || !strings.Contains(htmlPart, "Best regards") {
		t.Fatalf("B-2: signature text missing from one part\nplain:\n%s\nhtml:\n%s", plain, htmlPart)
	}
	if !strings.Contains(plain, "\n--\n") && !strings.Contains(plain, "\r\n--\r\n") {
		t.Fatalf("MC-3: plain part has no -- signature separator:\n%s", plain)
	}
	if strings.Contains(plain, "<table") || strings.Contains(plain, "<p ") {
		t.Fatalf("MC-3: plain part contains signature HTML tags:\n%s", plain)
	}
	if !strings.Contains(htmlPart, "https://logo.example/a.png") {
		t.Fatalf("B-2: HTML signature lost the https logo:\n%s", htmlPart)
	}
}

func TestCompose_ThreeCopyBCC(t *testing.T) {
	// D29 / B-9: transmitted copy has no Bcc header; the Sent copy keeps it;
	// d@x.test is an envelope recipient.
	out, err := email.Compose(email.ComposeInput{
		From:     "ada@box.test",
		To:       []string{"a@x.test"},
		Cc:       []string{"b@x.test", "c@x.test"},
		Bcc:      []string{"d@x.test"},
		Subject:  "Hi",
		Markdown: "Hello",
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	transmitted := string(out.Transmitted)
	sent := string(out.SentCopy)
	if strings.Contains(transmitted, "Bcc:") || strings.Contains(transmitted, "d@x.test") {
		t.Fatalf("D29: transmitted copy leaks Bcc:\n%s", transmitted)
	}
	if !strings.Contains(sent, "d@x.test") {
		t.Fatalf("D29: Sent copy dropped Bcc d@x.test:\n%s", sent)
	}
	found := false
	for _, rcpt := range out.EnvelopeRecipients {
		if rcpt == "d@x.test" {
			found = true
		}
	}
	if !found {
		t.Fatalf("D29: envelope recipients %v, want d@x.test", out.EnvelopeRecipients)
	}
}

func TestEnvelope_ReadByAgentDerived(t *testing.T) {
	// MC-36 / B-53 / B-55: the tag is derived from the flag list, case-insensitively.
	cases := []struct {
		flags []string
		want  bool
	}{
		{[]string{`\Seen`, "$OmnipusAgentRead"}, true},
		{[]string{"$omnipusagentread"}, true},
		{[]string{`\Seen`}, false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := email.ReadByAgentFromFlags(tc.flags); got != tc.want {
			t.Errorf("ReadByAgentFromFlags(%v) = %v, want %v", tc.flags, got, tc.want)
		}
	}
}

func assertSignatureKept(t *testing.T, raw string) {
	t.Helper()
	lower := strings.ToLower(raw)
	for _, banned := range []string{"<script", "onerror", "javascript:", "<form", "<iframe"} {
		if strings.Contains(lower, banned) {
			t.Errorf("FR-003: stored signature still contains %s:\n%s", banned, raw)
		}
	}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse sanitized signature: %v", err)
	}
	var sawStyle, sawTable, sawHTTPS, sawData bool
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "table":
				sawTable = true
			case "img":
				for _, a := range n.Attr {
					if a.Key == "src" && strings.HasPrefix(a.Val, "https://logo.example/") {
						sawHTTPS = true
					}
					if a.Key == "src" && strings.HasPrefix(a.Val, "data:image/") {
						sawData = true
					}
				}
			}
			for _, a := range n.Attr {
				if a.Key == "style" && strings.Contains(a.Val, "color:navy") {
					sawStyle = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if !sawStyle || !sawTable || !sawHTTPS || !sawData {
		t.Fatalf("FR-003: kept style=%v table=%v https-img=%v data-img=%v\n%s", sawStyle, sawTable, sawHTTPS, sawData, raw)
	}
}

func partsOf(t *testing.T, raw string) (plain, htmlPart string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("MC-3: transmitted message is not RFC 5322: %v\n%s", err, raw)
	}
	media, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || media != "multipart/alternative" {
		t.Fatalf("MC-3: Content-Type = %q (%v), want multipart/alternative", msg.Header.Get("Content-Type"), err)
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var sawPlain, sawHTML int
	for _, part := range strings.Split(string(body), "--"+params["boundary"]) {
		part = strings.TrimSpace(part)
		if part == "" || part == "--" {
			continue
		}
		header, content, ok := strings.Cut(part, "\r\n\r\n")
		if !ok {
			header, content, ok = strings.Cut(part, "\n\n")
		}
		if !ok {
			continue
		}
		lower := strings.ToLower(header)
		switch {
		case strings.Contains(lower, "text/plain"):
			sawPlain++
			plain = content
		case strings.Contains(lower, "text/html"):
			sawHTML++
			htmlPart = content
		}
	}
	if sawPlain != 1 || sawHTML != 1 {
		t.Fatalf("MC-3: plain parts=%d html parts=%d", sawPlain, sawHTML)
	}
	return plain, htmlPart
}
