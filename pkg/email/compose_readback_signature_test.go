package email

import (
	"strings"
	"testing"
)

// Oracle: the confirmed live-evidence round trip (squad-lead run against an
// isolated gateway + fakemail, 2026-09-29) that reproduces a duplicate
// signature and a corrupted heading end to end, at the Compose/view layer
// pkg/gateway/rest_mail_draft.go::handleMailDraftUpdate (Save) and
// handleMailDraftSendInner (Send) actually drive. Expected values are NOT
// read off the current implementation's output: a saved draft's signature
// must appear exactly once per transmitted part on send, and the sender's
// own unsigned first line must render as a plain paragraph -- never a
// heading -- because Compose's Markdown source is supposed to be the
// caller-supplied unsigned body, never a previously rendered copy of itself.
// That "exactly once" / "paragraph, not heading" requirement is the feature
// intent (one signature per outgoing message; the sender's own text is not
// restructured by the mail layer), independent of what either Compose call
// currently emits.
//
// Mechanism traced in this task: Compose's draft-body bookkeeping part
// (renderMarkdownPart, pkg/email/compose.go) always carries
// Content-Disposition: attachment; filename="message.md", so
// viewFromRaw's raw-Markdown branch (pkg/email/view.go::viewFromRaw, the
// `ct == "text/markdown" && name == "" && disp == ""` case) never matches
// it -- the part falls through to the generic attachment branch instead.
// Because MIME leaves are walked in wire order and the text/plain leaf
// (which already embeds the signature, appended by Compose's first call)
// is seen FIRST, viewFromRaw's lossy fallback (the `text/plain && name ==
// "" && disp == ""` case, `view.BodyMarkdown = view.TextBody`) sets
// BodyMarkdown to the ALREADY-SIGNED plain-text rendering before the walk
// ever reaches the (non-matching) markdown part, so nothing later
// overwrites it. Feeding that back into Compose as the Send path's
// "Markdown" source (rest_mail_draft.go::handleMailDraftSendInner,
// `markdown := cur.BodyMarkdown`) appends the signature a SECOND time --
// and the signature separator's plain "--" line, sitting directly under
// the original first line with no blank line between them, is a valid
// CommonMark setext-heading underline, turning the sender's own plain
// first line into an <h2>.
func TestComposeSendReadback_SignatureNotDuplicated(t *testing.T) {
	const (
		fromAddr  = "mia@box.test"
		toAddr    = "alice@box.test"
		firstLine = "Hello Alice, this is the F10 signature check body."
		sigHTML   = "<p>Kind regards,<br><b>Mia</b></p>"
		sigMarker = "Kind regards,"
	)

	// Step 1 -- Save: handleMailDraftUpdate composes the UNSIGNED body the
	// caller sent, with Draft: true (rest_mail_draft.go ComposeInput at
	// handleMailDraftUpdate, ~line 123-133).
	saveOut, err := Compose(ComposeInput{
		From:          fromAddr,
		To:            []string{toAddr},
		Subject:       "F10 signature check",
		Markdown:      firstLine,
		SignatureHTML: sigHTML,
		Draft:         true,
	})
	if err != nil {
		t.Fatalf("Save Compose: unexpected error: %v", err)
	}

	// Step 2 -- the "click Send" flow reads the saved draft back via
	// client.ReadView, whose parse is viewFromRaw. ParseViewRaw is the
	// documented in-process twin of that exact path (view.go::ParseViewRaw).
	saved := ParseViewRaw(saveOut.Transmitted)

	// Step 3 -- Send: handleMailDraftSendInner recomposes using
	// `markdown := cur.BodyMarkdown` with no Draft override, so Draft is the
	// zero value false (rest_mail_draft.go ComposeInput at
	// handleMailDraftSendInner, ~line 419-446).
	sendOut, err := Compose(ComposeInput{
		From:          fromAddr,
		To:            []string{toAddr},
		Subject:       "F10 signature check",
		Markdown:      saved.BodyMarkdown,
		SignatureHTML: sigHTML,
	})
	if err != nil {
		t.Fatalf("Send Compose: unexpected error: %v", err)
	}

	final := ParseViewRaw(sendOut.Transmitted)

	if got := strings.Count(final.TextBody, sigMarker); got != 1 {
		t.Fatalf("transmitted plain-text part contains %q %d time(s), want exactly 1:\n%s", sigMarker, got, final.TextBody)
	}
	if got := strings.Count(final.HTMLBody, sigMarker); got != 1 {
		t.Fatalf("transmitted HTML part contains %q %d time(s), want exactly 1:\n%s", sigMarker, got, final.HTMLBody)
	}

	wantParagraph := "<p>" + firstLine + "</p>"
	if !strings.Contains(final.HTMLBody, wantParagraph) {
		t.Fatalf("transmitted HTML part does not render the sender's own first line as a plain paragraph %q:\n%s", wantParagraph, final.HTMLBody)
	}
	// Assert the absence of a heading explicitly -- presence of the
	// paragraph above is not sufficient proof by itself, since goldmark
	// could in principle emit both a corrupted heading AND, elsewhere, an
	// unrelated paragraph that happens to match wantParagraph.
	for _, h := range []string{"<h1", "<h2", "<h3", "<h4", "<h5", "<h6"} {
		if strings.Contains(final.HTMLBody, h) {
			t.Fatalf("transmitted HTML part corrupted the sender's own first line into a heading (%s found):\n%s", h, final.HTMLBody)
		}
	}
}
