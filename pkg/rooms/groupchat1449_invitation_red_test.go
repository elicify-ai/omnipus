// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Invitation oracles: docs/internal/specs/1449-group-chat-spec.md DS-4,
// FR-039, FR-046, TD-1, TD-2, TD-9. The model is the stubbed edge. The room
// service decides status, truncation, delivery and the group's cost.
//
// Not covered here: DS-4 rows 15–21 (reservation races against receiver
// facts). They need the capture ledger, not the summary job.

const (
	groupChatSummaryReady = "about the launch"
	// 0.02 dollars is DS-4 row 14. One dollar is 1_000_000 micro-dollars,
	// the same unit §6.14 uses (one cent is 10_000). The whole call is the
	// group's (FR-046), so the group total is 20_000.
	groupChatSummaryMicro = int64(20000)
)

type scriptedSummarizer struct {
	fn func(context.Context, InvitationRequest) (InvitationSummary, error)
}

func (s scriptedSummarizer) SummarizeInvitation(ctx context.Context, req InvitationRequest) (InvitationSummary, error) {
	return s.fn(ctx, req)
}

func newGroupChatInvitationFixture(t *testing.T, sum Summarizer) (*fixture, string) {
	t.Helper()
	dir := t.TempDir()
	deps := newFakeDeps()
	origin, err := session.MainSessionID("sales", "mia")
	if err != nil {
		t.Fatal(err)
	}
	deps.extraSessions = map[string]bool{origin: true}
	svc := New(NewFileJournal(dir), Deps{Directory: deps, Receivers: deps, Effects: deps, Summarizer: sum})
	return &fixture{t: t, dir: dir, deps: deps, svc: svc}, origin
}

func createAdhocFromChat(t *testing.T, f *fixture, origin string, members ...string) generated.Room {
	t.Helper()
	name := "Launch"
	room, err := f.svc.Create("sales", generated.RoomCreateRequest{
		Kind: "adhoc", Name: &name, AgentIds: members, OriginSessionId: &origin,
	}, "admin")
	if err != nil {
		t.Fatalf("create ad-hoc group: %v", err)
	}
	return room
}

func groupChatInvitation(t *testing.T, f *fixture, roomID string) generated.Room {
	t.Helper()
	got, err := f.svc.Get(roomID)
	if err != nil {
		t.Fatalf("get room: %v", err)
	}
	return got
}

func TestGroupChat1449_InvitationSummaryTimeLimit(t *testing.T) {
	// DS-4 rows 1–3. 44s and 45s are used. 46s is discarded.
	cases := []struct {
		name   string
		delay  time.Duration
		status string
	}{
		{name: "44s is ready", delay: 44 * time.Second, status: InvitationReady},
		{name: "45s is ready", delay: 45 * time.Second, status: InvitationReady},
		{name: "46s is unavailable and discarded", delay: 46 * time.Second, status: InvitationUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
			sum := scriptedSummarizer{fn: func(_ context.Context, req InvitationRequest) (InvitationSummary, error) {
				if req.MaxEntries != 40 || req.MaxBytes != 16384 || req.WorkspaceID != "sales" {
					t.Errorf("summary request = %+v, want 40 entries, 16384 bytes, workspace sales (FR-039)", req)
				}
				mu.Lock()
				now = now.Add(tc.delay)
				mu.Unlock()
				return InvitationSummary{Text: groupChatSummaryReady, Called: true, CostUSD: 0.02, Priced: true}, nil
			}}
			f, origin := newGroupChatInvitationFixture(t, sum)
			f.svc.SetClockForTest(func() time.Time {
				mu.Lock()
				defer mu.Unlock()
				return now
			})
			room := createAdhocFromChat(t, f, origin, "mia", "jim")
			f.svc.WaitIdleForTest()
			got := groupChatInvitation(t, f, room.Id)
			if got.InvitationContextStatus == nil || string(*got.InvitationContextStatus) != tc.status {
				t.Fatalf("status = %v, want %s", got.InvitationContextStatus, tc.status)
			}
			if tc.status == InvitationReady {
				if got.InvitationContext == nil || got.InvitationContext.Text == nil || *got.InvitationContext.Text != groupChatSummaryReady {
					t.Fatalf("stored summary = %+v, want %q", got.InvitationContext, groupChatSummaryReady)
				}
				if got.AllocatedCostMicroUsd != groupChatSummaryMicro || got.AllocatedCostUnpriced {
					t.Fatalf("cost = %d unpriced=%v, want %d priced once (FR-046)", got.AllocatedCostMicroUsd, got.AllocatedCostUnpriced, groupChatSummaryMicro)
				}
				return
			}
			if got.InvitationContext != nil && got.InvitationContext.Text != nil && *got.InvitationContext.Text != "" {
				t.Fatalf("a late summary was stored: %q", *got.InvitationContext.Text)
			}
			if got.AllocatedCostMicroUsd != 0 || got.AllocatedCostUnpriced {
				t.Fatalf("a discarded summary counted cost %d unpriced=%v", got.AllocatedCostMicroUsd, got.AllocatedCostUnpriced)
			}
		})
	}
}

func TestGroupChat1449_InvitationSummaryIsCutAt4096AndCostsOnce(t *testing.T) {
	// TD-1, DS-4 rows 12, 13, 13b and 14. A cut summary stays ready and the
	// 0.02 dollar cost is counted once, including across a reload.
	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "4096 is stored whole", text: strings.Repeat("a", 4096), want: strings.Repeat("a", 4096)},
		{name: "4097 cuts at the last word", text: strings.Repeat("a", 4090) + " " + strings.Repeat("b", 6), want: strings.Repeat("a", 4090)},
		{name: "4200 with no space is a hard cut", text: strings.Repeat("x", 4200), want: strings.Repeat("x", 4096)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sum := scriptedSummarizer{fn: func(context.Context, InvitationRequest) (InvitationSummary, error) {
				return InvitationSummary{Text: tc.text, Called: true, CostUSD: 0.02, Priced: true}, nil
			}}
			f, origin := newGroupChatInvitationFixture(t, sum)
			room := createAdhocFromChat(t, f, origin, "mia", "jim")
			f.svc.WaitIdleForTest()
			got := groupChatInvitation(t, f, room.Id)
			if got.InvitationContextStatus == nil || string(*got.InvitationContextStatus) != InvitationReady {
				t.Fatalf("status = %v, want ready", got.InvitationContextStatus)
			}
			if got.InvitationContext == nil || got.InvitationContext.Text == nil || *got.InvitationContext.Text != tc.want {
				gotText := ""
				if got.InvitationContext != nil && got.InvitationContext.Text != nil {
					gotText = *got.InvitationContext.Text
				}
				t.Fatalf("stored summary length %d, want %d", len(gotText), len(tc.want))
			}
			if got.AllocatedCostMicroUsd != groupChatSummaryMicro {
				t.Fatalf("cost = %d, want %d counted once", got.AllocatedCostMicroUsd, groupChatSummaryMicro)
			}
			reloaded := New(NewFileJournal(f.dir), Deps{Directory: f.deps, Receivers: f.deps, Effects: f.deps})
			again, err := reloaded.Get(room.Id)
			if err != nil {
				t.Fatal(err)
			}
			if again.AllocatedCostMicroUsd != groupChatSummaryMicro {
				t.Fatalf("reloaded cost = %d, want %d (a second write must not double it)", again.AllocatedCostMicroUsd, groupChatSummaryMicro)
			}
		})
	}
}

func TestGroupChat1449_InvitationSummaryUnavailableWritesNoCost(t *testing.T) {
	// DS-4 row 4 and DS-5 row 10 / TD-2. No usable input and a provider
	// error both leave the summary unavailable and write no cost line.
	cases := []struct {
		name string
		give InvitationSummary
		err  error
	}{
		{name: "nothing to summarise", give: InvitationSummary{Called: false}},
		{name: "provider error", give: InvitationSummary{Called: true, CostUSD: 0.02, Priced: true}, err: context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sum := scriptedSummarizer{fn: func(context.Context, InvitationRequest) (InvitationSummary, error) {
				return tc.give, tc.err
			}}
			f, origin := newGroupChatInvitationFixture(t, sum)
			room := createAdhocFromChat(t, f, origin, "mia", "jim")
			f.svc.WaitIdleForTest()
			got := groupChatInvitation(t, f, room.Id)
			if got.InvitationContextStatus == nil || string(*got.InvitationContextStatus) != InvitationUnavailable {
				t.Fatalf("status = %v, want unavailable", got.InvitationContextStatus)
			}
			if got.AllocatedCostMicroUsd != 0 || got.AllocatedCostUnpriced {
				t.Fatalf("cost = %d unpriced=%v, want no cost line", got.AllocatedCostMicroUsd, got.AllocatedCostUnpriced)
			}
		})
	}
}

func TestGroupChat1449_InvitationUnpricedCallSetsTheFlagOnly(t *testing.T) {
	// T-21 unpriced flag. An unpriced call adds no dollar amount (§6.14).
	sum := scriptedSummarizer{fn: func(context.Context, InvitationRequest) (InvitationSummary, error) {
		return InvitationSummary{Text: groupChatSummaryReady, Called: true, CostUSD: 0.02, Priced: false}, nil
	}}
	f, origin := newGroupChatInvitationFixture(t, sum)
	room := createAdhocFromChat(t, f, origin, "mia", "jim")
	f.svc.WaitIdleForTest()
	got := groupChatInvitation(t, f, room.Id)
	if !got.AllocatedCostUnpriced || got.AllocatedCostMicroUsd != 0 {
		t.Fatalf("cost = %d unpriced=%v, want 0 and the unpriced flag", got.AllocatedCostMicroUsd, got.AllocatedCostUnpriced)
	}
}

func TestGroupChat1449_InvitationCarriesOnceToOtherMembersOnly(t *testing.T) {
	// DS-4 rows 9–11. The non-origin member's first request carries the
	// summary once. The origin agent never receives it. The next message does not.
	const summary = "ORACLE-SUMMARY-TEXT"
	sum := scriptedSummarizer{fn: func(context.Context, InvitationRequest) (InvitationSummary, error) {
		return InvitationSummary{Text: summary, Called: true, CostUSD: 0.02, Priced: true}, nil
	}}
	f, origin := newGroupChatInvitationFixture(t, sum)
	room := createAdhocFromChat(t, f, origin, "mia", "jim")
	f.svc.WaitIdleForTest()
	if err := f.post(room.Id, "first", "first ping"); err != nil {
		t.Fatal(err)
	}
	if err := f.post(room.Id, "second", "second ping"); err != nil {
		t.Fatal(err)
	}
	f.deps.mu.Lock()
	admits := append([]Admission(nil), f.deps.admits...)
	f.deps.mu.Unlock()
	var jimFirst, jimSecond, miaFirst int
	for _, a := range admits {
		switch {
		case a.Receiver.AgentID == "jim" && strings.Contains(a.DisplayText, "first ping"):
			jimFirst++
			// FR-043: quoted background. The marker is the scenario's word
			// "quoted", not one parenthetical from the current prefix.
			if !strings.Contains(a.ModelText, summary) || !strings.Contains(a.ModelText, "quoted") {
				t.Fatalf("Jim's first request did not carry the summary as quoted background: %q", a.ModelText)
			}
		case a.Receiver.AgentID == "jim" && strings.Contains(a.DisplayText, "second ping"):
			jimSecond++
			if strings.Contains(a.ModelText, summary) {
				t.Fatal("Jim's second request carried the summary again")
			}
		case a.Receiver.AgentID == "mia" && strings.Contains(a.ModelText, summary):
			miaFirst++
		}
	}
	if jimFirst != 1 || jimSecond != 1 {
		t.Fatalf("Jim admissions first=%d second=%d, want one of each", jimFirst, jimSecond)
	}
	if miaFirst != 0 {
		t.Fatal("the origin agent received the summary")
	}
}

func TestGroupChat1449_FirstMessageWhileGeneratingIsNotHeld(t *testing.T) {
	// DS-4 row 8. The first message is delivered at once, with no summary,
	// and the summary is never added to that request later.
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	sum := scriptedSummarizer{fn: func(context.Context, InvitationRequest) (InvitationSummary, error) {
		once.Do(func() { close(started) })
		<-release
		return InvitationSummary{Text: "LATE-SUMMARY", Called: true, CostUSD: 0.02, Priced: true}, nil
	}}
	f, origin := newGroupChatInvitationFixture(t, sum)
	f.deps.entered = make(chan string, 8)
	room := createAdhocFromChat(t, f, origin, "mia", "jim")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the summary job did not start")
	}
	if err := f.svc.PostMessage(room.Id, generated.RoomMessageRequest{ClientMessageId: "while-generating", Text: "do not wait", Mentions: []string{}}, "admin"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.deps.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("DS-4 row 8: the first message was held until the summary finished")
	}
	close(release)
	f.svc.WaitIdleForTest()
	f.deps.mu.Lock()
	defer f.deps.mu.Unlock()
	carried := 0
	for _, a := range f.deps.admits {
		if strings.Contains(a.DisplayText, "do not wait") {
			if strings.Contains(a.ModelText, "LATE-SUMMARY") {
				carried++
			}
		}
	}
	if carried != 0 {
		t.Fatalf("the message sent while generating carried the summary %d times", carried)
	}
}

func TestGroupChat1449_RestartDiscardsALateInvitationSummary(t *testing.T) {
	// DS-4 row 5. A restart while generating makes it unavailable, and a
	// result that arrives after that is discarded with no cost line.
	release := make(chan struct{})
	sum := scriptedSummarizer{fn: func(context.Context, InvitationRequest) (InvitationSummary, error) {
		<-release
		return InvitationSummary{Text: "late", Called: true, CostUSD: 0.02, Priced: true}, nil
	}}
	f, origin := newGroupChatInvitationFixture(t, sum)
	room := createAdhocFromChat(t, f, origin, "mia", "jim")
	fresh := New(NewFileJournal(f.dir), Deps{Directory: f.deps, Receivers: f.deps, Effects: f.deps, Summarizer: sum})
	got := groupChatInvitation(t, &fixture{t: t, dir: f.dir, deps: f.deps, svc: fresh}, room.Id)
	if got.InvitationContextStatus == nil || string(*got.InvitationContextStatus) != InvitationUnavailable {
		t.Fatalf("status after restart = %v, want unavailable", got.InvitationContextStatus)
	}
	fresh.commitInvitation(room.Id, InvitationSummary{Text: "late", Called: true, CostUSD: 0.02, Priced: true}, nil, time.Second)
	got = groupChatInvitation(t, &fixture{t: t, dir: f.dir, deps: f.deps, svc: fresh}, room.Id)
	if got.InvitationContextStatus == nil || string(*got.InvitationContextStatus) != InvitationUnavailable || got.AllocatedCostMicroUsd != 0 {
		t.Fatalf("late result after restart: status %v cost %d", got.InvitationContextStatus, got.AllocatedCostMicroUsd)
	}
	close(release)
	f.svc.WaitIdleForTest()
}

func TestGroupChat1449_DeletedGroupDiscardsTheInvitationSummary(t *testing.T) {
	// DS-4 row 6. Deleting the group while generating discards the result
	// and writes no cost line.
	release := make(chan struct{})
	sum := scriptedSummarizer{fn: func(context.Context, InvitationRequest) (InvitationSummary, error) {
		<-release
		return InvitationSummary{Text: "gone", Called: true, CostUSD: 0.02, Priced: true}, nil
	}}
	f, origin := newGroupChatInvitationFixture(t, sum)
	room := createAdhocFromChat(t, f, origin, "mia", "jim")
	if err := f.svc.Delete(room.Id, "admin"); err != nil {
		t.Fatal(err)
	}
	close(release)
	f.svc.WaitIdleForTest()
	if _, err := f.svc.Get(room.Id); StatusOf(err) != 404 {
		t.Fatalf("deleted group after a late summary: %v, want 404 and no cost line", err)
	}
}

func TestGroupChat1449_RemovingTheOriginAgentDiscardsTheSummary(t *testing.T) {
	// DS-4 row 7. Removing the starting agent while generating discards the result.
	release := make(chan struct{})
	sum := scriptedSummarizer{fn: func(context.Context, InvitationRequest) (InvitationSummary, error) {
		<-release
		return InvitationSummary{Text: "after removal", Called: true, CostUSD: 0.02, Priced: true}, nil
	}}
	f, origin := newGroupChatInvitationFixture(t, sum)
	room := createAdhocFromChat(t, f, origin, "mia", "jim", "r2")
	if _, err := f.svc.UpdateMembers(room.Id, generated.RoomMembersUpdateRequest{
		Action: generated.RoomMembersUpdateRequestActionRemove, AgentIds: []string{"mia"},
	}, "admin"); err != nil {
		t.Fatal(err)
	}
	close(release)
	f.svc.WaitIdleForTest()
	got := groupChatInvitation(t, f, room.Id)
	if got.InvitationContextStatus == nil || string(*got.InvitationContextStatus) != InvitationUnavailable || got.AllocatedCostMicroUsd != 0 {
		t.Fatalf("after the origin agent left: status %v cost %d", got.InvitationContextStatus, got.AllocatedCostMicroUsd)
	}
}
