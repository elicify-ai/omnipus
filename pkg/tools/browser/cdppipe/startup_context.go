package cdppipe

import "context"

// NewPipeAllocatorWithStartupContext separates handshake cancellation from the
// returned browser lifetime owned by parent.
func NewPipeAllocatorWithStartupContext(parent, startup context.Context, execPath string, opts PipeOptions) (context.Context, context.CancelFunc, error) {
	return newPipeAllocator(parent, startup, execPath, opts)
}
