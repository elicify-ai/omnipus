// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// U3 test-oracle gap M1 (CHECK finding): the cap-wiring path
// session_messaging_wire.go::wireSessionMessagingForAgent sets the delegate
// tool's enforced steer/respond caps from config
//
//	dt.SetSteerCaps(cfg.SessionMessaging.EffectiveSteerRatePerMinute(),
//	                cfg.SessionMessaging.EffectiveSteerBodyBytes())
//
// and NO test exercised it — a regression would silently revert the delegate to
// the tool's own constructor defaults for the life of the process, exactly the
// defect the wiring exists to close.
//
// Spec: docs/internal/specs/session-core-spec.md FR-010/FR-011; C-LIMIT (#1216).
// The expected values below all come from the config the test itself sets —
// never from reading the tool's fields first.
//
// Two tests, two jobs:
//
//   - ...CapsFollowConfigAcrossReload — the wiring/reload property: after a hot
//     reload that changes session_messaging.steer_body/steer_rate, the delegate
//     tool must enforce the NEW values. This is the RED half for the reload-order
//     defect (the caps are read from the config live at wiring time; a reload
//     that wires tools before publishing the new config leaves them one edit
//     behind).
//   - ...BodyCapIsEnforcedAtWiredValue — the instrument proof: the field this
//     file reads IS the enforcement boundary, so the reload assertion cannot be a
//     green that watches a stored value nothing consults.

package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// delegateToolForAgent fetches the *tools.DelegateTool registered on agentID's
// tool registry — the instance wireSessionMessagingForAgent wired.
func delegateToolForAgent(t *testing.T, al *AgentLoop, agentID string) *tools.DelegateTool {
	t.Helper()
	agent, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "agent %q must be registered", agentID)
	raw, ok := agent.Tools.Get("delegate")
	require.True(t, ok, "the delegate tool must be registered on agent %q", agentID)
	dt, ok := raw.(*tools.DelegateTool)
	require.Truef(t, ok, "delegate tool is %T, want *tools.DelegateTool", raw)
	return dt
}

// delegateSteerCaps reads the delegate tool's ENFORCED steer caps out of the
// two int fields tool.checkSteerCaps (pkg/tools/delegate_followup.go) reads to
// admit or refuse a steer/respond — the exact values wireSessionMessagingForAgent
// passes to SetSteerCaps. White-box, so it fails loudly if the tool's shape
// changes rather than silently reading zero.
func delegateSteerCaps(t *testing.T, dt *tools.DelegateTool) (ratePerMin, bodyBytes int) {
	t.Helper()
	v := reflect.ValueOf(dt).Elem()
	rateField := v.FieldByName("steerRatePerMin")
	bodyField := v.FieldByName("steerBodyBytes")
	require.Truef(t, rateField.IsValid() && rateField.Kind() == reflect.Int &&
		bodyField.IsValid() && bodyField.Kind() == reflect.Int,
		"*tools.DelegateTool no longer exposes the enforced steer caps as int fields "+
			"steerRatePerMin/steerBodyBytes — update this white-box probe to the new shape")
	return int(rateField.Int()), int(bodyField.Int())
}

// TestSessionCoreU3_DelegateSteerCapsFollowConfigAcrossReload proves FR-011/#1216:
// the delegate tool's enforced steer/respond caps are wired from the LIVE
// session_messaging config, so a reload that changes steer_body/steer_rate
// changes what the tool enforces.
//
// The two config values in each phase are DISTINCT and non-default (neither the
// 65,536/60 spec defaults nor any constructor default), so "the caps followed
// the config" cannot be satisfied by a constant.
func TestSessionCoreU3_DelegateSteerCapsFollowConfigAcrossReload(t *testing.T) {
	cfg := minimalTestConfig(t)
	cfg.SessionMessaging.SteerBody = 111111 // C-LIMIT: distinct, non-default
	cfg.SessionMessaging.SteerRatePerMinute = 11

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(al.Close)

	gotRate, gotBody := delegateSteerCaps(t, delegateToolForAgent(t, al, "mia"))
	require.Equal(t, 11, gotRate,
		"boot must wire the delegate steer rate from the live session_messaging.steer_rate")
	require.Equal(t, 111111, gotBody,
		"boot must wire the delegate steer body from the live session_messaging.steer_body")

	// A hot reload with DISTINCT new caps — the shape a Settings save produces.
	newCfg, err := al.GetConfig().Clone()
	require.NoError(t, err)
	newCfg.SessionMessaging.SteerBody = 222222
	newCfg.SessionMessaging.SteerRatePerMinute = 22
	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, newCfg),
		"reload with new session_messaging caps must succeed")

	gotRate, gotBody = delegateSteerCaps(t, delegateToolForAgent(t, al, "mia"))
	require.Equal(t, 22, gotRate,
		"after a reload the delegate must enforce the NEW session_messaging.steer_rate (FR-011/#1216)")
	require.Equal(t, 222222, gotBody,
		"after a reload the delegate must enforce the NEW session_messaging.steer_body (FR-011/#1216)")
}

// TestSessionCoreU3_DelegateSteerBodyCapIsEnforcedAtWiredValue is the instrument
// proof for the probe above: the wired body cap IS the boundary checkSteerCaps
// enforces. A steer one byte over the wired cap must be refused naming the WIRED
// cap; a steer exactly at the cap must not be refused for body size. If the
// enforcement stopped reading the wired field, this test dies even though the
// reload test above would still read it.
func TestSessionCoreU3_DelegateSteerBodyCapIsEnforcedAtWiredValue(t *testing.T) {
	const bodyCap = 40 // C-LIMIT: a tiny distinct cap, not the 65,536 default
	const rateCap = 7  // distinct from the 60 default

	al, _ := newAsyncNotifierTestLoop(t)
	cfg := al.GetConfig()
	cfg.SessionMessaging.SteerBody = bodyCap
	cfg.SessionMessaging.SteerRatePerMinute = rateCap

	ls := session.NewLifecycleStore(t.TempDir())
	require.NoError(t, ls.Persist(&session.LifecycleRecord{
		SessionID:      "u3-cap-probe-child",
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession,
		AgentID:        "mia",
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "u3-cap-probe-parent", RootSessionID: "u3-cap-probe-parent"},
	}))
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), ls)

	dt := delegateToolForAgent(t, al, "mia")

	// Sanity: the wiring ran and the field is the config value.
	_, wiredBody := delegateSteerCaps(t, dt)
	require.Equal(t, bodyCap, wiredBody,
		"the delegate's wired body cap must equal the configured steer_body")

	ctx := tools.WithDelegatePrincipal(context.Background(),
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "u3-cap-operator"})

	over := dt.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "u3-cap-probe-child", "text": strings.Repeat("x", bodyCap+1),
	})
	require.True(t, over.IsError,
		"a steer one byte over the wired body cap must be refused, got: %s", over.ForLLM)
	require.Contains(t, over.ForLLM, "exceeds the 40 byte cap",
		"the refusal must name the WIRED cap (40), proving the wired field gates enforcement")

	at := dt.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "u3-cap-probe-child", "text": strings.Repeat("x", bodyCap),
	})
	require.NotContains(t, at.ForLLM, "byte cap",
		"a steer exactly AT the wired body cap must not be refused for body size, got: %s", at.ForLLM)
}
