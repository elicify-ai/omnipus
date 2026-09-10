package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Keep the actual admission path and retained attachment request. Only stop the
// queue worker from starting so the test chooses the execution context exactly.
func admittedDedicatedRelease(t *testing.T, f handlerContextFixture) browserCommand {
	t.Helper()
	f.state.setDedicatedInput(true)
	t.Cleanup(func() { f.state.setDedicatedInput(false) })
	f.state.commands.mu.Lock()
	f.state.commands.running = true
	f.state.commands.mu.Unlock()
	f.handler.dispatchDedicatedControl(f.conn, f.state, "fixture-viewer", "user", []byte(`{"type":"browser_control","action":"release","input_epoch":0,"control_epoch":1}`), "browser_control", f.cfg)
	f.state.commands.mu.Lock()
	jobs := f.state.commands.jobs
	f.state.commands.jobs = nil
	f.state.commands.running = false
	f.state.commands.mu.Unlock()
	if len(jobs) != 1 {
		t.Fatalf("admitted release jobs=%d, want exactly one", len(jobs))
	}
	return jobs[0]
}

func TestDedicatedControlAttachmentWaitFailureIsVisible(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	job := admittedDedicatedRelease(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	job.run(ctx)
	if f.original.Err() != nil {
		t.Fatal("test must leave the original attachment live for a failure reply")
	}
	select {
	case envelope := <-f.conn.sendCh:
		if !f.conn.canSendFrame(envelope) {
			t.Fatal("failure acknowledgment must remain eligible on its live attachment")
		}
		var ack generated.BrowserInputControlAckFrame
		if err := json.Unmarshal(envelope.data, &ack); err != nil {
			t.Fatal(err)
		}
		if ack.Type != "browser_input_control_ack" || ack.SessionId != "chat" || ack.InputEpoch != 0 || ack.ControlEpoch != 1 || ack.Ok {
			t.Fatalf("unexpected failure acknowledgment: %+v", ack)
		}
		if ack.Reason == nil || !strings.Contains(strings.ToLower(*ack.Reason), "attachment") {
			t.Fatalf("failure must identify the attachment problem: %+v", ack.Reason)
		}
	default:
		t.Fatal("admitted control expired while awaiting attachment without a failure acknowledgment")
	}
	select {
	case extra := <-f.conn.sendCh:
		t.Fatalf("unexpected second response: %s", extra.data)
	default:
	}
}

func TestDedicatedControlRetiredAttachmentCannotReceiveFailure(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	job := admittedDedicatedRelease(t, f)
	f.state.clearAttachment()
	job.run(context.Background())
	select {
	case envelope := <-f.conn.sendCh:
		if f.conn.canSendFrame(envelope) {
			t.Fatalf("retired attachment authorized a late control response: %s", envelope.data)
		}
	default:
	}
}

func TestDedicatedStateUsesCurrentControlEpochAndRetainsPeerScope(t *testing.T) {
	wc := newTestBrowserWSConn()
	attachment, cancelAttachment := context.WithCancel(context.Background())
	defer cancelAttachment()
	source, cancelSource := context.WithCancel(attachment)
	defer cancelSource()
	d := &browserDedicatedInput{epoch: 1, offer: 1}
	offer := generated.BrowserInputOfferFrame{Type: "browser_input_offer", SessionId: "chat", AgentId: "agent", InputEpoch: 1, OfferId: 1, ControlEpoch: 0, Sdp: "offer"}
	send := d.stateSender(wc, browserAttachmentRequest{ctx: attachment}, "viewer", offer, source)
	readState := func(control int, reason string) browserOutboundFrame {
		t.Helper()
		send(reason)
		select {
		case envelope := <-wc.sendCh:
			var frame generated.BrowserInputStateFrame
			if err := json.Unmarshal(envelope.data, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Type != "browser_input_state" || frame.SessionId != "chat" || frame.InputEpoch != 1 || frame.OfferId != 1 || frame.ControlEpoch != control || frame.State != "failed" || frame.Reason == nil || *frame.Reason != reason {
				t.Fatalf("state must carry current control=%d and original peer identity: %+v", control, frame)
			}
			if !wc.canSendFrame(envelope) {
				t.Fatal("current peer failure must be eligible for delivery")
			}
			return envelope
		default:
			t.Fatal("current peer failure was not published")
			return browserOutboundFrame{}
		}
	}
	readState(0, "first failure")
	d.mu.Lock()
	d.control, d.applied = 1, 1
	d.mu.Unlock()
	// Source cancellation must suppress a stale ready message, while its
	// failure still reaches the live attachment under the CURRENT control.
	cancelSource()
	send("ready")
	select {
	case unexpected := <-wc.sendCh:
		t.Fatalf("canceled input source published ready: %s", unexpected.data)
	default:
	}
	queued := readState(1, "post-control failure")
	d.mu.Lock()
	d.epoch, d.offer = 2, 2
	d.mu.Unlock()
	if wc.canSendFrame(queued) {
		t.Fatal("queued failure from the replaced peer remained deliverable")
	}
	send("retired failure")
	select {
	case unexpected := <-wc.sendCh:
		t.Fatalf("retired peer published another failure: %s", unexpected.data)
	default:
	}
}
