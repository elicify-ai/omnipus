package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// Stop must interrupt pending navigation rather than wait behind it; pointer
// and key events must not acquire navigation cancellation semantics.
func TestBrowserStopLoadingInterruptsQueuedNavigation(t *testing.T) {
	wc, state := newTabActionTestFixtures(t)
	h := &BrowserWSHandler{}
	entered, canceled := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { state.commands.close(); h.activeConns.Wait() })
	require.True(t, state.commands.submit(&h.activeConns, browserCommand{navigation: true, run: func(ctx context.Context) { close(entered); <-ctx.Done(); close(canceled) }}))
	<-entered
	h.dispatchBrowserCommand(wc, state, "viewer", "user", []byte(`{"type":"browser_input","kind":"stop_loading"}`), string(generated.WsFrameTypeBrowserInput), &config.Config{})
	select {
	case <-canceled:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Stop loading waited behind the navigation it must cancel")
	}
}

func TestBrowserStopLoadingUsesDiscreteCommandErrors(t *testing.T) {
	for _, kind := range []string{"navigate", "navigate_back", "reload", "stop_loading"} {
		require.True(t, inputKindIsDiscrete(kind), "%s must surface each refused navigation command", kind)
	}
	for _, kind := range []string{"mouse_move", "mouse_down", "mouse_up", "wheel", "key_down", "key_up", "text", "unknown"} {
		require.False(t, inputKindIsDiscrete(kind), "%s must not acquire navigation semantics", kind)
	}
}

func TestBrowserStopLoadingInboundSchema(t *testing.T) {
	message, serverError := ValidateInboundFrameJSON("BrowserInputFrame", []byte(`{"type":"browser_input","kind":"stop_loading"}`))
	require.Empty(t, message, "the enabled inbound validator must admit Stop loading")
	require.False(t, serverError)
	message, _ = ValidateInboundFrameJSON("BrowserInputFrame", []byte(`{"type":"browser_input","kind":"stop_unknown"}`))
	require.NotEmpty(t, message, "adding Stop must not admit unknown commands")
}
