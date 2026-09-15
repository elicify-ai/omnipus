package gateway

import (
	"context"
	"sync/atomic"
)

type browserInputControlResultKey struct{}

// runBrowserInputControl requires an explicit success from the actual handler;
// an early return or reported refusal must never unlock the input channel.
func runBrowserInputControl(ctx context.Context, run func(context.Context)) bool {
	var success atomic.Bool
	run(context.WithValue(ctx, browserInputControlResultKey{}, &success))
	return ctx.Err() == nil && success.Load()
}
func markBrowserInputControlSuccess(ctx context.Context) {
	if result, ok := ctx.Value(browserInputControlResultKey{}).(*atomic.Bool); ok {
		result.Store(true)
	}
}
