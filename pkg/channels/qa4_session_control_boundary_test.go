package channels

// F4 oracle: QA4 dispatch / pr-test-analyzer F4 / ADR-20260928 D9.
// Authorization precedes either control, and control interception precedes all
// ordinary intake. BaseChannel, MessageBus and reply formatting remain real;
// only the external control endpoint and outbound transport are substituted.
// Reply text is compared with the command layer's public formatting contract:
// this pack verifies routing of that outcome, not the formatter's own wording.
// Mutations and independent CHECK are deferred, not claimed as performed.

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// not-wire-format: these are in-memory test observations, never serialized.
type qa4ChannelCall struct {
	method, channel, chatID, userID, instruction string
}

type qa4ChannelControlEndpoint struct {
	call  func(context.Context, qa4ChannelCall)
	fired bool
	armed bool
	err   error
}

func (e *qa4ChannelControlEndpoint) RequestCancelByChannelChat(ctx context.Context, channel, chatID, userID string) (bool, bool, error) {
	e.call(ctx, qa4ChannelCall{method: "cancel", channel: channel, chatID: chatID, userID: userID})
	return e.fired, e.armed, e.err
}

func (e *qa4ChannelControlEndpoint) RequestStopByChannelChat(ctx context.Context, channel, chatID, userID string) (bool, bool, error) {
	e.call(ctx, qa4ChannelCall{method: "stop", channel: channel, chatID: chatID, userID: userID})
	return e.fired, e.armed, e.err
}

func (e *qa4ChannelControlEndpoint) RequestRedirectByChannelChat(ctx context.Context, channel, chatID, userID, instruction string) error {
	e.call(ctx, qa4ChannelCall{method: "redirect", channel: channel, chatID: chatID, userID: userID, instruction: instruction})
	return e.err
}

type qa4ChannelCase struct {
	name         string
	denied       bool
	legacySender bool
	fired, armed bool
	controlErr   error
	sendErr      error
	wantReply    string
}

func TestQA4ChannelStop_AuthorizesBeforeControlAndInterceptsBeforeIntake(t *testing.T) {
	refused := errors.New("required Stop storage write was refused")
	sendErr := errors.New("outbound reply transport was refused")
	qa4ChannelControlCases(t, "stop", "/stop", "", []qa4ChannelCase{
		{name: "denied_structured_sender", denied: true},
		{name: "denied_legacy_sender", denied: true, legacySender: true},
		{name: "authorized_stop", fired: true, wantReply: commands.StopCommandReply(nil)},
		{name: "authorized_armed_stop", armed: true, wantReply: commands.StopCommandReply(commands.ErrCancelArmed)},
		{name: "authorized_no_active_turn", wantReply: commands.StopCommandReply(commands.ErrNoActiveTurn)},
		{name: "authorized_stop_refusal_is_visible", controlErr: refused, wantReply: commands.StopCommandReply(refused)},
		{name: "authorized_failed_reply_propagates", fired: true, sendErr: sendErr, wantReply: commands.StopCommandReply(nil)},
	})
}

func TestQA4ChannelRedirect_AuthorizesBeforeControlAndInterceptsBeforeIntake(t *testing.T) {
	// The command must strip only its token and surrounding whitespace, never
	// truncate or normalize the operator's multiline replacement instruction.
	const instruction = "Keep the original data.\nThen produce  two exact checks."
	refused := errors.New("replacement instruction could not be persisted")
	sendErr := errors.New("outbound redirect reply was refused")
	qa4ChannelControlCases(t, "redirect", "/stop-redirect  "+instruction+"  ", instruction, []qa4ChannelCase{
		{name: "denied_structured_sender", denied: true},
		{name: "denied_legacy_sender", denied: true, legacySender: true},
		{name: "authorized_redirect", wantReply: commands.StopRedirectReply(nil)},
		{name: "authorized_finished_helper_refusal", controlErr: commands.ErrNothingToRedirect, wantReply: commands.StopRedirectReply(commands.ErrNothingToRedirect)},
		{name: "authorized_redirect_refusal_is_visible", controlErr: refused, wantReply: commands.StopRedirectReply(refused)},
		{name: "authorized_failed_reply_propagates", sendErr: sendErr, wantReply: commands.StopRedirectReply(nil)},
	})
}

func qa4ChannelControlCases(t *testing.T, method, command, instruction string, rows []qa4ChannelCase) {
	t.Helper()
	// Instance-qualified entries protect the active-turn lookup identity; the
	// legacy entry also verifies the factory-name fallback. All adapters use
	// this same real HandleMessage authorization/intake boundary.
	instances := []struct{ factory, instance, platform string }{
		{"telegram", "telegram.main", "telegram"},
		{"whatsapp_native", "whatsapp.eu", "whatsapp"},
		{"telegram", "", "telegram"},
	}
	for _, instance := range instances {
		channelName := instance.instance
		if channelName == "" {
			channelName = instance.factory
		}
		t.Run(channelName, func(t *testing.T) {
			for _, row := range rows {
				t.Run(row.name, func(t *testing.T) {
					qa4ChannelControlCase(t, instance.factory, instance.instance, instance.platform, channelName, method, command, instruction, row)
				})
			}
		})
	}
}

func qa4RequireNoOrdinaryIntake(t *testing.T, msgBus *bus.MessageBus, boundary string) {
	t.Helper()
	select {
	case inbound := <-msgBus.InboundChan():
		t.Errorf("%s: control command leaked into ordinary intake before interception: %+v", boundary, inbound)
	default:
	}
}

func qa4ChannelControlCase(t *testing.T, factory, instance, platform, channelName, method, command, instruction string, row qa4ChannelCase) {
	t.Helper()
	msgBus := newTestBus()
	t.Cleanup(msgBus.Close)
	ch := NewBaseChannel(factory, nil, msgBus, []string{"123456"})
	if instance != "" {
		ch.SetInstanceID(instance)
	}
	const chatID = "qa4-trusted-chat"
	canonical := platform + ":123456"
	sender := bus.SenderInfo{Platform: platform, PlatformID: "123456", CanonicalID: canonical}
	peer := bus.Peer{Kind: bus.PeerDirect, ID: "123456"}
	owner := &mockChannel{sendFn: func(context.Context, bus.OutboundMessage) error { return nil }}
	ch.SetOwner(owner)
	ch.SetPlaceholderRecorder(newTestManager())

	// Positive instrument control: the same actual HandleMessage/bus pair
	// delivers normal authorized text, before controls are installed. An
	// unwired/closed bus cannot pass by yielding zero inbound messages.
	require.NoError(t, ch.HandleMessage(context.Background(), peer, "qa4-normal", "123456", chatID, "ordinary authorized text", nil, nil, sender))
	select {
	case got := <-msgBus.InboundChan():
		require.Equal(t, "ordinary authorized text", got.Content)
		require.Equal(t, channelName, got.Channel)
		require.Equal(t, channelName, got.InstanceID)
		require.Equal(t, chatID, got.ChatID)
		require.Equal(t, sender, got.Sender)
	default:
		t.Fatal("SETUP: positive ordinary-intake control did not reach the actual bus")
	}
	require.Equal(t, 1, owner.placeholdersSent, "SETUP: normal intake must activate the actual placeholder pipeline too")

	var calls []qa4ChannelCall
	var events []string
	ctx := context.WithValue(context.Background(), struct{ name string }{"qa4-context"}, "trusted request")
	endpoint := &qa4ChannelControlEndpoint{fired: row.fired, armed: row.armed, err: row.controlErr}
	endpoint.call = func(gotCtx context.Context, call qa4ChannelCall) {
		assert.Same(t, ctx, gotCtx, "control must receive this authorized request's context")
		qa4RequireNoOrdinaryIntake(t, msgBus, "control endpoint")
		calls = append(calls, call)
		events = append(events, call.method)
	}
	owner.sendFn = func(gotCtx context.Context, reply bus.OutboundMessage) error {
		assert.Same(t, ctx, gotCtx, "reply must retain the authorized control context")
		qa4RequireNoOrdinaryIntake(t, msgBus, "outbound reply")
		events = append(events, "reply")
		assert.Equal(t, bus.OutboundMessage{ChatID: chatID, Content: row.wantReply}, reply, "channel must send the actual control outcome to the trusted chat")
		return row.sendErr
	}
	ch.SetCancelInterceptor(endpoint)

	rawSender := "spoofed-raw-sender"
	if row.denied {
		// Even an allowlisted raw fallback cannot override a denied structured
		// sender: authorization must inspect the supplied trusted identity first.
		rawSender = "123456"
		sender = bus.SenderInfo{Platform: platform, PlatformID: "999999", CanonicalID: platform + ":999999"}
		if row.legacySender {
			rawSender = "999999"
			sender = bus.SenderInfo{}
		}
	}
	metadata := map[string]string{"instance_id": "attacker.instance", "sender_id": "attacker:identity", "instruction": "discard the operator instruction"}
	err := ch.HandleMessage(ctx, peer, "qa4-command", rawSender, chatID, command, nil, metadata, sender)
	if row.denied {
		require.NoError(t, err)
		assert.Empty(t, calls, "authorization must happen BEFORE control; a denied sender must not stop or redirect work")
		assert.Empty(t, events, "denied sender must trigger neither control nor reply transport")
		assert.Empty(t, owner.sentMessages)
	} else {
		if row.sendErr == nil {
			assert.NoError(t, err)
		} else {
			assert.ErrorIs(t, err, row.sendErr, "failed control reply must be returned, not demoted to a log/success")
		}
		assert.Equal(t, []qa4ChannelCall{{method: method, channel: channelName, chatID: chatID, userID: canonical, instruction: instruction}}, calls,
			"exactly the intended control must use trusted instance/chat/canonical sender and exact instruction, never spoofed metadata or raw fallback")
		assert.Equal(t, []string{method, "reply"}, events, "the control must run exactly once BEFORE its reply, and before any ordinary intake")
		assert.Len(t, owner.sentMessages, 1)
		if row.controlErr != nil {
			assert.NotEqual(t, commands.StopCommandReply(nil), row.wantReply, "refused control may not emit a Stop-success reply")
			assert.NotEqual(t, commands.StopRedirectReply(nil), row.wantReply, "refused control may not emit a redirect-success reply")
		}
	}
	qa4RequireNoOrdinaryIntake(t, msgBus, "HandleMessage returned")
	assert.Equal(t, 1, owner.placeholdersSent, "only the positive normal-text control may send a placeholder; session control must be intercepted before intake effects")
}
