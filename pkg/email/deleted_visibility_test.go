package email

import (
	"context"
	"testing"

	"github.com/emersion/go-imap/v2"
)

// US-7/MC-17: pagination is over visible drafts, not over physical UIDs.
// With no UIDPLUS the deleted middle UID remains on the IMAP server.
func TestReadFolderPage_ExcludesDeletedFromCursor(t *testing.T) {
	ctx := context.Background()
	cl, _ := startViewIMAP(t, "drafts", "drafts", "drafts")
	if err := cl.DeleteDraft(ctx, 2); err != nil {
		t.Fatalf("flag middle draft deleted: %v", err)
	}
	first, uv, trunc, err := cl.ReadFolderPage(ctx, FolderDrafts, 1, 0)
	if err != nil {
		t.Fatalf("first draft page: %v", err)
	}
	if len(first) != 1 || first[0].UID != 3 || !trunc {
		t.Fatalf("first draft page = %+v, truncated=%v; want UID 3 and another visible page", first, trunc)
	}
	second, uv2, more, err := cl.ReadFolderPage(ctx, FolderDrafts, 1, first[0].UID)
	if err != nil {
		t.Fatalf("second draft page: %v", err)
	}
	if len(second) != 1 || second[0].UID != 1 || more || uv2 != uv {
		t.Fatalf("second draft page = %+v, truncated=%v, uidvalidity=%d; want only UID 1, false, %d", second, more, uv2, uv)
	}
}

// Agent search and read-inbox return no \Deleted message; Search counts only
// visible hits, and fetchMessages rechecks flags before returning envelopes.
func TestReadInboxAndSearch_ExcludeDeletedMessages(t *testing.T) {
	ctx := context.Background()
	cl := startMemIMAP(t, [][]byte{
		mkMsg("Report old", "ada@box.test", "old body"),
		mkMsg("Report new", "ada@box.test", "new body"),
	}, nil)
	srv, _, err := cl.dialIMAP(ctx)
	if err != nil {
		t.Fatalf("dial to flag old inbox message: %v", err)
	}
	flags := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}, Silent: true}
	err = srv.Store(imap.UIDSetNum(1), flags, nil).Close()
	if err != nil {
		_ = srv.Close()
		t.Fatalf("flag old inbox message: %v", err)
	}
	if err = srv.Close(); err != nil {
		t.Fatalf("close flagging session: %v", err)
	}

	inbox, err := cl.ReadInbox(ctx, InboxOptions{Limit: 20})
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	if got := uidsOf(inbox); !eqUint32(got, []uint32{2}) {
		t.Fatalf("inbox UIDs = %v; want [2] (\x5cDeleted hidden)", got)
	}
	found, err := cl.Search(ctx, "Report", SearchOptions{Limit: 20})
	if err != nil {
		t.Fatalf("search inbox: %v", err)
	}
	if got := uidsOf(found.Messages); !eqUint32(got, []uint32{2}) || found.TotalMatches != 1 || found.Truncated {
		t.Fatalf("search UIDs=%v matches=%d truncated=%v; want [2], 1, false", got, found.TotalMatches, found.Truncated)
	}
}

// The newest sequence number may be a deferred-deleted message. The default
// Inbox path must keep looking backward to fill a page of visible messages.
func TestReadInbox_BackfillsPastDeletedNewest(t *testing.T) {
	ctx := context.Background()
	cl := startMemIMAP(t, [][]byte{
		mkMsg("first", "ada@box.test", "one"),
		mkMsg("second", "ada@box.test", "two"),
		mkMsg("third", "ada@box.test", "three"),
	}, nil)
	srv, _, err := cl.dialIMAP(ctx)
	if err != nil {
		t.Fatalf("dial to flag newest message: %v", err)
	}
	flags := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}, Silent: true}
	err = srv.Store(imap.UIDSetNum(3), flags, nil).Close()
	if err != nil {
		_ = srv.Close()
		t.Fatalf("flag newest message: %v", err)
	}
	if err = srv.Close(); err != nil {
		t.Fatalf("close flagging session: %v", err)
	}
	inbox, err := cl.ReadInbox(ctx, InboxOptions{Limit: 1})
	if err != nil {
		t.Fatalf("read single visible Inbox message: %v", err)
	}
	if got := uidsOf(inbox); !eqUint32(got, []uint32{2}) {
		t.Fatalf("Inbox UIDs = %v; want [2] after skipping deleted newest UID 3", got)
	}
}
