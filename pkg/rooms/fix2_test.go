package rooms

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
)

// Round-2 oracles: FR-216/217 and chief's A/B/C/E rulings. Real journal and
// service; only workspace/receiver/storage edges are faulted.
func TestRoomsFix2_RetryAfterHeldFenceDefer(t *testing.T) {
	for _, stillStopped := range []bool{false, true} {
		t.Run(fmt.Sprintf("stopped_%t", stillStopped), func(t *testing.T) {
			f, j := newFaultFixture(t)
			f.deps.held["jim"] = true
			room := f.squad("mia", "jim")
			require.NoError(t, f.post(room.Id, "held", "@Jim work"))
			d := f.deliveryOf(room.Id, "jim")
			require.Equal(t, StateHeld, d.State)
			j.set(func(j *faultJournal) { j.append = failState(StateWorking) })
			require.Equal(t, ConsumeDefer, f.svc.DecideConsume(room.Id, d.RequestID, "main-jim"))
			require.Positive(t, j.injected(), "instrument: working write fault fired")
			j.set(func(j *faultJournal) { j.append = nil })
			f.deps.mu.Lock()
			facts := f.deps.facts[d.RequestID]
			facts.Held = stillStopped // receiver truth after the unrelated revival
			f.deps.facts[d.RequestID] = facts
			f.deps.mu.Unlock()
			require.NoError(t, f.post(room.Id, "held", "@Jim work"))
			f.deps.mu.Lock()
			rewakes := append([]string(nil), f.deps.rewakes...)
			f.deps.mu.Unlock()
			require.Equal(t, []string{d.RequestID}, rewakes, "Retry reconsiders the existing held capture")
			want := "admitted/"
			if stillStopped {
				want = "held/"
			}
			require.Equal(t, want, f.stateOf(room.Id, d.RequestID))
			require.Len(t, f.admittedIDs(), 1, "Retry creates no second receiver capture")
		})
	}
}

func TestRoomsFix2_RetryCannotBypassMirror(t *testing.T) {
	for _, markerFault := range []bool{false, true} {
		t.Run(fmt.Sprintf("marker_%t", markerFault), func(t *testing.T) {
			f, j := newFaultFixture(t)
			room := f.squad("mia", "jim")
			require.NoError(t, f.post(room.Id, "original", "@Jim plan"))
			d := f.deliveryOf(room.Id, "jim")
			if markerFault {
				j.set(func(j *faultJournal) {
					j.append = func(stream string, _ any) error {
						if stream == StreamMirrors {
							return errFault
						}
						return nil
					}
				})
			} else {
				f.deps.mirrorErr = errFault
			}
			a := Answer{RoomID: room.Id, RequestID: d.RequestID,
				Responder:          addressing.Pair{WorkspaceID: "sales", AgentID: "jim"},
				ResponderSessionID: "main-jim", Content: "plan @Mia"}
			_, err := f.svc.DeliverAnswer(context.Background(), a)
			require.ErrorIs(t, err, ErrMirrorFailed)
			if markerFault {
				require.Positive(t, j.injected())
			}
			require.NoError(t, f.post(room.Id, "original", "@Jim plan"))
			require.Equal(t, []string{"jim"}, f.deps.admittedTo(), "unmirrored answer cannot forward through original-message Retry")
			j.set(func(j *faultJournal) { j.append = nil })
			f.deps.mu.Lock()
			f.deps.mirrorErr = nil
			f.deps.mu.Unlock()
			_, err = f.svc.DeliverAnswer(context.Background(), a)
			require.NoError(t, err)
			f.drain(room.Id)
			require.Equal(t, []string{"jim", "mia"}, f.deps.admittedTo())
			require.Len(t, f.entriesOf(room.Id), 2, "repair never duplicates the room answer")
		})
	}
}

type fix2RecordReadFault struct {
	*FileJournal
	fail bool
	hits int
}

func (j *fix2RecordReadFault) ReadRecord(id string, dst any) error {
	if j.fail {
		j.hits++
		return errFault
	}
	return j.FileJournal.ReadRecord(id, dst)
}

func TestRoomsFix2_UnreadableCostRoomMarksIncomplete(t *testing.T) {
	f := newFixture(t)
	room := f.squad("mia", "jim")
	j := &fix2RecordReadFault{FileJournal: NewFileJournal(f.dir), fail: true}
	svc := New(j, DepsFrom(f.deps))
	t.Cleanup(svc.Close)
	svc.AddTurnCost(TurnCost{RoomID: room.Id, TurnID: "known-charge", CostUSD: .03, Priced: true, Inputs: 1, RoomInputs: 1})
	require.Positive(t, j.hits, "instrument: allocation-time room read really failed")
	j.fail = false
	// Restart before any successful read: memory-only incomplete marks lose
	// the one-shot report just as surely as the original early return.
	got, err := f.reopen().svc.Get(room.Id)
	require.NoError(t, err)
	require.True(t, got.AllocatedCostUnpriced, "a temporarily unreadable room cannot silently lose a known charge")
	got, err = f.reopen().svc.Get(room.Id)
	require.NoError(t, err)
	require.True(t, got.AllocatedCostUnpriced, "incomplete mark survives a fresh service")
}

func TestRoomsFix2_CostFailuresAreVisible(t *testing.T) {
	for _, readFault := range []bool{false, true} {
		t.Run(fmt.Sprintf("workspace_read_%t", readFault), func(t *testing.T) {
			f, j := newFaultFixture(t)
			room := f.squad("mia", "jim")
			if readFault {
				f.deps.wsErr = errFault
			} else {
				j.set(func(j *faultJournal) {
					j.append = func(stream string, _ any) error {
						if stream == StreamTurns {
							return errFault
						}
						return nil
					}
				})
			}
			f.svc.AddTurnCost(TurnCost{RoomID: room.Id, TurnID: "priced", CostUSD: .03, Priced: true, Inputs: 3, RoomInputs: 1})
			if !readFault {
				require.Positive(t, j.injected(), "instrument: turn-line append fault")
			}
			f.deps.wsErr = nil
			j.set(func(j *faultJournal) { j.append = nil })
			for _, svc := range []*Service{f.svc, f.reopen().svc} {
				got, err := svc.Get(room.Id)
				require.NoError(t, err)
				require.True(t, got.AllocatedCostUnpriced, "known charge cannot silently vanish as a complete total")
				if !readFault {
					require.Zero(t, got.AllocatedCostMicroUsd, "a failed append adds no money")
				}
			}
			// Healthy control: $0.03 * 1/3 = 10,000 micro-dollars.
			f.svc.AddTurnCost(TurnCost{RoomID: room.Id, TurnID: "healthy", CostUSD: .03, Priced: true, Inputs: 3, RoomInputs: 1})
			got, err := f.svc.Get(room.Id)
			require.NoError(t, err)
			require.Equal(t, int64(10_000), got.AllocatedCostMicroUsd)
		})
	}
}

func TestRoomsFix2_WorkspaceCancellationErrorsAreReturned(t *testing.T) {
	f, j := newFaultFixture(t)
	f.deps.held["jim"] = true
	room := f.squad("mia", "jim")
	require.NoError(t, f.post(room.Id, "held", "@Jim work"))
	d := f.deliveryOf(room.Id, "jim")
	f.deps.ws.Agents = []Agent{{"mia", "Mia"}, {"r2", "Researcher2"}}
	j.set(func(j *faultJournal) { j.append = failState(StateCancelled) })
	_, err := f.svc.Get(room.Id)
	require.Error(t, err, "a room read must not return success with stale labels")
	require.Equal(t, 500, StatusOf(err), "committed membership with stale cancellation labels is a visible partial failure")
	require.Positive(t, j.injected())
	_, err = f.svc.List("sales")
	require.Error(t, err, "stale-label repair must expose its failed write")
	require.Equal(t, 500, StatusOf(err), "subsequent reads cannot claim those stale labels are healthy")
	j.set(func(j *faultJournal) { j.append = nil })
	got, err := f.svc.Get(room.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"mia"}, got.Members)
	require.Equal(t, "cancelled/removed", f.stateOf(room.Id, d.RequestID))
	require.Len(t, f.admittedIDs(), 1, "repair never admits removed work")
}

func TestRoomsFix2_ConsumeReleaseIsRepairable(t *testing.T) {
	f, j := newFaultFixture(t)
	room := f.squad("mia", "jim")
	require.NoError(t, f.post(room.Id, "c1", "@Jim work"))
	d := f.deliveryOf(room.Id, "jim")
	require.Equal(t, ConsumeRun, f.svc.DecideConsume(room.Id, d.RequestID, "main-jim"))
	j.set(func(j *faultJournal) { j.append = failState(StateAdmitted) })
	releaseErr := f.svc.ReleaseConsume(room.Id, d.RequestID)
	require.Error(t, releaseErr, "undo must expose failure, not only log it")
	require.Positive(t, j.injected(), "instrument: undo append fault fired")
	_, err := f.svc.Get(room.Id)
	require.Error(t, err, "unrepaired working state must not report success")
	require.Equal(t, 500, StatusOf(err), "unrepaired working state is not served as healthy")
	j.set(func(j *faultJournal) { j.append = nil })
	// Restart BEFORE another live read: durable undo intent must win over
	// restart's interrupted classification, because no turn received this input.
	fresh := f.reopen()
	_, err = fresh.svc.Get(room.Id)
	require.NoError(t, err)
	require.Equal(t, "admitted/", fresh.stateOf(room.Id, d.RequestID))
	require.Equal(t, ConsumeRun, fresh.svc.DecideConsume(room.Id, d.RequestID, "main-jim"))
}

func TestRoomsFix2_ContextFullLineBoundary(t *testing.T) {
	// Spec FR-200: 8,192 bytes, counting complete [author] text lines and
	// their separator. Two entries isolate the byte cap from the 20-row cap.
	for _, total := range []int{8191, 8192, 8193} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			f := newFixture(t)
			room := f.squad("mia", "jim")
			prefix := "[the user] "
			older := "OLDER|" + strings.Repeat("a", 4200-len(prefix)-len("OLDER|"))
			newer := "NEWER|" + strings.Repeat("b", total-4200-1-len(prefix)-len("NEWER|"))
			require.NoError(t, f.post(room.Id, "older", older, "mia"))
			require.NoError(t, f.post(room.Id, "newer", newer, "mia"))
			require.NoError(t, f.post(room.Id, "target", "@Jim work"))
			var text string
			f.deps.mu.Lock()
			for _, a := range f.deps.admits {
				if a.Receiver.AgentID == "jim" {
					text = a.ModelText
				}
			}
			f.deps.mu.Unlock()
			start := strings.Index(text, "[the user]")
			end := strings.Index(text, "\n\nMessage from")
			require.GreaterOrEqual(t, start, 0)
			require.Greater(t, end, start)
			want := prefix + newer
			if total <= 8192 {
				want = prefix + older + "\n" + want
			}
			require.Equal(t, want, text[start:end], "full serialized history, including author labels and separator")
		})
	}
}
