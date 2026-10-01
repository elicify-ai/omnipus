// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_hub_rebind_race_test.go — PR #1139 (subagent-control-plane) investigation:
// tests/e2e/steered-session-stop.spec.ts:389 observed a stuck
// data-status="running" activity row after stopping a steered child whose
// terminal subagent_end is published to the PARENT (root) session's hub
// (ADR-057 FR-011) while the single shared browser connection is mid-rebind
// between the root and child sessions ("Open" click, then back to root).
//
// Hypothesis under test (frontend-lead's CI trace: "sequence gap —
// re-attaching {sessionId: <root>, have: 61, got: 63}" firing right at the
// rebind): a connection rebinding away from root and back while a terminal
// frame is concurrently published to root's hub can permanently lose that
// frame — never delivered live (not bound at publish time) AND never
// delivered by a later catch-up (if the client's tracked cursor is wrong).
//
// This hammers exactly that: one goroutine publishes directly through the
// ROOT session hub (the same hubPublishMetaAlsoTo -> publishMeta path
// hubSubTurnEnd uses) in a tight loop with no synchronization; concurrently,
// the single real WS connection thrashes attach_session(root) ->
// attach_session(child) -> attach_session(root) with no synchronization
// either — reproducing the "Open click races the terminal publish" shape
// server-side, with no dependency on the full steer/cancel cascade. A
// background reader tracks, from the real wire bytes, every root-session
// seq the tab was ever handed (live delivery or any catch-up tail) and the
// client's own running cursor (updated on every frame with a seq, exactly
// like a real frontend would). At the end, a final clean attach reconciles
// cursor vs head; the invariant is that every seq the hub ever issued for
// root was observed by the tab at least once, somewhere along the way — no
// permanent loss.
package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestHub_RebindRace_LiveFrameNeverLost is the server-side reproduction
// attempt for the #1139 e2e failure: does a tight, unsynchronized
// root<->child rebind loop on one connection ever permanently lose a frame
// concurrently published to the session it is rebinding away from/back to?
func TestHub_RebindRace_LiveFrameNeverLost(t *testing.T) {
	rig := newFixtureRig(t)

	rootMeta, err := rig.h.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	childMeta, err := rig.h.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	root, child := rootMeta.ID, childMeta.ID

	a := dialFxTab(t, rig.url, "A")
	a.readUntil(t, isType("session_state"))

	// Initial attach to root: no cursor yet (unknown_position -> snapshot).
	attach(t, a, root, 0, "")
	cu0 := a.readUntil(t, isType("catch_up_complete"))
	bootID := bootIDOf(cu0)
	require.NotEmpty(t, bootID)

	// haveRoot/haveChild: the tab's own running cursor per session, updated
	// on EVERY frame carrying that session's seq (live or tail) — exactly
	// what a real frontend tracks, not just catch_up_complete.
	var haveRoot atomic.Int64
	haveRoot.Store(frameSeqOf(cu0))
	var haveChild atomic.Int64

	// seenRoot: every distinct root seq the tab was EVER handed, by any
	// channel, for the whole test.
	seenRoot := struct {
		mu sync.Mutex
		m  map[int64]bool
	}{m: map[int64]bool{}}
	recordRootSeq := func(seq int64) {
		seenRoot.mu.Lock()
		seenRoot.m[seq] = true
		seenRoot.mu.Unlock()
	}

	readerDone := make(chan struct{})
	readerErr := make(chan error, 1)
	stopReader := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stopReader:
				return
			default:
			}
			_ = a.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, raw, err := a.conn.ReadMessage()
			if err != nil {
				select {
				case <-stopReader:
				default:
					readerErr <- err
				}
				return
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				continue
			}
			sid, _ := m["session_id"].(string)
			seqF, hasSeq := m["seq"].(float64)
			if !hasSeq {
				continue
			}
			seq := int64(seqF)
			switch sid {
			case root:
				recordRootSeq(seq)
				for {
					cur := haveRoot.Load()
					if seq <= cur || haveRoot.CompareAndSwap(cur, seq) {
						break
					}
				}
			case child:
				for {
					cur := haveChild.Load()
					if seq <= cur || haveChild.CompareAndSwap(cur, seq) {
						break
					}
				}
			}
		}
	}()

	// Publisher: tight loop, no synchronization, publishing directly through
	// the ROOT hub — the same entry point hubSubTurnEnd/hubSubagentState use
	// (publishBytes -> publishMeta{}), simulating the cancel cascade's
	// terminal frames landing on root while the connection rebinds.
	//
	// Deliberately well under hubJournalMaxFrames (2048): this test isolates
	// the REBIND race, not the journal's own by-design retention trimming
	// (§3.1) — a publish count near/over the cap would make old frames
	// legitimately unservable regardless of any rebind race, confounding the
	// result (first run at 4000 "confirmed" loss that evaporated at 500;
	// the missing seqs were exactly the trimmed-away oldest entries).
	const publishN = 500
	rootHub := rig.h.hubs.getOrCreate(root)
	var publishWG sync.WaitGroup
	publishWG.Add(1)
	go func() {
		defer publishWG.Done()
		for i := 0; i < publishN; i++ {
			frame := []byte(fmt.Sprintf(`{"type":"subagent_end_probe","session_id":%q,"marker":%d}`, root, i))
			rootHub.publishBytes(frame)
		}
	}()

	// Rebind thrasher: attach_session(child) -> attach_session(root) with no
	// delay, racing the publisher above. Uses the tab's own tracked cursor
	// for root, exactly like a real reconnect/reattach would.
	const rebindN = 400
	for i := 0; i < rebindN; i++ {
		attach(t, a, child, haveChild.Load(), bootID)
		attach(t, a, root, haveRoot.Load(), bootID)
	}

	publishWG.Wait()
	finalHead := rootHub.snapshotHead()

	// Let in-flight frames drain, then settle with one final clean attach to
	// root using the tab's own tracked cursor — this is the self-healing
	// reattach the frontend performs on a detected gap; if the hub is
	// correct, it closes every remaining gap.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		seenRoot.mu.Lock()
		n := len(seenRoot.m)
		seenRoot.mu.Unlock()
		if uint64(n) >= finalHead-rootHub.base {
			break
		}
		attach(t, a, root, haveRoot.Load(), bootID)
		time.Sleep(20 * time.Millisecond)
	}

	close(stopReader)
	select {
	case <-readerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("reader goroutine did not exit")
	}
	select {
	case err := <-readerErr:
		t.Fatalf("reader: unexpected WS read error: %v", err)
	default:
	}

	seenRoot.mu.Lock()
	defer seenRoot.mu.Unlock()
	var missing []int64
	for seq := rootHub.base + 1; seq <= finalHead; seq++ {
		if !seenRoot.m[uint64ToInt64(seq)] {
			missing = append(missing, int64(seq))
		}
	}
	if len(missing) > 0 {
		sample := missing
		if len(sample) > 20 {
			sample = sample[:20]
		}
		strs := make([]string, len(sample))
		for i, s := range sample {
			strs[i] = fmt.Sprintf("%d", s)
		}
		t.Fatalf("RACE CONFIRMED: %d/%d root-hub frames were never delivered to the tab "+
			"(live or via any catch-up), even after settling — sample missing seqs: [%s]",
			len(missing), finalHead-rootHub.base, strings.Join(strs, ","))
	}
}

func uint64ToInt64(u uint64) int64 { return int64(u) }
