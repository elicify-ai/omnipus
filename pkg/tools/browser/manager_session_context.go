package browser

import (
	"context"
	"fmt"
	"time"
)

// SessionContext permits cold creation within the caller's operation lifetime.
// Queued tab commands join startup only after admission; accepted targets retain
// their own lifetime after this call returns.
func (m *BrowserManager) SessionContext(caller context.Context, sessionID string) (context.Context, error) {
	release, err := m.acquireLiveTabCommand(caller, sessionID)
	if err != nil {
		return nil, err
	}
	defer release()
	return m.sessionUnderGate(caller, sessionID)
}

// The caller already owns the tab-command gate. Both legacy and contextual
// Session calls inherit its retirement without reacquiring admission.
func (m *BrowserManager) sessionUnderGate(caller context.Context, sessionID string) (context.Context, error) {
	m.mu.Lock()
	gate := m.tabCommands[sessionID]
	// Only session resolution needs this lifetime; mouse/key admission does not
	// allocate an unused cancellation context for every interactive command.
	if gate.lifetime == nil && !gate.retired {
		gate.lifetime, gate.stop = context.WithCancelCause(context.Background())
	}
	lifetime := gate.lifetime
	retired := gate.retired
	m.mu.Unlock()
	if retired {
		return nil, errBrowserSessionChanged
	}
	ctx, stop := sessionStartupContext(caller, lifetime)
	defer stop()
	return m.sessionWithContext(ctx, sessionID)
}

func sessionStartupContext(caller, lifetime context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(caller)
	stop := context.AfterFunc(lifetime, func() { cancel(context.Cause(lifetime)) })
	if err := context.Cause(lifetime); err != nil {
		cancel(err)
	}
	return &sessionStartupRequest{Context: ctx, caller: caller, lifetime: lifetime}, func() { stop(); cancel(context.Canceled) }
}

// Err reads the original gate directly: AfterFunc delivery can lag retirement.
// Done still provides the normal wakeup, and accepted targets never retain this
// operation context as their parent.
type sessionStartupRequest struct {
	context.Context
	caller   context.Context
	lifetime context.Context
}

func (c *sessionStartupRequest) Err() error {
	if err := c.caller.Err(); err != nil {
		return err
	}
	if c.lifetime.Err() != nil {
		return context.Canceled
	}
	return c.Context.Err()
}
func sessionStartupError(ctx context.Context) error {
	if c, ok := ctx.(*sessionStartupRequest); ok {
		if err := context.Cause(c.caller); err != nil {
			return err
		}
		if err := context.Cause(c.lifetime); err != nil {
			return err
		}
	}
	return context.Cause(ctx)
}

func runFirstAttachContext(ctx context.Context, fn func() error, timeout time.Duration) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- fn() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if callerErr := context.Cause(ctx); callerErr != nil {
			return callerErr
		}
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return fmt.Errorf("browser: timed out after %s waiting for the browser to attach the tab (target may be unresponsive)", timeout)
	}
}

// bootstrapBrowserCtxContext links cancellation only until the new browser
// context is accepted. Its returned cleanup owns the temporary parent scope.
func (m *BrowserManager) bootstrapBrowserCtxContext(caller, parent context.Context) (context.Context, context.CancelFunc, error) {
	if err := sessionStartupError(caller); err != nil {
		return nil, nil, err
	}
	// Fake tab factories have no allocator; production always has a parent.
	if parent == nil {
		parent = context.Background()
	}
	scope, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(caller, cancel)
	ctx, release, err := m.bootstrapBrowserCtx(scope)
	stop()
	if callerErr := sessionStartupError(caller); callerErr != nil {
		err = callerErr
	}
	if err != nil {
		cancel()
		if release != nil {
			release()
		}
		return nil, nil, err
	}
	return ctx, func() { cancel(); release() }, nil
}
