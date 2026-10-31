package common

import (
	"context"
	"time"
)

// StreamActivityFunc observes a streaming call's activity (issue #1478). The
// silence monitor calls it once when the stream starts and on every consumed
// event, with the silence limit that would end the call, so the agent loop
// can tell the person the model has gone quiet without changing that limit.
type StreamActivityFunc func(stallLimit time.Duration)

type streamActivityKey struct{}

// WithStreamActivity attaches fn to ctx for the provider calls made under it.
func WithStreamActivity(ctx context.Context, fn StreamActivityFunc) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, streamActivityKey{}, fn)
}

func streamActivityFrom(ctx context.Context) StreamActivityFunc {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(streamActivityKey{}).(StreamActivityFunc)
	return fn
}
