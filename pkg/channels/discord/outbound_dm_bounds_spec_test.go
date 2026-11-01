package discord

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/stretchr/testify/require"
)

type t24RoundTrip func(*http.Request) (*http.Response, error)

func (f t24RoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type t24ObservedBody struct {
	io.ReadCloser
	onClose func()
}

func (b *t24ObservedBody) Close() error {
	err := b.ReadCloser.Close()
	b.onClose()
	return err
}

func TestT24_DiscordStart_RESTOnlyGroupIsSentToItsChannelWithoutDMOpen(t *testing.T) {
	msg := t09StartMessage(t, "333", "QA thread")
	ch, network := t09Adapter(t,
		t09HTTPResponse{Status: 200, Body: `{"id":"333","type":11}`},
		t09HTTPResponse{Status: 200, Body: `{"id":"777","channel_id":"333","content":"QA thread"}`},
	)
	require.NoError(t, ch.Send(context.Background(), msg), "T-9: REST-only public thread is a group, not a person")
	t09RequireRoutes(t, network, []string{t09Route("GET", "/channels/333"), t09Route("POST", "/channels/333/messages")})
}

func TestT24_DiscordStart_UnknownChannelAndUnknownUserNeverOpenOrSend(t *testing.T) {
	msg := t09StartMessage(t, "444", "QA not found")
	ch, network := t09Adapter(t,
		t09HTTPResponse{Status: 404, Body: `{"code":10003,"message":"Unknown Channel"}`},
		t09HTTPResponse{Status: 404, Body: `{"code":10013,"message":"Unknown User"}`},
	)
	require.Error(t, ch.Send(context.Background(), msg), "S-14 row6: nonexistent target is an adapter error, never accepted delivery")
	t09RequireRoutes(t, network, []string{t09Route("GET", "/channels/444"), t09Route("GET", "/users/444")})
}

func TestT24b_ExternalHTTPControl_LateSuccessIsVisibleEvenWithCancelledContext(t *testing.T) {
	// Instrument control, not product success: the adversarial external edge
	// must be capable of returning a late successful DM after cancellation.
	ch, network := t09Adapter(t, t09HTTPResponse{Status: 200, Body: `{"id":"999","type":1}`})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	actual, err := ch.session.UserChannelCreate("333", discordgo.WithContext(ctx), discordgo.WithRestRetries(0))
	require.NoError(t, err, "instrument: real decoder can receive an adversarial late HTTP success")
	require.Equal(t, "999", actual.ID)
	t09RequireRoutes(t, network, []string{t09Route("POST", "/users/@me/channels")})
}

// Admit two fresh real lookups simultaneously after releasing the late DM.
// Their external HTTP barriers prove both shared slots are reusable, rather
// than checking a snapshot immediately after a response-body Close callback.
func t24ProveBothSlotsReusable(t *testing.T, observeLate func()) {
	t.Helper()
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	done := make(chan channels.OutboundTargetResult, 2)
	for _, id := range []string{"777", "888"} {
		ch, _ := t09Adapter(t)
		resolver := t09Resolver(t, ch)
		ch.session.Client.Transport = t24RoundTrip(func(req *http.Request) (*http.Response, error) {
			entered <- struct{}{}
			select {
			case <-release:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			bare := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
			return &http.Response{StatusCode: 200, Header: http.Header{}, Request: req,
				Body: io.NopCloser(strings.NewReader(`{"id":"` + bare + `","type":0}`))}, nil
		})
		go func(target string, r channels.OutboundTargetResolver) {
			done <- r.ResolveOutboundTarget(ctx, "discord:"+target)
		}(id, resolver)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("S-89: both wrapper slots were not reusable after late DM completed")
		}
	}
	observeLate()
	once.Do(func() { close(release) })
	for i := 0; i < 2; i++ {
		select {
		case result := <-done:
			require.Equal(t, channels.TargetErrorCode(""), result.Code, "slot-reuse positive control is a real successful lookup")
		case <-ctx.Done():
			t.Fatal("instrument: admitted slot-reuse lookups did not finish")
		}
	}
}

// Direct-adapter slices. The separate real-manager reload/join and phased
// two-slot starvation rows below observe their own actual lifecycle barriers.
func TestT24b_DMOpen_DeadlineAndCancellationBoundRealExternalWork(t *testing.T) {
	for _, phase := range []string{"deadline", "request_cancel"} {
		t.Run(phase, func(t *testing.T) {
			msg := t09StartMessage(t, "333", "QA must not arrive")
			ch, network := t09Adapter(t,
				t09HTTPResponse{Status: 404, Body: `{"code":10003,"message":"Unknown Channel"}`},
				t09HTTPResponse{Status: 200, Body: `{"id":"333","username":"qa"}`},
				t09HTTPResponse{Status: 200, Body: `{"id":"999","type":1}`},
			)
			_ = t09Resolver(t, ch) // actual lookup capability, never a fake resolver.
			userReached, dmReached, lateFinished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			releaseHTTP := make(chan struct{})
			var userOnce, dmOnce, lateOnce, releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(releaseHTTP) }) })
			ch.session.Client.Transport = t24RoundTrip(func(req *http.Request) (*http.Response, error) {
				response, err := network.RoundTrip(req)
				if req.URL.Path == "/api/v"+discordgo.APIVersion+"/users/333" {
					userOnce.Do(func() { close(userReached) })
				}
				if err == nil && req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/users/@me/channels") {
					dmOnce.Do(func() { close(dmReached) })
					if phase == "request_cancel" {
						<-releaseHTTP // adversarial external HTTP ignores cancellation.
					}
					response.Body = &t24ObservedBody{ReadCloser: response.Body, onClose: func() { lateOnce.Do(func() { close(lateFinished) }) }}
				}
				return response, err
			})
			// Real discordgo admission mutex; acquired before RequestOptions apply.
			bucket := ch.session.Ratelimiter.GetBucket(discordgo.EndpointUserChannels(""))
			if phase == "deadline" {
				bucket.Lock()
			}
			var unlock sync.Once
			release := func() {
				if phase == "deadline" {
					unlock.Do(bucket.Unlock)
				}
				releaseOnce.Do(func() { close(releaseHTTP) })
			}
			t.Cleanup(release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			callerDone := make(chan struct{})
			go func() { defer close(callerDone); result <- ch.Send(ctx, msg) }()
			t.Cleanup(func() {
				cancel()
				release()
				select {
				case <-callerDone:
				case <-time.After(6 * time.Second):
					t.Error("instrument: outer Send caller did not stop during cleanup")
				}
			})
			select {
			case <-userReached:
			case err := <-result:
				t.Fatalf("S-89 fixture: Send returned before resolving the person/DM admission: %v", err)
			case <-time.After(6 * time.Second):
				t.Fatal("instrument: real person lookup did not reach the external HTTP edge")
			}
			expected := []string{t09Route("GET", "/channels/333"), t09Route("GET", "/users/333")}
			if phase == "request_cancel" {
				select {
				case <-dmReached:
				case err := <-result:
					t.Fatalf("instrument: Send returned before DM HTTP cancellation barrier: %v", err)
				case <-time.After(6 * time.Second):
					t.Fatal("instrument: DM-open did not reach external HTTP")
				}
				expected = append(expected, t09Route("POST", "/users/@me/channels"))
				cancel()
			}
			select {
			case err := <-result:
				require.Error(t, err, "S-89: deadline/cancellation is an adapter error, not delivery")
			case <-time.After(6 * time.Second): // normative5s + small1s allowance, not a widened product timeout.
				t.Fatal("FR-046: DM-open wrapper did not bound real admission/HTTP work")
			}
			t09RequireRoutes(t, network, expected)
			release() // external late call now returns a successful DM999.
			select {
			case <-lateFinished:
			case <-time.After(6 * time.Second):
				t.Fatal("instrument: released late DM-open did not finish its real HTTP response")
			}
			t24ProveBothSlotsReusable(t, func() {
				t09RequireRoutes(t, network, []string{t09Route("GET", "/channels/333"), t09Route("GET", "/users/333"), t09Route("POST", "/users/@me/channels")})
			})
		})
	}
}

func TestT24b_DMOpen_HTTP502HasOneAttemptAndNoMessageContinuation(t *testing.T) {
	ch, network := t09Adapter(t,
		t09HTTPResponse{Status: 404, Body: `{"code":10003,"message":"Unknown Channel"}`},
		t09HTTPResponse{Status: 200, Body: `{"id":"333","username":"qa"}`},
		t09HTTPResponse{Status: 502, Body: `{"code":0,"message":"bad gateway"}`},
	)
	require.Error(t, ch.Send(context.Background(), t09StartMessage(t, "333", "QA failed DM")),
		"S-89 r8: failed DM-open is an adapter error")
	t09RequireRoutes(t, network, []string{t09Route("GET", "/channels/333"), t09Route("GET", "/users/333"), t09Route("POST", "/users/@me/channels")})
}

func TestT24b_DMOpen_ReloadJoinRequiresTheRealWorkerCancellationBarrier(t *testing.T) {
	logPath := t24ErrorLog(t)
	// Registered before the manager: release/join participants, stop workers,
	// and only then restore process-wide observations (cleanup is LIFO).
	t.Cleanup(func() { SetBeforeDMOpenHookForTest(nil); SetWrappedCallSlotHookForTest(nil) })
	adapter, network := t09Adapter(t,
		t09HTTPResponse{Status: 404, Body: `{"code":10003,"message":"Unknown Channel"}`},
		t09HTTPResponse{Status: 200, Body: `{"id":"333","username":"qa"}`},
		t09HTTPResponse{Status: 200, Body: `{"id":"999","type":1}`},
	)
	manager, mb, lifecycle := t24Manager(t, adapter)
	person, admitted, joined := make(chan string, 1), make(chan string, 8), make(chan string, 2)
	locks, dmEntered := make(chan string, 2), make(chan struct{})
	lateFinished := make(chan struct{})
	var lateOnce, dmOnce sync.Once
	var participants sync.WaitGroup
	adapter.session.Client.Transport = t24RoundTrip(func(req *http.Request) (*http.Response, error) {
		response, err := network.RoundTrip(req)
		if err == nil && req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/users/@me/channels") {
			response.Body = &t24ObservedBody{ReadCloser: response.Body, onClose: func() { lateOnce.Do(func() { close(lateFinished) }) }}
		}
		return response, err
	})
	// The real discordgo mutex blocks BEFORE RequestOptions can apply context.
	bucket := adapter.session.Ratelimiter.GetBucket(discordgo.EndpointUserChannels(""))
	bucket.Lock()
	var unlock sync.Once
	release := func() { unlock.Do(bucket.Unlock) }
	ctx, cancel := context.WithCancel(context.Background())
	SetBeforeDMOpenHookForTest(func(id string) { person <- id })
	SetWrappedCallSlotHookForTest(func(call string) {
		if call == "dm_open" {
			dmOnce.Do(func() { close(dmEntered) })
		}
		admitted <- call
	})
	manager.SetReloadWriteLockHookForTest(func(stage string) { locks <- stage })
	manager.SetWorkerJoinHookForTest(func(instance, phase string) {
		if instance == "discord" {
			joined <- phase // only signal while Reload holds the real write lock
		}
	})
	t.Cleanup(func() {
		cancel()
		release()
		done := make(chan struct{})
		go func() { participants.Wait(); close(done) }()
		t24Receive(t, done, "instrument: real reload/follow-up participants did not finish during cleanup")
		select {
		case <-dmEntered:
			t24Receive(t, lateFinished, "instrument: released raw DM-open did not finish during cleanup")
		default:
		}
		manager.SetReloadWriteLockHookForTest(nil)
		manager.SetWorkerJoinHookForTest(nil)
	})
	msg := t09StartMessage(t, "333", "QA late DM must not arrive")
	msg.AgentID, msg.WorkspaceID = "mia", "ws-t24"
	require.NoError(t, mb.PublishOutbound(ctx, msg))
	require.Equal(t, "333", t24Receive(t, person, "instrument: actual start did not resolve the listed person"))
	for _, want := range []string{"channel", "user", "dm_open"} {
		require.Equal(t, want, t24Receive(t, admitted, "instrument: real wrapper never admitted the DM-open phase"))
	}
	t09RequireRoutes(t, network, []string{t09Route("GET", "/channels/333"), t09Route("GET", "/users/333")})
	bound, cancelBound := context.WithTimeout(context.Background(), 6*time.Second) // one S-89 bound for the real reload and join
	defer cancelBound()
	reloaded := make(chan error, 1)
	participants.Add(1)
	go func() { defer participants.Done(); reloaded <- manager.Reload(ctx, lifecycle, nil) }()
	for _, want := range []string{"acquired", "reacquired"} {
		require.Equal(t, want, t24ReceiveUntil(t, bound, locks, "instrument: actual Reload did not reach its write-lock stages"))
	}
	for _, want := range []string{"joining", "joined"} {
		require.Equal(t, want, t24ReceiveUntil(t, bound, joined, "S-89 phase2: actual worker join stalled behind DM admission"))
	}
	require.NoError(t, t24ReceiveUntil(t, bound, reloaded, "S-89 phase2: actual Reload did not finish within the DM bound"))
	t24RequireAdapterError(t, logPath)
	followUp := make(chan bool, 1)
	participants.Add(1)
	go func() {
		defer participants.Done()
		current, ok := manager.GetChannel("discord")
		followUp <- ok && current == adapter
	}()
	require.True(t, t24ReceiveUntil(t, bound, followUp, "S-89: post-reload lifecycle-lock operation stalled"))
	// Only after the actual worker is joined do we release the successful late
	// DM result. It can no longer continue into message delivery.
	release()
	t24Receive(t, lateFinished, "instrument: successful late DM-open response never finished")
	t24ProveBothSlotsReusable(t, func() {
		t09RequireRoutes(t, network, []string{t09Route("GET", "/channels/333"), t09Route("GET", "/users/333"), t09Route("POST", "/users/@me/channels")})
	})
}

func TestT24b_DMOpen_StarvationRequiresTheSharedTwoSlotPhaseBarrier(t *testing.T) {
	logPath := t24ErrorLog(t)
	t.Cleanup(func() { SetBeforeDMOpenHookForTest(nil); SetWrappedCallSlotHookForTest(nil) })
	adapter, network := t09Adapter(t,
		t09HTTPResponse{Status: 404, Body: `{"code":10003,"message":"Unknown Channel"}`},
		t09HTTPResponse{Status: 200, Body: `{"id":"333","username":"qa"}`},
	)
	manager, _, _ := t24Manager(t, adapter)
	person, admitted := make(chan string, 1), make(chan string, 8)
	resumeDM, releaseHTTP := make(chan struct{}), make(chan struct{})
	var resumeOnce, releaseOnce sync.Once
	var participants sync.WaitGroup
	rawCalls := make([]struct{ entered, finished <-chan struct{} }, 0, 2) // S-89: both shared slots
	ctx, cancel := context.WithCancel(context.Background())
	SetBeforeDMOpenHookForTest(func(id string) { person <- id; <-resumeDM })
	SetWrappedCallSlotHookForTest(func(call string) { admitted <- call })
	t.Cleanup(func() {
		cancel()
		resumeOnce.Do(func() { close(resumeDM) })
		releaseOnce.Do(func() { close(releaseHTTP) })
		done := make(chan struct{})
		go func() { participants.Wait(); close(done) }()
		t24Receive(t, done, "instrument: actual send/lookup callers did not finish during cleanup")
		for _, raw := range rawCalls {
			select {
			case <-raw.entered:
				t24Receive(t, raw.finished, "instrument: released raw lookup did not finish during cleanup")
			default:
			}
		}
	})
	msg := t09StartMessage(t, "333", "QA starved DM must not arrive")
	msg.AgentID, msg.WorkspaceID = "mia", "ws-t24"
	sent := make(chan error, 1)
	participants.Add(1)
	go func() { defer participants.Done(); sent <- manager.SendMessage(ctx, msg) }()
	require.Equal(t, "333", t24Receive(t, person, "instrument: start never reached before-DM phase after real person resolution"))
	for _, want := range []string{"channel", "user"} {
		require.Equal(t, want, t24Receive(t, admitted, "instrument: actual start did not traverse both lookup phases"))
	}
	// Occupy BOTH actual shared slots only AFTER this start resolved its person.
	// External HTTP deliberately ignores cancellation, so timed-out lookups keep
	// their slots until explicitly released (the real abandoned-call hazard).
	parked, lateDone := make(chan string, 2), make(chan string, 2)
	lookedUp := make(chan channels.OutboundTargetResult, 2)
	for _, id := range []string{"777", "888"} {
		other, _ := t09Adapter(t)
		resolver := t09Resolver(t, other)
		rawEntered, rawFinished := make(chan struct{}), make(chan struct{})
		rawCalls = append(rawCalls, struct{ entered, finished <-chan struct{} }{rawEntered, rawFinished})
		var enteredOnce, finishedOnce sync.Once
		other.session.Client.Transport = t24RoundTrip(func(req *http.Request) (*http.Response, error) {
			enteredOnce.Do(func() { close(rawEntered) })
			bare := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
			parked <- bare
			<-releaseHTTP
			return &http.Response{StatusCode: 200, Header: http.Header{}, Request: req,
				Body: &t24ObservedBody{ReadCloser: io.NopCloser(strings.NewReader(`{"id":"` + bare + `","type":0}`)), onClose: func() {
					finishedOnce.Do(func() { lateDone <- bare; close(rawFinished) })
				}}}, nil
		})
		participants.Add(1)
		go func(target string, r channels.OutboundTargetResolver) {
			defer participants.Done()
			lookedUp <- r.ResolveOutboundTarget(ctx, "discord:"+target)
		}(id, resolver)
	}
	ids := []string{}
	for i := 0; i < 2; i++ {
		require.Equal(t, "channel", t24Receive(t, admitted, "instrument: both real lookup slots were not admitted"))
		ids = append(ids, t24Receive(t, parked, "instrument: both admitted lookups did not park at external HTTP"))
	}
	require.ElementsMatch(t, []string{"777", "888"}, ids)
	resumeOnce.Do(func() { close(resumeDM) })
	// Manager.SendMessage's API returns nil after its retry driver finishes;
	// the adapter error itself is observed at the real logging boundary below.
	require.NoError(t, t24Receive(t, sent, "S-89: starved DM admission exceeded five seconds plus allowance"))
	t24RequireAdapterError(t, logPath)
	require.Len(t, admitted, 0, "S-89: a starved DM-open never entered discordgo or acquired a separate slot")
	t09RequireRoutes(t, network, []string{t09Route("GET", "/channels/333"), t09Route("GET", "/users/333")})
	for i := 0; i < 2; i++ {
		require.Equal(t, channels.TargetErrLookupUnavailable, t24Receive(t, lookedUp, "instrument: parked lookup did not honor its own deadline").Code)
	}
	releaseOnce.Do(func() { close(releaseHTTP) })
	lateIDs := []string{}
	for i := 0; i < 2; i++ {
		lateIDs = append(lateIDs, t24Receive(t, lateDone, "instrument: released external lookup did not finish"))
	}
	require.ElementsMatch(t, []string{"777", "888"}, lateIDs)
	t24ProveBothSlotsReusable(t, func() {
		t09RequireRoutes(t, network, []string{t09Route("GET", "/channels/333"), t09Route("GET", "/users/333")})
	})
}
