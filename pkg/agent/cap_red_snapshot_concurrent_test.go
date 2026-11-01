package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestCapRED_PlanEngine_ConcurrentTickAdmissionOwnTuples(t *testing.T) {
	h := newTestPlanEngine(t)
	t0 := h.clock.Now()
	var calls atomic.Int32
	h.pe.RegisterActiveCounter("goal", func() (int, error) {
		n := calls.Add(1)
		h.clock.Set(t0.Add(time.Duration(n) * time.Second))
		// Independent process-edge inputs: odd calls see three goals,
		// even calls nine. Timestamp/count pairs must never be mixed.
		if n%2 == 1 {
			return 3, nil
		}
		return 9, nil
	})
	source, hasSource := any(h.pe).(tools.JobCapSnapshotSource)
	if !hasSource {
		t.Error("CAP-B8/PS13 RED: real CapSnapshot publisher missing; no source substituted")
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 24; i++ {
			h.pe.Admit("goal")
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 24; i++ {
			h.pe.Tick(context.Background())
		}
	}()
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	close(start)
	last := time.Time{}
	read := func() {
		if !hasSource {
			return
		}
		a, max, reliable, at, _ := source.CapSnapshot()
		if at.IsZero() {
			return
		} // A read before the first publication is legitimate.
		n := int(at.Sub(t0) / time.Second)
		want := 3
		if n%2 == 0 {
			want = 9
		}
		capRedAssert(t, "PS13 snapshot is one complete counting-call tuple", []any{a, max, reliable}, []any{want, 16, true})
		if at.Before(last) {
			t.Errorf("PS13 snapshot regressed from %s to %s", last, at)
		}
		last = at
	}
	for {
		select {
		case <-finished:
			read()
			capRedAssert(t, "PS13 each admission and each tick counted independently", calls.Load(), int32(48))
			if hasSource {
				a, max, r, at, tick := source.CapSnapshot()
				capRedAssert(t, "PS13 latest own observation wins", []any{a, max, r, at}, []any{9, 16, true, t0.Add(48 * time.Second)})
				if tick.IsZero() {
					t.Error("PS13 tick did not publish its own pass-end time")
				}
			}
			return
		default:
			read()
			time.Sleep(time.Microsecond)
		}
	}
}
