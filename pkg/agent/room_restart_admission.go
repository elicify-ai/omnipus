package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func authenticatedHumanRoomSender(sender addressing.Sender) bool {
	return sender.Principal != "" && sender.Agent.IsZero()
}

// roomRestartPrincipal consults the committed server-owned capture, not wake text.
func (al *AgentLoop) roomRestartPrincipal(msg bus.InboundMessage) (steer.Principal, bool) {
	if !isRoomRequestWake(msg) || msg.TranscriptEntryID == "" || al.RequestLedger() == nil {
		return steer.Principal{}, false
	}
	c, found, err := al.RequestLedger().Get(msg.SessionID, msg.TranscriptEntryID)
	if err != nil || !found || c.Discarded || c.Delivered || c.Source.Kind != addressing.SourceRoom ||
		c.HumanRestartBoot == 0 || c.HumanRestartBoot != al.bootEpochFor() || !authenticatedHumanRoomSender(c.Sender) {
		return steer.Principal{}, false
	}
	lc := al.GetSessionLifecycleStore()
	if lc == nil {
		return steer.Principal{}, false
	}
	rec, err := lc.Load(msg.SessionID)
	if err != nil || !restartStoppedStandingRoot(rec) {
		return steer.Principal{}, false
	}
	intents, err := lc.UnfinishedStopIntents(msg.SessionID)
	if err != nil || acceptedStopSelectsExecution(rec, intents) {
		return steer.Principal{}, false
	}
	return steer.Principal{Kind: steer.PrincipalKindHuman, ID: c.Sender.Principal}, true
}

func (al *AgentLoop) newHumanRoomCanRestart(a RoomRequestAdmission, mainID string) bool {
	if !authenticatedHumanRoomSender(a.Sender) || al.bootEpochFor() == 0 {
		return false
	}
	lc := al.GetSessionLifecycleStore()
	if lc == nil {
		return false
	}
	rec, err := lc.Load(mainID)
	if err != nil || !restartStoppedStandingRoot(rec) {
		return false
	}
	intents, err := lc.UnfinishedStopIntents(mainID)
	return err == nil && !acceptedStopSelectsExecution(rec, intents)
}

func roomRestartContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, restartWakeRevivalKey{}, true)
}
