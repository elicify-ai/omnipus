package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// The cascade lists a session as Reached when its Stop is stamped, and lists it
// again as Unreachable when firing the live cancel then fails
// (agent.SteerCanceller.cancelStamped). One session in both lists must be
// counted once, as not stopped - UAT W-03 showed "stopped 1 of 2; 1
// unreachable" for a Stop that touched a single session.
func TestCancelPartialSummary_StampedSessionWhoseCancelFailedIsCountedOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report steer.CancelReport
		want   string
	}{
		{
			name: "the only session failed to cancel",
			report: steer.CancelReport{
				Reached:     []string{"task"},
				Unreachable: []steer.UnreachableSession{{ID: "task", Reason: "cancel failed"}},
			},
			want: "stopped 0 of 1; 1 unreachable",
		},
		{
			name: "one of two sessions failed to cancel",
			report: steer.CancelReport{
				Reached:     []string{"root", "task"},
				Unreachable: []steer.UnreachableSession{{ID: "task", Reason: "cancel failed"}},
			},
			want: "stopped 1 of 2; 1 unreachable",
		},
		{
			name: "the same session is unreachable for two reasons",
			report: steer.CancelReport{
				Reached: []string{"root"},
				Unreachable: []steer.UnreachableSession{
					{ID: "task", Reason: "first reason"}, {ID: "task", Reason: "second reason"},
				},
			},
			want: "stopped 1 of 2; 1 unreachable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cancelPartialSummary(tc.report); got != tc.want {
				t.Fatalf("cancelPartialSummary = %q, want %q", got, tc.want)
			}
		})
	}
}

// A failure to list the helper tree for the background-shell sweep says
// nothing about the root's own Stop: the root is stopped, and the unlisted tree
// is reported on its own, never as "stopped 0 of 1".
func TestCancelPartialSummary_UnlistedHelperTreeDoesNotUncountTheStoppedRoot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report steer.CancelReport
		want   string
	}{
		{
			name: "only the helper tree could not be listed",
			report: steer.CancelReport{
				Reached:     []string{"root"},
				Unreachable: []steer.UnreachableSession{{ID: "root", Reason: "could not be listed", HelperTreeUnlisted: true}},
			},
			want: "stopped 1 of 1; helper sessions could not be listed",
		},
		{
			name: "a real unreachable session and an unlisted tree",
			report: steer.CancelReport{
				Reached: []string{"root", "task"},
				Unreachable: []steer.UnreachableSession{
					{ID: "task", Reason: "cancel failed"},
					{ID: "root", Reason: "could not be listed", HelperTreeUnlisted: true},
				},
			},
			want: "stopped 1 of 2; 1 unreachable; helper sessions could not be listed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cancelPartialSummary(tc.report); got != tc.want {
				t.Fatalf("cancelPartialSummary = %q, want %q", got, tc.want)
			}
		})
	}
}
