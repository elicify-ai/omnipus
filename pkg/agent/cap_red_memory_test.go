package agent

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// S47a / D11. These expectations are the complete r6 R-7 table, not
// values computed from either applyMemoryCap or a host memory reading.
func TestCapRED_Fold_MemoryAndAbsentBound_Table(t *testing.T) {
	for _, reading := range []struct {
		name     string
		high, ok bool
		limits   []int
		causes   []string
	}{
		{"healthy", false, true, []int{0, 1, 2, 8}, []string{"", "", "", ""}},
		{"pressure", true, true, []int{2, 1, 2, 2}, []string{"memory_pressure", "", "", "memory_pressure"}},
		{"unverifiable", false, false, []int{2, 1, 2, 2}, []string{"memory_unverifiable", "", "", "memory_unverifiable"}},
	} {
		t.Run(reading.name, func(t *testing.T) {
			high, ok := reading.high, reading.ok
			capRedMemory(t, &high, &ok)
			for i, c := range []int{0, 1, 2, 8} {
				t.Run(fmt.Sprintf("configured_%d", c), func(t *testing.T) {
					limit, cause := foldMemory(c)
					capRedAssert(t, "R-7 exact fold", []any{limit, cause}, []any{reading.limits[i], reading.causes[i]})
				})
			}
		})
	}
}

func TestCapRED_ActualGates_MemoryDatasets(t *testing.T) {
	cases := []struct {
		id                string
		configured, limit int
		high, ok          bool
		cause             string
	}{
		{"D1_unset", 2000, 2000, false, true, ""},
		{"D2_configured", 8, 8, false, true, ""},
		{"D3_unset_pressure", 2000, 2, true, true, "memory_pressure"},
		{"D4_explicit_pressure", 8, 2, true, true, "memory_pressure"},
		{"D5_one", 1, 1, true, true, ""},
		{"D6_unset_unverifiable", 2000, 2, false, false, "memory_unverifiable"},
		{"D7_explicit_unverifiable", 8, 2, false, false, "memory_unverifiable"},
		{"configured_two_pressure", 2, 2, true, true, ""},
		{"configured_one_unverifiable", 1, 1, false, false, ""},
		{"configured_two_unverifiable", 2, 2, false, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			high, ok := tc.high, tc.ok
			capRedMemory(t, &high, &ok)
			t.Run("task_semaphore", func(t *testing.T) {
				ds := newDispatchSemaphore(tc.configured)
				capRedHold(t, ds, tc.limit)
				before := time.Now()
				d := capRedAcquire(t, ds)
				if d.release != nil {
					t.Cleanup(d.release)
				}
				capRedAssert(t, "refused at decision limit", d.granted, false)
				capRedAssert(t, "complete immutable decision", d.complete, true)
				capRedAssert(t, "decision tuple", []any{d.occupancy, d.limit, d.cause}, []any{tc.limit, tc.limit, tc.cause})
				capRedAssert(t, "stored cap remains configured", ds.Cap(), tc.configured)
				if d.complete && (d.observed.Before(before) || d.observed.After(time.Now())) {
					t.Errorf("observedAt %s is not from the decision", d.observed)
				}
				if d.release != nil && !d.granted {
					t.Error("refusal must not return an owned release")
				}
			})
			t.Run("steered", func(t *testing.T) {
				g := newSteerAdmission(func() int { return tc.configured })
				capRedFillSteer(t, g, tc.limit)
				d := capRedSteer(t, g, "waiting", 1, "waiting-run", 1)
				capRedAssert(t, "queued at memory-folded limit", d.granted, false)
				capRedAssert(t, "complete steer decision", d.complete, true)
				capRedAssert(t, "steer evidence", []any{d.position, d.occupancy, d.limit, d.cause}, []any{1, tc.limit, tc.limit, tc.cause})
			})
			t.Run("inbound_cause", func(t *testing.T) {
				a := newAdmissionController(tc.configured)
				for i := 0; i < tc.limit; i++ {
					admitted, release := a.TryAdmit(fmt.Sprint(i))
					if !admitted {
						t.Fatalf("SETUP inbound holder %d refused", i)
					}
					t.Cleanup(release)
				}
				admitted, cause, release := a.TryAdmitWithReason("waiting")
				if release != nil {
					t.Cleanup(release)
				}
				capRedAssert(t, "inbound refused", admitted, false)
				capRedAssert(t, "inbound exact cause", cause, tc.cause)
			})
		})
	}
}

func TestCapRED_ThresholdAndCauseTransitions_NoState(t *testing.T) {
	for _, tc := range []struct {
		used  uint64
		limit int
		cause string
	}{
		{84, 8, ""}, {85, 8, ""}, {86, 2, "memory_pressure"}, // D8/D9: strict > .85.
	} {
		t.Run(fmt.Sprint(tc.used), func(t *testing.T) {
			restore := config.SetMemoryProviderForTest(config.MemoryPressureHighFromSignalsForTest(100-tc.used, 100), nil)
			defer restore()
			limit, cause := foldMemory(8)
			capRedAssert(t, "threshold tuple", []any{limit, cause}, []any{tc.limit, tc.cause})
		})
	}
	// D9/S43 deliberately has no acquisition. A changing fold cannot revoke
	// anything because there are no reservations in this test.
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	for i, want := range []struct {
		high, ok bool
		limit    int
		cause    string
	}{
		{false, true, 8, ""}, {true, true, 2, "memory_pressure"}, {false, true, 8, ""},
		{true, true, 2, "memory_pressure"}, {false, false, 2, "memory_unverifiable"},
		{true, true, 2, "memory_pressure"}, {false, true, 8, ""},
	} {
		high, ok = want.high, want.ok
		limit, cause := foldMemory(8)
		capRedAssert(t, fmt.Sprintf("D9_D11_S28b_sample_%d", i), []any{limit, cause}, []any{want.limit, want.cause})
	}
}

func TestCapRED_DispatchSema_RecoversWithoutResize_HeldSlots(t *testing.T) {
	// S47/D10: two held, then three, release the new admission, then two.
	high, ok := true, true
	capRedMemory(t, &high, &ok)
	ds := newDispatchSemaphore(8)
	capRedHold(t, ds, 2)
	third := capRedAcquire(t, ds)
	if third.release != nil {
		t.Cleanup(third.release)
	}
	capRedAssert(t, "pressured third refused", third.granted, false)
	capRedAssert(t, "pressure decision", []any{third.limit, third.cause}, []any{2, "memory_pressure"})
	if third.granted {
		third.release()
	} // Restore SETUP occupancy after the detected fault, not the oracle.
	high = false
	fourth := capRedAcquire(t, ds)
	if fourth.release != nil {
		t.Cleanup(fourth.release)
	}
	capRedAssert(t, "healthy fourth admitted", fourth.granted, true)
	capRedAssert(t, "healthy decision", []any{fourth.limit, fourth.cause}, []any{8, ""})
	capRedAssert(t, "three genuinely held", ds.InFlight(), 3)
	if fourth.release != nil {
		fourth.release()
	}
	high = true
	fifth := capRedAcquire(t, ds)
	if fifth.release != nil {
		t.Cleanup(fifth.release)
	}
	capRedAssert(t, "pressure returns at two held", fifth.granted, false)
	capRedAssert(t, "stored cap never folded", ds.Cap(), 8)
}

func TestCapRED_DispatchSema_ReleaseOwnership(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	ds := newDispatchSemaphore(2)
	held := capRedHold(t, ds, 2)
	held[0]()
	held[0]()
	capRedAssert(t, "S58 double release retains other holder", ds.InFlight(), 1)
	held[1]()
	capRedAssert(t, "all holders released", ds.InFlight(), 0)
}

func TestCapRED_Inbound_FollowupLeaseSurvivesLowerLimit(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	a := newAdmissionController(8)
	for i := 0; i < 8; i++ {
		yes, rel := a.TryAdmit(fmt.Sprint(i))
		if !yes {
			t.Fatal("SETUP expected eight healthy leases")
		}
		t.Cleanup(rel)
	}
	high = true
	yes, cause, release := a.TryAdmitWithReason("0")
	if release != nil {
		release()
	}
	capRedAssert(t, "S16a lease-followup", []any{yes, cause, a.ActiveScopes()}, []any{true, "", 8})
}

func TestCapRED_DispatchSema_OneAcquisitionPath(t *testing.T) {
	// R-1.5 explicitly deletes the ungated test-only acquisition path.
	_, present := reflect.TypeOf(newDispatchSemaphore(2)).MethodByName("WaitAndAcquire")
	capRedAssert(t, "retired WaitAndAcquire method absent", present, false)
}
