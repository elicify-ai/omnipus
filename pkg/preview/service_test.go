package preview

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

func previewUnitService(t *testing.T) *Service {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	home := t.TempDir()
	svc, err := New(Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
	require.NoError(t, err)
	return svc
}

// T-01 / AD-5: page arithmetic is independent of any renderer's output.
func TestPreviewClamp_SpecCases(t *testing.T) {
	cases := []struct {
		name        string
		count       int
		request     Request
		first, last int
		clamped     bool
		refusal     string
	}{
		{"single", 30, Request{First: 3, Last: 3}, 3, 3, false, ""},
		{"range", 30, Request{First: 2, Last: 5}, 2, 5, false, ""},
		{"default", 30, Request{Default: true}, 1, 10, false, ""},
		{"next", 30, Request{First: 11, Last: 20}, 11, 20, false, ""},
		{"partial", 30, Request{First: 25, Last: 40}, 25, 30, true, ""},
		{"spec_example", 12, Request{First: 9, Last: 20}, 9, 12, true, ""},
		{"last", 30, Request{First: 30, Last: 30}, 30, 30, false, ""},
		{"beyond", 30, Request{First: 31, Last: 31}, 0, 0, false, "the document has 30 pages"},
		{"one_page_default", 1, Request{Default: true}, 1, 1, false, ""},
		{"nine_page_default", 9, Request{Default: true}, 1, 9, false, ""},
		{"ten_page_default", 10, Request{Default: true}, 1, 10, false, ""},
		{"eleven_page_default", 11, Request{Default: true}, 1, 10, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got Result
			err := clamp(&got, tc.count, tc.request)
			if tc.refusal != "" {
				require.EqualError(t, err, tc.refusal)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.count, got.PageCount)
			require.Equal(t, tc.first, got.First)
			require.Equal(t, tc.last, got.Last)
			require.Equal(t, tc.clamped, got.Clamped)
		})
	}
}

// T-12 / AD-6. Full channels make cancellation deterministic, not a
// stopwatch assertion about whether a goroutine happened to run.
func TestPreviewAdmission_RendererAndOfficeBudgets(t *testing.T) {
	svc := previewUnitService(t)
	first, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	defer first.Release()
	second, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	defer second.Release()
	require.Len(t, svc.rendererRuns, 2, "AD-6: exactly two admitted renderer runs")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	third, err := svc.Admit(ctx, false)
	require.Nil(t, third)
	require.EqualError(t, err, "stopped")
	// An Office request blocked on rendering must return any Office slot it
	// acquired before it noticed cancellation.
	third, err = svc.Admit(ctx, true)
	require.Nil(t, third)
	require.EqualError(t, err, "stopped")
	require.Empty(t, svc.officeRuns)
	first.Release()
	first.Release()
	second.Release()
	require.Empty(t, svc.rendererRuns, "Release is idempotent")
	office, err := svc.Admit(context.Background(), true)
	require.NoError(t, err)
	defer office.Release()
	require.Len(t, svc.officeRuns, 1)
	require.Len(t, svc.rendererRuns, 1)
	denied, err := svc.Admit(ctx, true)
	require.Nil(t, denied)
	require.EqualError(t, err, "stopped")
	require.Len(t, svc.officeRuns, 1, "canceling a queued Office call cannot release the active call")
	office.Release()
	require.Empty(t, svc.rendererRuns)
	require.Empty(t, svc.officeRuns)
}

func TestPreviewAdmission_QueuedDeadlineReleasesSlots(t *testing.T) {
	svc := previewUnitService(t)
	active, err := svc.Admit(context.Background(), true)
	require.NoError(t, err)
	defer active.Release()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	queued, err := svc.Admit(ctx, true)
	require.Nil(t, queued)
	require.EqualError(t, err, "timed out after 120 s")
	active.Release()
	replacement, err := svc.Admit(context.Background(), true)
	require.NoError(t, err)
	replacement.Release()
	require.Empty(t, svc.officeRuns)
	require.Empty(t, svc.rendererRuns)
	require.Equal(t, 120*time.Second, CallTimeout, "AD-6 whole-call limit includes queue wait")
}

func TestPreviewService_RequiresAdmission(t *testing.T) {
	svc := previewUnitService(t)
	got, err := svc.Preview(context.Background(), nil, Request{Name: "one.pdf", Ext: ".pdf", Data: []byte("%PDF-")})
	require.Nil(t, got)
	require.EqualError(t, err, "preview: admission required")
}
