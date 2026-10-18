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

// gatedResolver signals when a lookup starts, then answers with addrs once
// release closes — or fails with the ctx error first, recording that its
// ctx ended (the proof a cancellation reached the lookup itself).
type gatedResolver struct {
	entered  chan struct{}
	release  chan struct{}
	addrs    []net.IPAddr
	ctxEnded chan error
}

func newGatedResolver(addr string) *gatedResolver {
	return &gatedResolver{
		entered:  make(chan struct{}, 1),
		release:  make(chan struct{}),
		addrs:    []net.IPAddr{{IP: net.ParseIP(addr)}},
		ctxEnded: make(chan error, 1),
	}
}

func (r *gatedResolver) LookupIPAddr(ctx context.Context, _ string) ([]net.IPAddr, error) {
	r.entered <- struct{}{}
	select {
	case <-r.release:
		return r.addrs, nil
	case <-ctx.Done():
		r.ctxEnded <- ctx.Err()
		return nil, ctx.Err()
	}
}

func waitEntered(t *testing.T, r *gatedResolver) {
	t.Helper()
	select {
	case <-r.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the navigate never reached its DNS lookup")
	}
}

func waitDispatchResult(t *testing.T, errCh <-chan error) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("navigate did not return after its lookup was released or cancelled")
		return nil
	}
}

// newTabSwitchNavigateLiveView is a live view on oldTarget whose browser
// commands report which tab (targetKey value) they were sent to.
type navigateTargetKey struct{}

func newTabSwitchNavigateLiveView(t *testing.T, resolver security.Resolver, oldTarget context.Context, dispatchedTo chan<- string) *LiveView {
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
		tabCtx:    oldTarget,
		runCDP: func(ctx context.Context, _ time.Duration, _ ...chromedp.Action) error {
			dispatchedTo <- fixtureValue[string](ctx.Value(navigateTargetKey{}))
			return nil
		},
	}
}

func requireNothingDispatched(t *testing.T, dispatchedTo <-chan string) {
	t.Helper()
	select {
	case target := <-dispatchedTo:
		t.Fatalf("the obsolete navigate reached the browser on the %q tab", target)
	default:
	}
}

// A navigate whose DNS check is still pending when the shared browser
// switches tabs must not land on the newly selected tab: the request was
// made for the old page (browser-input-connection spec R3 — Lifetime).
// Retiring the old tab cancels the pending lookup itself, like any other
// input bound to that tab.
func TestLiveView_DispatchInput_Navigate_TabChangeDuringValidationIsRefused(t *testing.T) {
	oldTarget := context.WithValue(context.Background(), navigateTargetKey{}, "old")
	newTarget := context.WithValue(context.Background(), navigateTargetKey{}, "new")
	resolver := newGatedResolver("93.184.215.14") // public
	dispatchedTo := make(chan string, 2)
	lv := newTabSwitchNavigateLiveView(t, resolver, oldTarget, dispatchedTo)

	errCh := make(chan error, 1)
	go func() {
		errCh <- lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "https://slow-dns.example/"})
	}()
	waitEntered(t, resolver)
	// The tab switch, exactly as onTabsChanged performs it.
	lv.mu.Lock()
	lv.tabCtx = newTarget
	lv.mu.Unlock()
	lv.retireInputTarget(oldTarget, newTarget)
	select {
	case ended := <-resolver.ctxEnded:
		require.ErrorIs(t, ended, context.Canceled, "retiring the tab must cancel the pending lookup")
	case <-time.After(2 * time.Second):
		t.Fatal("retiring the old tab did not cancel the pending DNS lookup")
	}
	close(resolver.release)

	err := waitDispatchResult(t, errCh)
	require.Error(t, err, "a navigate made for the old tab must not be carried out on the new one")
	require.True(t, IsBenignLiveInputError(err), "a retired target is the ordinary retry-on-the-current-tab refusal: %v", err)
	require.Contains(t, err.Error(), "input target changed")
	requireNothingDispatched(t, dispatchedTo)
}

// The same, when the lookup completes after the active tab already changed
// but before the retirement reached the pending check: the navigate is still
// refused rather than sent to the replacement tab.
func TestLiveView_DispatchInput_Navigate_TabReplacedBeforeLookupReturnsIsRefused(t *testing.T) {
	oldTarget := context.WithValue(context.Background(), navigateTargetKey{}, "old")
	newTarget := context.WithValue(context.Background(), navigateTargetKey{}, "new")
	resolver := newGatedResolver("93.184.215.14") // public
	dispatchedTo := make(chan string, 2)
	lv := newTabSwitchNavigateLiveView(t, resolver, oldTarget, dispatchedTo)

	errCh := make(chan error, 1)
	go func() {
		errCh <- lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "https://slow-dns.example/"})
	}()
	waitEntered(t, resolver)
	lv.mu.Lock()
	lv.tabCtx = newTarget
	lv.mu.Unlock()
	close(resolver.release) // the lookup succeeds; no retirement has run yet

	err := waitDispatchResult(t, errCh)
	require.Error(t, err)
	require.True(t, IsBenignLiveInputError(err), "%v", err)
	require.Contains(t, err.Error(), "input target changed")
	requireNothingDispatched(t, dispatchedTo)
}

// Positive control for the two tests above: with no tab change, the same
// gated lookup completes and the navigate reaches the tab it was made for.
func TestLiveView_DispatchInput_Navigate_GatedLookupWithoutTabChangeDispatches(t *testing.T) {
	oldTarget := context.WithValue(context.Background(), navigateTargetKey{}, "old")
	resolver := newGatedResolver("93.184.215.14") // public
	dispatchedTo := make(chan string, 2)
	lv := newTabSwitchNavigateLiveView(t, resolver, oldTarget, dispatchedTo)

	errCh := make(chan error, 1)
	go func() {
		errCh <- lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "https://slow-dns.example/"})
	}()
	waitEntered(t, resolver)
	close(resolver.release)

	require.NoError(t, waitDispatchResult(t, errCh))
	select {
	case target := <-dispatchedTo:
		require.Equal(t, "old", target)
	default:
		t.Fatal("the validated navigate never reached the browser")
	}
}

// A caller that ends while the DNS check is pending (the gateway command
// deadline, or a newer command superseding it) stops the lookup itself and
// the navigate, well before the page-load timeout.
func TestLiveView_DispatchInput_Navigate_CallerCancelDuringValidationStopsLookup(t *testing.T) {
	resolver := newGatedResolver("93.184.215.14")
	var dispatched bool
	lv := newSlowDNSNavigateLiveView(t, resolver, &dispatched)
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- lv.dispatchInputContext(caller, "viewerA", LiveInput{Kind: "navigate", URL: "https://slow-dns.example/"})
	}()
	waitEntered(t, resolver)
	cancel()

	err := waitDispatchResult(t, errCh)
	require.Error(t, err)
	require.Contains(t, err.Error(), "input canceled")
	require.ErrorIs(t, <-resolver.ctxEnded, context.Canceled, "the cancellation must reach the DNS lookup")
	require.False(t, dispatched, "a cancelled navigate must never reach CDP dispatch")
}

// The input source ending (the viewer's connection closing) likewise stops a
// pending DNS check.
func TestLiveView_DispatchInput_Navigate_SourceCancelDuringValidationStopsLookup(t *testing.T) {
	resolver := newGatedResolver("93.184.215.14")
	var dispatched bool
	lv := newSlowDNSNavigateLiveView(t, resolver, &dispatched)
	source, endSource := context.WithCancel(context.Background())
	defer endSource()

	errCh := make(chan error, 1)
	go func() {
		errCh <- lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "https://slow-dns.example/", SourceContext: source})
	}()
	waitEntered(t, resolver)
	endSource()

	err := waitDispatchResult(t, errCh)
	require.Error(t, err)
	require.False(t, IsBenignLiveInputError(err))
	require.ErrorIs(t, <-resolver.ctxEnded, context.Canceled, "the source ending must reach the DNS lookup")
	require.False(t, dispatched, "a navigate whose source ended must never reach CDP dispatch")
}

// A caller deadline shorter than the page-load timeout bounds the DNS check
// (the gateway's remaining command allowance), not the page-load timeout.
func TestLiveView_DispatchInput_Navigate_CallerDeadlineBoundsValidation(t *testing.T) {
	resolver := newGatedResolver("93.184.215.14")
	var dispatched bool
	lv := newSlowDNSNavigateLiveView(t, resolver, &dispatched) // PageTimeout 5s
	caller, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := lv.dispatchInputContext(caller, "viewerA", LiveInput{Kind: "navigate", URL: "https://slow-dns.example/"})
	elapsed := time.Since(start)
	require.Error(t, err)
	require.Less(t, elapsed, 2*time.Second, "the caller deadline, not the 5s page timeout, must bound the lookup")
	require.ErrorIs(t, <-resolver.ctxEnded, context.DeadlineExceeded)
	require.False(t, dispatched)
}
