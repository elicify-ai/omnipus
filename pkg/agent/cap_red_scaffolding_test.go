package agent

// RED scaffolding: signature adapters ONLY, to keep tests executable on the
// pinned pre-feature code. Every decision invokes the REAL production method.
// No helper folds memory, grants work, formats messages, resolves lifecycle
// state, publishes evidence or delivers notices. Missing evidence remains
// explicitly missing; it is never reconstructed by re-reading a gate.
// Remove old-signature branches when the production signatures change.

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

type capRedDecision struct {
	granted                    bool
	position, occupancy, limit int
	cause                      string
	observed                   time.Time
	complete                   bool
	release                    func()
	grants                     []steerQueueEntry
}

func capRedAcquire(t *testing.T, ds *DispatchSemaphore) capRedDecision {
	t.Helper()
	out := reflect.ValueOf(ds.TryAcquire).Call(nil)
	d := capRedDecision{granted: out[0].Bool(), occupancy: -999, limit: -999, cause: "MISSING_DECISION_EVIDENCE"}
	if len(out) == 2 {
		if !out[1].IsNil() {
			d.release = capRedValue[func()](t, "real legacy release", out[1].Interface())
		}
		return d
	}
	if len(out) != 6 {
		t.Fatalf("CAP-B2 TryAcquire returned %d values, want full six-value decision", len(out))
	}
	d.occupancy, d.limit, d.cause = int(out[1].Int()), int(out[2].Int()), out[3].String()
	d.observed = capRedValue[time.Time](t, "real semaphore ObservedAt", out[4].Interface())
	if !out[5].IsNil() {
		d.release = capRedValue[func()](t, "real decision release", out[5].Interface())
	}
	d.complete = true
	return d
}

func capRedSteer(t *testing.T, g *steerAdmission, sid string, generation int, runID string, boot uint64) capRedDecision {
	t.Helper()
	out := reflect.ValueOf(g.tryAdmitRun).Call([]reflect.Value{reflect.ValueOf(sid), reflect.ValueOf(generation), reflect.ValueOf(runID), reflect.ValueOf(boot)})
	if len(out) == 3 {
		return capRedDecision{granted: out[0].Bool(), position: int(out[1].Int()), limit: int(out[2].Int()), occupancy: -999, cause: "MISSING_DECISION_EVIDENCE"}
	}
	if len(out) != 1 || out[0].Kind() != reflect.Struct {
		t.Fatalf("CAP-B1 tryAdmitRun must return one decision, got %v", out)
	}
	v := out[0]
	d := capRedDecision{granted: v.FieldByName("granted").Bool(), position: int(v.FieldByName("queuePosition").Int()), occupancy: int(v.FieldByName("occupancy").Int()), limit: int(v.FieldByName("limit").Int()), cause: v.FieldByName("cause").String(), complete: true}
	grants := v.FieldByName("grants")
	for i := 0; i < grants.Len(); i++ {
		d.grants = append(d.grants, capRedEntry(grants.Index(i)))
	}
	// Keep time extraction outside the decision oracle: its equality is tested
	// against the real queue entry, without unsafe access to unexported fields.
	if v.FieldByName("observedAt").Equal(reflect.ValueOf(time.Time{})) {
		t.Error("CAP-B1 decision observation time is zero")
	}
	return d
}
func capRedEntry(v reflect.Value) steerQueueEntry {
	return steerQueueEntry{sessionID: v.FieldByName("sessionID").String(), generation: int(v.FieldByName("generation").Int()), runID: v.FieldByName("runID").String(), bootSeq: v.FieldByName("bootSeq").Uint()}
}
func capRedRelease(t *testing.T, g *steerAdmission, claim executionClaim) []steerQueueEntry {
	t.Helper()
	out := reflect.ValueOf(g.releaseExecution).Call([]reflect.Value{reflect.ValueOf(claim)})
	if len(out) == 2 {
		if !out[1].Bool() {
			return nil
		}
		return []steerQueueEntry{capRedValue[steerQueueEntry](t, "real legacy promotion", out[0].Interface())}
	}
	if len(out) != 1 {
		t.Fatalf("CAP-B1 releaseExecution has %d return values, want ordered batch", len(out))
	}
	return capRedValue[[]steerQueueEntry](t, "real promotion batch", out[0].Interface())
}
func capRedIDs(entries []steerQueueEntry) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.sessionID
	}
	return ids
}
func capRedMemory(t *testing.T, high, ok *bool) {
	t.Helper()
	restore := config.SetMemoryProviderForTest(func() (bool, bool) { return *high, *ok }, func() (uint64, bool) { return 1 << 30, *ok })
	t.Cleanup(restore)
}
func capRedValue[T any](t *testing.T, label string, value any) T {
	t.Helper()
	actual, ok := value.(T)
	if !ok {
		t.Fatalf("%s has type %T, want %s", label, value, reflect.TypeFor[T]())
	}
	return actual
}
func capRedAssert(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v; want %#v (cap spec r6)", label, got, want)
	}
}
func capRedHold(t *testing.T, ds *DispatchSemaphore, n int) []func() {
	t.Helper()
	held := make([]func(), 0, n)
	for i := 0; i < n; i++ {
		d := capRedAcquire(t, ds)
		if !d.granted || d.release == nil {
			t.Fatalf("SETUP slot %d of %d not acquired: %+v", i+1, n, d)
		}
		held = append(held, d.release)
		t.Cleanup(d.release)
	}
	return held
}
func capRedFillSteer(t *testing.T, g *steerAdmission, n int) []executionClaim {
	t.Helper()
	claims := make([]executionClaim, 0, n)
	for i := 0; i < n; i++ {
		c := executionClaim{SessionID: fmt.Sprintf("holder-%d", i), Generation: 1, RunID: fmt.Sprintf("run-%d", i), BootSeq: 1}
		d := capRedSteer(t, g, c.SessionID, c.Generation, c.RunID, c.BootSeq)
		if !d.granted {
			t.Fatalf("SETUP holder %d was not granted: %+v", i, d)
		}
		claims = append(claims, c)
	}
	return claims
}
