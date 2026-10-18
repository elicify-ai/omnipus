package browser

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// --- navigate: the SSRF pre-check runs on the page-load budget ---
//
// Release run 39029558027 (UAT-09b): a user-driven live "navigate" to
// httpbin.org was refused as "navigation blocked by SSRF policy: DNS
// resolution failed for httpbin.org: lookup httpbin.org: i/o timeout". The
// SSRF host check resolves the name, and it ran inside the live-input
// dispatch budget (interactiveInputTimeout, 2s, shared with the tab and
// input gate waits), so any lookup slower than that refused the navigation.
// The agent's browser_navigate tool and this path's own original design
// (TestLiveView_DispatchInput_Navigate_DNSResolutionIsBounded) bound the
// check by the page-load timeout instead. These tests pin that bound with a
// resolver that answers only after longer than the 2s input budget.

// delayedResolver answers with addrs after delay, or fails with the ctx
// error if the ctx ends first — what a slow but working DNS server does.
type delayedResolver struct {
	delay time.Duration
	addrs []net.IPAddr
}

func (r delayedResolver) LookupIPAddr(ctx context.Context, _ string) ([]net.IPAddr, error) {
	select {
	case <-time.After(r.delay):
		return r.addrs, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// slowerThanInputBudget is past the 2s interactive input budget and well
// inside the 5s page-load timeout the tests configure.
const slowerThanInputBudget = interactiveInputTimeout + 500*time.Millisecond

func newSlowDNSNavigateLiveView(t *testing.T, resolver security.Resolver, dispatched *bool) *LiveView {
	t.Helper()
	ssrf := security.NewSSRFChecker(nil)
	ssrf.SetResolver(resolver)
	mgr, err := NewBrowserManager(BrowserConfig{PageTimeout: 5 * time.Second}, ssrf)
	require.NoError(t, err)
	t.Cleanup(mgr.Live().Shutdown)
	return &LiveView{
		mgr:       mgr,
		sessionID: "s1",
		viewers:   make(map[string]struct{}),
		tabCtx:    context.Background(),
		runCDP: func(context.Context, time.Duration, ...chromedp.Action) error {
			*dispatched = true
			return nil
		},
	}
}

func TestLiveView_DispatchInput_Navigate_SlowPublicDNSWithinPageTimeoutIsAllowed(t *testing.T) {
	var dispatched bool
	lv := newSlowDNSNavigateLiveView(t, delayedResolver{
		delay: slowerThanInputBudget,
		addrs: []net.IPAddr{{IP: net.ParseIP("93.184.215.14")}}, // public
	}, &dispatched)

	err := lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "https://slow-dns.example/cookies"})
	require.NoError(t, err, "a public host whose DNS answers within the page-load timeout must navigate")
	require.True(t, dispatched, "the allowed navigate must reach CDP dispatch")
}

// Negative control: the same slow resolver answering with a private address
// must still be refused by the SSRF policy (not by a timeout), and never
// dispatched.
func TestLiveView_DispatchInput_Navigate_SlowPrivateDNSStaysBlocked(t *testing.T) {
	var dispatched bool
	lv := newSlowDNSNavigateLiveView(t, delayedResolver{
		delay: slowerThanInputBudget,
		addrs: []net.IPAddr{{IP: net.ParseIP("10.0.0.7")}}, // RFC 1918
	}, &dispatched)

	err := lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "https://rebind.example/"})
	require.Error(t, err)
	require.False(t, IsBenignLiveInputError(err), "a blocked navigate must be a real, surfaced error")
	require.Contains(t, err.Error(), "navigate blocked")
	require.Contains(t, err.Error(), "resolved to blocked IP", "the refusal must come from the SSRF policy, not a timeout")
	require.False(t, dispatched, "a private-resolving host must never reach CDP dispatch")
}

// Every path into the CDP dispatch keeps the SSRF gate: a navigate that
// reaches dispatchTrackedInput without having passed the pre-check is
// refused there, with no lookup and no dispatch.
func TestLiveView_DispatchTrackedInput_UnvalidatedNavigateRefused(t *testing.T) {
	var dispatched bool
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched = true
		return nil
	})

	err := lv.dispatchTrackedInput(context.Background(), context.Background(), "viewerA",
		LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"})
	require.Error(t, err)
	require.False(t, IsBenignLiveInputError(err))
	require.Contains(t, err.Error(), "navigate blocked")
	require.False(t, dispatched, "an unvalidated navigate must never reach CDP dispatch")
}
