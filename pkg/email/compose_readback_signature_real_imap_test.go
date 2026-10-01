package email

import (
	"context"
	"strings"
	"testing"
)

// F10 real-IMAP-round-trip coverage.
//
// Oracle: the SAME feature intent TestComposeSendReadback_SignatureNotDuplicated
// already asserts (compose_readback_signature_test.go) -- a saved draft's
// unsigned Markdown source must come back unchanged and MarkdownLossy must be
// false -- but proven through an ACTUAL client-against-server IMAP protocol
// round trip (APPEND then FETCH against a real go-imap server,
// imapmemserver, the same library family tests/e2e/fixtures/fakemail wraps)
// instead of ParseViewRaw on in-memory bytes. renderMarkdownPart
// (pkg/email/compose.go) documents the contract this pins: the draft-body
// bookkeeping part is recognized ONLY by its X-Omnipus-Part: draft-body
// marker header, never by name/disposition being empty -- so a fresh IMAP
// FETCH of a just-saved draft must return the caller's own unsigned Markdown
// verbatim, never the plain-text fallback's already-signed rendering.
//
// Investigation note (2026-09-29, this task): two hypotheses for why the
// live gateway+fakemail run still showed the bug were checked against this
// same server family and NOT confirmed:
//   - "BODY[] loses a part's own MIME headers": go-imap's FetchItemBodySection
//     doc comment (fetch.go) states the zero value fetches the WHOLE message,
//     and imapmemserver's ExtractBodySection (imapserver/message.go) proves
//     it: for a whole-message fetch it re-serializes only the TOP-LEVEL
//     header (textproto.WriteHeader) and then io.Copy's the remainder of the
//     stored bytes verbatim -- sub-part headers, including X-Omnipus-Part,
//     are never touched. appendBytes (imapmemserver/mailbox.go) stores the
//     APPENDed bytes byte-for-byte, no re-parse.
//   - "viewFromRaw's NextPart() loop (view.go, ~line 609) silently swallows a
//     parse error mid-walk and never reaches the marked part": both tests
//     below decode every part correctly on both sides of the fix (the
//     pre-fix run below shows the OLD detection predicate's own documented
//     miss, not a truncated walk), so there is no sign of a silent
//     early-break here.
//
// Both tests below are GREEN on this branch's HEAD (the fix already applied)
// and were confirmed RED on the fix's immediate parent commit
// (bb592d011, "test(mail): RED for duplicate signature and heading
// corruption on Send readback" -- one commit before 6e9c4fbe2) via a
// worktree checked out at that SHA with this same file copied in and run
// with the identical narrow command; see the dispatch report for the exact
// commands and output. That RED/GREEN pairing is this test's own proof of
// failability at this real-IMAP layer -- it could not have been observed by
// running the code once.
func TestReadView_RealIMAPRoundTrip_SaveOnly(t *testing.T) {
	const (
		fromAddr  = "mia@box.test"
		toAddr    = "alice@box.test"
		firstLine = "Hello Alice, this is the F10 re-verification body."
		sigHTML   = "<p>Kind regards,<br><b>Mia</b></p>"
	)

	saveOut, err := Compose(ComposeInput{
		From:          fromAddr,
		To:            []string{toAddr},
		Subject:       "F10 real IMAP readback",
		Markdown:      firstLine,
		SignatureHTML: sigHTML,
		Draft:         true,
	})
	if err != nil {
		t.Fatalf("Compose(save): unexpected error: %v", err)
	}

	// APPEND the composed bytes to a REAL in-memory IMAP server (not
	// ParseViewRaw on the same bytes in-process) and FETCH them back through
	// the real client path (ReadView -> viewFromRaw).
	cl := startViewIMAPRaw(t, []viewMsg{{folder: "drafts", raw: saveOut.Transmitted}}, nil)
	v, err := cl.ReadView(context.Background(), FolderDrafts, "mid:"+saveOut.MessageID)
	if err != nil {
		t.Fatalf("ReadView: unexpected error: %v", err)
	}

	if v.MarkdownLossy {
		t.Fatalf("MarkdownLossy = true after a real IMAP APPEND+FETCH round trip, want false: "+
			"the marker-header part was not recognized and the plain-text fallback (BodyMarkdown=%q) was used instead",
			v.BodyMarkdown)
	}
	if got := strings.TrimSpace(v.BodyMarkdown); got != firstLine {
		t.Fatalf("BodyMarkdown = %q, want exactly %q (Compose's unsigned source, unchanged by the round trip)", got, firstLine)
	}
	// Negative assertion: the OLD bug's exact signature (plain-text fallback
	// baked into BodyMarkdown) would contain this fragment. Its absence is
	// the distinguishing proof, not just the positive equality above.
	if strings.Contains(v.BodyMarkdown, "Kind regards,") {
		t.Fatalf("BodyMarkdown = %q contains the signature -- the draft-body marker part was not recognized", v.BodyMarkdown)
	}
}

// TestReadView_RealIMAPRoundTrip_FullSaveFlow mirrors
// pkg/gateway/rest_mail_draft.go::handleMailDraftUpdate's actual sequence
// end to end at the Go level: an existing agent-authored draft (created
// unsigned, exactly like tools/email_compose.go's create_email_draft, which
// never sets SignatureHTML) is APPENDed first; Save then ReadViews it
// (cur), Composes a NEW version with Draft: cur.IsOmnipusDraft and the
// mailbox's SignatureHTML (handleMailDraftUpdate's own ComposeInput shape),
// APPENDs the new copy, and DeleteDrafts the old UID -- then a FRESH GET, by
// Message-ID (mid: ref, as a real subsequent panel refresh would use, never
// a stale numeric uid: ref), ReadViews the folder again.
func TestReadView_RealIMAPRoundTrip_FullSaveFlow(t *testing.T) {
	const (
		fromAddr  = "mia@box.test"
		toAddr    = "alice@box.test"
		firstLine = "Hello Alice, this is the F10 re-verification body."
		sigHTML   = "<p>Kind regards,<br><b>Mia</b></p>"
	)

	// Step 0: the agent's original draft -- unsigned (create_email_draft
	// never passes SignatureHTML).
	origOut, err := Compose(ComposeInput{
		From: fromAddr, To: []string{toAddr}, Subject: "F10 real IMAP readback",
		Markdown: firstLine, Draft: true,
	})
	if err != nil {
		t.Fatalf("Compose(orig): unexpected error: %v", err)
	}

	cl := startViewIMAPRaw(t, []viewMsg{{folder: "drafts", raw: origOut.Transmitted}}, nil)
	ctx := context.Background()

	cur, err := cl.ReadView(ctx, FolderDrafts, "mid:"+origOut.MessageID)
	if err != nil {
		t.Fatalf("ReadView(cur): unexpected error: %v", err)
	}

	// Step 1: Save -- handleMailDraftUpdate's exact ComposeInput shape
	// (Markdown from the request body, Draft from the existing draft's own
	// flag, SignatureHTML from the mailbox config).
	saveOut, err := Compose(ComposeInput{
		From: fromAddr, To: []string{toAddr}, Subject: "F10 real IMAP readback",
		Markdown: firstLine, MessageID: bracketMessageID(cur.MessageID),
		Draft: cur.IsOmnipusDraft, SignatureHTML: sigHTML,
	})
	if err != nil {
		t.Fatalf("Compose(save): unexpected error: %v", err)
	}
	if _, _, appendErr := cl.AppendMessage(ctx, cl.DraftsFolderName(), []string{"\\Draft"}, saveOut.Transmitted); appendErr != nil {
		t.Fatalf("AppendMessage(save): unexpected error: %v", appendErr)
	}
	if derr := cl.DeleteDraft(ctx, cur.UID); derr != nil {
		// MAJ-009/MC-28: production treats this as a warning, never an
		// error -- the new copy exists regardless. Match that here: fail
		// loudly only if it stops the test from reaching its real
		// assertion, never silently.
		t.Fatalf("DeleteDraft(old): unexpected error (production treats this as non-fatal, but the test setup needs it to succeed to isolate one copy): %v", derr)
	}

	// Step 2: a FRESH GET, by Message-ID, like a real subsequent refresh.
	fresh, err := cl.ReadView(ctx, FolderDrafts, "mid:"+saveOut.MessageID)
	if err != nil {
		t.Fatalf("ReadView(fresh): unexpected error: %v", err)
	}

	if fresh.MarkdownLossy {
		t.Fatalf("MarkdownLossy = true on the fresh GET after Save, want false: BodyMarkdown=%q", fresh.BodyMarkdown)
	}
	if got := strings.TrimSpace(fresh.BodyMarkdown); got != firstLine {
		t.Fatalf("BodyMarkdown = %q, want exactly %q (the unsigned source Save composed, unchanged)", got, firstLine)
	}
	if strings.Contains(fresh.BodyMarkdown, "Kind regards,") {
		t.Fatalf("BodyMarkdown = %q contains the signature -- Send would double-sign and corrupt the sender's own first line into a heading", fresh.BodyMarkdown)
	}
}

// bracketMessageID mirrors pkg/gateway/rest_mail_draft.go::bracketMessageID
// (angle-bracket normalization) without importing the gateway package --
// email.Client.ReadView/Compose both operate purely on RFC 5322 strings.
func bracketMessageID(id string) string {
	if strings.HasPrefix(id, "<") {
		return id
	}
	return "<" + id + ">"
}
