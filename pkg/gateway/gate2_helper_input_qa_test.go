package gateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// Architect A8 / UAT S3b. Oracle: steering-command ADR C1 includes the signed-in
// human typing in an existing helper's chat. This is input to the real helper,
// not permission to mint a standalone worker chat. C2 retains authentication
// and ordinary session authority. Worker profile plus session_id is the actual
// frame shape offered by the helper pane (architect report A8).
func TestQAGate2HumanHelperInput_OwnerWorkerProfileSteersExistingHelper(t *testing.T) {
	f := newQA2HelperInputFixture(t)
	conn := qa2AuthenticatedHelperSocket(t, f, "qa2-owner")
	const instruction = "Use the updated figures in this existing helper."
	const clientID = "qa2-a8-owner-input"
	accepted, refusal := qa2SendHelperInput(t, conn, f.child.SessionID, "hans", instruction, clientID)
	if !accepted {
		t.Fatalf("A8: authenticated OWNER input to existing worker helper was refused: %q; want durable ordinary steering acceptance, not the standalone-worker guard", refusal)
	}
	store := f.loop.GetSessionStore()
	entries := qa2ReopenHelperTranscript(t, store, f.child.SessionID)
	matched := 0
	for _, entry := range entries {
		if entry.Role != "user" || entry.ClientMessageID != clientID {
			continue
		}
		matched++
		if entry.Content != instruction || entry.AgentID != "hans" {
			t.Errorf("accepted helper input = %+v, want exact text under worker hans", entry)
		}
		provenance, found, err := store.LookupMessageProvenance(f.child.SessionID, entry.ID)
		if err != nil || !found || provenance.Principal != "qa2-owner" || provenance.Content != instruction {
			t.Errorf("accepted owner provenance = %+v/%v/found=%v, want exact authenticated owner and text", provenance, err, found)
		}
	}
	if matched != 1 {
		t.Fatalf("A8: durable existing-helper owner input count=%d, want exactly one", matched)
	}
	// A message to a working helper continues that producing execution; it
	// must not stop it, mint another admission, or reroute to a chat persona.
	f.p.open(0)
	qa2AwaitHelperProvider(t, f.p, 1)
	requests := f.p.snapshot()
	seen := 0
	for _, message := range requests[1] {
		if message.Role == "user" && message.Content == instruction {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("A8: real continuing helper model input contains exact owner text %d times, want once (never a fake bus-only delivery)", seen)
	}
	got, err := session.NewLifecycleStore(f.loop.GetSessionLifecycleStore().Dir()).Load(f.child.SessionID)
	if err != nil || got.State != session.LifecycleRunning || got.Stop != nil || got.StopNote != nil || got.Generation != f.child.Generation || !reflect.DeepEqual(got.ExecutionID, f.child.ExecutionID) {
		t.Errorf("A8: existing helper changed producer on a plain message: %+v/%v; want the original running execution, no Stop", got, err)
	}
}

func TestQAGate2HumanHelperInput_NonOwnerCannotSteerExistingWorker(t *testing.T) {
	for _, agentID := range []string{"hans", ""} {
		name := "explicit_worker_profile"
		if agentID == "" {
			name = "omitted_agent_cannot_bypass_owner_authority"
		}
		t.Run(name, func(t *testing.T) {
			f := newQA2HelperInputFixture(t)
			conn := qa2AuthenticatedHelperSocket(t, f, "qa2-other")
			const instruction = "An unrelated account must not steer this helper."
			accepted, refusal := qa2SendHelperInput(t, conn, f.child.SessionID, agentID, instruction, "qa2-a8-other-input")
			if accepted || refusal == "" {
				t.Errorf("A8 authority: authenticated NON-owner disposition = accepted:%v refusal:%q, want explicit refusal", accepted, refusal)
			}
			for _, entry := range qa2ReopenHelperTranscript(t, f.loop.GetSessionStore(), f.child.SessionID) {
				if entry.ClientMessageID == "qa2-a8-other-input" || (entry.Role == "user" && entry.Content == instruction) {
					t.Errorf("A8 authority: non-owner input was saved in another account's helper: %+v; want no write/admission", entry)
				}
			}
			if calls := len(f.p.snapshot()); calls != 1 {
				t.Errorf("A8 authority: non-owner caused %d provider admissions, want only the original helper", calls)
			}
		})
	}
}

func TestQAGate2HumanHelperInput_EmptySessionStillCannotMintStandaloneWorkerChat(t *testing.T) {
	f := newQA2HelperInputFixture(t)
	conn := qa2AuthenticatedHelperSocket(t, f, "qa2-owner")
	before, err := f.loop.GetSessionStore().ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	accepted, refusal := qa2SendHelperInput(t, conn, "", "hans", "Minting standalone worker chat must stay forbidden.", "qa2-a8-standalone")
	if accepted || !strings.Contains(strings.ToLower(refusal), "worker") {
		t.Errorf("A8 standalone-worker negative control = accepted:%v refusal:%q, want named worker refusal", accepted, refusal)
	}
	after, err := f.loop.GetSessionStore().ListSessions()
	if err != nil || len(after) != len(before) {
		t.Errorf("A8 standalone-worker refusal minted a session: before=%d after=%d err=%v, want no new session", len(before), len(after), err)
	}
	if calls := len(f.p.snapshot()); calls != 1 {
		t.Errorf("A8 standalone-worker refusal admitted work: calls=%d, want only the existing helper's initial turn", calls)
	}
}

func qa2ReopenHelperTranscript(t *testing.T, store *session.UnifiedStore, id string) []session.TranscriptEntry {
	t.Helper()
	fresh, err := session.NewUnifiedStore(store.BaseDir())
	if err != nil {
		t.Fatalf("reopen real helper conversation store: %v", err)
	}
	defer func() {
		if err := fresh.Close(); err != nil {
			t.Errorf("close reopened helper conversation store: %v", err)
		}
	}()
	entries, err := fresh.ReadTranscript(id)
	if err != nil {
		t.Fatalf("read reopened helper transcript: %v", err)
	}
	return entries
}
