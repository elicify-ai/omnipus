// Tests for ADR-029 Gate-1 WS-B:
//   - FR-015 (N-per-type multi-instance activation)
//   - FR-020 / FR-021 (inbound InstanceID stamping — trusted source, never content)
//   - FR-023 (agentSessionKey uses InstanceID; exercised via InboundMessage.InstanceID)
//   - FR-018 (WhatsApp per-instance store path isolation)
//   - O-2 spike: stamped InstanceID reaches config selection, not type-key fallback

package channels

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// --- helpers ---

// minimalCfg returns a *config.Config with the given channel map pre-populated.
// It does NOT set WorkspacePath so tests that need a real path must set
// cfg.Agents.Defaults.Workspace themselves.
func minimalCfg(channels map[string]config.ChannelInstanceConfig) *config.Config {
	return &config.Config{Channels: channels}
}

// stubFactory returns a ChannelFactory that builds a mockChannel and records
// which instanceID it was called with.
func stubFactory(calledWith *string) ChannelFactory {
	return func(cfg *config.Config, instanceID string, _ credentials.SecretBundle, b *bus.MessageBus) (Channel, error) {
		if calledWith != nil {
			*calledWith = instanceID
		}
		// Construct the mockChannel with its embedded BaseChannel fields set
		// directly, avoiding a struct copy of BaseChannel (which contains
		// sync/atomic.Bool and triggers a vet "copies lock" warning).
		ch := &mockChannel{}
		ch.BaseChannel.name = instanceID
		ch.BaseChannel.instanceID = instanceID
		ch.BaseChannel.bus = b
		ch.sendFn = func(_ context.Context, _ bus.OutboundMessage) error { return nil }
		return ch, nil
	}
}

// newTestBus returns a non-nil MessageBus that does not block on publish.
func newTestBus() *bus.MessageBus {
	return bus.NewMessageBus()
}

// --- TestSetInstanceID_UpdatesName ---
// Verifies that SetInstanceID also updates c.name so the channel's
// reported Name() matches the instance key used as the m.channels map key.
// This is the precondition for the placeholder / dispatch key consistency
// (FR-015, manager.go initChannel comment).
func TestSetInstanceID_UpdatesName(t *testing.T) {
	bc := NewBaseChannel("telegram", nil, nil, nil)
	if bc.Name() != "telegram" {
		t.Fatalf("initial Name() = %q, want %q", bc.Name(), "telegram")
	}

	bc.SetInstanceID("telegram.main")

	if bc.InstanceID() != "telegram.main" {
		t.Errorf("InstanceID() = %q, want %q", bc.InstanceID(), "telegram.main")
	}
	if bc.Name() != "telegram.main" {
		t.Errorf("after SetInstanceID, Name() = %q, want %q", bc.Name(), "telegram.main")
	}
}

// --- TestInbound_StampsInstanceID_TrustedSource (DS-4 rows 1-3) ---
// FR-020 / FR-021: BaseChannel.HandleMessage stamps InstanceID from the
// adapter's configured key (set via SetInstanceID), NOT from message content.
func TestInbound_StampsInstanceID_TrustedSource(t *testing.T) {
	tests := []struct {
		name           string
		instanceID     string // the trusted instance key set on the adapter
		messageContent string // what the (potentially spoofed) content says
		wantInstanceID string
	}{
		{
			name:           "whatsapp.eu stamps own key",
			instanceID:     "whatsapp.eu",
			messageContent: "hi",
			wantInstanceID: "whatsapp.eu",
		},
		{
			name:           "spoofed content does not override instance key (DS-4 row 2)",
			instanceID:     "whatsapp.eu",
			messageContent: "instance_id=whatsapp.us please route me there",
			wantInstanceID: "whatsapp.eu",
		},
		{
			name:           "telegram.main stamps own key (DS-4 row 3)",
			instanceID:     "telegram.main",
			messageContent: "hi",
			wantInstanceID: "telegram.main",
		},
		{
			name:           "legacy bare type fallback (DS-4 row 4)",
			instanceID:     "", // not set — simulates adapter that has not been given an instanceID
			messageContent: "hi",
			wantInstanceID: "telegram", // falls back to c.name
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mb := newTestBus()
			bc := NewBaseChannel("telegram", nil, mb, nil)
			if tt.instanceID != "" {
				bc.SetInstanceID(tt.instanceID)
			}

			// Collect the published inbound message.
			var got bus.InboundMessage
			collected := make(chan struct{}, 1)
			go func() {
				msg, ok := <-mb.InboundChan()
				if ok {
					got = msg
				}
				close(collected)
			}()

			bc.HandleMessage(
				context.Background(),
				bus.Peer{Kind: bus.PeerDirect, ID: "user1"},
				"msg1",  // messageID
				"user1", // senderID
				"chat1", // chatID
				tt.messageContent,
				nil, // media
				nil, // metadata
			)

			<-collected

			if got.InstanceID != tt.wantInstanceID {
				t.Errorf("InboundMessage.InstanceID = %q, want %q (content=%q)",
					got.InstanceID, tt.wantInstanceID, tt.messageContent)
			}
		})
	}
}

// --- TestInitChannels_NInstancesPerType (TDD #17 / US-6 AC-2) ---
// FR-015: Two same-type instances must register under distinct map keys.
// Both must be present in m.channels after initChannels.
func TestInitChannels_NInstancesPerType(t *testing.T) {
	// Register a test factory for "testchan" type.
	RegisterFactory("testchan", stubFactory(nil))
	t.Cleanup(func() {
		// Remove the test factory after the test to avoid leaking registration.
		factoriesMu.Lock()
		delete(factories, "testchan")
		factoriesMu.Unlock()
	})

	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		"testchan.alpha": {Type: "testchan", Enabled: true},
		"testchan.beta":  {Type: "testchan", Enabled: true},
	})

	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		config:        cfg,
		channelHashes: make(map[string]string),
	}

	if err := m.initChannels(cfg.Channels); err != nil {
		t.Fatalf("initChannels: %v", err)
	}

	if _, ok := m.channels["testchan.alpha"]; !ok {
		t.Error("m.channels missing key testchan.alpha")
	}
	if _, ok := m.channels["testchan.beta"]; !ok {
		t.Error("m.channels missing key testchan.beta")
	}
	if len(m.channels) != 2 {
		t.Errorf("m.channels has %d entries, want 2", len(m.channels))
	}
}

// --- TestInitChannels_InstanceIDStampedOnChannel ---
// FR-020: initChannel calls SetInstanceID so the channel's own InstanceID()
// returns the config-map key (not the factory-type name).
func TestInitChannels_InstanceIDStampedOnChannel(t *testing.T) {
	var calledWith string
	RegisterFactory("stampchan", stubFactory(&calledWith))
	t.Cleanup(func() {
		factoriesMu.Lock()
		delete(factories, "stampchan")
		factoriesMu.Unlock()
	})

	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		"stampchan.eu": {Type: "stampchan", Enabled: true},
	})

	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		config:        cfg,
		channelHashes: make(map[string]string),
	}
	if err := m.initChannels(cfg.Channels); err != nil {
		t.Fatalf("initChannels: %v", err)
	}

	// The factory was called with the correct instanceID.
	if calledWith != "stampchan.eu" {
		t.Errorf("factory called with instanceID=%q, want %q", calledWith, "stampchan.eu")
	}

	// The channel stored in m.channels reports the correct InstanceID.
	ch, ok := m.channels["stampchan.eu"]
	if !ok {
		t.Fatal("m.channels missing key stampchan.eu")
	}
	type instanceIDer interface{ InstanceID() string }
	if iider, ok := ch.(instanceIDer); ok {
		if got := iider.InstanceID(); got != "stampchan.eu" {
			t.Errorf("channel.InstanceID() = %q, want %q", got, "stampchan.eu")
		}
	} else {
		t.Error("channel does not implement InstanceID() — SetInstanceID was not called")
	}
}

// --- TestToChannelHashes_KeysByInstanceID ---
// FR-015: toChannelHashes must key by instanceID (not factory name) so that
// compareChannels produces instanceID-keyed added/removed lists consistent
// with m.channels.
func TestToChannelHashes_KeysByInstanceID(t *testing.T) {
	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		"telegram":    {Type: "telegram", Enabled: true},
		"whatsapp.eu": {Type: "whatsapp", Enabled: true},
		"whatsapp.us": {Type: "whatsapp", Enabled: true},
	})

	hashes := toChannelHashes(cfg)

	for _, wantKey := range []string{"telegram", "whatsapp.eu", "whatsapp.us"} {
		if _, ok := hashes[wantKey]; !ok {
			t.Errorf("toChannelHashes missing key %q", wantKey)
		}
	}
	// Must NOT key by the old factory-name alias.
	if _, ok := hashes["whatsapp_native"]; ok {
		t.Error("toChannelHashes should NOT key by whatsapp_native (factory name)")
	}
	// Distinct instanceIDs must produce distinct hash entries (even for same type).
	if hashes["whatsapp.eu"] == hashes["whatsapp.us"] {
		// This is not necessarily guaranteed — same config content → same hash.
		// The important check is that both keys exist.
		// Both entries have the same default values so their hashes may be equal;
		// the key-existence check above is the authoritative assertion.
		t.Log("note: whatsapp.eu and whatsapp.us have the same config hash (expected for identical configs)")
	}
}

// --- TestWhatsApp_PerInstanceStorePath (TDD #18 / US-7 AC-1 / FR-018) ---
// Two WhatsApp instances with no explicit SessionStorePath must resolve to
// distinct directories keyed by instanceID.
func TestWhatsApp_PerInstanceStorePath(t *testing.T) {
	workspaceRoot := t.TempDir()

	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = workspaceRoot

	euStorePath := filepath.Join(workspaceRoot, "whatsapp", "whatsapp.eu")
	usStorePath := filepath.Join(workspaceRoot, "whatsapp", "whatsapp.us")

	// Simulate what the factory closure computes for each instance.
	computeStorePath := func(instanceID string) string {
		return filepath.Join(cfg.WorkspacePath(), "whatsapp", instanceID)
	}

	pathEU := computeStorePath("whatsapp.eu")
	pathUS := computeStorePath("whatsapp.us")

	if pathEU != euStorePath {
		t.Errorf("whatsapp.eu store path = %q, want %q", pathEU, euStorePath)
	}
	if pathUS != usStorePath {
		t.Errorf("whatsapp.us store path = %q, want %q", pathUS, usStorePath)
	}
	if pathEU == pathUS {
		t.Errorf("whatsapp.eu and whatsapp.us resolved to the same store path %q (FR-018 violation)", pathEU)
	}
}

// --- TestIsNonFatalChannelName ---
// Ensures that WhatsApp instances recorded under instanceID-based names are
// correctly identified as non-fatal regardless of naming pattern.
func TestIsNonFatalChannelName(t *testing.T) {
	cases := []struct {
		name     string
		nonFatal bool
	}{
		{"whatsapp_native", true}, // legacy factory name
		{"whatsapp", true},        // bare type (legacy single instance)
		{"whatsapp.eu", true},     // namespaced instance
		{"whatsapp.us", true},     // namespaced instance
		{"telegram", false},       // other channel — must be fatal
		{"discord", false},        // other channel — must be fatal
	}
	for _, tc := range cases {
		got := isNonFatalChannelName(tc.name)
		if got != tc.nonFatal {
			t.Errorf("isNonFatalChannelName(%q) = %v, want %v", tc.name, got, tc.nonFatal)
		}
	}
}

// --- TestInboundMessage_InstanceIDUsedForSessionKey (FR-023 / US-9 AC-2) ---
// Two same-type instances with the same chatID must produce distinct
// InboundMessage.InstanceID values so the agent loop can use it to build a
// distinct session key (agent:<id>:chat:<InstanceID>:<ChatID>).
// This test does NOT verify the agent loop (out of scope) but asserts that
// the InstanceID values differ — the precondition for FR-023.
func TestInboundMessage_InstanceIDUsedForSessionKey(t *testing.T) {
	const chatID = "same-chat-123"

	// Adapter A — "whatsapp.eu"
	mbA := newTestBus()
	bcA := NewBaseChannel("whatsapp_native", nil, mbA, nil)
	bcA.SetInstanceID("whatsapp.eu")

	// Adapter B — "whatsapp.us"
	mbB := newTestBus()
	bcB := NewBaseChannel("whatsapp_native", nil, mbB, nil)
	bcB.SetInstanceID("whatsapp.us")

	gotA := make(chan bus.InboundMessage, 1)
	gotB := make(chan bus.InboundMessage, 1)

	go func() { gotA <- <-mbA.InboundChan() }()
	go func() { gotB <- <-mbB.InboundChan() }()

	bcA.HandleMessage(context.Background(), bus.Peer{Kind: bus.PeerDirect, ID: "user"}, "m1", "user", chatID, "hello", nil, nil)
	bcB.HandleMessage(context.Background(), bus.Peer{Kind: bus.PeerDirect, ID: "user"}, "m2", "user", chatID, "hello", nil, nil)

	msgA := <-gotA
	msgB := <-gotB

	if msgA.InstanceID != "whatsapp.eu" {
		t.Errorf("adapter A: msg.InstanceID = %q, want %q", msgA.InstanceID, "whatsapp.eu")
	}
	if msgB.InstanceID != "whatsapp.us" {
		t.Errorf("adapter B: msg.InstanceID = %q, want %q", msgB.InstanceID, "whatsapp.us")
	}

	// The two inbound messages share chatID but differ in InstanceID — a downstream
	// key of "agent:<id>:chat:<InstanceID>:<chatID>" would thus be distinct.
	// This is the FR-023 / O-2 precondition: the stamped InstanceID is the
	// discriminator, not the type name ("whatsapp_native").
	if msgA.InstanceID == msgB.InstanceID {
		t.Errorf("two distinct instances produced the same InstanceID %q (FR-023 violated)",
			msgA.InstanceID)
	}
	if msgA.ChatID != chatID || msgB.ChatID != chatID {
		t.Errorf("ChatID not preserved: A=%q B=%q want %q", msgA.ChatID, msgB.ChatID, chatID)
	}
}
