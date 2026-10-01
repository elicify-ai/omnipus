package commands_test

// cmd_stop_channel_seam_test.go — RED wave (qa-lead,
// test/a-redirect-transport-red): the channel-side redirect seam pin.
//
// EXTERNAL test package on purpose: pkg/channels imports pkg/commands
// (pkg/channels/interfaces.go), so an in-package test file (package commands)
// cannot import pkg/channels — "import cycle not allowed in test". An external
// test package (commands_test) may. This file holds ONLY the channel-seam
// test; the corrected command pack itself stays in package commands
// (cmd_stop_test.go).

import (
	"context"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// TestRedirectTransport_ChannelInterceptorSiblingPresent: Tier B channels
// must gain a redirect interception sibling of the cancel interceptor so
// channel /stop-redirect is intercepted BEFORE intake (§3.1 channel row:
// "adapter-level interception sibling of DispatchCancelIfRecognized … calling
// the same redirect primitive with (channelName, chatID, userID)"). Asserted
// reflectively: the METHOD NAME and its parameter shape are pinned; the return
// signature is deliberately NOT pinned (the seam map fixes the seam, not the
// signature — over-pinning an uninvented signature would over-constrain GREEN).
func TestRedirectTransport_ChannelInterceptorSiblingPresent(t *testing.T) {
	chanIface := reflect.TypeOf((*channels.CancelInterceptor)(nil)).Elem()

	sibling, ok := chanIface.MethodByName("RequestRedirectByChannelChat")
	if !ok {
		t.Fatalf("BLOCKED: channels.CancelInterceptor has no redirect sibling method — required by §3.1 channel row (adapter-level interception before intake, sibling of DispatchCancelIfRecognized; proposed name RequestRedirectByChannelChat mirroring RequestCancelByChannelChat, carrying channelName, chatID, userID and the instruction)")
	}
	// Parameters: context first, then primitive strings (the channel
	// resolution triple + the instruction). No pkg/agent types may leak in
	// (the interface exists to avoid that import cycle — cancelparse.go doc).
	if sibling.Type.NumIn() < 5 {
		t.Errorf("RequestRedirectByChannelChat has %d params, want at least 5 (ctx + channelName + chatID + userID + instruction)", sibling.Type.NumIn())
	}
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	if sibling.Type.NumIn() > 0 && sibling.Type.In(0) != ctxType {
		t.Errorf("RequestRedirectByChannelChat first param = %v, want context.Context", sibling.Type.In(0))
	}
	for i := 1; i < sibling.Type.NumIn(); i++ {
		if sibling.Type.In(i).Kind() != reflect.String {
			t.Errorf("RequestRedirectByChannelChat param %d = %v, want a primitive string (no pkg/agent types across this seam)", i, sibling.Type.In(i))
		}
	}
}
