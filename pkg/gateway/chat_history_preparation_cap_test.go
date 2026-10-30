package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// #1452 A2.8 (founder decision 2026-10-30): at most two history page
// preparations run at once per process. A third waits for a slot, is not
// refused, and gives up only when its own request ends.
func TestHistoryPagePreparation_CapOfTwoWaitsAndCancels(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	s := api.agentLoop.GetSessionStore()
	sid := createTestSession(t, api)
	require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: "cap-row", Role: "user", AgentID: "mia", Content: "hello", Timestamp: time.Date(2026, 10, 30, 5, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat}))
	require.NoError(t, s.Close())

	entered := make(chan struct{}, 64)
	release := make(chan struct{})
	prev := historyPageFaultHook
	historyPageFaultHook = func(e historyPageFaultEvent) {
		if e.Point == "after_weigh" && e.SessionID == sid {
			entered <- struct{}{}
			<-release
		}
	}
	t.Cleanup(func() { historyPageFaultHook = prev })
	read := func(ctx context.Context) error {
		r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20}, historyPageProjectionDeps{ToolStore: newToolResultStore(api.homePath)})
		if err != nil {
			return err
		}
		_, err = r.Read(ctx, s)
		return err
	}
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- read(context.Background()) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(30 * time.Second):
			t.Fatal("positive control: two preparations run at the same time")
		}
	}
	require.Len(t, historyPreparationSlots, historyPreparationLimit, "both slots are held")

	// A third request waits: it never reaches the store while both slots are held.
	ctx, cancel := context.WithCancel(context.Background())
	third := make(chan error, 1)
	go func() { third <- read(ctx) }()
	select {
	case <-entered:
		t.Fatal("a third preparation ran while two were in progress")
	case err := <-third:
		t.Fatalf("a waiting request must not be refused, got %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-third:
		require.True(t, errors.Is(err, context.Canceled), "the waiter ends with its own request's cancellation, got %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("a cancelled waiter must stop waiting")
	}

	// A fourth request waits and then completes once a slot frees.
	wg.Add(1)
	go func() { defer wg.Done(); results <- read(context.Background()) }()
	select {
	case <-entered:
		t.Fatal("the fourth preparation ran while two were in progress")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	close(results)
	n := 0
	for err := range results {
		require.NoError(t, err)
		n++
	}
	require.Equal(t, 3, n)
	require.Len(t, historyPreparationSlots, 0, "every slot is released")
}
