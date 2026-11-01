package gateway

// T-33 writer-level interleavings: real REST/config writer, external adapter
// target resolution only. Real Discord limiter/saturation are separate tests.

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/stretchr/testify/require"
)

func TestT33_SlowLookupDoesNotHoldTheSettingsLockAgainstAnotherRevocation(t *testing.T) {
	f, adapter := t32Fixture(t, "discord", []string{"discord:222"})
	t13Seed(t, f, "discord.b", "discord", false, "agent_may_start", []string{"discord:222"})
	r1, r2 := t14Read(t, f, "discord").Revision, t14Read(t, f, "discord.b").Revision
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	adapter.lookup = func(context.Context, string) channels.OutboundTargetResult {
		enterOnce.Do(func() { close(entered) })
		<-release
		return channels.OutboundTargetResult{}
	}
	first := t33AsyncPut(t, context.Background(), f, "discord", "agent_may_start", []string{"222", "777"}, r1)
	t33CleanupRequest(t, first, func() { releaseOnce.Do(func() { close(release) }) })
	t33Enter(t, entered, "instrument: adding target777 never reached external lookup")
	second := t33AsyncPut(t, context.Background(), f, "discord.b", "reply_only", []string{"222"}, r2)
	t33CleanupRequest(t, second, func() { releaseOnce.Do(func() { close(release) }) })
	revoked := t33Await(t, second, 5*time.Second, "S-26: revocation waits behind another connection's lookup (configMu held over network)")
	require.Equal(t, http.StatusOK, revoked.Code, revoked.Body.String())
	select {
	case <-release:
		t.Fatal("instrument: lookup was released before the revocation result")
	default:
	}
	t13AssertOutbound(t, f, "discord.b", "reply_only", []string{"discord:222"})
	releaseOnce.Do(func() { close(release) })
	added := t33Await(t, first, 5*time.Second, "instrument: released first writer did not finish")
	require.Equal(t, http.StatusOK, added.Code, added.Body.String())
	t13AssertOutbound(t, f, "discord", "agent_may_start", []string{"discord:222", "discord:777"})
	require.Equal(t, []string{"discord:777"}, adapter.lookupSnapshot(), "only the added target reached lookup")
}

func TestT33_ConfigureDuringLookupMakesTheValidatedRevisionStale(t *testing.T) {
	f, adapter := t32Fixture(t, "discord", []string{"discord:222"})
	r1 := t14Read(t, f, "discord").Revision
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	adapter.lookup = func(context.Context, string) channels.OutboundTargetResult {
		enterOnce.Do(func() { close(entered) })
		<-release
		return channels.OutboundTargetResult{}
	}
	pending := t33AsyncPut(t, context.Background(), f, "discord", "agent_may_start", []string{"222", "777"}, r1)
	t33CleanupRequest(t, pending, func() { releaseOnce.Do(func() { close(release) }) })
	t33Enter(t, entered, "instrument: first writer did not enter lookup")
	configured := t33Await(t, t33AsyncRequest(context.Background(), f, http.MethodPut, "/api/v1/channels/discord/configure", `{"allow_from":["qa-t33-new-sender"]}`), 15*time.Second,
		"instrument: real Configure prerequisite did not finish; no stale-commit verdict")
	require.Equal(t, http.StatusOK, configured.Code, "S-27: real allow_from-only Configure save succeeds using the stored credential: %s", configured.Body.String())
	require.NotEqual(t, r1, t14Read(t, f, "discord").Revision, "S-27 prerequisite: Configure moved the live conflict token")
	before := t13Before(t, f)
	releaseOnce.Do(func() { close(release) })
	rejected := t33Await(t, pending, 5*time.Second, "instrument: stale lookup writer did not finish")
	t14Rejection(t, rejected, http.StatusConflict, "stale_revision")
	t13Unchanged(t, f, before)
	t13AssertOutbound(t, f, "discord", "agent_may_start", []string{"discord:222"})
}

func TestT33_CancelledLookupCannotCommitItsLateSuccessfulResult(t *testing.T) {
	f, adapter := t32Fixture(t, "discord", []string{"discord:222"})
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	adapter.lookup = func(context.Context, string) channels.OutboundTargetResult {
		enterOnce.Do(func() { close(entered) })
		<-release // external edge ignores cancellation.
		return channels.OutboundTargetResult{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pending := t33AsyncPut(t, ctx, f, "discord", "agent_may_start", []string{"222", "777"}, t14Read(t, f, "discord").Revision)
	t33CleanupRequest(t, pending, func() { releaseOnce.Do(func() { close(release) }) })
	t33Enter(t, entered, "instrument: cancellable request did not reach external lookup")
	before := t13Before(t, f)
	cancel()
	releaseOnce.Do(func() { close(release) })
	rec := t33Await(t, pending, 5*time.Second, "S-27: cancelled writer did not return after its external edge completed")
	require.NotEqual(t, http.StatusOK, rec.Code, "cancelled request must not report a successful write")
	t13Unchanged(t, f, before)
	t13AssertOutbound(t, f, "discord", "agent_may_start", []string{"discord:222"})
}
