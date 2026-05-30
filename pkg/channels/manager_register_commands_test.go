// Package channels — manager_register_commands_test.go
//
// Tests that StartAll invokes RegisterCommands on every started channel that
// implements CommandRegistrarCapable (C1 fix — resolves architect review finding C1).

package channels

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// registrarChannel is a test double that implements CommandRegistrarCapable.
// It records every RegisterCommands call so the test can assert it was invoked.
type registrarChannel struct {
	mockChannel

	mu    sync.Mutex
	calls [][]commands.Definition
}

func (r *registrarChannel) RegisterCommands(_ context.Context, defs []commands.Definition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, defs)
	return nil
}

func (r *registrarChannel) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// Compile-time assertion: registrarChannel implements both Channel and
// CommandRegistrarCapable.
var (
	_ Channel                 = (*registrarChannel)(nil)
	_ CommandRegistrarCapable = (*registrarChannel)(nil)
)

// TestStartAll_InvokesRegisterCommands verifies that StartAll calls
// RegisterCommands on channels implementing CommandRegistrarCapable within
// 1s (C1 fix, FR-28 fallback: failure is WARN-logged but startup continues).
func TestStartAll_InvokesRegisterCommands(t *testing.T) {
	t.Parallel()

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	m, err := NewManager(&config.Config{}, credentials.SecretBundle{}, msgBus, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ch := &registrarChannel{
		mockChannel: mockChannel{
			sendFn: func(_ context.Context, _ bus.OutboundMessage) error { return nil },
		},
	}

	// Register the channel directly so StartAll picks it up.
	m.channels["test-register"] = ch

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	t.Cleanup(func() { m.StopAll(context.Background()) }) //nolint:errcheck

	// RegisterCommands is called in a goroutine with a 30s timeout; give it
	// up to 1s to fire (should be near-instant in tests).
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if ch.callCount() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if ch.callCount() == 0 {
		t.Fatal("RegisterCommands was not called within 1s of StartAll")
	}

	ch.mu.Lock()
	lastDefs := ch.calls[0]
	ch.mu.Unlock()

	if len(lastDefs) == 0 {
		t.Error("RegisterCommands was called with an empty definition list; expected BuiltinDefinitions()")
	}
}

// TestStartAll_RegisterCommands_NotCalledOnNonRegistrar confirms that channels
// without CommandRegistrarCapable are not affected (no panic or unexpected call).
func TestStartAll_RegisterCommands_NotCalledOnNonRegistrar(t *testing.T) {
	t.Parallel()

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	m, err := NewManager(&config.Config{}, credentials.SecretBundle{}, msgBus, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Plain mockChannel does NOT implement CommandRegistrarCapable.
	plain := &mockChannel{
		sendFn: func(_ context.Context, _ bus.OutboundMessage) error { return nil },
	}
	m.channels["test-plain"] = plain

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	t.Cleanup(func() { m.StopAll(context.Background()) }) //nolint:errcheck

	// Brief pause to make sure no panic occurs.
	time.Sleep(50 * time.Millisecond)
	// Test passes if we got here without panicking.
}
