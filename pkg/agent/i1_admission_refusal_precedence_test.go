package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/require"
)

// The caller's deadline can end the predecessor wait, but cannot disguise an
// owner that is still pending. Explicit cancellation still wins, and an expired
// caller with no predecessor must never receive a fresh execution.
func TestI1AdmissionRefusalPrecedenceKeepsExecution(t *testing.T) {
	for _, name := range []string{"pending_deadline", "pending_cancelled", "settled_deadline"} {
		t.Run(name, func(t *testing.T) {
			al, _, sid, _ := newAdmissionSessionsLoop(t, newGatedProvider())
			opts := processOptions{SessionKey: "agent:mia:main", TranscriptStore: al.GetSessionStore()}
			msg := bus.InboundMessage{Channel: "webchat", SessionID: sid,
				Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel", UserInitiated: true}
			first, err := al.prepareOrdinaryExecution(context.Background(), msg, opts)
			require.NoError(t, err)
			require.NotNil(t, first.execution)
			settled := name == "settled_deadline"
			if settled {
				require.NoError(t, al.finishExecutionDisposition(first.execution))
			} else {
				t.Cleanup(func() { require.NoError(t, al.finishExecutionDisposition(first.execution)) })
			}
			store := al.GetSessionLifecycleStore()
			before, err := store.Load(sid)
			require.NoError(t, err)
			path := filepath.Join(store.Dir(), sid+".jsonl")
			journal, err := os.ReadFile(path)
			require.NoError(t, err)
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want := ErrPreviousExecutionPending
			if name == "pending_cancelled" {
				cancel()
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
				want = context.Canceled
			} else if settled {
				want = context.DeadlineExceeded
			}
			defer cancel()
			refused, err := al.prepareOrdinaryExecution(ctx, msg, opts)
			require.ErrorIs(t, err, want)
			if errors.Is(want, ErrPreviousExecutionPending) {
				require.NotErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.NotErrorIs(t, err, ErrPreviousExecutionPending)
			}
			require.Nil(t, refused.execution, "a refused input cannot own an execution")
			after, err := store.Load(sid)
			require.NoError(t, err)
			require.Equal(t, before, after, "refusal cannot mint another generation or run")
			afterJournal, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, journal, afterJournal, "refusal cannot append another execution")
		})
	}
}
