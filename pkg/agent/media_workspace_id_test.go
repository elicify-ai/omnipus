// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// media_workspace_id_test.go — FIX 1 regression: the sole construction site
// of bus.OutboundMediaMessage (loop.go, the tool-media delivery block) never
// set WorkspaceID, so every channel's SendMedia call resolved media refs via
// store.ResolveWithCallerWorkspace(part.Ref, "") — always the private/global
// room, never the turn's actual workspace. This silently degraded
// workspace-scoped media resolution for every channel send.
//
// This test drives the real channel-binding → session-workspace-inheritance
// path (ADR-029 FR-022, exercised end-to-end in session_workspace_id_test.go)
// combined with the real tool-media-delivery path (exercised end-to-end in
// loop_test.go's TestProcessMessage_MediaToolDeliveryEmitsMediaAndCallsFollowUp)
// and asserts the two now compose: a media-producing tool call inside a
// workspace-bound channel session must deliver SendMedia with WorkspaceID set
// to that workspace.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestProcessMessage_MediaToolDelivery_StampsWorkspaceID is the FIX 1
// regression. A telegram instance bound to workspace "sales" (mirroring
// session_workspace_id_test.go's binding setup) receives a message that
// triggers a media-producing tool (handledMediaTool, the same tool
// loop_test.go's media-delivery test already exercises). The resulting
// SendMedia call must carry WorkspaceID == "sales" — before the fix it was
// always "".
func TestProcessMessage_MediaToolDelivery_StampsWorkspaceID(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		// Bind a telegram instance to workspace "sales" — the same shape
		// session_workspace_id_test.go uses to prove a channel session
		// inherits WorkspaceID from its bound instance. No Identity override
		// is set, so routing still falls through to the default agent
		// (matching the plain, unbound "telegram" setup the media-delivery
		// test already relies on).
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram.sales": {
				Type:        "telegram",
				Enabled:     true,
				WorkspaceID: "sales",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &handledMediaProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	store := media.NewFileMediaStore()
	al.SetMediaStore(store)
	telegramChannel := &fakeMediaChannel{fakeChannel: fakeChannel{id: "rid-telegram"}}
	al.SetChannelManager(newStartedTestChannelManager(t, msgBus, store, "telegram", telegramChannel))

	imagePath := filepath.Join(tmpDir, "screen-workspace.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("fake screenshot"), 0o644))

	al.RegisterTool(&handledMediaTool{
		store: store,
		path:  imagePath,
	})
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "expected default agent")
	// No-default-policy model (CLAUDE.md hard constraint 6): handled_media_tool
	// needs an explicit agent-level grant, or it fails closed to "deny" before
	// the media-delivery behavior under test ever gets a chance to run.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"handled_media_tool": "allow"},
	})

	response, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:    "telegram",
		InstanceID: "telegram.sales",
		ChatID:     "chat1",
		Sender: bus.SenderInfo{
			CanonicalID: "user1",
		},
		Content: "take a screenshot of the screen and send it to me",
	})
	require.NoError(t, err, "processMessage() must succeed")
	require.Equal(t, "Here is the screenshot.", response,
		"expected the follow-up caption from the second LLM call")

	require.Len(t, telegramChannel.sentMedia, 1,
		"expected exactly 1 synchronously sent media message")
	assert.Equal(t, "sales", telegramChannel.sentMedia[0].WorkspaceID,
		"SendMedia's OutboundMediaMessage.WorkspaceID must carry the turn's "+
			"workspace (inherited from the bound channel instance) so channels' "+
			"store.ResolveWithCallerWorkspace resolves media scoped to that "+
			"workspace instead of silently falling back to the private/global room")
}

// TestProcessMessage_MediaToolDelivery_UnboundChannel_EmptyWorkspaceID is the
// negative-case companion: an UNBOUND channel instance (no WorkspaceID
// configured) must still deliver media with an empty WorkspaceID — FIX 1
// must not leak a stale or guessed workspace onto unbound turns.
func TestProcessMessage_MediaToolDelivery_UnboundChannel_EmptyWorkspaceID(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		// No cfg.Channels entry — "telegram" is an unbound instance.
	}

	msgBus := bus.NewMessageBus()
	provider := &handledMediaProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	store := media.NewFileMediaStore()
	al.SetMediaStore(store)
	telegramChannel := &fakeMediaChannel{fakeChannel: fakeChannel{id: "rid-telegram"}}
	al.SetChannelManager(newStartedTestChannelManager(t, msgBus, store, "telegram", telegramChannel))

	imagePath := filepath.Join(tmpDir, "screen-unbound.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("fake screenshot"), 0o644))

	al.RegisterTool(&handledMediaTool{
		store: store,
		path:  imagePath,
	})
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "expected default agent")
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"handled_media_tool": "allow"},
	})

	response, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "telegram",
		ChatID:  "chat1",
		Sender: bus.SenderInfo{
			CanonicalID: "user1",
		},
		Content: "take a screenshot of the screen and send it to me",
	})
	require.NoError(t, err)
	require.Equal(t, "Here is the screenshot.", response)

	require.Len(t, telegramChannel.sentMedia, 1)
	assert.Equal(t, "", telegramChannel.sentMedia[0].WorkspaceID,
		"an unbound channel instance must not stamp any workspace onto the outbound media")
}
