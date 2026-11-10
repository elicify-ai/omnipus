package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Test-only convenience wrappers (S1 final-gate M17). Each used to sit in a
// production file although no production code calls it: the tests drove a path
// production never takes (for example completeSteeredTurn derives its own
// execution claim, where production always passes the turn's own). They live
// here, in a _test file, so they cannot be mistaken for - or reached from - the
// real code.

func (al *AgentLoop) completeSteeredTurn(ctx context.Context, snapshot *session.LifecycleRecord, result turnResult, runErr error) error {
	// A synthetic completion retains its selected record's identity. A
	// caller holding the producing turn uses its immutable handle instead.
	_, err := al.completeSteeredTurnDurably(ctx, snapshot, result, runErr, al.executionClaimFor(snapshot))
	return err
}

func (al *AgentLoop) deliverSteeredCompletion(ctx context.Context, rec *session.LifecycleRecord, outcome steer.Outcome, nextState session.LifecycleState, answer, failureReason string) (bool, error) {
	// Direct callers carry their selected record, not a later registry lookup.
	return al.deliverSteeredCompletionForExecution(ctx, rec, outcome, nextState, answer, failureReason, al.executionClaimFor(rec))
}

func (te *TaskExecutor) startTaskNowViaLauncher(ctx context.Context, t *task.Task) (string, error) {
	return te.startTaskNowViaLauncherAs(ctx, t, nil)
}

func (te *TaskExecutor) mintTaskLifecycleRecord(sessionID string, t *task.Task) error {
	return te.mintTaskLifecycleRecordFor(sessionID, t, nil)
}

func (al *AgentLoop) admitBoundConnectorInput(msg *bus.InboundMessage) (refusal string, err error) {
	return al.admitBoundConnectorInputCtx(context.Background(), msg)
}

func (ts *turnState) writeErrorTranscriptWithAbandonment(
	kind, stage, message string,
	code LLMErrorCode,
	allowAbandoned bool,
	pe ...*ProviderError,
) {
	ts.writeErrorTranscriptCore(kind, stage, message, code, allowAbandoned, nil, pe...)
}

func resolveMediaRefs(
	messages []providers.Message,
	store media.MediaStore,
	maxSize int,
	model string,
) []providers.Message {
	return resolveMediaRefsWithOffload(messages, store, maxSize, "", model, nil, nil, nil, "")
}
