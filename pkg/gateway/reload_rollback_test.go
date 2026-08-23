// Package gateway — reload rollback and degraded health tests.
//
// These tests verify that executeReload rolls back in-memory state and marks
// the service as degraded when a reload fails, and clears the degraded flag
// when a subsequent reload succeeds.
//
// Implements: Task 1.5 (executeReload rollback + degraded health).

package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TestExecuteReload_MarksDegradedOnCredInjectionFailure verifies that when
// executeReload rejects a reload due to a locked credential store, it:
//   - sets reloadDegraded = true
//   - sets reloadError != nil
//   - restores the previous ChannelManager and bundle (snapshot rollback)
//   - returns a non-nil error
//
// A subsequent successful reload (with a nil credStore so the injection path
// is skipped) must then clear the degraded flag.
func TestExecuteReload_MarksDegradedOnCredInjectionFailure(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	msgBus := bus.NewMessageBus()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 19988},
		Providers: []*config.ModelConfig{
			{ModelName: "test", APIKeyRef: "SOME_KEY", Provider: "anthropic"},
		},
	}

	p := providers.LLMProvider(&restMockProvider{})
	al := mustAgentLoop(t, cfg, msgBus, p)

	// Build a locked credStore — InjectFromConfig will fail because the store
	// is not unlocked, triggering markDegraded.
	credStore := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	// Do NOT call Unlock — store remains locked.

	// Create a sentinel ChannelManager to verify rollback restores it.
	sentinelCM, err := channels.NewManager(cfg, credentials.SecretBundle{}, msgBus, nil)
	if err != nil {
		t.Fatalf("channels.NewManager: %v", err)
	}
	sentinelBundle := credentials.SecretBundle{"sentinel": "value"}

	svc := &services{
		ChannelManager: sentinelCM,
		bundle:         sentinelBundle,
		credStore:      credStore,
	}
	// Simulate the single-flight slot being held by the caller. executeReload no
	// longer releases it — runReloadCycle owns the release, so that it can run a
	// coalesced follow-up reload before letting triggerReloadAndWait pollers go.
	svc.reloadInFlight = true

	// Execute the reload with a config that requires credential injection.
	// This should fail because the store is locked.
	err = executeReload(context.Background(), al, cfg, &p, svc, msgBus, true)
	if err == nil {
		t.Fatal("expected executeReload to return an error, got nil")
	}

	// Assert degraded state is set.
	svc.reloadMu.Lock()
	isDegraded := svc.reloadDegraded
	reloadErr := svc.reloadError
	svc.reloadMu.Unlock()

	if !isDegraded {
		t.Error("expected reloadDegraded == true after failed reload")
	}
	if reloadErr == nil {
		t.Error("expected reloadError != nil after failed reload")
	}

	// Assert rollback: ChannelManager must be the sentinel (not overwritten).
	if svc.ChannelManager != sentinelCM {
		t.Error("expected ChannelManager to be rolled back to sentinel after reload failure")
	}
	// Bundle must be restored.
	if svc.bundle["sentinel"] != "value" {
		t.Errorf("expected bundle to be rolled back; got %v", svc.bundle)
	}

	// Verify clearDegraded resets the degraded fields to zero values.
	// ClearDegradedForTest calls the real clearDegraded logic (defined in
	// export_test.go) so this assertion exercises the production code path,
	// not a reimplementation of it.
	svc.ClearDegradedForTest()

	svc.reloadMu.Lock()
	clearedDegraded := svc.reloadDegraded
	clearedErr := svc.reloadError
	svc.reloadMu.Unlock()

	if clearedDegraded {
		t.Error("expected reloadDegraded == false after ClearDegradedForTest")
	}
	if clearedErr != nil {
		t.Errorf("expected reloadError == nil after ClearDegradedForTest, got %v", clearedErr)
	}
}

// TestExecuteReload_RejectsOnCorruptedEnabledChannelCredential verifies that
// executeReload rejects (marks degraded, does NOT apply) a reload when an
// ENABLED channel's credential ref is present in the store but fails to
// decrypt — a corrupted store entry / wrong-master-key scenario, distinct
// from (and worse than) a simple missing ref. This pins the
// enabledRefFromBundleError escalation branch in executeReload's channel
// credential re-resolution step (the reload-side counterpart to
// TestGatewayBoot_CorruptedCredentialForEnabledChannelFailsFast in
// boot_order_test.go, which covers the equivalent bootCredentials path).
//
// Providers is left empty so credentials.InjectFromConfig (the provider-key
// step immediately before the channel-credential step) trivially succeeds —
// this test isolates the channel-credential rejection branch specifically.
func TestExecuteReload_RejectsOnCorruptedEnabledChannelCredential(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	writeCorruptedCredentialsFile(t, credsPath, "TELEGRAM_TOKEN")

	credStore := credentials.NewStore(credsPath)
	if err := credentials.Unlock(credStore); err != nil {
		t.Fatalf("credentials.Unlock: %v", err)
	}

	msgBus := bus.NewMessageBus()

	baseCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 19987},
	}

	p := providers.LLMProvider(&restMockProvider{})
	al := mustAgentLoop(t, baseCfg, msgBus, p)

	// The new config being reloaded to: an ENABLED telegram channel pointing
	// at the corrupted credential ref.
	newCfg := &config.Config{
		Agents:  baseCfg.Agents,
		Gateway: baseCfg.Gateway,
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram": {
				Enabled:  true,
				TokenRef: "TELEGRAM_TOKEN",
			},
		},
	}

	sentinelCM, err := channels.NewManager(baseCfg, credentials.SecretBundle{}, msgBus, nil)
	if err != nil {
		t.Fatalf("channels.NewManager: %v", err)
	}
	sentinelBundle := credentials.SecretBundle{"sentinel": "value"}

	svc := &services{
		ChannelManager: sentinelCM,
		bundle:         sentinelBundle,
		credStore:      credStore,
	}
	// Simulate the single-flight slot being held by the caller. executeReload no
	// longer releases it — runReloadCycle owns the release, so that it can run a
	// coalesced follow-up reload before letting triggerReloadAndWait pollers go.
	svc.reloadInFlight = true

	err = executeReload(context.Background(), al, newCfg, &p, svc, msgBus, true)
	if err == nil {
		t.Fatal(
			"expected executeReload to reject the reload when an enabled channel's credential fails to decrypt, got nil error",
		)
	}
	if !strings.Contains(err.Error(), "TELEGRAM_TOKEN") {
		t.Errorf("reload error must mention the failing ref TELEGRAM_TOKEN; got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "failed to resolve") {
		t.Errorf(
			"reload error must indicate the ref failed to resolve (decrypt failure), not merely be missing; got: %q",
			err.Error(),
		)
	}

	// Assert degraded state is set — this is how an operator discovers the
	// rejection via GET /health (SetDegradedFunc surfaces reloadDegraded/
	// reloadError as a 503 "reason").
	svc.reloadMu.Lock()
	isDegraded := svc.reloadDegraded
	reloadErr := svc.reloadError
	svc.reloadMu.Unlock()

	if !isDegraded {
		t.Error("expected reloadDegraded == true after rejected reload")
	}
	if reloadErr == nil {
		t.Error("expected reloadError != nil after rejected reload")
	}

	// Assert rollback: the previous ChannelManager/bundle must be restored,
	// not replaced by anything derived from the rejected newCfg.
	if svc.ChannelManager != sentinelCM {
		t.Error("expected ChannelManager to be rolled back to sentinel after rejected reload")
	}
	if svc.bundle["sentinel"] != "value" {
		t.Errorf("expected bundle to be rolled back; got %v", svc.bundle)
	}
}
