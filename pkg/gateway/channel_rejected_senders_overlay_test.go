package gateway

// T-04 / FR-061 / S-53, WP-1 CHECK F2: exercise the real GET /channels
// projection, not applyRejectedSendersOverlay in isolation or a fake reporter.
// Oracles: channels-spec-r8 FR-060/061, S-53 and ChannelEntry contract:
// two rejects of the same sender -> {count:2,recent:[id]}; no rejects -> the
// optional field is ABSENT (not null, an empty object, or count zero).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels/line"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// t04ListedChannel reads the registered/authenticated production list route.
// The generated carrier checks values; raw JSON keys distinguish omission
// from null, which a nil generated pointer alone cannot establish.
func t04ListedChannel(t *testing.T, f *wu6Fixture, id string) (gen.ChannelEntry, map[string]json.RawMessage) {
	t.Helper()
	rec := f.do(http.MethodGet, "/api/v1/channels", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var entries []json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &entries))
	var matches []json.RawMessage
	for _, raw := range entries {
		var entry gen.ChannelEntry
		require.NoError(t, json.Unmarshal(raw, &entry))
		if string(entry.Id) == id {
			matches = append(matches, raw)
		}
	}
	require.Len(t, matches, 1, "instrument: GET /channels must identify the selected real instance exactly once")
	var entry gen.ChannelEntry
	require.NoError(t, json.Unmarshal(matches[0], &entry))
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(matches[0], &fields))
	return entry, fields
}

func TestT04_RejectedSendersOverlay_EmptyAdapterOmitsField(t *testing.T) {
	f := newWU6Fixture(t)
	adapter, exists := f.svc.ChannelManager.GetChannel("line.healthy")
	require.True(t, exists)
	require.IsType(t, &line.LINEChannel{}, adapter, "instrument: this is a real adapter, not a reporter stub")
	require.True(t, adapter.IsRunning())

	entry, fields := t04ListedChannel(t, f, "line.healthy")
	require.Nil(t, entry.RejectedSenders, "FR-061: an adapter with no rejected messages has no diagnostic")
	require.NotContains(t, fields, "rejected_senders", "ChannelEntry contract: omit the field, not null or an empty record")
}

func TestT04_RejectedSendersOverlay_RealAdapterTwoRejectsReachListOnlyForThatInstance(t *testing.T) {
	f := t02BootFixture(t, func(base *issue1277Fixture) {
		// Keep a second real adapter running with no rejections, to detect
		// a projection that accidentally reuses another instance's record.
		require.NoError(t, base.store.Set(issue1277BrokenRef, "qa1277-restored-peer-secret"))
		inst := base.cfg.Channels["line.healthy"]
		inst.AllowFrom = config.FlexibleStringSlice{"line:qa-allowed"}
		base.cfg.Channels["line.healthy"] = inst
	})
	adapter, exists := f.svc.ChannelManager.GetChannel("line.healthy")
	require.True(t, exists)
	concrete, ok := adapter.(*line.LINEChannel)
	require.True(t, ok, "instrument: the production manager constructed the real LINE adapter")
	require.True(t, concrete.IsRunning())
	sender := bus.SenderInfo{CanonicalID: "line:qa-rejected", PlatformID: "qa-rejected", Platform: "line"}
	require.False(t, concrete.IsAllowedSender(sender), "instrument: this sender is outside the saved nonempty allow list")
	for _, messageID := range []string{"fr061-first", "fr061-repeat"} {
		require.NoError(t, concrete.HandleMessage(context.Background(), bus.Peer{}, messageID,
			"qa-rejected", "qa-chat", "SENTINEL-TEXT-551", nil, nil, sender))
	}

	entry, fields := t04ListedChannel(t, f, "line.healthy")
	require.NotNil(t, entry.RejectedSenders, "FR-061/S-53: the real list route must expose this adapter's recorded rejections")
	require.Equal(t, 2, entry.RejectedSenders.Count, "S-53: count messages, not distinct senders")
	require.Equal(t, []string{"line:qa-rejected"}, entry.RejectedSenders.Recent, "S-53: repeated sender appears once")
	require.Contains(t, fields, "rejected_senders")
	require.JSONEq(t, `{"count":2,"recent":["line:qa-rejected"]}`, string(fields["rejected_senders"]),
		"FR-061/ChannelRejectedSenders: exact diagnostic wire shape, with no message text or extra data")

	peer, exists := f.svc.ChannelManager.GetChannel("line")
	require.True(t, exists)
	require.IsType(t, &line.LINEChannel{}, peer)
	require.True(t, peer.IsRunning())
	peerEntry, peerFields := t04ListedChannel(t, f, "line")
	require.Nil(t, peerEntry.RejectedSenders, "FR-061: diagnostics belong to their own adapter instance")
	require.NotContains(t, peerFields, "rejected_senders", "no-rejection peer remains field-absent")
}
