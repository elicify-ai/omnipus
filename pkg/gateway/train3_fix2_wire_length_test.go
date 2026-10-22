package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Founder decision 2026-10-20 (gate round 2: security S4, silent-failure
// SF-07, architect N1). The browser validates approval frames with the
// generated Zod schemas, whose string .max(n) compares JavaScript
// String.length — UTF-16 code units (zod 3.25.76 ZodString._parse,
// `input.data.length > check.value`). One character outside the Basic
// Multilingual Plane, such as U+20000, is two units. A name or label the
// server emits must therefore fit in UTF-16 units, or the browser drops the
// WHOLE live frame and the WHOLE reload snapshot (every other pending card in
// it too). An over-long optional name/label is omitted; the approval itself
// always arrives.

const train3Fix2Supplementary = "\U00020000" // one code point, two UTF-16 units

// train3Fix2BrowserFits is the browser validator's rule for z.string().max(n).
func train3Fix2BrowserFits(s string, n int) bool {
	return len(utf16.Encode([]rune(s))) <= n
}

var (
	// 79 code points (under 128) but 144 UTF-16 units (over 128).
	train3Fix2NameOver = "UnicodeHelper " + strings.Repeat(train3Fix2Supplementary, 65)
	// Exactly 128 UTF-16 units: the boundary is still emitted.
	train3Fix2NameAt = strings.Repeat(train3Fix2Supplementary, 64)
	// 106 code points (under 200) but 202 UTF-16 units (over 200).
	train3Fix2LabelOver = "for group " + strings.Repeat(train3Fix2Supplementary, 96)
	// Exactly 200 UTF-16 units.
	train3Fix2LabelAt = "for group " + strings.Repeat(train3Fix2Supplementary, 95)
)

func train3Fix2Handler(t *testing.T) (*WSHandler, *wsConn) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "test-default-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "mia"},
				{ID: "uni-over", Name: train3Fix2NameOver},
				{ID: "uni-at", Name: train3Fix2NameAt},
				{ID: "plain", Name: "Plain Helper"},
			},
		},
	}
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)
	handler.approvalRegV2 = newApprovalRegistryV2(64, 300*time.Second)
	wc := &wsConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{}), userID: "train3-fix2"}
	handler.mu.Lock()
	if handler.sessions == nil {
		handler.sessions = make(map[string]*wsConn)
	}
	handler.sessions["train3-fix2"] = wc
	handler.mu.Unlock()
	return handler, wc
}

func train3Fix2Request(t *testing.T, h *WSHandler, callID, agentID, label string) *approvalEntry {
	t.Helper()
	entry, accepted := h.approvalRegV2.requestApproval(callID, "bash", map[string]any{"command": "echo hi"},
		agentID, "sess-"+callID, "turn-"+callID, approvalExtras{OriginLabel: label})
	require.True(t, accepted)
	go func() { <-entry.resultCh }()
	t.Cleanup(func() { go func() { h.approvalRegV2.resolve(entry.ApprovalID, ApprovalActionCancel, false) }() })
	return entry
}

func train3Fix2Ptr(p *string) string {
	if p == nil {
		return "<omitted>"
	}
	return *p
}

// Live frames: every tool_approval_required frame reaches the browser in a
// shape its validator accepts. A supplementary-plane name or label over the
// limit is omitted (the card still arrives); one exactly at the limit is kept.
func TestTrain3Fix2_LiveApprovalFieldsFitTheBrowserValidator(t *testing.T) {
	cases := []struct {
		name, agentID, label string
		wantName, wantLabel  string
	}{
		{"name over the limit is omitted", "uni-over", "", "<omitted>", "<omitted>"},
		{"name exactly at the limit is kept", "uni-at", "", train3Fix2NameAt, "<omitted>"},
		{"label over the limit is omitted", "plain", train3Fix2LabelOver, "Plain Helper", "<omitted>"},
		{"label exactly at the limit is kept", "plain", train3Fix2LabelAt, "Plain Helper", train3Fix2LabelAt},
	}
	h, wc := train3Fix2Handler(t)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := train3Fix2Request(t, h, "tc-live-"+string(rune('a'+i)), tc.agentID, tc.label)
			h.broadcastToolApprovalRequired(entry)
			var frame generated.ToolApprovalRequiredFrame
			select {
			case raw := <-wc.sendCh:
				require.NoError(t, json.Unmarshal(raw, &frame))
			case <-time.After(2 * time.Second):
				t.Fatal("no tool_approval_required frame: the approval itself must always arrive")
			}
			require.Equal(t, entry.ApprovalID, frame.ApprovalId)
			require.Equal(t, tc.wantName, train3Fix2Ptr(frame.ActingAgentName))
			require.Equal(t, tc.wantLabel, train3Fix2Ptr(frame.OriginLabel))
			if frame.ActingAgentName != nil {
				require.True(t, train3Fix2BrowserFits(*frame.ActingAgentName, approvalActingNameMaxLen))
			}
			if frame.OriginLabel != nil {
				require.True(t, train3Fix2BrowserFits(*frame.OriginLabel, approvalOriginLabelMaxLen))
			}
		})
	}
}

// Reload snapshot: one over-limit name/label entry must not cost the others —
// the snapshot lists every pending approval, the valid one intact, the
// over-limit fields omitted, the at-limit ones kept, and every emitted name and
// label fits the browser validator (one misfit rejects the WHOLE snapshot).
func TestTrain3Fix2_ReloadSnapshotKeepsEveryApprovalBrowserValid(t *testing.T) {
	h, wc := train3Fix2Handler(t)
	over := train3Fix2Request(t, h, "tc-snap-over", "uni-over", train3Fix2LabelOver)
	at := train3Fix2Request(t, h, "tc-snap-at", "uni-at", train3Fix2LabelAt)
	valid := train3Fix2Request(t, h, "tc-snap-valid", "plain", "asked by Plain Helper")

	var state generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(h.sessionStateBytes(wc, "sess-tc-snap-valid"), &state))
	byID := map[string]generated.SessionStatePendingApproval{}
	for _, p := range state.PendingApprovals {
		byID[p.ApprovalId] = p
		if p.ActingAgentName != nil {
			require.True(t, train3Fix2BrowserFits(*p.ActingAgentName, approvalActingNameMaxLen),
				"approval %s: acting_agent_name would make the browser reject the whole snapshot", p.ApprovalId)
		}
		if p.OriginLabel != nil {
			require.True(t, train3Fix2BrowserFits(*p.OriginLabel, approvalOriginLabelMaxLen),
				"approval %s: origin_label would make the browser reject the whole snapshot", p.ApprovalId)
		}
	}
	require.Len(t, byID, 3, "every pending approval is in the snapshot")

	require.Equal(t, "<omitted>", train3Fix2Ptr(byID[over.ApprovalID].ActingAgentName))
	require.Equal(t, "<omitted>", train3Fix2Ptr(byID[over.ApprovalID].OriginLabel))
	require.Equal(t, train3Fix2NameAt, train3Fix2Ptr(byID[at.ApprovalID].ActingAgentName))
	require.Equal(t, train3Fix2LabelAt, train3Fix2Ptr(byID[at.ApprovalID].OriginLabel))
	require.Equal(t, "Plain Helper", train3Fix2Ptr(byID[valid.ApprovalID].ActingAgentName))
	require.Equal(t, "asked by Plain Helper", train3Fix2Ptr(byID[valid.ApprovalID].OriginLabel))
}
