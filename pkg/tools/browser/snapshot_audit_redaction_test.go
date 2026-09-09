// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

// This file holds the ONE test that stands between a page's field values and
// the audit log, and it is deliberately separate from snapshot_test.go so it
// is hard to delete by accident.
//
// The property is FR-028's, and the reason it matters is ADR-075's own design:
// every agent on a workspace drives the OPERATOR'S browser, with the
// operator's live logins in it. browser_snapshot renders field VALUES
// unconditionally by operator ruling (FR-018), so the rendered text routinely
// contains a password, a card number or a session token. The audit log is a
// file whose whole purpose is to be retained and read later. Copying the one
// into the other would turn a debugging aid into a durable credential store.
//
// The redaction here is STRUCTURAL, not a filter: recordSnapshot receives the
// whole snapshotRender — text included — and puts only counts and an origin
// into Details. There is no pattern list to get wrong and nothing to tune;
// the guarantee is that a particular field is never read. A structural
// guarantee is exactly the kind that a one-line "add the text for debugging"
// change removes silently, which is why the assertion below is over the RAW
// BYTES of the written record rather than over named fields.

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// pageSecrets are the values planted on the fake page. Each is
// secret-SHAPED — a real password, a real-format PAN, a bearer token — so a
// leak is recognisable in the failure output as the thing it would be in a
// customer's audit file.
var pageSecrets = []string{
	"hunter2-Tr0ub4dor&3",
	"4111111111111111",
	"sk-live-9f2b7c41a8de",
}

// rawAuditBytes returns every byte the logger has written, as text. Asserting
// over this rather than over a decoded map is the point: a value smuggled into
// any field, at any nesting depth, under any key, still reds.
func rawAuditBytes(t *testing.T, dir string) string {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err, "the audit file must exist — a test that finds no file would pass this file's every assertion vacuously")
	defer func() { _ = f.Close() }()

	var b strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		b.WriteString(sc.Text())
		b.WriteString("\n")
	}
	require.NoError(t, sc.Err())
	return b.String()
}

// secretBearingSnapshot renders a login/checkout page whose fields hold every
// value in pageSecrets, and asserts they really are in the AGENT'S copy.
//
// That precondition is load-bearing. Without it a build that stopped emitting
// values altogether — FR-018 reversed — would satisfy "the secret is not in
// the audit record" while having proved nothing about the audit record at all.
func secretBearingSnapshot(t *testing.T) snapshotRender {
	t.Helper()
	nodes := []*accessibility.Node{
		axNode("1", "RootWebArea", "Checkout", "", false, "2", "3", "4", "5"),
		axNode("2", "textbox", "Password", pageSecrets[0], false),
		axNode("3", "textbox", "Card number", pageSecrets[1], false),
		axNode("4", "textbox", "API key", pageSecrets[2], false),
		axNode("5", "button", "Pay now", "", false),
	}
	render := renderSnapshot(nodes)
	for _, secret := range pageSecrets {
		require.Contains(t, render.Text, secret,
			"the snapshot the AGENT receives must contain %q, or this test proves nothing about the "+
				"audit record: with no secret in the render there is no secret to leak. Values are "+
				"emitted unconditionally by operator ruling (FR-018)", secret)
	}
	require.Equal(t, 3, render.ValueNodes)
	return render
}

// TestSnapshotAudit_PageSecretsNeverReachTheAuditRecord drives the REAL
// emitter — SnapshotTool.recordSnapshot — into a REAL audit.Logger writing a
// REAL audit.jsonl, with a render that genuinely carries three secrets, and
// asserts none of them appears anywhere in the bytes on disk.
//
// Everything here is the production path except the CDP call that would have
// produced the nodes: renderSnapshot is the real renderer, recordSnapshot is
// the real emitter, audit.Logger is the real writer with its real Entry
// serialisation and HMAC chaining.
func TestSnapshotAudit_PageSecretsNeverReachTheAuditRecord(t *testing.T) {
	h := newAuditHarness(t)
	tool := &SnapshotTool{}
	tool.SetAuditLogger(h.log)

	render := secretBearingSnapshot(t)

	tool.recordSnapshot(
		auditToolCtx("ray"),
		browserTestKey("audit-redaction"),
		TabOwnerWorkspace(),
		"https://shop.example.com",
		render,
	)
	require.NoError(t, h.log.Close(), "flush the logger before reading its file")

	raw := rawAuditBytes(t, h.dir)

	// (1) A record was actually written. Without this the whole test is
	// satisfied by an emitter that logs nothing — the classic way a redaction
	// test goes green for the wrong reason.
	require.Contains(t, raw, audit.EventBrowserSnapshot,
		"no browser_snapshot record was written at all, so the absence of the secrets below proves nothing")

	// (2) The record carries the METADATA an operator needs, so "it leaked
	// nothing" is not achieved by recording nothing useful.
	for _, want := range []string{
		`"page_origin":"https://shop.example.com"`,
		`"value_nodes_emitted":3`,
		`"workspace_id":"audit-redaction"`,
	} {
		require.Contains(t, raw, want,
			"the browser_snapshot record must still answer 'a capture happened, of this shape, on this "+
				"origin' — %s is missing", want)
	}

	// (3) THE PROPERTY. None of the page's field values is anywhere in the
	// bytes on disk.
	for _, secret := range pageSecrets {
		require.NotContains(t, raw, secret,
			"a value read off the OPERATOR'S logged-in page reached the audit log: %q.\n\n"+
				"Under ADR-075 every agent on a workspace drives the operator's own browser, so this is "+
				"their password/card/token, copied into a file that is retained by design and read by "+
				"whoever can read the audit trail. FR-028 makes the browser_snapshot event METADATA "+
				"ONLY: recordSnapshot receives the whole render and must put only counts and an origin "+
				"in Details.", secret)
	}

	// (4) Not the whole rendered text either, under any key. The three secrets
	// above are the recognisable case; this catches the general one, since the
	// outline also names every field on the page.
	require.NotContains(t, raw, `textbox "Password"`,
		"the rendered accessibility outline itself reached the audit record. Even without a field value "+
			"it enumerates the operator's page; the event is metadata only")
}
