package agent

import (
	"fmt"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func stoppedHelperEvent(rec *session.LifecycleRecord, tr session.StoppedTransition) generated.HelperEvent {
	event := generated.HelperEvent{HelperSessionId: rec.SessionID, CreatedAt: tr.At.UTC()}
	switch tr.Cause {
	case session.StopCauseRestart:
		event.Kind = generated.HelperEventKindRestarted
		event.Text = "Interrupted by a restart."
		if rec.Is3P {
			event.Text += " The external helper was not resumed."
		}
	case session.StopCauseTimeout:
		event.Kind = generated.HelperEventKindTimedOut
		event.Text = "The helper stopped because an old time limit was hit. It was not resumed."
	default:
		event.Kind = generated.HelperEventKindStopped
		event.Text = fmt.Sprintf("The helper was stopped by %s. It will not resume on its own.", tr.Actor)
	}
	return event
}
