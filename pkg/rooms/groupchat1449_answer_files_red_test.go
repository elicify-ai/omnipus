// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Answer-file oracles: DS-1 row 32 (A-4), DS-12 rows 16–19 (A-9, TD-3, TD-4)
// and S-152. 100 MB is 100 × 1024 × 1024 bytes, the upload part limit.

const groupChatFileBytes = int64(100 << 20)

type countingMedia struct {
	mu       sync.Mutex
	resolves int
	held     map[string]int // owner + "\x00" + ref -> live retains
}

func (m *countingMedia) ResolveAttachment(workspaceID, ref string) (generated.MediaPart, error) {
	m.mu.Lock()
	m.resolves++
	m.mu.Unlock()
	id := strings.TrimPrefix(ref, "media://workspace/"+workspaceID+"/")
	return generated.MediaPart{
		Type: "file", Filename: id + ".txt", ContentType: "text/plain",
		Url: "/api/v1/media/workspace/" + workspaceID + "/" + id,
	}, nil
}

func (m *countingMedia) Retain(_, ref, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held == nil {
		m.held = map[string]int{}
	}
	m.held[owner+"\x00"+ref]++
	return nil
}

func (m *countingMedia) Release(_, ref, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := owner + "\x00" + ref
	if m.held[key] > 0 {
		m.held[key]--
	}
	return nil
}

func (m *countingMedia) liveOwners(ref string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for key, count := range m.held {
		if strings.HasSuffix(key, "\x00"+ref) && count > 0 {
			n++
		}
	}
	return n
}

type noopPublisher struct{}

func (noopPublisher) PublishAnswerFile(string, PublishFile) (generated.MediaPart, string, error) {
	return generated.MediaPart{}, "", errors.New("publish was not part of this case")
}

func (noopPublisher) UnpublishAnswerFile(string, PublishFile) error { return nil }

func newGroupChatFileFixture(t *testing.T) (*fixture, *countingMedia) {
	t.Helper()
	dir := t.TempDir()
	deps := newFakeDeps()
	media := &countingMedia{}
	svc := New(NewFileJournal(dir), Deps{
		Directory: deps, Receivers: deps, Effects: deps, Media: media, Publisher: noopPublisher{},
	})
	svc.SetStagingDir(t.TempDir())
	return &fixture{t: t, dir: dir, deps: deps, svc: svc}, media
}

func groupChatRef(id string) string { return "media://workspace/sales/" + id }

func TestGroupChat1449_DuplicateMediaRefIsListedTwiceAndRetainedOnce(t *testing.T) {
	// DS-1 row 32, A-4, TD-4. The same reference twice is accepted as listed.
	f, media := newGroupChatFileFixture(t)
	room := f.squad("mia", "jim")
	ref := groupChatRef("note")
	refs := []string{ref, ref}
	err := f.svc.PostMessage(room.Id, generated.RoomMessageRequest{
		ClientMessageId: "dup", Text: "see both", Mentions: []string{}, Media: &refs,
	}, "admin")
	if err != nil {
		t.Fatalf("DS-1 row 32: duplicate ref was refused: %v", err)
	}
	f.drain(room.Id)
	page, err := f.svc.History(room.Id, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found *generated.RoomEntry
	for i := range page.Entries {
		if page.Entries[i].Text == "see both" {
			found = &page.Entries[i]
		}
	}
	if found == nil || len(found.Attachments) != 2 || found.Attachments[0].Url != found.Attachments[1].Url {
		t.Fatalf("entry attachments = %+v, want the same file listed twice", found)
	}
	if media.resolves != 2 {
		t.Fatalf("resolved %d times, want both listings (no silent de-duplication)", media.resolves)
	}
	if media.liveOwners(ref) != 1 {
		t.Fatalf("retention owners of %s = %d, want one per (entry, reference)", ref, media.liveOwners(ref))
	}
}

func TestGroupChat1449_AnswerListsTheSameFileTwice(t *testing.T) {
	// DS-12 row 16, A-9, TD-4.
	f, media := newGroupChatFileFixture(t)
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "ask", "@Jim the file"); err != nil {
		t.Fatal(err)
	}
	jim := f.deliveryOf(room.Id, "jim")
	ref := groupChatRef("shot")
	_, err := f.svc.DeliverAnswer(context.Background(), Answer{
		RoomID: room.Id, RequestID: jim.RequestID,
		Responder:          addressing.Pair{WorkspaceID: "sales", AgentID: "jim"},
		ResponderSessionID: "main-jim",
		Content:            "both copies",
		Files:              []AnswerFile{{Ref: ref, Filename: "shot.txt"}, {Ref: ref, Filename: "shot.txt"}},
	})
	if err != nil {
		t.Fatalf("DS-12 row 16: duplicate answer file was refused: %v", err)
	}
	page, err := f.svc.History(room.Id, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found *generated.RoomEntry
	for i := range page.Entries {
		if page.Entries[i].Text == "both copies" {
			found = &page.Entries[i]
		}
	}
	if found == nil || len(found.Attachments) != 2 {
		t.Fatalf("answer attachments = %+v, want two listed attachments", found)
	}
	if media.liveOwners(ref) != 1 {
		t.Fatalf("retention owners = %d, want one per (entry, reference)", media.liveOwners(ref))
	}
}

func TestGroupChat1449_AnswerFileCountAndSizeBounds(t *testing.T) {
	// DS-12 rows 17–19, TD-3. 16 accepted. 17 and 100 MB + 1 byte are
	// definitive refusals before acceptance: nothing posted, nothing staged.
	t.Run("16 files are accepted", func(t *testing.T) {
		f, _ := newGroupChatFileFixture(t)
		room, requestID := groupChatJimRequest(t, f)
		files := make([]AnswerFile, 16)
		for i := range files {
			files[i] = AnswerFile{Ref: groupChatRef("f" + string(rune('a'+i)))}
		}
		if _, err := f.svc.DeliverAnswer(context.Background(), groupChatAnswer(room.Id, requestID, "sixteen", files)); err != nil {
			t.Fatalf("DS-12 row 17: 16 files were refused: %v", err)
		}
		if got := groupChatAttachmentCount(t, f, room.Id, "sixteen"); got != 16 {
			t.Fatalf("16-file answer shows %d attachments", got)
		}
		if !groupChatStagingEmpty(t, f.svc.stagingDir) {
			t.Fatal("TD-3: staged bytes were kept after the answer was committed")
		}
	})
	t.Run("17 files are refused before acceptance", func(t *testing.T) {
		f, media := newGroupChatFileFixture(t)
		room, requestID := groupChatJimRequest(t, f)
		before := groupChatEntryCount(t, f, room.Id)
		files := make([]AnswerFile, 17)
		for i := range files {
			files[i] = AnswerFile{Ref: groupChatRef("f" + string(rune('a'+i)))}
		}
		_, err := f.svc.DeliverAnswer(context.Background(), groupChatAnswer(room.Id, requestID, "seventeen", files))
		if !errors.Is(err, ErrAnswerFilesRefused) {
			t.Fatalf("DS-12 row 18: err = %v, want a definitive refusal", err)
		}
		if media.resolves != 0 {
			t.Fatal("17 files were resolved; the refusal must happen before acceptance")
		}
		if groupChatEntryCount(t, f, room.Id) != before {
			t.Fatal("17 files posted an entry")
		}
		if !groupChatStagingEmpty(t, f.svc.stagingDir) {
			t.Fatal("17 files left staged bytes")
		}
	})
	t.Run("100 MB plus one byte is refused before it is read", func(t *testing.T) {
		f, _ := newGroupChatFileFixture(t)
		room, requestID := groupChatJimRequest(t, f)
		before := groupChatEntryCount(t, f, room.Id)
		opened := false
		_, err := f.svc.DeliverAnswer(context.Background(), groupChatAnswer(room.Id, requestID, "too-big", []AnswerFile{{
			Identity: "huge.bin", Filename: "huge.bin", Size: groupChatFileBytes + 1,
			Open: func() (io.ReadCloser, error) {
				opened = true
				return io.NopCloser(strings.NewReader("x")), nil
			},
		}}))
		if !errors.Is(err, ErrAnswerFilesRefused) || opened {
			t.Fatalf("DS-12 row 19: err = %v opened = %v, want a refusal before the file is read", err, opened)
		}
		if !groupChatStagingEmpty(t, f.svc.stagingDir) {
			t.Fatal("an over-size file was staged")
		}
		if groupChatEntryCount(t, f, room.Id) != before {
			t.Fatal("an over-size file posted an entry")
		}
	})
}

func groupChatJimRequest(t *testing.T, f *fixture) (generated.Room, string) {
	t.Helper()
	room := f.squad("mia", "jim")
	if err := f.post(room.Id, "ask", "@Jim files"); err != nil {
		t.Fatal(err)
	}
	return room, f.deliveryOf(room.Id, "jim").RequestID
}

func TestGroupChat1449_StoredEntryLongerThanTodaysLimitStillOpens(t *testing.T) {
	// S-152, TD-8. A 100,000-byte entry stored when steer_body was 131,072
	// still opens and is shown in full. A new message of that size is refused.
	f := newFixture(t)
	f.deps.body = 131072
	room := f.squad("mia", "jim")
	stored := strings.Repeat("a", 100000)
	r, _, err := f.svc.openRoom(room.Id)
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	err = f.svc.appendEntry(r, storedEntry{Entry: generated.RoomEntry{
		Id: "stored-before", RoomId: room.Id, Text: stored,
		Author: generated.RoomEntryAuthor{Kind: "user"}, Addressees: []string{},
		Timestamp: "2026-10-28T00:00:00Z",
	}}, nil)
	r.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.svc.History(room.Id, nil, 100)
	if err != nil {
		t.Fatalf("S-152: reading the old entry was refused: %v", err)
	}
	shown := ""
	for _, entry := range page.Entries {
		if entry.Id == "stored-before" {
			shown = entry.Text
		}
	}
	if shown != stored {
		t.Fatalf("S-152: shown length %d, want the full 100000 bytes", len(shown))
	}
	before := groupChatEntryCount(t, f, room.Id)
	postErr := f.post(room.Id, "new-long", strings.Repeat("b", 100000))
	groupChatRequireStatus(t, postErr, 422, "65536")
	if groupChatEntryCount(t, f, room.Id) != before {
		t.Fatal("S-152: a new over-limit message was stored")
	}
}

func groupChatAnswer(roomID, requestID, text string, files []AnswerFile) Answer {
	return Answer{
		RoomID: roomID, RequestID: requestID,
		Responder:          addressing.Pair{WorkspaceID: "sales", AgentID: "jim"},
		ResponderSessionID: "main-jim",
		Content:            text,
		Files:              files,
	}
}

func groupChatAttachmentCount(t *testing.T, f *fixture, roomID, text string) int {
	t.Helper()
	page, err := f.svc.History(roomID, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page.Entries {
		if entry.Text == text {
			return len(entry.Attachments)
		}
	}
	t.Fatalf("no entry %q", text)
	return 0
}

func groupChatStagingEmpty(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries) == 0
}
