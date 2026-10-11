package session

import (
	"strings"
	"testing"
)

func TestTitleFromFirstMessage(t *testing.T) {
	long := strings.Repeat("é", 100)
	cases := []struct{ name, in, want string }{
		{"short stays whole", "Plan my week", "Plan my week"},
		{"trimmed", "  hello  \n", "hello"},
		{"empty", "   ", ""},
		{"exactly 60 runes kept", strings.Repeat("a", 60), strings.Repeat("a", 60)},
		{"61 runes cut to 57 plus ellipsis", strings.Repeat("a", 61), strings.Repeat("a", 57) + "..."},
		{"cut counts runes not bytes", long, strings.Repeat("é", 57) + "..."},
	}
	for _, c := range cases {
		if got := TitleFromFirstMessage(c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestMainSessionTitle(t *testing.T) {
	if got := MainSessionTitle("Jim"); got != "Jim - Main Session" {
		t.Fatalf("got %q", got)
	}
}

func TestSetTitleIfEmpty(t *testing.T) {
	us := newUnifiedStoreForTest(t)

	chat, err := us.NewSession(SessionTypeChat, "web", "jim")
	if err != nil {
		t.Fatalf("new chat: %v", err)
	}
	wrote, err := us.SetTitleIfEmpty(chat.ID, "Plan my week")
	if err != nil || !wrote {
		t.Fatalf("an untitled chat must take the title: wrote=%v err=%v", wrote, err)
	}
	if got, _ := us.GetMeta(chat.ID); got.Title != "Plan my week" {
		t.Fatalf("title not stored: %q", got.Title)
	}

	// The first title is final: a later reply must not replace it.
	if wrote, _ := us.SetTitleIfEmpty(chat.ID, "Something else"); wrote {
		t.Fatal("a titled session must not be re-titled")
	}

	// A title set by hand survives.
	hand, _ := us.NewSession(SessionTypeChat, "web", "jim")
	mine := "My own name"
	if err := us.SetMeta(hand.ID, MetaPatch{Title: &mine}); err != nil {
		t.Fatalf("hand title: %v", err)
	}
	if wrote, _ := us.SetTitleIfEmpty(hand.ID, "auto"); wrote {
		t.Fatal("a hand-set title must never be overwritten")
	}
	if got, _ := us.GetMeta(hand.ID); got.Title != mine {
		t.Fatalf("hand title lost: %q", got.Title)
	}

	// A main never takes an automatic title.
	main, err := us.GetOrCreateMainSession("ws-1", "jim")
	if err != nil {
		t.Fatalf("main: %v", err)
	}
	if wrote, _ := us.SetTitleIfEmpty(main.ID, "auto"); wrote {
		t.Fatal("a main session must not be auto-titled")
	}
	if got, _ := us.GetMeta(main.ID); got.Title != "" {
		t.Fatalf("main gained a stored title: %q", got.Title)
	}

	// A blank title is not a title.
	blank, _ := us.NewSession(SessionTypeChat, "web", "jim")
	if wrote, _ := us.SetTitleIfEmpty(blank.ID, "  "); wrote {
		t.Fatal("a blank title must not be written")
	}
}
