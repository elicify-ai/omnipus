// Omnipus — a cancelled or timed-out candidate stream is not an index fault.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// failingCandidates is a real store whose candidate stream ends with a fixed error.
type failingCandidates struct {
	propindex.Store
	err error
}

func (s failingCandidates) Candidates(context.Context, propindex.Selector, func(propindex.Candidate) (propindex.Verdict, error)) error {
	return s.err
}

// TestFind_CancelledStreamIsNotReportedAsABrokenIndex.
//
// Before: every Candidates error, a cancellation included, became an
// index_unavailable refusal with the remedy "run knowledge_describe
// check_integrity" — which tells the user their index is broken when the query
// was merely stopped.
func TestFind_CancelledStreamIsNotReportedAsABrokenIndex(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"cancelled", context.Canceled, "cancelled"},
		{"timed out", context.DeadlineExceeded, "timed out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.plant(1, "growing", "41.25")
			d := f.deps()
			// The error arrives wrapped, as propindex returns it.
			d.Store = failingCandidates{Store: f.store, err: wrapStream(tc.err)}

			resp, err := Find(context.Background(), d, req(withType("plant")))
			if err == nil {
				t.Fatal("a stopped query reported success")
			}
			if !errors.Is(err, tc.err) {
				t.Errorf("error %q does not wrap %v", err, tc.err)
			}
			if IsRefusal(err) || resp.Refused {
				t.Errorf("a stopped query was reported as a refusal: %v", err)
			}
			for _, bad := range []string{"check_integrity", "could not stream", "unavailable"} {
				if strings.Contains(err.Error(), bad) {
					t.Errorf("error %q blames the index (%q)", err, bad)
				}
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say the query %s", err, tc.want)
			}
			// The model-facing path must not blame the index either.
			text, cerr := Call(context.Background(), d, []byte(`{"type":"plant"}`))
			if !errors.Is(cerr, tc.err) {
				t.Errorf("Call error %v does not wrap %v", cerr, tc.err)
			}
			if text != "" {
				t.Errorf("a stopped query rendered text a reader could take for an answer:\n%s", text)
			}
			if resp.Complete || len(resp.Rows) != 0 || len(resp.Problems) != 0 {
				t.Errorf("a stopped query returned an answer: complete=%v rows=%d problems=%v",
					resp.Complete, len(resp.Rows), resp.Problems)
			}
		})
	}
}

// TestFind_GenuineStreamFailureStillRefusesAsIndexUnavailable is the other half:
// the fix must not turn every stream error into a cancellation.
func TestFind_GenuineStreamFailureStillRefusesAsIndexUnavailable(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")
	d := f.deps()
	d.Store = failingCandidates{Store: f.store, err: errors.New("database disk image is malformed")}

	resp := mustRefuse(t, d, req(withType("plant")))
	if len(resp.Problems) != 1 || resp.Problems[0].Code != "index_unavailable" {
		t.Fatalf("problems = %+v, want one index_unavailable", resp.Problems)
	}
}

func wrapStream(err error) error {
	return fmt.Errorf("propindex: the candidate stream was cancelled: %w", err)
}
