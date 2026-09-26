package email

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	extension "github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	htmlrenderer "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Outbound composition (email-mail-view-spec.md MC-2/MC-3/MC-4, FR-003, FR-022,
// D29): one Markdown source becomes a multipart/alternative message with
// exactly one text/plain and one text/html part, an allowlisted HTML render,
// an AST-derived plain-text render, RFC 2047 headers, a CRLF-safe recipient
// parse, and the three-copy BCC rule (transmitted copy carries no Bcc header;
// the Sent copy keeps it; every address is an envelope recipient).
//
// The outbound HTML allowlist is NOT the Library preview policy: outbound mail
// allows links http/https/mailto, images https-only, and no style attribute
// from agent Markdown (MC-2). Signature HTML has its own, richer policy
// (SanitizeSignatureHTML) because a signature is stored user content.

const (
	// maxOutboundBodyBytes is MC-22 / FR-031: the Markdown body is bounded at
	// 1 MiB before any SMTP connection is opened.
	maxOutboundBodyBytes = 1 << 20

	// maxSignatureHTMLBytes is MC-1: a stored signature is at most 16,384 chars.
	maxSignatureHTMLBytes = 16384

	// agentDraftHeader marks a locally composed draft (FR-029).
	agentDraftHeader = "X-Omnipus-Draft"

	// fallbackMessageIDDomain is used when the From address has no domain.
	fallbackMessageIDDomain = "omnipus.invalid"
)

// Attachment is one workspace file to attach to an outbound message (D33). The
// bytes are resolved and read by the caller (the mail tools); Compose only
// encodes them into MIME parts.
type Attachment struct {
	// Name is the file name presented to recipients (Content-Disposition).
	Name string
	// ContentType is the MIME type; empty means application/octet-stream.
	ContentType string
	// Data are the file bytes.
	Data []byte
}

// ComposeInput is the full outbound composition request.
type ComposeInput struct {
	From string
	// To, Cc and Bcc are recipient lists (MC-15 / D26). Entries may carry
	// display names ("Ada <ada@box.test>"); anything after an embedded CR/LF is
	// treated as an injection attempt and dropped (MC-4).
	To  []string
	Cc  []string
	Bcc []string
	// Subject and Markdown are the message subject and body.
	Subject  string
	Markdown string
	// SignatureHTML is the account signature (already-safe HTML from
	// SanitizeSignatureHTML; Compose re-sanitizes defensively).
	SignatureHTML string
	// InReplyTo, when set, becomes the In-Reply-To and References headers.
	InReplyTo string
	// MessageID, when non-empty, overrides the generated Message-ID: the
	// draft-edit path re-APPENDs with the SAME Message-ID (round-1 MIN-004)
	// so panel edits never orphan mid: refs. Validated with the mid: grammar
	// (angle brackets, an @, max 998 bytes, no CR/LF). Empty = generated.
	MessageID string
	// Attachments ride the message as MIME parts (MC-32; capped by the caller).
	Attachments []Attachment
	// Draft marks this message a locally saved draft: it gains the
	// X-Omnipus-Draft header and an extra text/markdown part carrying the
	// original Markdown so the approval panel can re-render it (FR-029/FR-034).
	Draft bool
}

// ComposeOutput carries all three copies a send path needs (FR-022 / D29):
// the transmitted bytes (no Bcc header), the Sent-folder copy (Bcc kept), and
// the envelope recipients (To + Cc + Bcc).
type ComposeOutput struct {
	Transmitted        []byte
	SentCopy           []byte
	EnvelopeRecipients []string
	MessageID          string
}

// Compose renders one Markdown body into the multipart/alternative wire
// message (plus the Sent copy and envelope recipients). It never touches the
// network; the caller dials.
func Compose(in ComposeInput) (ComposeOutput, error) {
	if strings.TrimSpace(in.From) == "" {
		return ComposeOutput{}, fmt.Errorf("compose: from address is empty")
	}
	fromAddr, err := mail.ParseAddress(strings.TrimSpace(in.From))
	if err != nil {
		return ComposeOutput{}, fmt.Errorf("compose: invalid from address %q", in.From)
	}
	toAddrs, badTo := parseRecipientList(in.To)
	ccAddrs, badCc := parseRecipientList(in.Cc)
	bccAddrs, badBcc := parseRecipientList(in.Bcc)
	if bad := firstBad(badTo, badCc, badBcc); bad != "" {
		return ComposeOutput{}, fmt.Errorf("compose: invalid recipient address %q", bad)
	}
	if len(toAddrs)+len(ccAddrs)+len(bccAddrs) == 0 {
		return ComposeOutput{}, fmt.Errorf("compose: no recipients given")
	}
	if strings.TrimSpace(in.Markdown) == "" {
		return ComposeOutput{}, fmt.Errorf("compose: body is empty")
	}

	plain := markdownToPlain(in.Markdown)
	htmlPart := outboundSanitize(markdownToHTML(in.Markdown))
	if in.SignatureHTML != "" {
		sigHTML, err := SanitizeSignatureHTML(in.SignatureHTML)
		if err != nil {
			return ComposeOutput{}, fmt.Errorf("compose: signature: %w", err)
		}
		plain += "\n--\n" + htmlToText(sigHTML)
		htmlPart += signatureWrap(sigHTML)
	}

	messageID := "<" + randomHex(16) + "@" + domainOf(fromAddr.Address) + ">"
	if in.MessageID != "" {
		if _, err := parseMailRef("mid:" + in.MessageID); err != nil {
			return ComposeOutput{}, fmt.Errorf("compose: invalid Message-ID override")
		}
		messageID = in.MessageID
	}
	subject := encodeHeaderValue(sanitizeHeader(in.Subject))
	altBoundary := randomBoundary()
	altBody := renderAlternative(plain, htmlPart, altBoundary)

	rootType := fmt.Sprintf("multipart/alternative; boundary=%q", altBoundary)
	body := altBody
	if len(in.Attachments) > 0 || in.Draft {
		mixBoundary := randomBoundary()
		var mix strings.Builder
		mix.WriteString("--" + mixBoundary + "\r\n")
		mix.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q\r\n\r\n", altBoundary))
		mix.WriteString(altBody)
		if in.Draft {
			mix.WriteString(renderMarkdownPart(mixBoundary, in.Markdown))
		}
		for _, att := range in.Attachments {
			mix.WriteString(renderAttachmentPart(mixBoundary, att))
		}
		mix.WriteString("--" + mixBoundary + "--\r\n")
		body = mix.String()
		rootType = fmt.Sprintf("multipart/mixed; boundary=%q", mixBoundary)
	}

	hdrs := composeHeaders(composeHeaderInput{
		from:      formatAddress(fromAddr),
		to:        formatAddressList(toAddrs),
		cc:        formatAddressList(ccAddrs),
		bcc:       formatAddressList(bccAddrs),
		subject:   subject,
		inReplyTo: sanitizeHeader(in.InReplyTo),
		messageID: messageID,
		draft:     in.Draft,
		rootType:  rootType,
	})

	out := ComposeOutput{
		EnvelopeRecipients: envelopeRecipients(toAddrs, ccAddrs, bccAddrs),
		MessageID:          messageID,
	}
	out.Transmitted = []byte(hdrs.transmitted + "\r\n" + body)
	out.SentCopy = []byte(hdrs.sentCopy + "\r\n" + body)
	return out, nil
}

// --- signature sanitizing (FR-003, MC-1) ---

// sigStyleAttrRe bounds the style attribute to declarations without parens, so
// url(...), expression(...) and @import cannot ride a stored signature.
var sigStyleAttrRe = regexp.MustCompile(`^[a-zA-Z0-9 #:;,.'"\-_%!]*$`)

// sigLinkSchemeRe allows links http/https/mailto only (MC-2).
var sigLinkSchemeRe = regexp.MustCompile(`^(?:https?|mailto):`)

// sigImageSrcRe allows https and data:image/ sources only (FR-003).
var sigImageSrcRe = regexp.MustCompile(`^(?:https://|data:image/)`)

// signaturePolicy is the allowlist for stored signature HTML. It is richer
// than the outbound body policy: signatures are user (not agent Markdown)
// content, so style attributes and tables survive.
func signaturePolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements(
		"p", "br", "div", "span", "strong", "em", "b", "i", "u", "s", "hr",
		"table", "thead", "tbody", "tfoot", "tr", "td", "th", "caption",
		"ul", "ol", "li", "a", "img", "font", "h1", "h2", "h3", "h4", "h5", "h6",
		"pre", "code", "blockquote",
	)
	p.AllowAttrs("href").Matching(sigLinkSchemeRe).OnElements("a")
	p.AllowAttrs("src").Matching(sigImageSrcRe).OnElements("img")
	p.AllowAttrs("alt", "title", "width", "height", "align").OnElements("img")
	p.AllowAttrs("align", "border", "cellpadding", "cellspacing", "width").OnElements("table", "td", "th", "tr")
	p.AllowAttrs("color", "face", "size").OnElements("font")
	p.AllowAttrs("style").Matching(sigStyleAttrRe).Globally()
	return p
}

// SanitizeSignatureHTML applies the stored-signature allowlist. Raw input over
// maxSignatureHTMLBytes (16,384) chars is rejected with an error naming the
// limit (MC-1); the sanitized result is safe to store and to append to
// outbound mail.
func SanitizeSignatureHTML(raw string) (string, error) {
	if len(raw) > maxSignatureHTMLBytes {
		return "", fmt.Errorf("signature is %d characters; the maximum is %d", len(raw), maxSignatureHTMLBytes)
	}
	return signaturePolicy().Sanitize(raw), nil
}

// signatureWrap renders the sanitized signature for the HTML part.
func signatureWrap(sigHTML string) string {
	return "\n<div class=\"omnipus-signature\">" + sigHTML + "</div>"
}

// --- outbound body policy (MC-2) ---

// outLinkSchemeRe allows agent-Markdown links http/https/mailto only.
var outLinkSchemeRe = regexp.MustCompile(`^(?:https?|mailto):`)

// outImageSrcRe allows https images only in agent Markdown (MC-2).
var outImageSrcRe = regexp.MustCompile(`^https://`)

// outboundBodyPolicy is the HTML allowlist for the agent's Markdown body:
// formatting and structure elements, links http/https/mailto, images
// https-only, and no style attribute anywhere.
func outboundBodyPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements(
		"p", "br", "hr", "strong", "em", "b", "i", "u", "s", "del", "ins",
		"code", "pre", "blockquote", "span", "div",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"ul", "ol", "li", "table", "thead", "tbody", "tfoot", "tr", "td", "th",
		"a", "img",
	)
	p.AllowAttrs("href").Matching(outLinkSchemeRe).OnElements("a")
	p.AllowAttrs("src").Matching(outImageSrcRe).OnElements("img")
	p.AllowAttrs("alt", "title").OnElements("img")
	return p
}

// outboundSanitize applies the outbound body allowlist.
func outboundSanitize(htmlPart string) string { return outboundBodyPolicy().Sanitize(htmlPart) }

// --- recipients (MC-4, MC-27) ---

// parseRecipientList parses recipient entries into addresses, deduplicated
// case-insensitively in first-seen order. Each entry is split on CR/LF and
// commas BEFORE parsing, so a CRLF injection attempt ("a@x.test\r\nBcc:
// eve@x.test") degrades into an unparseable piece that is reported, never
// transported (MC-4).
func parseRecipientList(entries []string) (addrs []mail.Address, bad []string) {
	seen := make(map[string]bool)
	for _, entry := range entries {
		pieces := strings.FieldsFunc(entry, func(r rune) bool {
			return r == '\r' || r == '\n' || r == ','
		})
		for _, piece := range pieces {
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}
			a, err := mail.ParseAddress(piece)
			if err != nil {
				bad = append(bad, piece)
				continue
			}
			key := strings.ToLower(a.Address)
			if seen[key] {
				continue
			}
			seen[key] = true
			addrs = append(addrs, *a)
		}
	}
	return addrs, bad
}

// firstBad returns the first non-empty string, so a rejected address is named.
func firstBad(lists ...[]string) string {
	for _, l := range lists {
		if len(l) > 0 {
			return l[0]
		}
	}
	return ""
}

// envelopeRecipients merges the address lists for the SMTP envelope.
func envelopeRecipients(lists ...[]mail.Address) []string {
	var out []string
	for _, l := range lists {
		for _, a := range l {
			out = append(out, a.Address)
		}
	}
	return out
}

// isASCIIName reports whether s is safe to write into a header unencoded.
func isASCIIName(s string) bool {
	for _, r := range s {
		if r > 126 || r < 32 {
			return false
		}
	}
	return true
}

// formatAddress renders one address, RFC 2047 encoding the display name when
// it is not plain ASCII (MC-4). A bare address renders bare (no angle
// brackets), matching what recipients' clients display.
func formatAddress(a *mail.Address) string {
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return a.Address
	}
	if !isASCIIName(name) {
		return mime.QEncoding.Encode("utf-8", name) + " <" + a.Address + ">"
	}
	return a.String()
}

// formatAddressList renders an address list for a To/Cc/Bcc header.
func formatAddressList(addrs []mail.Address) string {
	parts := make([]string, 0, len(addrs))
	for i := range addrs {
		parts = append(parts, formatAddress(&addrs[i]))
	}
	return strings.Join(parts, ", ")
}

// encodeHeaderValue RFC 2047 encodes a header value when it is not plain ASCII.
func encodeHeaderValue(v string) string {
	if v == "" || isASCIIName(v) {
		return v
	}
	return mime.QEncoding.Encode("utf-8", v)
}

// domainOf returns the domain of an address, or the fallback domain.
func domainOf(addr string) string {
	if _, dom, ok := strings.Cut(addr, "@"); ok && dom != "" {
		return dom
	}
	return fallbackMessageIDDomain
}

// --- Markdown rendering (MC-3, MC-30) ---

// markdownToHTML renders Markdown with raw HTML enabled (WithUnsafe) and then
// runs the outbound allowlist — goldmark escaping would leave hostile markup
// visible as text, while the sanitize pass removes it structurally (MC-2).
func markdownToHTML(md string) string {
	conv := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(htmlrenderer.WithUnsafe()),
	)
	var buf bytes.Buffer
	if err := conv.Convert([]byte(md), &buf); err != nil {
		return "<p>[render error]</p>"
	}
	return buf.String()
}

// markdownToPlain derives the plain-text part from the SAME Markdown parse as
// the HTML part (MC-30 / DS-2): links render as "text (href)", list items are
// indented, code stays literal, soft line breaks survive as newlines, and raw
// HTML is dropped.
func markdownToPlain(md string) string {
	pr := &plainTextRenderer{}
	conv := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRenderer(renderer.NewRenderer(renderer.WithNodeRenderers(util.Prioritized(pr, 1)))),
	)
	doc := conv.Parser().Parse(text.NewReader([]byte(md)))
	var buf bytes.Buffer
	if err := conv.Renderer().Render(&buf, []byte(md), doc); err != nil {
		return md
	}
	return strings.TrimRight(buf.String(), "\n")
}

// plainTextRenderer is a goldmark node renderer producing the plain-text part.
type plainTextRenderer struct{}

// RegisterFuncs binds every node kind the renderer handles. Kinds left
// unregistered fall back to goldmark's default (which would emit HTML), so
// every AST kind GFM can produce is bound here.
func (r *plainTextRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindDocument, r.skip)       // container: children only
	reg.Register(ast.KindParagraph, r.paragraph) //
	reg.Register(ast.KindTextBlock, r.skip)      // container: children only
	reg.Register(ast.KindHeading, r.heading)     //
	reg.Register(ast.KindText, r.text)           //
	reg.Register(ast.KindString, r.stringNode)   //
	reg.Register(ast.KindCodeSpan, r.skip)       // children carry the code text
	reg.Register(ast.KindFencedCodeBlock, r.codeBlock)
	reg.Register(ast.KindCodeBlock, r.codeBlock)     //
	reg.Register(ast.KindEmphasis, r.skip)           // children only
	reg.Register(extast.KindStrikethrough, r.skip)   // children only
	reg.Register(ast.KindLink, r.link)               //
	reg.Register(ast.KindAutoLink, r.autoLink)       //
	reg.Register(ast.KindImage, r.image)             // alt text + (destination)
	reg.Register(ast.KindList, r.list)               // newline on exit separates a following block
	reg.Register(ast.KindListItem, r.listItem)       //
	reg.Register(ast.KindBlockquote, r.blockquote)   //
	reg.Register(ast.KindThematicBreak, r.thematic)  //
	reg.Register(ast.KindHTMLBlock, r.drop)          // raw HTML: dropped from plain
	reg.Register(ast.KindRawHTML, r.drop)            // raw HTML: dropped from plain
	reg.Register(extast.KindTaskCheckBox, r.taskBox) //
	reg.Register(extast.KindTable, r.skip)           // container: children only
	reg.Register(extast.KindTableHeader, r.skip)     //
	reg.Register(extast.KindTableRow, r.tableRow)
	reg.Register(extast.KindTableCell, r.tableCell)
}

func (r *plainTextRenderer) skip(_ util.BufWriter, _ []byte, _ ast.Node, _ bool) (ast.WalkStatus, error) {
	return ast.WalkContinue, nil
}

func (r *plainTextRenderer) drop(_ util.BufWriter, _ []byte, _ ast.Node, _ bool) (ast.WalkStatus, error) {
	return ast.WalkSkipChildren, nil
}

func (r *plainTextRenderer) paragraph(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_ = n
		_, _ = w.WriteString("\n\n")
	}
	return ast.WalkContinue, nil
}

func (r *plainTextRenderer) heading(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("\n\n")
	}
	return ast.WalkContinue, nil
}

func (r *plainTextRenderer) text(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		t := n.(*ast.Text)
		_, _ = w.Write(t.Segment.Value(source))
		if t.SoftLineBreak() {
			_, _ = w.WriteString("\n")
		}
		if t.HardLineBreak() {
			_, _ = w.WriteString("\n")
		}
	}
	return ast.WalkContinue, nil
}

func (r *plainTextRenderer) stringNode(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.Write(n.(*ast.String).Value)
	}
	return ast.WalkContinue, nil
}

func (r *plainTextRenderer) codeBlock(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			segment := lines.At(i)
			_, _ = w.Write(segment.Value(source))
		}
		_, _ = w.WriteString("\n")
	}
	return ast.WalkSkipChildren, nil
}

func (r *plainTextRenderer) link(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString(" (" + string(n.(*ast.Link).Destination) + ")")
	}
	return ast.WalkContinue, nil
}

func (r *plainTextRenderer) image(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString(" (" + string(n.(*ast.Image).Destination) + ")")
	}
	return ast.WalkContinue, nil
}

func (r *plainTextRenderer) autoLink(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString(" (" + string(n.(*ast.AutoLink).URL(source)) + ")")
	}
	return ast.WalkContinue, nil
}

func (r *plainTextRenderer) listItem(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("\n" + strings.Repeat("  ", listDepth(n)))
	}
	return ast.WalkContinue, nil
}

// list writes the newline a following block needs, since paragraph
// separators are emitted on paragraph exit and a list ending a block
// would otherwise run into it.
func (r *plainTextRenderer) list(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("\n")
	}
	return ast.WalkContinue, nil
}

// listDepth counts the node itself (when it is a list item) plus enclosing
// list items, so a top-level item's content indents one level and nested
// lists indent deeper.
func listDepth(n ast.Node) int {
	depth := 0
	for p := n; p != nil; p = p.Parent() {
		if p.Kind() == ast.KindListItem {
			depth++
		}
	}
	return depth
}

func (r *plainTextRenderer) blockquote(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("\n\n")
	}
	return ast.WalkContinue, nil
}

func (r *plainTextRenderer) thematic(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("---\n\n")
	}
	return ast.WalkSkipChildren, nil
}

func (r *plainTextRenderer) taskBox(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		if n.(*extast.TaskCheckBox).IsChecked {
			_, _ = w.WriteString("[x] ")
		} else {
			_, _ = w.WriteString("[ ] ")
		}
	}
	return ast.WalkContinue, nil
}

func (r *plainTextRenderer) tableRow(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("\n")
	}
	return ast.WalkContinue, nil
}

func (r *plainTextRenderer) tableCell(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString(" ")
	} else {
		_, _ = w.WriteString(" ")
	}
	return ast.WalkContinue, nil
}

// isPlainOnlyText reports whether Markdown parses to a single paragraph of
// plain text — the shape the historical single-part builder covered.
func isPlainOnlyText(md string) bool {
	conv := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := conv.Parser().Parse(text.NewReader([]byte(md)))
	if doc.ChildCount() != 1 || doc.FirstChild().Kind() != ast.KindParagraph {
		return false
	}
	for c := doc.FirstChild().FirstChild(); c != nil; c = c.NextSibling() {
		if c.Kind() != ast.KindText {
			return false
		}
	}
	return true
}

// --- MIME assembly ---

// composeHeaderInput carries every value the header block needs.
type composeHeaderInput struct {
	from      string
	to        string
	cc        string
	bcc       string
	subject   string
	inReplyTo string
	messageID string
	draft     bool
	rootType  string
}

// composeHeaders renders the RFC 5322 header block in both required shapes:
// transmitted (FR-022: no Bcc header at all) and sentCopy (the Bcc header
// kept, so the sender's copy shows who else received it).
func composeHeaders(in composeHeaderInput) (out struct{ transmitted, sentCopy string }) {
	var b strings.Builder
	b.WriteString("From: " + in.from + "\r\n")
	b.WriteString("To: " + in.to + "\r\n")
	if in.cc != "" {
		b.WriteString("Cc: " + in.cc + "\r\n")
	}
	b.WriteString("Subject: " + in.subject + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + in.messageID + "\r\n")
	if in.inReplyTo != "" {
		b.WriteString("In-Reply-To: " + in.inReplyTo + "\r\n")
		b.WriteString("References: " + in.inReplyTo + "\r\n")
	}
	if in.draft {
		b.WriteString(agentDraftHeader + ": 1\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: " + in.rootType + "\r\n")
	out.transmitted = b.String()
	out.sentCopy = b.String()
	if in.bcc != "" {
		out.sentCopy += "Bcc: " + in.bcc + "\r\n"
	}
	return out
}

// renderAlternative renders the multipart/alternative body: exactly one
// text/plain and one text/html part (MC-3), plain first, HTML last.
func renderAlternative(plain, htmlPart, boundary string) string {
	var b strings.Builder
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(plain + "\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(htmlPart + "\r\n")
	b.WriteString("--" + boundary + "--\r\n")
	return b.String()
}

// renderMarkdownPart renders the draft-only text/markdown part carrying the
// original Markdown (FR-034: the approval panel re-renders what the agent
// wrote, not the sanitized HTML).
func renderMarkdownPart(boundary, md string) string {
	var b strings.Builder
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/markdown; charset=UTF-8\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"message.md\"\r\n")
	b.WriteString("Content-Transfer-Encoding: " + textEncoding([]byte(md)) + "\r\n\r\n")
	b.WriteString(writeTextPayload([]byte(md)))
	b.WriteString("\r\n")
	return b.String()
}

// renderAttachmentPart renders one attachment MIME part inside the
// multipart/mixed root. 7-bit-clean payloads are sent verbatim so the file
// bytes stay readable on the wire; anything else is base64 (MC-32 keeps the
// size caps in the caller, pre-dial).
func renderAttachmentPart(boundary string, att Attachment) string {
	name := sanitizeHeader(att.Name)
	name = strings.ReplaceAll(name, `"`, "'")
	if name == "" {
		name = "attachment.bin"
	}
	ct := att.ContentType
	if ct == "" {
		ct = mime.TypeByExtension("." + extOf(name))
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	ct = strings.ReplaceAll(ct, `"`, "'")
	var b strings.Builder
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: " + ct + "; name=\"" + name + "\"\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"" + name + "\"\r\n")
	b.WriteString("Content-Transfer-Encoding: " + textEncoding(att.Data) + "\r\n\r\n")
	b.WriteString(writeTextPayload(att.Data))
	b.WriteString("\r\n")
	return b.String()
}

// is7bitClean reports whether the bytes can ride the wire without encoding.
func is7bitClean(data []byte) bool {
	for _, by := range data {
		if by > 126 || (by < 32 && by != '\r' && by != '\n' && by != '\t') {
			return false
		}
	}
	return true
}

// textEncoding picks the Content-Transfer-Encoding for a payload.
func textEncoding(data []byte) string {
	if is7bitClean(data) {
		return "7bit"
	}
	return "base64"
}

// writeTextPayload renders a payload under its chosen encoding.
func writeTextPayload(data []byte) string {
	if is7bitClean(data) {
		return string(data)
	}
	return base64LineWrap(data)
}

// base64LineWrap base64-encodes data with RFC 2045 76-column line wrapping.
func base64LineWrap(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc)
	return b.String()
}

// extOf returns the lowercase extension of a file name without the dot.
func extOf(name string) string {
	dot := strings.LastIndex(name, ".")
	if dot < 0 || dot == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[dot+1:])
}

// --- random helpers ---

// randomHex returns n random bytes hex-encoded (2n chars).
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is a process-level problem; a deterministic
		// fallback keeps the message valid rather than panicking the tool.
		return strings.Repeat("0", 2*n)
	}
	return hex.EncodeToString(buf)
}

// randomBoundary returns a MIME boundary that cannot appear in rendered parts.
func randomBoundary() string { return "omnipus-" + randomHex(12) }
