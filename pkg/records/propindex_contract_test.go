// Omnipus — the refusal contract must itself be able to fail.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeTB records what the contract would have reported, so the contract can be
// tested the same way it asks callers to be tested.
type fakeTB struct{ errs []string }

func (f *fakeTB) Helper() {}
func (f *fakeTB) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

// TestRefusalContract_CatchesTheThreeWaysAnEntryPointCanBreakIt is the
// self-verification gate. A contract helper that cannot fail is worse than none:
// every caller that adopts it would report a guarantee nobody is checking.
func TestRefusalContract_CatchesTheThreeWaysAnEntryPointCanBreakIt(t *testing.T) {
	if PropertyIndexAvailable {
		t.Skip("the contract is a no-op on a SQLite-capable build; " +
			"run with -tags records_no_sqlite to exercise it")
	}

	cases := []struct {
		name     string
		call     func() ([]int, error)
		wantErrs int
		wantText string
	}{
		{
			name:     "the empty success — the bug FR-020h exists to prevent",
			call:     func() ([]int, error) { return []int{}, nil },
			wantErrs: 1,
			wantText: "returned SUCCESS",
		},
		{
			name:     "an error, but not the platform refusal",
			call:     func() ([]int, error) { return nil, errors.New("database is locked") },
			wantErrs: 1,
			wantText: "not with the platform refusal",
		},
		{
			name: "the refusal, re-wrapped so the platform name is lost",
			call: func() ([]int, error) {
				_ = RequirePropertyIndex(CapabilityTypedFilter)
				return nil, fmt.Errorf("query failed: %w", ErrPropertyIndexUnavailable)
			},
			wantErrs: 1,
			wantText: "no longer names the platform",
		},
		{
			name: "correct: the refusal returned unchanged",
			call: func() ([]int, error) {
				return nil, RequirePropertyIndex(CapabilityTypedFilter)
			},
			wantErrs: 0,
		},
		{
			name: "correct: the refusal wrapped with %w, message preserved",
			call: func() ([]int, error) {
				err := RequirePropertyIndex(CapabilityTypedFilter)
				return nil, fmt.Errorf("knowledge_find: %w", err)
			},
			wantErrs: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeTB{}
			AssertRefusesWhenIndexUnavailable(f, CapabilityTypedFilter, tc.call)

			if len(f.errs) != tc.wantErrs {
				t.Fatalf("contract reported %d problems, want %d: %v",
					len(f.errs), tc.wantErrs, f.errs)
			}
			if tc.wantText != "" && !strings.Contains(f.errs[0], tc.wantText) {
				t.Errorf("contract's message does not explain the failure — want it to mention "+
					"%q, got: %s", tc.wantText, f.errs[0])
			}
		})
	}
}

// TestRefusalContract_IsSilentOnACapableBuild — the contract must not assert the
// opposite guarantee where the index really does exist.
func TestRefusalContract_IsSilentOnACapableBuild(t *testing.T) {
	if !PropertyIndexAvailable {
		t.Skip("this build has no properties index")
	}
	f := &fakeTB{}
	AssertRefusesWhenIndexUnavailable(f, CapabilityTypedFilter,
		func() ([]int, error) { return []int{1, 2, 3}, nil })
	if len(f.errs) != 0 {
		t.Errorf("the contract fired on a SQLite-capable build, where a successful query is "+
			"the correct outcome: %v", f.errs)
	}
}
