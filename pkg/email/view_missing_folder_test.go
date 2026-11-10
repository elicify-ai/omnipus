package email

import (
	"context"
	"errors"
	"testing"
)

// Oracle: docs/mail.md "If something goes wrong" — a mailbox that cannot be read
// shows an error with Retry, and "Select a mailbox to load its folders". A mailbox
// whose Sent (or Drafts) folder does not exist must still open: the folder shows
// zero messages instead of failing the whole panel (founder report 2026-10-01:
// 37 of 39 live failures were `status sent (Sent): NONEXISTENT`).

func TestFolderCounts_MissingSentFolderStillOpensMailbox(t *testing.T) {
	cl, _ := startViewIMAP(t, "inbox")
	cl.acct.SentFolder = "NoSuchSentFolder"

	stats, err := cl.FolderCounts(context.Background())
	if err != nil {
		t.Fatalf("FolderCounts must not fail when the Sent folder is missing: %v", err)
	}
	if len(stats) != 3 {
		t.Fatalf("stats = %d, want 3 (inbox, sent, drafts): %+v", len(stats), stats)
	}
	by := map[string]FolderStat{}
	for _, s := range stats {
		by[s.Slug] = s
	}
	if by[FolderInbox].Total != 1 || by[FolderInbox].UIDValidity == 0 {
		t.Fatalf("inbox must keep its real counts: %+v", by[FolderInbox])
	}
	if by[FolderSent].Total != 0 || by[FolderSent].Unseen != 0 || by[FolderSent].DisplayName != "NoSuchSentFolder" {
		t.Fatalf("missing sent folder must report 0 messages under its configured name: %+v", by[FolderSent])
	}
}

func TestFolderCounts_MissingDraftsFolderStillOpensMailbox(t *testing.T) {
	cl, _ := startViewIMAP(t, "inbox")
	cl.acct.DraftsFolder = "NoSuchDrafts"

	stats, err := cl.FolderCounts(context.Background())
	if err != nil {
		t.Fatalf("FolderCounts must not fail when the Drafts folder is missing: %v", err)
	}
	by := map[string]FolderStat{}
	for _, s := range stats {
		by[s.Slug] = s
	}
	if len(stats) != 3 || by[FolderDrafts].Total != 0 || by[FolderInbox].Total != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

// Negative control: only a missing folder is softened. A cancelled request must
// still fail loudly (it is not a folder problem) — and it must fail from the
// cancellation itself, never from the FR-W1-2 wiring refusal, which would let
// this control pass without exercising cancellation at all.
func TestFolderCounts_CancelledRequestStillFails(t *testing.T) {
	cl, _ := startViewIMAP(t, "inbox")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cl.FolderCounts(ctx); err == nil {
		t.Fatal("a cancelled FolderCounts must return an error, not empty stats")
	} else if errors.Is(err, ErrSessionSourceMissing) {
		t.Fatalf("cancelled FolderCounts failed with the wiring refusal, not the cancellation: %v", err)
	}
}
