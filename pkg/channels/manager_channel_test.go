package channels

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

func TestToChannelHashes(t *testing.T) {
	logger.SetLevel(logger.DEBUG)
	cfg := config.DefaultConfig()
	results := toChannelHashes(cfg)
	assert.Equal(t, 0, len(results))
	logger.Debugf("results: %v", results)

	cfg2 := config.DefaultConfig()
	inst2 := cfg2.Channels["dingtalk"]
	inst2.Enabled = true
	cfg2.Channels["dingtalk"] = inst2
	results2 := toChannelHashes(cfg2)
	assert.Equal(t, 1, len(results2))
	logger.Debugf("results2: %v", results2)
	added, removed := compareChannels(results, results2)
	assert.EqualValues(t, []string{"dingtalk"}, added)
	assert.EqualValues(t, []string(nil), removed)

	cfg3 := config.DefaultConfig()
	inst3 := cfg3.Channels["telegram"]
	inst3.Enabled = true
	cfg3.Channels["telegram"] = inst3
	results3 := toChannelHashes(cfg3)
	assert.Equal(t, 1, len(results3))
	logger.Debugf("results3: %v", results3)
	added, removed = compareChannels(results2, results3)
	assert.EqualValues(t, []string{"dingtalk"}, removed)
	assert.EqualValues(t, []string{"telegram"}, added)

	inst3b := cfg3.Channels["telegram"]
	inst3b.TokenRef = "TELEGRAM_TOKEN_REF_114314"
	cfg3.Channels["telegram"] = inst3b
	results4 := toChannelHashes(cfg3)
	assert.Equal(t, 1, len(results4))
	logger.Debugf("results4: %v", results4)
	added, removed = compareChannels(results3, results4)
	assert.EqualValues(t, []string{"telegram"}, removed)
	assert.EqualValues(t, []string{"telegram"}, added)

	cc, err := toChannelConfig(cfg3, added)
	assert.NoError(t, err)
	tgInst := config.InstanceToTelegram(cc["telegram"])
	logger.Debugf("cc telegram: %#v", tgInst)
	assert.Equal(t, "TELEGRAM_TOKEN_REF_114314", tgInst.TokenRef)
	assert.Equal(t, true, tgInst.Enabled)

	cc, err = toChannelConfig(cfg2, added)
	assert.NoError(t, err)
	tgInst2 := config.InstanceToTelegram(cc["telegram"])
	logger.Debugf("cc telegram (cfg2): %#v", tgInst2)
	assert.Equal(t, "", tgInst2.TokenRef)
	assert.Equal(t, false, tgInst2.Enabled)
}

// TestToChannelHashes_WhatsAppKeysByInstanceID is the function-level regression
// guard for ADR-029 Gate-1 WS-B: the WhatsApp config lives under the JSON key
// "whatsapp" (or "whatsapp.eu" for namespaced instances). m.channels and
// m.channelHashes are both keyed by instanceID so the reload diff
// (compareChannels) produces instanceID-keyed added/removed lists.
//
// Pre-ADR-029 the hash was keyed by "whatsapp_native" (the factory name) so
// that initChannels used m.channels["whatsapp_native"]. That invariant is gone:
// initChannel now keys m.channels by instanceID, so hashes must also key by
// instanceID. This test asserts the new correct behavior.
//
// Asserting purely at the function level (toChannelHashes / compareChannels /
// toChannelConfig) so it needs no real whatsmeow connection or network.
func TestToChannelHashes_WhatsAppKeysByInstanceID(t *testing.T) {
	cfg := config.DefaultConfig()
	waInst := cfg.Channels["whatsapp"]
	waInst.Enabled = true
	cfg.Channels["whatsapp"] = waInst

	hashes := toChannelHashes(cfg)

	// The hash map must be keyed by the instanceID ("whatsapp"), NOT the factory name.
	if _, ok := hashes["whatsapp"]; !ok {
		t.Fatalf("toChannelHashes must key enabled WhatsApp under instanceID "+
			"\"whatsapp\"; got keys: %v", hashes)
	}
	if _, ok := hashes["whatsapp_native"]; ok {
		t.Fatalf("toChannelHashes must NOT key WhatsApp under factory name "+
			"\"whatsapp_native\" (FR-015/WS-B: m.channels is keyed by instanceID); got keys: %v", hashes)
	}

	// A reload that enables WhatsApp (empty old map → enabled new map) must produce
	// added = ["whatsapp"] and nothing removed.
	added, removed := compareChannels(map[string]string{}, hashes)
	assert.EqualValues(t, []string{"whatsapp"}, added,
		"enabling WhatsApp must add the instanceID \"whatsapp\", not the factory name")
	assert.EqualValues(t, []string(nil), removed,
		"enabling WhatsApp must not mark anything removed")

	// toChannelConfig must still select the WhatsApp config block when the list
	// holds the instanceID — otherwise initChannels would never see it enabled.
	cc, err := toChannelConfig(cfg, added)
	assert.NoError(t, err)
	waResult := config.InstanceToWhatsApp(cc["whatsapp"])
	assert.True(t, waResult.Enabled,
		"toChannelConfig must populate WhatsApp.Enabled when list holds \"whatsapp\"")
}
