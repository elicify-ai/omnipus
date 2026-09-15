package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// A renderer stall can prevent the initial layout read, skipping toolbar
// compensation. A later readable layout must get one content correction before
// publication. Ordinary Chrome clamps remain acceptable after that attempt.
// Only the Chrome boundary is scripted; admission, measurement and capture stay real.
func TestViewportRecoversSkippedInitialCompensation(t *testing.T) {
	for _, tc := range []struct {
		name                                           string
		retryRead, scrollbar, clamp, strict            bool
		wantContents, wantReads, wantWidth, wantHeight int
		wantError                                      bool
	}{
		{name: "first final read recovers", wantContents: 1, wantReads: 2, wantWidth: 1000, wantHeight: 700},
		{name: "retry final read recovers", retryRead: true, wantContents: 1, wantReads: 3, wantWidth: 1000, wantHeight: 700},
		{name: "scrollbar alone needs no correction", scrollbar: true, wantReads: 1, wantWidth: 985, wantHeight: 700},
		{name: "ordinary clamp accepted after one correction", clamp: true, wantContents: 1, wantReads: 2, wantWidth: 1000, wantHeight: 650},
		{name: "strict target still refuses persistent clamp", clamp: true, strict: true, wantContents: 1, wantReads: 2, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newViewportFrameFixture(t)
			f.measuredW, f.measuredH = 1000, 557 // A 143px toolbar shortfall, as in the live reproduction.
			if tc.scrollbar {
				f.measuredW, f.measuredH = 985, 700
			}
			original := f.lv.runCDP
			reads, contents := 0, 0
			f.lv.runCDP = func(ctx context.Context, budget time.Duration, actions ...chromedp.Action) error {
				for _, action := range actions {
					switch a := action.(type) {
					case layoutMetricsAction:
						if reads == 0 {
							return context.DeadlineExceeded
						}
					case viewportFrameGeometryAction:
						reads++
						if tc.retryRead && reads == 1 {
							return context.DeadlineExceeded
						}
					case windowContentsSizeAction:
						contents++
						require.Equal(t, 1000, a.width)
						require.Equal(t, 700, a.height)
						f.measuredH = 700
						if tc.clamp {
							f.measuredH = 650
						}
					case viewportContentGeometryAction:
						*a.width, *a.height = f.measuredW, f.measuredH
						*a.clientWidth, *a.clientHeight = f.measuredW, f.measuredH
						if tc.scrollbar {
							*a.width = 1000
						}
					}
				}
				return original(ctx, budget, actions...)
			}
			applied, err := f.lv.applyViewportContextWithConvergence(context.Background(), f.lv.tabCtx, 1000, 700, 1.25, tc.strict)
			require.True(t, applied)
			if tc.wantError {
				require.ErrorContains(t, err, "new tab viewport did not converge")
				require.Empty(t, f.commands, "a strict mismatch cannot authorize capture")
			} else {
				require.NoError(t, err)
				require.Len(t, f.commands, 1)
				require.Equal(t, tc.wantWidth, f.commands[0].Width)
				require.Equal(t, tc.wantHeight, f.commands[0].Height)
				require.Equal(t, f.before.TargetID, f.commands[0].TargetID)
				require.Equal(t, 1.25, f.commands[0].Scale)
			}
			require.Equal(t, tc.wantContents, contents, "at most one content correction")
			require.Equal(t, tc.wantReads, reads)
			require.Equal(t, 1, f.bounds, "recovery must not repeat outer window bounds")
			require.False(t, f.cs.FrameState().Ready, "measurement is not presentation acknowledgement")
		})
	}
}
