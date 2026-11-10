package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// U8 security F9 + F1 remainder, at the router. The receiver's send_message
// policy read at preflight and the one read at admission are two decisions: a
// policy that became Ask in between needs the existing approval; an Ask that was
// already approved is not asked twice; and an approval that returns for a
// since-revoked authorization refuses before the caller saves anything.

// hookDeps runs onEligible on every membership read, so a test can change the
// receiver's policy at an exact point between the two decisions.
type hookDeps struct {
	*fakeAddressDeps
	calls      atomic.Int32
	onEligible func(n int32)
}

func (h *hookDeps) PairEligible(ws, agentID string) (bool, error) {
	n := h.calls.Add(1)
	if h.onEligible != nil {
		h.onEligible(n)
	}
	return h.fakeAddressDeps.PairEligible(ws, agentID)
}

type f9Approver struct {
	approve bool
	calls   atomic.Int32
}

func (c *f9Approver) RequestApproval(context.Context, PolicyApprovalReq) (bool, string, bool) {
	c.calls.Add(1)
	return c.approve, "", false
}

func admissionFor(f *addrFixture, askApproved bool) RequestAdmission {
	return RequestAdmission{
		Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, Content: "please review X",
		Sender: addressing.Sender{Principal: "p"}, SenderLabel: "a person",
		Source:      addressing.Source{Kind: addressing.SourceConversation, SessionID: "src-session"},
		AskApproved: askApproved,
	}
}

func receiverMainExists(f *addrFixture) bool {
	mainID, _ := session.MainSessionID(addrWS, addrReceiver)
	_, err := f.al.GetSessionStore().GetMeta(mainID)
	return err == nil
}

func TestAdmitRequest_PolicyBecameAskAfterPreflight_NeedsApproval(t *testing.T) {
	for _, tc := range []struct {
		name     string
		approve  bool
		wantErr  bool
		wantCall int32
	}{
		{"denied approval refuses with nothing written", false, true, 1},
		{"granted approval admits", true, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAddrFixture(t)
			ap := &f9Approver{approve: tc.approve}
			f.al.SetToolApprover(ap)
			hd := &hookDeps{fakeAddressDeps: f.deps}
			f.al.SetAddressDeps(hd)
			setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyAllow)
			// Preflight sees Allow.
			if err := f.al.CheckPeerAdmission(context.Background(), addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, "src-session"); err != nil {
				t.Fatal(err)
			}
			if ap.calls.Load() != 0 {
				t.Fatal("SETUP: an Allow preflight must not ask")
			}
			// The policy is tightened before admission reads it.
			hd.onEligible = func(int32) { setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyAsk) }

			_, _, err := f.al.AdmitRequest(context.Background(), admissionFor(f, false))

			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if ap.calls.Load() != tc.wantCall {
				t.Fatalf("approval requested %d times, want %d: the newly required Ask must go through approval", ap.calls.Load(), tc.wantCall)
			}
			if tc.wantErr && receiverMainExists(f) {
				t.Fatal("the receiver's main was created for a request that was not approved")
			}
		})
	}
}

// Controls: an unchanged Allow admits without asking; an Ask already approved at
// preflight is not asked a second time.
func TestAdmitRequest_UnchangedAllowAndAlreadyApprovedAskAdmitWithoutAskingAgain(t *testing.T) {
	f := newAddrFixture(t)
	ap := &f9Approver{approve: false}
	f.al.SetToolApprover(ap)
	if _, _, err := f.al.AdmitRequest(context.Background(), admissionFor(f, false)); err != nil {
		t.Fatalf("unchanged Allow must admit: %v", err)
	}
	if ap.calls.Load() != 0 {
		t.Fatal("an unchanged Allow must never ask")
	}

	g := newAddrFixture(t)
	ap2 := &f9Approver{approve: false} // would refuse if asked again
	g.al.SetToolApprover(ap2)
	setSendMessagePolicy(t, g.al, addrReceiver, config.ToolPolicyAsk)
	if _, _, err := g.al.AdmitRequest(context.Background(), admissionFor(g, true)); err != nil {
		t.Fatalf("an Ask already approved for this request must admit: %v", err)
	}
	if ap2.calls.Load() != 0 {
		t.Fatal("an already-approved Ask must not be asked again")
	}
}

// F1 remainder: CheckPeerAdmission is what the WebSocket path runs BEFORE it saves
// the source message. An approval that returns for a since-revoked authorization
// must refuse there, not only at the later admission.
func TestCheckPeerAdmission_RevocationDuringAskRefusesBeforeAnySave(t *testing.T) {
	cases := map[string]func(f *addrFixture){
		"membership removed": func(f *addrFixture) { f.deps.mu.Lock(); f.deps.eligible = map[string]bool{}; f.deps.mu.Unlock() },
		"policy became deny": func(f *addrFixture) { setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyDeny) },
	}
	for name, revoke := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAddrFixture(t)
			setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyAsk)
			f.al.SetToolApprover(revokingApprover{revoke: func() { revoke(f) }})
			err := f.al.CheckPeerAdmission(context.Background(), addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, "src-session")
			if err == nil {
				t.Fatal("an approval returned for a since-revoked authorization must refuse at the preflight itself")
			}
		})
	}
	// Control: approved and still authorized passes.
	f := newAddrFixture(t)
	setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyAsk)
	f.al.SetToolApprover(revokingApprover{revoke: func() {}})
	if err := f.al.CheckPeerAdmission(context.Background(), addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, "src-session"); err != nil {
		t.Fatalf("approved and still authorized must pass: %v", err)
	}
}

// U8 security r3 F11: Allow at preflight -> Ask at admission -> the person's
// approval is pending -> the receiver is revoked (membership removed, or Deny)
// -> approve. The approval must not commit: authorization is read again after
// the approval returns, before any receiver write.
func TestAdmitRequest_RevocationDuringNewlyRequiredAsk_WritesNothing(t *testing.T) {
	cases := map[string]func(f *addrFixture){
		"membership removed": func(f *addrFixture) { f.deps.mu.Lock(); f.deps.eligible = map[string]bool{}; f.deps.mu.Unlock() },
		"policy became deny": func(f *addrFixture) { setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyDeny) },
	}
	for name, revoke := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAddrFixture(t)
			hd := &hookDeps{fakeAddressDeps: f.deps}
			f.al.SetAddressDeps(hd)
			setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyAllow)
			// Preflight (Allow) passes; the policy tightens at the admission read.
			if err := f.al.CheckPeerAdmission(context.Background(), addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, "src-session"); err != nil {
				t.Fatal(err)
			}
			var tightened atomic.Bool
			hd.onEligible = func(int32) {
				if tightened.CompareAndSwap(false, true) { // one-shot: only the admission read
					setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyAsk)
				}
			}
			// The approval comes back approved, but only after the revoke ran.
			f.al.SetToolApprover(revokingApprover{revoke: func() { revoke(f) }})

			_, _, err := f.al.AdmitRequest(context.Background(), admissionFor(f, false))

			if err == nil {
				t.Fatal("an approval returned for a since-revoked authorization must refuse at commit")
			}
			if receiverMainExists(f) {
				t.Fatal("the receiver's main was created under revoked authorization")
			}
			if _, ok := addrDrainInbound(t, f.bus); ok {
				t.Fatal("a request was published under revoked authorization")
			}
		})
	}
}
