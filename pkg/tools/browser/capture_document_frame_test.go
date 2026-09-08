package browser

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func documentFixture(t *testing.T) (*CaptureSession, *adapterRelay) {
	t.Helper()
	cs, r := adapterFixture(t)
	require.True(t, cs.CommitFrameBoundary(1, "page-a", 100))
	return cs, r
}

func TestDocumentTransitionAlwaysRetiresPreviousDocument(t *testing.T) {
	cs, _ := documentFixture(t)
	first, err := cs.beginDocumentTransition("page-a")
	require.NoError(t, err)
	defer first.cancel()
	require.Equal(t, uint64(2), cs.FrameState().Generation)
	second, err := cs.beginDocumentTransition("page-a")
	require.NoError(t, err)
	defer second.cancel()
	require.Equal(t, uint64(3), cs.FrameState().Generation, "same-target pending navigation must create another identity")
	require.ErrorIs(t, first.ctx.Err(), context.Canceled)
	require.NoError(t, second.ctx.Err())
	require.False(t, cs.documentTransitionCurrent(first))
	require.True(t, cs.documentTransitionCurrent(second))
}

func TestDocumentTransitionViewportCannotAuthorizePendingDocument(t *testing.T) {
	for _, qualified := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "qualified"}[qualified], func(t *testing.T) {
			cs, r := documentFixture(t)
			var sends atomic.Int32
			if qualified {
				recoveryBind(t, cs, r)
			} else {
				cs.BindIngest(func(action string, _ *string, _, _, _ int) error {
					if action == "recapture" {
						sends.Add(1)
					}
					return nil
				}, func() {})
			}
			token, err := cs.beginDocumentTransition("page-a")
			require.NoError(t, err)
			defer token.cancel()
			pending, err := cs.BeginFrameTransition("page-a", 1024, 768, 2)
			require.NoError(t, err)
			require.True(t, cs.documentTransitionCurrent(token))
			require.NoError(t, token.ctx.Err())
			assert.Zero(t, pending.Width, "viewport cannot expose old-document geometry")
			assert.Zero(t, pending.Height)
			assert.False(t, pending.Ready)
			assert.False(t, cs.CommitFrameBoundary(pending.Generation, "page-a", 200))
			assert.False(t, cs.AcceptsInputGeneration(pending.CaptureID, pending.Generation))
			cs.RecaptureAt(1024, 768)
			assert.False(t, cs.RecaptureFrame(pending))
			assert.False(t, cs.RecaptureFrameContext(context.Background(), pending))
			assert.Zero(t, sends.Load())
			assert.Empty(t, r.recoveryFrames())
			assert.Zero(t, r.recaptureCount())
			measured, err := cs.completeDocumentTransition(token, 1024, 768, 2)
			require.NoError(t, err)
			require.Equal(t, uint64(4), measured.Generation, "measured geometry needs a fresh immutable wire identity")
			require.Equal(t, 1024, measured.Width)
			require.Equal(t, 768, measured.Height)
			require.Equal(t, float64(2), measured.Scale)
			require.False(t, measured.Ready)
			require.ErrorIs(t, token.ctx.Err(), context.Canceled)
			require.False(t, cs.documentTransitionCurrent(token))
			assert.False(t, cs.AcceptsInputGeneration(measured.CaptureID, measured.Generation))
			require.True(t, cs.CommitFrameBoundary(measured.Generation, "page-a", 300))
			require.True(t, cs.AcceptsInputGeneration(measured.CaptureID, measured.Generation))
		})
	}
}

func TestDocumentTransitionRejectsRetainedCompletion(t *testing.T) {
	for _, event := range []string{"new document", "target switch", "stop"} {
		t.Run(event, func(t *testing.T) {
			cs, _ := documentFixture(t)
			old, err := cs.beginDocumentTransition("page-a")
			require.NoError(t, err)
			defer old.cancel()
			switch event {
			case "new document":
				next, documentErr := cs.beginDocumentTransition("page-a")
				require.NoError(t, documentErr)
				defer next.cancel()
			case "target switch":
				_, transitionErr := cs.BeginFrameTransition("page-b", 900, 700, 1)
				require.NoError(t, transitionErr)
			case "stop":
				cs.Stop()
			}
			before := cs.FrameState()
			assert.ErrorIs(t, old.ctx.Err(), context.Canceled)
			assert.False(t, cs.documentTransitionCurrent(old))
			_, err = cs.completeDocumentTransition(old, 700, 500, 1)
			require.ErrorIs(t, err, ErrStaleCaptureFrame)
			require.Equal(t, before, cs.FrameState(), "retired document completion changed current frame")
		})
	}
}

func TestDocumentTransitionInvalidCompletionPreservesPendingClaim(t *testing.T) {
	for _, geometry := range []struct {
		w, h  int
		scale float64
	}{{0, 0, 1}, {0, 600, 1}, {800, 0, 1}, {16385, 600, 1}, {800, 600, math.NaN()}, {800, 600, 0.5}, {800, 600, math.Inf(1)}} {
		cs, _ := documentFixture(t)
		token, err := cs.beginDocumentTransition("page-a")
		require.NoError(t, err)
		defer token.cancel()
		before := cs.FrameState()
		_, err = cs.completeDocumentTransition(token, geometry.w, geometry.h, geometry.scale)
		require.Error(t, err)
		require.True(t, cs.documentTransitionCurrent(token))
		require.NoError(t, token.ctx.Err())
		require.Equal(t, before, cs.FrameState())
	}
}

type documentWaitContext struct {
	context.Context
	waiting chan struct{}
}

func (c documentWaitContext) Done() <-chan struct{} {
	select {
	case c.waiting <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

func TestDocumentTransitionCompletionWakesGeometryWaiter(t *testing.T) {
	cs, _ := documentFixture(t)
	token, err := cs.beginDocumentTransition("page-a")
	require.NoError(t, err)
	defer token.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	observed := documentWaitContext{ctx, make(chan struct{}, 4)}
	done := make(chan confirmedFrameResult, 1)
	go func() { f, e := cs.WaitConfirmedFrame(observed, "", 0); done <- confirmedFrameResult{f, e} }()
	select {
	case <-observed.waiting:
	case <-time.After(time.Second):
		t.Fatal("geometry waiter did not reach its blocking select")
	}
	_, err = cs.BeginFrameTransition("page-a", 1024, 768, 2)
	require.NoError(t, err)
	select {
	case got := <-done:
		t.Fatalf("viewport authorized pending document: %+v", got)
	case <-observed.waiting:
	case <-time.After(time.Second):
		t.Fatal("viewport did not wake geometry waiter")
	}
	measured, err := cs.completeDocumentTransition(token, 641, 479, 1.25)
	require.NoError(t, err)
	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.Equal(t, measured, got.frame)
	case <-time.After(time.Second):
		t.Fatal("document completion did not wake geometry waiter")
	}
}

func TestDocumentPendingStartupReleasesGateBeforeWaiting(t *testing.T) {
	for _, finish := range []string{"complete", "cancel", "stop"} {
		t.Run(finish, func(t *testing.T) {
			cs, mgr, active := preparedCaptureFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{})
			done := make(chan confirmedFrameResult, 1)
			var token *captureDocumentTransition
			var calls atomic.Int32
			go func() {
				frame, err := cs.prepareEncoderFrame(ctx, func(context.Context) (int, int, float64, error) {
					if calls.Add(1) == 1 {
						var err error
						token, err = cs.beginDocumentTransition("verified-target")
						close(entered)
						if err != nil {
							return 0, 0, 0, err
						}
						return 800, 600, 1, nil
					}
					return 641, 479, 1.25, nil
				})
				done <- confirmedFrameResult{frame, err}
			}()
			<-entered
			if token != nil {
				defer token.cancel()
			}
			gateCtx, cancelGate := context.WithTimeout(context.Background(), time.Second)
			defer cancelGate()
			release, err := mgr.acquireLiveTabCommand(gateCtx, testSessionID)
			if err != nil {
				cancel()
				<-done
				t.Fatalf("startup held command gate while awaiting document paint: %v", err)
			}
			var premature *confirmedFrameResult
			select {
			case got := <-done:
				premature = &got
				t.Errorf("startup published old-document measurement: %+v", got)
			default:
			}
			switch finish {
			case "complete":
				_, err = cs.completeDocumentTransition(token, 641, 479, 1.25)
			case "cancel":
				cancel()
			case "stop":
				cs.Stop()
			}
			release()
			require.NoError(t, err)
			var got confirmedFrameResult
			if premature != nil {
				got = *premature
			} else {
				select {
				case got = <-done:
				case <-time.After(time.Second):
					t.Fatal("document-gated startup did not finish")
				}
			}
			if finish == "complete" {
				require.NoError(t, got.err)
				require.Equal(t, 641, got.frame.Width)
				require.Equal(t, 479, got.frame.Height)
				require.Equal(t, 1.25, got.frame.Scale)
				require.False(t, got.frame.Ready)
				require.Equal(t, int32(2), calls.Load())
			} else {
				require.ErrorIs(t, got.err, context.Canceled)
				require.Equal(t, CaptureFrameState{}, got.frame)
			}
			require.NoError(t, active.Err(), "capture operation canceled persistent browser target")
		})
	}
}

func TestDocumentTransitionRetiresLegacyRecaptureContinuation(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "completed"}[complete], func(t *testing.T) {
			cs, _ := documentFixture(t)
			var sends atomic.Int32
			cs.BindIngest(func(action string, _ *string, _, _, _ int) error {
				if action == "recapture" {
					sends.Add(1)
				}
				return nil
			}, func() {})
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			cs.foregroundAssertFn = func(context.Context) bool { close(entered); <-release; return true }
			cs.RecaptureForTabChangeAt(800, 600)
			<-entered
			token, err := cs.beginDocumentTransition("page-a")
			require.NoError(t, err)
			defer token.cancel()
			if complete {
				_, err = cs.completeDocumentTransition(token, 641, 479, 1.25)
				require.NoError(t, err)
			}
			unblock()
			deadline := time.Now().Add(time.Second)
			for {
				cs.mu.Lock()
				running := cs.tabChangeRecaptureRunning
				cs.mu.Unlock()
				if !running {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("legacy recapture continuation did not end")
				}
				time.Sleep(time.Millisecond)
			}
			require.Zero(t, sends.Load(), "old-document recapture reached transport after its foreground wait")
		})
	}
}

func TestDocumentTransitionExhaustionPreservesCurrentToken(t *testing.T) {
	cs, _ := documentFixture(t)
	token, err := cs.beginDocumentTransition("page-a")
	require.NoError(t, err)
	defer token.cancel()
	cs.mu.Lock()
	cs.frames.mu.Lock()
	cs.frames.current.Generation = 9007199254740991
	cs.frames.mu.Unlock()
	cs.mu.Unlock()
	before := cs.FrameState()
	next, err := cs.beginDocumentTransition("page-a")
	if next != nil {
		defer next.cancel()
	}
	require.Error(t, err)
	require.Nil(t, next)
	require.True(t, cs.documentTransitionCurrent(token))
	require.NoError(t, token.ctx.Err())
	require.Equal(t, before, cs.FrameState())
	_, err = cs.completeDocumentTransition(token, 641, 479, 1)
	require.Error(t, err)
	require.True(t, cs.documentTransitionCurrent(token))
	require.Equal(t, before, cs.FrameState())
}

func TestDocumentPendingRejectsHealthAndIngestClaims(t *testing.T) {
	for _, qualified := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "qualified"}[qualified], func(t *testing.T) {
			cs, r := documentFixture(t)
			var epoch uint64
			if qualified {
				epoch = recoveryBind(t, cs, r)
			} else {
				_, epoch = cs.BindIngest(func(string, *string, int, int, int) error { return nil }, func() {})
			}
			token, err := cs.beginDocumentTransition("page-a")
			require.NoError(t, err)
			defer token.cancel()
			frame, err := cs.BeginFrameTransition("page-a", 1024, 768, 2)
			require.NoError(t, err)
			sample := CaptureHealthObservation{CaptureGeneration: frame.Generation, TargetID: "page-a", Generation: 47, TrackState: "live"}
			require.True(t, cs.RecordIngestHeartbeat(epoch, &sample), "original socket heartbeat remains admissible")
			assert.Equal(t, CaptureHealthObservation{}, cs.CaptureHealth(), "pending document accepted a frame health sample")
			r.mu.Lock()
			r.stats = webrtc.Stats{HasVideo: true, VideoPackets: 10, VideoReceipt: webrtc.VideoReceipt{BindingToken: 47, Generation: frame.Generation, TargetID: "page-a", Serial: 10}}
			r.mu.Unlock()
			assert.False(t, cs.WatchdogSnapshot().ReceiptCurrent)
			rec := &healthRecorder{}
			cs.SetOnVideoHealth(rec.observe)
			cs.ReportCaptureFailure()
			r.triggerIngestLive()
			cs.RecordVideoProgress()
			assert.Zero(t, rec.count(VideoHealthLost))
			assert.Zero(t, rec.count(VideoHealthRecovered))
			assert.Zero(t, r.recaptureCount())
			cs.mu.Lock()
			live := cs.ingestVideoLive
			cs.mu.Unlock()
			assert.False(t, live)
			if qualified {
				_, err = cs.HandleIngestOfferForBinding(context.Background(), epoch, 1, "offer", frame.Generation, "page-a")
				require.ErrorIs(t, err, webrtc.ErrStaleIngestOffer)
				require.Empty(t, r.calls())
			}
		})
	}
}
