package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// revokingApprover approves, but only after running revoke - the change made
// while the human's approval was pending.
type revokingApprover struct{ revoke func() }

func (r revokingApprover) RequestApproval(context.Context, PolicyApprovalReq) (bool, string, bool) {
	r.revoke()
	return true, "", false
}

// F1: authorization revoked while an Ask approval is pending must refuse at
// admission, with nothing captured, appended, created or published.
func TestSendPeer_AuthorizationRevokedDuringAskRefusesWithNothingWritten(t *testing.T) {
	cases := map[string]func(f *addrFixture){
		"membership removed": func(f *addrFixture) { f.deps.mu.Lock(); f.deps.eligible = map[string]bool{}; f.deps.mu.Unlock() },
		"policy became deny": func(f *addrFixture) { setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyDeny) },
	}
	for name, revoke := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAddrFixture(t)
			setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyAsk)
			f.al.SetToolApprover(revokingApprover{revoke: func() { revoke(f) }})
			if _, err := f.r.SendPeer(context.Background(), f.peerReq()); err == nil {
				t.Fatal("an approval granted for a since-revoked authorization must not admit the request")
			}
			if _, ok := addrDrainInbound(t, f.bus); ok {
				t.Fatal("a turn was started for a revoked request")
			}
			mainID, _ := session.MainSessionID(addrWS, addrReceiver)
			if _, err := f.al.GetSessionStore().GetMeta(mainID); err == nil {
				t.Fatal("the receiver's main was created for a revoked request")
			}
		})
	}
}

// Positive control: the same Ask, approved with nothing revoked, admits.
func TestSendPeer_AskApprovedAndStillAuthorizedAdmits(t *testing.T) {
	f := newAddrFixture(t)
	setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyAsk)
	f.al.SetToolApprover(revokingApprover{revoke: func() {}})
	if _, err := f.r.SendPeer(context.Background(), f.peerReq()); err != nil {
		t.Fatalf("an approved, still-authorized request must admit: %v", err)
	}
}
