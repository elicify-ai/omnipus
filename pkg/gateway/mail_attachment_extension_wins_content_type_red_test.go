package gateway

// Combined feature-gate review, item 7 (INFO, security-lead): the vacuous
// fixture + stale comment finding.
//
// (1) VACUOUS FIXTURE: mail_fixture_red_test.go::TestAttachmentDownload_HTMLIsNeverInline
// serves an attachment named "../../evil.html" whose part ALSO declares
// Content-Type: text/html — so the extension-derived type
// (libraryExtContentTypes[".html"], per rest_mail_read.go's own MC-42
// comment: "never from the part's self-declared type when the table knows
// the extension") and the part's self-declared type happen to be IDENTICAL
// for that input. The test's ct == "text/html" assertion therefore passes
// no matter which of the two the code actually prioritizes — it cannot
// distinguish "extension decides" from "self-declared type decides",
// despite MC-42/Hard-Constraint-#6 requiring the former specifically
// BECAUSE the two can disagree (a "dangerous extension" attack: a
// text/plain-declared part named *.html that a naive server would trust
// the declared type for). This test below closes that gap: an attachment
// named *.html but SELF-DECLARED text/plain — the two now disagree, so the
// assertion can actually tell which rule the code follows.
//
// (2) STALE COMMENT: pkg/gateway/inline_serving.go::applyMailByteHeaders's
// doc comment says "Content-Type from the part's recorded type with the
// extension as fallback (caller-derived)" — stating self-declared-type-
// PRIMARY, extension-FALLBACK. Its one caller, rest_mail_read.go (lines
// ~335-347), does the OPPOSITE: it checks libraryExtContentTypes[ext] FIRST
// and only falls back to part.ContentType when the extension is unknown —
// extension-PRIMARY, self-declared-FALLBACK, matching MC-42 and Hard
// Constraint #6 ("extension decides ... never the MIME part's self-declared
// type when it disagrees with a dangerous extension"), not the comment's
// stated order. Reported as a finding below (no test can assert a comment's
// wording; backend-lead should correct the comment to match the actual,
// correct priority).
//
// This test itself passes on CURRENT code — the underlying priority is
// already implemented correctly; only the OLD test's fixture and this
// comment failed to reflect it. It's included as the differentiating
// regression guard the old fixture should have been.

import (
	"net/http"
	"testing"
)

const mismatchedExtHTMLRaw = "From: a@b.test\r\nTo: mailbox@test.local\r\nSubject: ext-priority\r\n" +
	"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\nMessage-ID: <ext-priority@b.test>\r\n" +
	"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=extbnd\r\n\r\n" +
	"--extbnd\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
	"--extbnd\r\nContent-Type: application/octet-stream\r\n" +
	"Content-Disposition: attachment; filename=\"disguised.html\"\r\n\r\n" +
	"<script>alert(1)</script>\r\n--extbnd--\r\n"

func TestAttachmentDownload_ExtensionContentTypeWinsOverDisagreeingSelfDeclaredType(t *testing.T) {
	env := newMailRedEnv(t)
	msgPath := mailMessagesPath("inbox") + "/uid:1:1"
	requireMailLive(t, env.mux, http.MethodGet, msgPath, "MC-42 / spec §2.3")
	imapPort, cl := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)
	appendRaw(t, cl, "INBOX", []byte(mismatchedExtHTMLRaw), nil)

	part := mailDo(env.mux, http.MethodGet, msgPath+"/attachments/1", nextMailIP(), true, "")
	if part.Code == http.StatusNotFound {
		part = mailDo(env.mux, http.MethodGet, msgPath+"/attachments/2", nextMailIP(), true, "")
	}
	if part.Code != http.StatusOK {
		t.Fatalf("MC-42: attachment download = %d, want 200. body=%s", part.Code, part.Body.String())
	}

	// The part self-declares application/octet-stream; the filename's
	// extension is .html. MC-42/Hard Constraint #6 requires the EXTENSION
	// to decide — the served type must be text/html, never
	// application/octet-stream, precisely BECAUSE the two disagree.
	ct := part.Header().Get("Content-Type")
	if ct != "text/html" && ct != "text/html; charset=utf-8" {
		t.Fatalf("MC-42: extension-derived content type = %q, want text/html — the part self-declared "+
			"application/octet-stream for a *.html-named attachment; the EXTENSION must win, never the "+
			"part's self-declared type, when the two disagree", ct)
	}
	disp := part.Header().Get("Content-Disposition")
	if disp == "" {
		t.Fatal("MC-42: Content-Disposition missing")
	}
	// Never inline regardless of the (wrong) self-declared type.
	if part.Header().Get("Content-Type") == "application/octet-stream" {
		t.Fatal("MC-42: the self-declared type must never override the dangerous .html extension")
	}
}
