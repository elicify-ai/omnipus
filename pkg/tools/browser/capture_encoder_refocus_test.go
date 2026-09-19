package browser

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/chromedp/cdproto/cdp"
	"github.com/stretchr/testify/require"
)

// recordingExecutor records every CDP method it is asked to execute, so the
// tests can assert on the exact command the refocus helper issues. It never
// touches a real browser.
type recordingExecutor struct {
	methods []string
	params  []any
	err     error
}

func (e *recordingExecutor) Execute(ctx context.Context, method string, params any, reply any) error {
	e.methods = append(e.methods, method)
	e.params = append(e.params, params)
	return e.err
}

func TestRefocusCapturedTabBeforeEncoderLoad_ActivatesCapturedTarget(t *testing.T) {
	exec := &recordingExecutor{}
	ctx := cdp.WithExecutor(context.Background(), exec)

	require.NoError(t, refocusCapturedTabBeforeEncoderLoad(ctx, "page-abc"))

	require.Equal(t, []string{"Target.activateTarget"}, exec.methods)
	require.Len(t, exec.params, 1)
	b, err := json.Marshal(exec.params[0])
	require.NoError(t, err)
	require.JSONEq(t, `{"targetId":"page-abc"}`, string(b))
}

// An empty target identity is a deliberate no-op: the refocus is wired in for
// every capture, and a frame with no target (not yet measured) must not send
// Target.activateTarget with an empty id at Chrome.
func TestRefocusCapturedTabBeforeEncoderLoad_EmptyIDIsNoOp(t *testing.T) {
	exec := &recordingExecutor{}
	ctx := cdp.WithExecutor(context.Background(), exec)

	require.NoError(t, refocusCapturedTabBeforeEncoderLoad(ctx, ""))

	require.Empty(t, exec.methods)
}

// The helper SURFACES the activate failure to its caller; the production call
// site decides to log-and-continue. Swallowing it here would make the
// Chrome-153 freeze workaround silently absent with no trace anywhere.
func TestRefocusCapturedTabBeforeEncoderLoad_ErrorSurfacesToCaller(t *testing.T) {
	boom := errors.New("activate refused")
	exec := &recordingExecutor{err: boom}
	ctx := cdp.WithExecutor(context.Background(), exec)

	err := refocusCapturedTabBeforeEncoderLoad(ctx, "page-abc")

	require.ErrorIs(t, err, boom)
}
