package gateway

// T-33: real Discord session/limiter/wrapper via the real outbound REST writer.
// HTTP is the only replaced boundary; bucket wait and semaphore fixtures stay
// separate (S-46 versus S-46b), with no t.Parallel/global transport overlap.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/stretchr/testify/require"
)

func TestT33_ExternalHTTPControl_DefaultSessionUsesTheScriptedDiscordEdge(t *testing.T) {
	network := t33ExternalHTTP(t)
	network.handle = func(req *http.Request, _ int) (*http.Response, error) { return t33GroupResponse(req), nil }
	session, err := discordgo.New("Bot qa-t33-control")
	require.NoError(t, err)
	actual, err := session.Channel("777", discordgo.WithRestRetries(0))
	require.NoError(t, err)
	require.Equal(t, "777", actual.ID)
	require.Equal(t, discordgo.ChannelTypeGuildText, actual.Type)
	paths, maximum := network.snapshot()
	require.Equal(t, []string{"GET /api/v" + discordgo.APIVersion + "/channels/777"}, paths)
	require.Equal(t, 1, maximum)
}

func TestT33_RealLimiterHeaderWaitIsInsideTheFiveSecondLookupBound(t *testing.T) {
	f, _ := t32Fixture(t, "discord", []string{"discord:222"})
	resolver := t33RealDiscord(t, f, "discord")
	network := t33ExternalHTTP(t)
	lateClosed := make(chan struct{})
	var lateOnce sync.Once
	network.handle = func(req *http.Request, index int) (*http.Response, error) {
		response := t33GroupResponse(req)
		if index == 0 {
			// Real Bucket.Release consumes these headers. Eight seconds is
			// deliberately beyond the normative5s, not a raised product bound.
			response.Header.Set("X-Ratelimit-Remaining", "0")
			response.Header.Set("X-Ratelimit-Reset-After", "8")
		} else if strings.HasSuffix(req.URL.Path, "/channels/777") {
			response.Body = &t33ClosedBody{ReadCloser: response.Body, closeSignal: func() { lateOnce.Do(func() { close(lateClosed) }) }}
		}
		return response, nil
	}
	primed := resolver.ResolveOutboundTarget(context.Background(), "discord:777")
	require.Equal(t, channels.TargetErrorCode(""), primed.Code, "instrument: real session parsed the header-bearing prime")
	before := t13Before(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	pending := t33AsyncPut(t, ctx, f, "discord", "agent_may_start", []string{"222", "777"}, t14Read(t, f, "discord").Revision)
	t33CleanupRequest(t, pending, cancel)
	// Keep the fake external HTTP boundary installed while the real eight-
	// second bucket wait naturally settles, even when a product assertion fails.
	t.Cleanup(func() {
		cancel()
		select {
		case <-lateClosed:
		case <-time.After(9 * time.Second):
			t.Error("instrument: late limiter work did not settle before HTTP transport restoration")
		}
	})
	rec := t33Await(t, pending, 6*time.Second, "S-46: wrapper did not bound the real limiter's context-blind admission wait")
	require.LessOrEqual(t, time.Since(started), 6*time.Second, "5s normative deadline plus1s allowance")
	t14Rejection(t, rec, http.StatusBadGateway, "lookup_unavailable")
	t13Unchanged(t, f, before)
	paths, _ := network.snapshot()
	require.Equal(t, []string{"GET /api/v" + discordgo.APIVersion + "/channels/777"}, paths, "no second HTTP while the actual bucket still delays admission")
	select {
	case <-lateClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: naturally released late limiter call did not finish its HTTP body")
	}
	// A fresh target uses the same real wrapper after the late external call.
	reuse := resolver.ResolveOutboundTarget(context.Background(), "discord:888")
	require.Equal(t, channels.TargetErrorCode(""), reuse.Code)
	t13Unchanged(t, f, before)
	paths, maximum := network.snapshot()
	require.Equal(t, []string{"GET /api/v" + discordgo.APIVersion + "/channels/777", "GET /api/v" + discordgo.APIVersion + "/channels/777", "GET /api/v" + discordgo.APIVersion + "/channels/888"}, paths)
	require.LessOrEqual(t, maximum, 2, "external HTTP in-flight work respects the wrapper pool (not an internal goroutine census)")
}

func TestT33_ProcessWideTwoSlotSaturationRefusesThirdWithoutHTTPThenReusesSlots(t *testing.T) {
	f, _ := t32Fixture(t, "discord", []string{"discord:222"})
	for _, id := range []string{"discord", "discord.b", "discord.c", "discord.d", "discord.e"} {
		_ = t33RealDiscord(t, f, id)
	}
	network := t33ExternalHTTP(t)
	entered, lateClosed := make(chan struct{}, 3), make(chan struct{}, 3)
	release := make(chan struct{})
	freshEntered, freshRelease := make(chan struct{}, 2), make(chan struct{})
	var releaseOnce, freshOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	unblockFresh := func() { unblock(); freshOnce.Do(func() { close(freshRelease) }) }
	t.Cleanup(unblockFresh)
	network.handle = func(req *http.Request, _ int) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/channels/777") || strings.HasSuffix(req.URL.Path, "/channels/888") || strings.HasSuffix(req.URL.Path, "/channels/999") {
			entered <- struct{}{}
			<-release // real external HTTP ignores context so the slot remains occupied.
			response := t33GroupResponse(req)
			var once sync.Once
			response.Body = &t33ClosedBody{ReadCloser: response.Body, closeSignal: func() { once.Do(func() { lateClosed <- struct{}{} }) }}
			return response, nil
		}
		if strings.HasSuffix(req.URL.Path, "/channels/111") || strings.HasSuffix(req.URL.Path, "/channels/112") {
			freshEntered <- struct{}{}
			<-freshRelease
		}
		return t33GroupResponse(req), nil
	}
	before := t13Before(t, f)
	one := t33AsyncPut(t, context.Background(), f, "discord", "agent_may_start", []string{"222", "777"}, t14Read(t, f, "discord").Revision)
	t33CleanupRequest(t, one, unblock)
	t33Enter(t, entered, "instrument: first actual wrapper call did not enter external HTTP")
	two := t33AsyncPut(t, context.Background(), f, "discord.b", "agent_may_start", []string{"222", "888"}, t14Read(t, f, "discord.b").Revision)
	t33CleanupRequest(t, two, unblock)
	t33Enter(t, entered, "instrument: second actual wrapper slot did not enter external HTTP")
	three := t33AsyncPut(t, context.Background(), f, "discord.c", "agent_may_start", []string{"222", "999"}, t14Read(t, f, "discord.c").Revision)
	t33CleanupRequest(t, three, unblock)
	third := t33Await(t, three, 6*time.Second, "S-46b: third request failed to return within its admission deadline")
	t14Rejection(t, third, http.StatusBadGateway, "lookup_unavailable")
	paths, maximum := network.snapshot()
	require.Equal(t, []string{"GET /api/v" + discordgo.APIVersion + "/channels/777", "GET /api/v" + discordgo.APIVersion + "/channels/888"}, paths,
		"S-46b: third target999 never entered discordgo HTTP, even after earlier callers' outer deadlines")
	require.Equal(t, 2, maximum, "two actually parked external calls; not one-call pseudo-saturation")
	for _, pending := range []<-chan *httptest.ResponseRecorder{one, two} {
		refused := t33Await(t, pending, time.Second, "instrument: already-expired parked caller did not return")
		t14Rejection(t, refused, http.StatusBadGateway, "lookup_unavailable")
	}
	t13Unchanged(t, f, before)
	unblock()
	for i := 0; i < 2; i++ {
		select {
		case <-lateClosed:
		case <-time.After(5 * time.Second):
			t.Fatal("instrument: released external calls did not close their response bodies")
		}
	}
	// Hold the first fresh lookup while admitting the second: both slots,
	// not just one reusable slot demonstrated by two sequential requests.
	four := t33AsyncPut(t, context.Background(), f, "discord.d", "agent_may_start", []string{"222", "111"}, t14Read(t, f, "discord.d").Revision)
	t33CleanupRequest(t, four, unblockFresh)
	t33Enter(t, freshEntered, "S-46b: first released slot not reusable")
	five := t33AsyncPut(t, context.Background(), f, "discord.e", "agent_may_start", []string{"222", "112"}, t14Read(t, f, "discord.e").Revision)
	t33CleanupRequest(t, five, unblockFresh)
	t33Enter(t, freshEntered, "S-46b: second released slot not reusable while first is occupied")
	unblockFresh()
	for _, pending := range []<-chan *httptest.ResponseRecorder{four, five} {
		rec := t33Await(t, pending, 5*time.Second, "S-46b: fresh request did not finish after release")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}
	t13AssertOutbound(t, f, "discord.d", "agent_may_start", []string{"discord:222", "discord:111"})
	t13AssertOutbound(t, f, "discord.e", "agent_may_start", []string{"discord:222", "discord:112"})
	paths, maximum = network.snapshot()
	require.Equal(t, []string{"GET /api/v" + discordgo.APIVersion + "/channels/777", "GET /api/v" + discordgo.APIVersion + "/channels/888", "GET /api/v" + discordgo.APIVersion + "/channels/111", "GET /api/v" + discordgo.APIVersion + "/channels/112"}, paths)
	require.Equal(t, 2, maximum)
}

func TestT33_RealDiscordLookupHTTP502IsOneAttemptAndSavesNothing(t *testing.T) {
	f, _ := t32Fixture(t, "discord", []string{"discord:222"})
	_ = t33RealDiscord(t, f, "discord")
	network := t33ExternalHTTP(t)
	network.handle = func(req *http.Request, _ int) (*http.Response, error) {
		return t33HTTPResponse(req, http.StatusBadGateway, `{"code":0,"message":"QA bad gateway"}`), nil
	}
	before := t13Before(t, f)
	rec := t14Put(t, f, "discord", "agent_may_start", []string{"222", "777"}, t14Read(t, f, "discord").Revision)
	t14Rejection(t, rec, http.StatusBadGateway, "lookup_unavailable")
	t13Unchanged(t, f, before)
	paths, maximum := network.snapshot()
	require.Equal(t, []string{"GET /api/v" + discordgo.APIVersion + "/channels/777"}, paths, "T-9 r8: WithRestRetries(0), not only rate-limit retry-off")
	require.Equal(t, 1, maximum)
}
