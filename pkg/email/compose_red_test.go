package email

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Oracles: docs/internal/specs/email-mail-view-spec.md MC-2, MC-3, MC-4, MC-21,
// MC-22, MC-30 and scenarios B-3, B-6, B-10. Expected bytes are derived from
// those sections (and CommonMark for an ATX heading), not from a sample run.

const (
	// outboundBodyMax is MC-22 / FR-031: 1 MiB. One byte over is rejected and
	// nothing is transmitted.
	outboundBodyMax = 1 << 20
)

func TestComposeMultipart_NoSignature(t *testing.T) {
	// B-3 / MC-3: an empty signature still produces multipart/alternative, and
	// the signature separator is absent.
	const body = "# Title\n\nSee [docs](https://docs.example/a).\n"
	got := buildEmailBody("ada@box.test", "a@x.test", "Hello", body, "")

	plain, html := mustAlternativeParts(t, got)
	if strings.Contains(plain, "\n--\n") || strings.Contains(plain, "\r\n--\r\n") {
		t.Fatalf("MC-3: empty signature still produced a signature separator in the plain part:\n%s", plain)
	}
	if !strings.Contains(html, "<h1>") || !strings.Contains(html, "Title") {
		t.Fatalf("B-6: HTML part = %q, want a CommonMark h1 containing Title", html)
	}
	if !strings.Contains(plain, "docs (https://docs.example/a)") {
		t.Fatalf("MC-30: plain part = %q, want link rendered as %q", plain, "docs (https://docs.example/a)")
	}
}

func TestComposeHeaders_RFC2047(t *testing.T) {
	// MC-4 / B-10: non-ASCII subject and display name are RFC 2047 encoded and
	// decode back to the original. Raw UTF-8 in the header is a failure.
	const (
		subject = "Grüße"
		from    = "Günter <ada@box.test>"
	)
	got := buildEmailBody(from, "a@x.test", subject, "body", "")
	msg, err := mail.ReadMessage(strings.NewReader(got))
	if err != nil {
		t.Fatalf("MC-4: composed message is not RFC 5322: %v\n%s", err, got)
	}
	dec := new(mime.WordDecoder)
	assertEncodedHeader(t, dec, "Subject", msg.Header.Get("Subject"), subject)
	assertEncodedHeader(t, dec, "From", msg.Header.Get("From"), from)
}

func TestComposeMultipart_HeaderInjectionGuard(t *testing.T) {
	// MC-4: a CRLF in a recipient must not become another header. Subject
	// stripping already exists; the recipient path is the one this asserts.
	got := buildEmailBody("ada@box.test", "a@x.test\r\nBcc: eve@x.test", "Hello", "body", "")
	if strings.Contains(got, "\nBcc:") || strings.Contains(got, "\rBcc:") {
		t.Fatalf("MC-4: recipient CRLF injected a Bcc header:\n%s", got)
	}
	msg, err := mail.ReadMessage(strings.NewReader(got))
	if err != nil {
		t.Fatalf("MC-4: message with a hostile recipient did not parse: %v\n%s", err, got)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Fatalf("MC-4: Bcc header = %q, want empty", msg.Header.Get("Bcc"))
	}
	if msg.Header.Get("To") != "a@x.test" {
		t.Fatalf("MC-4: To = %q, want %q", msg.Header.Get("To"), "a@x.test")
	}
}

func TestPlainTextPart_ASTDerivation_HardWraps(t *testing.T) {
	// MC-30 / DS-2: plain text comes from the same Markdown parse as the HTML.
	// Links are "text (href)", list items are indented, code stays literal, and
	// a single newline survives (hard wraps).
	const body = "See [docs](https://docs.example/a).\n\n- one\n\n`keep-literal`\n\nalpha\nbeta\n"
	got := buildEmailBody("ada@box.test", "a@x.test", "Hello", body, "")
	plain, html := mustAlternativeParts(t, got)

	if !strings.Contains(plain, "docs (https://docs.example/a)") {
		t.Fatalf("MC-30: plain link = %q, want %q", plain, "docs (https://docs.example/a)")
	}
	if strings.Contains(plain, "[docs](") {
		t.Fatalf("MC-30: plain part kept the Markdown link syntax:\n%s", plain)
	}
	if !listItemIndented(plain, "one") {
		t.Fatalf("MC-30: list item %q is not indented in the plain part:\n%s", "one", plain)
	}
	if !strings.Contains(plain, "keep-literal") || strings.Contains(plain, "<code>") {
		t.Fatalf("MC-30: code must stay literal text, plain part:\n%s", plain)
	}
	if !strings.Contains(plain, "alpha\nbeta") && !strings.Contains(plain, "alpha\r\nbeta") {
		t.Fatalf("MC-30: single newline between alpha and beta was not kept:\n%s", plain)
	}
	if strings.Contains(html, "[docs](") {
		t.Fatalf("MC-30: HTML part still contains Markdown source:\n%s", html)
	}
}

func TestOutboundBodyAllowlist(t *testing.T) {
	// MC-2 outbound body policy: links are http/https/mailto only, images are
	// https-only, agent Markdown contributes no style attribute, and schemes
	// outside the allowlist are dropped rather than rewritten into javascript:.
	const body = "Before <script>alert(1)</script>\n\n" +
		"[ok](https://docs.example/a)\n\n" +
		"[bad](javascript:alert(1))\n\n" +
		"![pixel](http://img.example/a.png)\n\n" +
		"![logo](https://logo.example/a.png)\n\n" +
		"<span style=\"color:red\">styled</span>\n"
	got := buildEmailBody("ada@box.test", "a@x.test", "Hello", body, "")
	_, html := mustAlternativeParts(t, got)

	if strings.Contains(strings.ToLower(html), "<script") {
		t.Fatalf("MC-2: HTML part still has a script element:\n%s", html)
	}
	if strings.Contains(strings.ToLower(html), "javascript:") {
		t.Fatalf("MC-2: javascript: scheme was kept or mangled into the HTML:\n%s", html)
	}
	if strings.Contains(strings.ToLower(html), "onerror") || strings.Contains(html, "onerror=") {
		t.Fatalf("MC-2: event-handler attribute survived:\n%s", html)
	}
	if strings.Contains(html, "http://img.example/a.png") {
		t.Fatalf("MC-2: non-https image was kept:\n%s", html)
	}
	if !strings.Contains(html, "https://docs.example/a") {
		t.Fatalf("MC-2: https link was dropped:\n%s", html)
	}
	if !strings.Contains(html, "https://logo.example/a.png") {
		t.Fatalf("MC-2: https image was dropped:\n%s", html)
	}
	if strings.Contains(strings.ToLower(html), "style=") {
		t.Fatalf("MC-2: agent Markdown kept a style attribute:\n%s", html)
	}
	if strings.Contains(html, "mailto:") && !strings.Contains(body, "mailto:") {
		t.Fatalf("MC-2: HTML invented a mailto link that was not in the body")
	}
}

func TestComposeSizeBound(t *testing.T) {
	// MC-22 / B-10: 1 MiB + 1 byte is rejected before any SMTP connection.
	ln := listenLocal(t)
	var dials atomic.Int32
	go acceptAndCount(ln, &dials)

	cl := clientOn(t, ln.Addr().String())
	body := strings.Repeat("a", outboundBodyMax+1)
	err := cl.Send(context.Background(), SendRequest{To: "a@x.test", Subject: "big", Body: body})
	if dials.Load() != 0 {
		t.Fatalf("MC-22: 1 MiB+1 body opened %d SMTP connection(s), want 0; err=%v", dials.Load(), err)
	}
	if err == nil {
		t.Fatal("MC-22: 1 MiB+1 body was accepted, want an error and nothing transmitted")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "1048576") && !strings.Contains(err.Error(), "1 MiB") && !strings.Contains(strings.ToLower(err.Error()), "body") {
		t.Fatalf("MC-22: error %q does not name the 1 MiB body bound", err)
	}
}

func TestClientSend_ContextCancel_Aborts(t *testing.T) {
	// MC-21 / #629: a canceled caller context aborts Send. A dial error from
	// ignoring the context is not that outcome.
	cl := clientOn(t, "127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cl.Send(ctx, SendRequest{To: "a@x.test", Subject: "s", Body: "hello"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MC-21: Send(canceled ctx) err=%v, want context.Canceled", err)
	}
}

func TestClientSend_DialBlackhole_ReturnsWithinBound(t *testing.T) {
	// MC-21 / FR-027: a black-holed SMTP dial returns within the caller deadline
	// instead of the unbounded dial.
	cl := clientOn(t, "192.0.2.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := sendWithin(t, 2*time.Second, func() error {
		return cl.Send(ctx, SendRequest{To: "a@x.test", Subject: "s", Body: "hello"})
	})
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("MC-21: blackhole Send err=%v, want the caller deadline", err)
	}
}

func TestClientSend_StallAfterConnect_ReturnsWithinBound(t *testing.T) {
	// MC-21: a peer that accepts and then stalls must still return within the
	// caller deadline. An unbounded banner read is a hang.
	ln := listenLocal(t)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open and send nothing.
			time.Sleep(30 * time.Second)
			_ = c.Close()
		}
	}()
	cl := clientOn(t, ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := sendWithin(t, 2*time.Second, func() error {
		return cl.Send(ctx, SendRequest{To: "a@x.test", Subject: "s", Body: "hello"})
	})
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("MC-21: stall-after-connect err=%v, want the caller deadline", err)
	}
}

func assertEncodedHeader(t *testing.T, dec *mime.WordDecoder, name, raw, want string) {
	t.Helper()
	if strings.Contains(raw, "ü") {
		t.Errorf("MC-4: %s header contains raw UTF-8 %q; want an RFC 2047 encoded-word", name, raw)
	}
	if !strings.Contains(strings.ToLower(raw), "=?") {
		t.Errorf("MC-4: %s header %q is not an RFC 2047 encoded-word", name, raw)
	}
	decoded, err := dec.DecodeHeader(raw)
	if err != nil {
		t.Errorf("MC-4: decode %s %q: %v", name, raw, err)
		return
	}
	if decoded != want {
		t.Errorf("MC-4: decoded %s = %q, want %q", name, decoded, want)
	}
}

func mustAlternativeParts(t *testing.T, raw string) (plain, html string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("MC-3: composed message is not RFC 5322: %v\n%s", err, raw)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("MC-3: Content-Type %q: %v\n%s", msg.Header.Get("Content-Type"), err, raw)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("MC-3: Content-Type = %q, want multipart/alternative\n%s", mediaType, raw)
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatalf("MC-3: multipart/alternative has no boundary\n%s", raw)
	}
	rawBody, err := io.ReadAll(msg.Body)
	if err != nil {
		t.Fatalf("MC-3: read body: %v", err)
	}
	plain, html, err = splitAlternative(string(rawBody), boundary)
	if err != nil {
		t.Fatalf("MC-3: %v\n%s", err, raw)
	}
	return plain, html
}

func splitAlternative(body, boundary string) (plain, html string, err error) {
	parts := strings.Split(body, "--"+boundary)
	var sawPlain, sawHTML int
	for _, part := range parts {
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
		ctype := ""
		for _, line := range strings.Split(header, "\n") {
			line = strings.TrimRight(line, "\r")
			if strings.HasPrefix(strings.ToLower(line), "content-type:") {
				ctype = strings.ToLower(strings.TrimSpace(line[len("content-type:"):]))
			}
		}
		switch {
		case strings.HasPrefix(ctype, "text/plain"):
			sawPlain++
			plain = content
		case strings.HasPrefix(ctype, "text/html"):
			sawHTML++
			html = content
		}
	}
	if sawPlain != 1 || sawHTML != 1 {
		return "", "", errors.New("want exactly one text/plain and one text/html part, got plain=" + strconv.Itoa(sawPlain) + " html=" + strconv.Itoa(sawHTML))
	}
	return plain, html, nil
}

func listItemIndented(plain, item string) bool {
	for _, line := range strings.Split(plain, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != item {
			continue
		}
		return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
	}
	return false
}

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func acceptAndCount(ln net.Listener, dials *atomic.Int32) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		dials.Add(1)
		_ = c.Close()
	}
}

func clientOn(t *testing.T, addr string) *Client {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("addr %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	cl, err := NewClient(Account{
		IMAPHost: host,
		IMAPPort: port,
		SMTPHost: host,
		SMTPPort: port,
		Username: "ada@box.test",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return cl
}

func sendWithin(t *testing.T, limit time.Duration, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("MC-21: Send did not return within %s", limit)
		return context.DeadlineExceeded
	}
}
