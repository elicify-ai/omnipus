// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// priority_validation_test.go — regression coverage for the M2 priority
// validation fix (UAT uat-report-adr057-CONSOLIDATED-2026-08-03.md M2/M7).
//
// Root cause recap: Task.Priority is a plain `int` with 0 meaning "unset"
// (Task.Priority's own doc comment, EffectivePriority). Once an explicit
// priority:0 crosses the JSON->struct boundary into that int field, it is
// indistinguishable from "field absent" — so a naive `t.Priority != 0 && out
// of range` check (Create's old behavior) or a naive default-fill-then-check
// (create_task's old behavior) never reaches the range check for an explicit
// zero. The fix distinguishes unset from explicit-zero at the SEAM that still
// holds presence information (a wire *int, an args map "ok" check, or
// Patch's *int), using the single shared task.ValidatePriority as the range
// check everywhere that presence has already been established.
//
// This file locks in both halves of that contract at the store layer:
//   - Store.Create (bare Task.Priority int): 0 always means unset and is
//     NEVER rejected here — the explicit-vs-absent distinction is the
//     CALLER's job (REST/tool tests cover that half).
//   - Store.Update (Patch.Priority *int): a non-nil pointer is unambiguous
//     presence, so an explicit *0 patch IS rejected here, at the store layer
//     itself — this is the one layer where "explicit zero rejected" is
//     directly testable via the shared Patch type.
package task

import (
	"strings"
	"testing"
)

// TestValidatePriority_Matrix directly exercises the shared range-check
// helper across the full boundary matrix (Binding Rule 4: every rejection
// case is paired with an acceptance case at the same boundary).
func TestValidatePriority_Matrix(t *testing.T) {
	cases := []struct {
		name    string
		p       int
		wantErr bool
	}{
		{"zero_rejected", 0, true},
		{"one_accepted_lower_bound", 1, false},
		{"five_accepted_upper_bound", 5, false},
		{"six_rejected", 6, true},
		{"negative_rejected", -1, true},
		{"large_rejected", 100, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePriority(c.p)
			if c.wantErr && err == nil {
				t.Fatalf("ValidatePriority(%d): expected error, got nil", c.p)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("ValidatePriority(%d): expected nil, got %v", c.p, err)
			}
			if c.wantErr && !strings.Contains(err.Error(), "priority must be between 1 and 5") {
				t.Errorf("ValidatePriority(%d): unexpected error text: %v", c.p, err)
			}
		})
	}
}

// TestCreate_Priority_UnsetVsInvalid_Matrix covers Store.Create's own
// contract: a bare Task.Priority of 0 is ALWAYS treated as "unset" (accepted,
// no error) because the raw struct field carries no presence information —
// the explicit-zero rejection is enforced upstream, at the REST/tool seam,
// before a Task is ever built. Non-zero out-of-range values ARE rejected
// here (this part of the check was already correct pre-fix).
func TestCreate_Priority_UnsetVsInvalid_Matrix(t *testing.T) {
	cases := []struct {
		name     string
		priority int
		wantErr  bool
	}{
		{"absent_zero_treated_as_unset", 0, false},
		{"one_lower_bound_valid", 1, false},
		{"five_upper_bound_valid", 5, false},
		{"six_invalid", 6, true},
		{"negative_invalid", -1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStore(t)
			tk := mkTask("priority matrix", "ws-priority")
			tk.Priority = c.priority
			err := s.Create(tk)
			if c.wantErr {
				if err == nil {
					t.Fatalf("Create with priority=%d: expected error, got nil", c.priority)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create with priority=%d: unexpected error: %v", c.priority, err)
			}
			got, gerr := s.Get(tk.ID)
			if gerr != nil {
				t.Fatalf("Get: %v", gerr)
			}
			if got.Priority != c.priority {
				t.Fatalf("persisted priority = %d, want %d", got.Priority, c.priority)
			}
			// EffectivePriority defaults an unset (0) value to 3, and is a
			// no-op passthrough for any explicitly-valid value.
			wantEffective := c.priority
			if wantEffective == 0 {
				wantEffective = 3
			}
			if eff := got.EffectivePriority(); eff != wantEffective {
				t.Fatalf("EffectivePriority() = %d, want %d", eff, wantEffective)
			}
		})
	}
}

// TestUpdate_Priority_ExplicitZeroPatch_Rejected is the store-layer
// regression lock for M2(a): unlike Store.Create's bare int, Patch.Priority
// is a *int, so a caller that sends an explicit priority:0 through a Patch
// (as every REST/tool update path does) carries unambiguous presence
// information — this MUST be rejected, never silently accepted as "unset".
func TestUpdate_Priority_ExplicitZeroPatch_Rejected(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Priority = 2
	if err := s.Create(tk); err != nil {
		t.Fatal(err)
	}

	zero := 0
	_, err := s.Update(tk.ID, Patch{Priority: &zero})
	if err == nil {
		t.Fatal("Update with explicit Patch{Priority: &0} must be rejected, got nil error")
	}
	if !strings.Contains(err.Error(), "priority must be between 1 and 5") {
		t.Errorf("unexpected error: %v", err)
	}

	// Priority must be unchanged after the rejected update.
	got, gerr := s.Get(tk.ID)
	if gerr != nil {
		t.Fatalf("Get: %v", gerr)
	}
	if got.Priority != 2 {
		t.Fatalf("priority must be unchanged after rejected update, got %d (was 2)", got.Priority)
	}
}

// TestUpdate_Priority_ValidAndInvalidPatch_Matrix covers the remaining Patch
// boundary matrix: 1 and 5 (valid bounds), 6 and -1 (invalid), and a nil
// Patch.Priority (absent — leaves the existing value untouched).
func TestUpdate_Priority_ValidAndInvalidPatch_Matrix(t *testing.T) {
	cases := []struct {
		name     string
		priority *int
		wantErr  bool
		want     int // expected priority after the call (only checked when !wantErr)
	}{
		{"one_lower_bound_valid", intPtr(1), false, 1},
		{"five_upper_bound_valid", intPtr(5), false, 5},
		{"six_invalid", intPtr(6), true, 0},
		{"negative_invalid", intPtr(-1), true, 0},
		{"absent_leaves_unchanged", nil, false, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStore(t)
			tk := mkTask("t", "ws")
			tk.Priority = 2
			if err := s.Create(tk); err != nil {
				t.Fatal(err)
			}

			_, err := s.Update(tk.ID, Patch{Priority: c.priority})
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for priority patch %v", c.priority)
				}
				// Unchanged (original 2) after a rejected update.
				got, _ := s.Get(tk.ID)
				if got.Priority != 2 {
					t.Errorf("priority must be unchanged after rejected update, got %d", got.Priority)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, gerr := s.Get(tk.ID)
			if gerr != nil {
				t.Fatalf("Get: %v", gerr)
			}
			if got.Priority != c.want {
				t.Fatalf("priority = %d, want %d", got.Priority, c.want)
			}
		})
	}
}

func intPtr(i int) *int { return &i }
