package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// These are #1277 RED tests, not characterization of ADR-004's old fatal
// enabled-channel rule. The 2026-10-12 founder decision supersedes that rule:
// only the affected integration degrades; store-level failures stay fatal.
func TestIssue1277_BootMissingChannelSecretDegradesOnlyItsInstance(t *testing.T) {
	f := newIssue1277Fixture(t)
	cfg, bundle, _ := f.boot(t)
	require.NotContains(t, bundle, issue1277BrokenRef, "a missing secret must not acquire a fallback value")
	require.Equal(t, "qa1277-healthy-secret", bundle.GetString(issue1277HealthyRef))
	api, svc, _, _ := f.startRuntime(t, cfg, bundle)
	issue1277RequireDegradedChannel(t, api, svc.ChannelManager)
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
	issue1277RequireDoctorReason(t, api)
}

// All four combinations derive from the same immunity rule: enabled/disabled
// and a broken channel being the sole integration or having a healthy peer.
// This case ensures gateway start is not conditional on a healthy channel.
func TestIssue1277_BootOnlyBrokenChannelStillStarts(t *testing.T) {
	f := newIssue1277Fixture(t)
	delete(f.cfg.Channels, "line.healthy")
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	cfg, bundle, _ := f.boot(t)
	api, svc, _, _ := f.startRuntime(t, cfg, bundle)
	issue1277RequireDegradedChannel(t, api, svc.ChannelManager)
	require.Empty(t, svc.ChannelManager.GetEnabledChannels(), "missing-secret channel must not run")
	issue1277RequireHealthyProvider(t, api.agentLoop)
	require.Equal(t, int64(1), f.providerCalls.Load(), "positive control must execute the provider call")
}

// Positive control for the same channel, refs, store, HTTP edge and boot path.
// A fix that simply disables channels or blocks all integrations must fail it.
func TestIssue1277_HealthyChannelRefStarts(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-restored-secret"))
	cfg, bundle, _ := f.boot(t)
	require.Equal(t, "qa1277-restored-secret", bundle.GetString(issue1277BrokenRef))
	api, svc, _, _ := f.startRuntime(t, cfg, bundle)
	require.Empty(t, svc.ChannelManager.FailedChannels())
	require.ElementsMatch(t, []string{"line", "line.healthy"}, svc.ChannelManager.GetEnabledChannels())
	ch, exists := svc.ChannelManager.GetChannel("line")
	require.True(t, exists)
	require.True(t, ch.IsRunning(), "healthy credential must actually start the channel")
	row := issue1277ChannelRows(t, api)["line"]
	require.True(t, row.Enabled)
	require.False(t, row.Degraded != nil && *row.Degraded)
	require.Nil(t, row.DegradedReason, "healthy connection must have no stale credential error")
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
}

// Store-level positive control for fatality: an explicitly selected missing key
// file cannot fall back to a prompt or turn into a single-channel warning.
func TestIssue1277_CannotUnlockStoreRemainsFatal(t *testing.T) {
	f := newIssue1277Fixture(t)
	missingKeyFile := filepath.Join(f.home, "absent-master-key")
	t.Setenv("OMNIPUS_MASTER_KEY", "")
	t.Setenv("OMNIPUS_KEY_FILE", missingKeyFile)
	cfg, bundle, store, err := bootCredentials(f.home, f.configPath)
	require.ErrorIs(t, err, os.ErrNotExist, "store unlock failure must remain fatal")
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr)
	require.Equal(t, missingKeyFile, pathErr.Path)
	require.Contains(t, err.Error(), "OMNIPUS_KEY_FILE")
	require.Nil(t, cfg, "failed unlock must publish no config")
	require.Nil(t, bundle, "failed unlock must publish no bundle")
	require.Nil(t, store, "failed unlock must publish no unlocked store")
}

// Whole-file corruption is not an integration-scoped missing/unreadable entry.
func TestIssue1277_CorruptStoreRemainsFatal(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.home, "credentials.json"), []byte(`{broken-json`), 0o600))
	cfg, bundle, store, err := bootCredentials(f.home, f.configPath)
	require.Error(t, err, "whole-store corruption must remain fatal")
	var syntaxErr *json.SyntaxError
	require.ErrorAs(t, err, &syntaxErr, "must fail on the store JSON, not on an unrelated config/adapter error")
	require.Contains(t, err.Error(), "corrupted")
	require.Nil(t, cfg)
	require.Nil(t, bundle)
	require.Nil(t, store)
}

func TestIssue1277_RESTRefreshAppliesWithMissingChannelSecret(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-before-delete"))
	cfg, bundle, _ := f.boot(t)
	api, _, _, _ := f.startRuntime(t, cfg, bundle)
	require.NoError(t, f.store.Delete(issue1277BrokenRef))
	f.cfg.Gateway.LogLevel = "debug" // unrelated user setting must not be rolled back.
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	require.NoError(t, api.refreshConfigAndRewireServices(f.configPath),
		"#1277: a missing channel secret must not reject an unrelated Settings refresh")
	require.Equal(t, "debug", api.agentLoop.GetConfig().Gateway.LogLevel)
	issue1277RequireHealthyProvider(t, api.agentLoop)
}

func TestIssue1277_ReloadAfterCredentialDeletionAppliesAndDegradesOnlyChannel(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-before-delete"))
	cfg, bundle, _ := f.boot(t)
	api, svc, msgBus, provider := f.startRuntime(t, cfg, bundle)
	ch, exists := svc.ChannelManager.GetChannel("line")
	require.True(t, exists)
	require.True(t, ch.IsRunning(), "precondition: the deleted credential must be in live use")
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)

	f.cfg.Gateway.LogLevel = "debug" // pins applied vs merely 'reload accepted'.
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	svc.reloadTrigger = newReloadTrigger(svc, api.agentLoop)
	api.agentLoop.SetReloadFunc(svc.reloadTrigger)
	stop, drained := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(stop); <-drained })
	go func() {
		defer close(drained)
		for {
			select {
			case <-stop:
				return
			case <-svc.manualReloadChan:
				runReloadCycle(api.agentLoop, svc, nil, 0, func(c *config.Config) error {
					return executeReload(context.Background(), api.agentLoop, c, &provider, svc, msgBus, false)
				}, svc.loadConfigForSwap)
			}
		}
	}()
	w := httptest.NewRecorder()
	r := withReAuthAdmin(t, api, httptest.NewRequest(http.MethodDelete, "/api/v1/credentials/"+issue1277BrokenRef, nil))
	api.HandleCredentials(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var response map[string]json.RawMessage // no new wire type: inspect the existing inline response.
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.JSONEq(t, `"removed"`, string(response["status"]))
	require.JSONEq(t, `"`+issue1277BrokenRef+`"`, string(response["key"]))
	_, err := f.store.Get(issue1277BrokenRef)
	var notFound *credentials.NotFoundError
	require.ErrorAs(t, err, &notFound, "precondition: deletion must be durable")
	require.Equal(t, issue1277BrokenRef, notFound.Name)
	require.NotContains(t, response, "reload_warning", "#1277: deletion must apply live, not leave the old credential running")
	require.False(t, svc.reloadOutcome.lastFailed(), "completed reload must succeed, not just release the pending flag")
	degraded, reason := svc.reloadDegradedStatus()
	require.False(t, degraded, "one integration must not degrade the gateway's health")
	require.Empty(t, reason)
	require.Equal(t, "debug", api.agentLoop.GetConfig().Gateway.LogLevel)
	require.NotContains(t, svc.bundle, issue1277BrokenRef, "removed credential must not survive in live bundle")
	require.False(t, ch.IsRunning(), "the old channel instance must stop using its removed credential")
	issue1277RequireDegradedChannel(t, api, svc.ChannelManager)
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
	issue1277RequireDoctorReason(t, api)
	_, nextBundle, _ := f.boot(t)
	require.NotContains(t, nextBundle, issue1277BrokenRef, "the next boot must survive the durable dangling ref too")
}

func issue1277RequireDegradedChannel(t *testing.T, api *restAPI, mgr *channels.Manager) {
	t.Helper()
	failed := mgr.FailedChannels()
	require.Len(t, failed, 1, "only the integration with the broken ref must fail")
	require.Equal(t, "line", failed[0].Name)
	require.Contains(t, failed[0].Err.Error(), issue1277BrokenRef, "server-side error must name the missing secret")
	if ch, exists := mgr.GetChannel("line"); exists {
		require.False(t, ch.IsRunning(), "missing-secret channel must not start or keep running")
	}
	rows := issue1277ChannelRows(t, api)
	require.Contains(t, rows, "line", "a broken integration must remain reachable in Settings")
	row := rows["line"]
	require.True(t, row.Enabled, "do not hide the failure by disabling persisted configuration")
	require.NotNil(t, row.InstanceId, "the degraded Settings row must retain its instance identity")
	require.Equal(t, "line", *row.InstanceId)
	require.NotNil(t, row.Degraded)
	require.True(t, *row.Degraded)
	require.NotNil(t, row.DegradedReason)
	require.Contains(t, *row.DegradedReason, issue1277BrokenRef)
}

func issue1277RequireHealthyPeer(t *testing.T, f *issue1277Fixture, api *restAPI, mgr *channels.Manager) {
	t.Helper()
	rows := issue1277ChannelRows(t, api)
	require.Contains(t, rows, "line.healthy")
	row := rows["line.healthy"]
	require.True(t, row.Enabled)
	require.False(t, row.Degraded != nil && *row.Degraded, "a healthy sibling of the same type must not inherit the error")
	require.Nil(t, row.DegradedReason)
	ch, exists := mgr.GetChannel("line.healthy")
	require.True(t, exists)
	require.True(t, ch.IsRunning(), "healthy sibling must still run")
	before := f.healthyPushes.Load()
	require.NoError(t, ch.Send(context.Background(), bus.OutboundMessage{ChatID: "qa1277-user", Content: "healthy channel still works"}))
	require.Equal(t, before+1, f.healthyPushes.Load(), "the healthy channel must actually send with its resolved credential")
	before = f.providerCalls.Load()
	issue1277RequireHealthyProvider(t, api.agentLoop)
	require.Equal(t, before+1, f.providerCalls.Load())
}

func issue1277RequireDoctorReason(t *testing.T, api *restAPI) {
	t.Helper()
	w := httptest.NewRecorder()
	api.HandleDoctor(w, httptest.NewRequest(http.MethodPost, "/api/v1/doctor", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var result gen.DoctorResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	for _, issue := range result.Issues {
		text := issue.Title + " " + issue.Description
		if strings.Contains(text, "line") && strings.Contains(text, issue1277BrokenRef) {
			return
		}
	}
	t.Fatalf("#1277: doctor must report the degraded channel and secret %q; got issues %+v", issue1277BrokenRef, result.Issues)
}

// Instrument control: a disabled dangling ref is still a reported resolution
// error, but it never aborts gateway boot or starts a channel.
func TestIssue1277_DisabledMissingChannelRefDoesNotStart(t *testing.T) {
	f := newIssue1277Fixture(t)
	inst := f.cfg.Channels["line"]
	inst.Enabled = false
	f.cfg.Channels["line"] = inst
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	cfg, bundle, store := f.boot(t)
	_, errs := credentials.ResolveBundle(cfg, store)
	found := false
	for _, err := range errs {
		var missing *credentials.NotFoundError
		if errors.As(err, &missing) && missing.Name == issue1277BrokenRef {
			found = true
		}
	}
	require.True(t, found, "instrument: resolver must detect the dangling ref even when boot continues")
	api, svc, _, _ := f.startRuntime(t, cfg, bundle)
	_, running := svc.ChannelManager.GetChannel("line")
	require.False(t, running)
	require.Empty(t, svc.ChannelManager.FailedChannels(), "disabled integrations are not errors")
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
}
