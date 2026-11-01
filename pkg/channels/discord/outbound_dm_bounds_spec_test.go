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

// Direct-adapter slices; real-manager reload/join and phase-specific starvation
// remain the explicit BLOCKED rows below, not inferred from these HTTP probes.
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
	t.Fatal("BLOCKED: bounded Discord DM-open and deterministic reload/worker cancellation barrier not implemented on WP-2 — required by T-24b/S-89 phase2/FR-046; no fake reload or read-lock probe substitutes for the real worker join")
}

func TestT24b_DMOpen_StarvationRequiresTheSharedTwoSlotPhaseBarrier(t *testing.T) {
	t.Fatal("BLOCKED: shared bounded Discord look-up/DM-open wrapper and deterministic before-DM admission barrier not implemented on WP-2 — required by T-24b/S-89 starvation/FR-046; both lookup slots must be parked after person resolution, never inferred from one blocked call")
}
