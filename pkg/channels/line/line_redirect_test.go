package line

// D9 /stop-redirect follow-up caller test (backend-lead, dispatched corrective
// brief: "small follow-up caller tests to prove at least one other adapter").
// The wecom RED pack (pkg/channels/wecom/wecom_test.go::
// TestDispatchIncoming_StopRedirectAdapterPath) pins the interception on
// WeCom; this file proves the SAME wiring on the LINE adapter: the adapter
// path intercepts /stop-redirect through channels.DispatchRedirectIfRecognized
// before the agent loop's intake — one interceptor call with the adapter's
// exact (channelName, chatID, senderID, instruction) tuple — while ordinary
// text keeps flowing and /cancel stays on the cancel path.
//
// The recording interceptor answers with a VISIBLE error (never a faked
// success), mirroring the wecom fake: while the D2 composition is unwired a
// redirect ack must read as a real failure, not a completed redirect.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

type recordingLineInterceptor struct {
	redirectCalls   int
	redirectChannel string
	redirectChatID  string
	redirectSender  string
	redirectInstr   string
	cancelCalls     int
}

func (r *recordingLineInterceptor) RequestCancelByChannelChat(
	context.Context, string, string, string,
) (bool, bool, error) {
	r.cancelCalls++
	return false, false, nil
}

func (r *recordingLineInterceptor) RequestRedirectByChannelChat(
	_ context.Context,
	channelName, chatID, senderID, instruction string,
) error {
	r.redirectCalls++
	r.redirectChannel = channelName
	r.redirectChatID = chatID
	r.redirectSender = senderID
	r.redirectInstr = instruction
	return errors.New("recordingLineInterceptor: redirect crossing recorded; this fake wires no real redirect")
}

func lineInboundCount(msgBus *bus.MessageBus) int {
	n := 0
	for {
		select {
		case <-msgBus.InboundChan():
			n++
		default:
			return n
		}
	}
}

func TestProcessEvent_StopRedirectAdapterPath(t *testing.T) {
	t.Run("redirect_command_intercepted_once_before_intake", func(t *testing.T) {
		ch, msgBus := newTestLINEChannel("s")
		ic := &recordingLineInterceptor{}
		ch.SetCancelInterceptor(ic)

		ch.processEvent(lineEvent{
			Type:       "message",
			ReplyToken: "rt-sr-1",
			Source:     lineSource{Type: "user", UserID: "U-sr-1"},
			Message:    json.RawMessage(`{"id":"m-sr-1","type":"text","text":"/stop-redirect do this"}`),
		})

		if ic.redirectCalls != 1 {
			t.Fatalf(
				"/stop-redirect was not routed through the adapter's redirect dispatch: redirectCalls = %d, want 1 (cancelCalls = %d, inbound published = %d)",
				ic.redirectCalls, ic.cancelCalls, lineInboundCount(msgBus),
			)
		}
		if ic.redirectChannel != "line" ||
			ic.redirectChatID != "U-sr-1" ||
			ic.redirectSender != "U-sr-1" ||
			ic.redirectInstr != "do this" {
			t.Fatalf(
				"redirect primitive arguments = (channel=%q, chat=%q, sender=%q, instruction=%q), want (line, U-sr-1, U-sr-1, do this)",
				ic.redirectChannel, ic.redirectChatID, ic.redirectSender, ic.redirectInstr,
			)
		}
		if n := lineInboundCount(msgBus); n != 0 {
			t.Fatalf(
				"/stop-redirect fell through to the agent-loop intake: %d inbound message(s) published, want 0 — the command must be consumed before HandleMessage (corrected transport ruling §3.1)",
				n,
			)
		}
	})

	t.Run("bare_redirect_command_consumed_without_handler_call", func(t *testing.T) {
		ch, msgBus := newTestLINEChannel("s")
		ic := &recordingLineInterceptor{}
		ch.SetCancelInterceptor(ic)

		ch.processEvent(lineEvent{
			Type:       "message",
			ReplyToken: "rt-sr-2",
			Source:     lineSource{Type: "user", UserID: "U-sr-2"},
			Message:    json.RawMessage(`{"id":"m-sr-2","type":"text","text":"/stop-redirect"}`),
		})

		if ic.redirectCalls != 0 {
			t.Fatalf(
				"bare /stop-redirect reached the redirect handler: redirectCalls = %d, want 0 (the bare form is usage-only and changes nothing)",
				ic.redirectCalls,
			)
		}
		if n := lineInboundCount(msgBus); n != 0 {
			t.Fatalf(
				"bare /stop-redirect fell through to the agent-loop intake: inbound published = %d, want 0 (it must be consumed with the usage reply)",
				n,
			)
		}
	})

	t.Run("ordinary_text_dispatches_normally_without_redirect", func(t *testing.T) {
		ch, msgBus := newTestLINEChannel("s")
		ic := &recordingLineInterceptor{}
		ch.SetCancelInterceptor(ic)

		ch.processEvent(lineEvent{
			Type:       "message",
			ReplyToken: "rt-sr-3",
			Source:     lineSource{Type: "user", UserID: "U-sr-3"},
			Message:    json.RawMessage(`{"id":"m-sr-3","type":"text","text":"do this"}`),
		})

		select {
		case inbound := <-msgBus.InboundChan():
			if inbound.ChatID != "U-sr-3" || inbound.Content != "do this" {
				t.Fatalf("ordinary text inbound = (chat=%q, content=%q), want (U-sr-3, do this)", inbound.ChatID, inbound.Content)
			}
		case <-time.After(time.Second):
			t.Fatal("ordinary text never reached the agent-loop intake")
		}
		if ic.redirectCalls != 0 {
			t.Fatalf("ordinary text triggered redirect: redirectCalls = %d, want 0", ic.redirectCalls)
		}
		if ic.cancelCalls != 0 {
			t.Fatalf("ordinary text triggered cancel: cancelCalls = %d, want 0", ic.cancelCalls)
		}
	})

	t.Run("cancel_command_stays_on_cancel_path", func(t *testing.T) {
		ch, msgBus := newTestLINEChannel("s")
		ic := &recordingLineInterceptor{}
		ch.SetCancelInterceptor(ic)

		ch.processEvent(lineEvent{
			Type:       "message",
			ReplyToken: "rt-sr-4",
			Source:     lineSource{Type: "user", UserID: "U-sr-4"},
			Message:    json.RawMessage(`{"id":"m-sr-4","type":"text","text":"/cancel"}`),
		})

		if ic.cancelCalls != 1 {
			t.Fatalf("/cancel cancelCalls = %d, want 1 (cancel must keep firing the cancel state machine)", ic.cancelCalls)
		}
		if ic.redirectCalls != 0 {
			t.Fatalf("/cancel triggered redirect: redirectCalls = %d, want 0", ic.redirectCalls)
		}
		if n := lineInboundCount(msgBus); n != 0 {
			t.Fatalf("/cancel reached the agent-loop intake: inbound published = %d, want 0", n)
		}
	})

	// qa-lead adoption subtest (2026-10-04): the four subtests above were
	// authored by backend-lead in commit 9df692c4a and missed the denied
	// sender; this subtest is the one authorized corrective coverage unit
	// from the D9 adapters CHECK WARN (the allow-list-skip mutant survived).
	// Expected values derive from the spec, not from this implementation:
	// ADR-20260928-sub-agent-control-plane D9 — a sender the allow-list
	// denies must not reach the redirect dispatch, the cancel dispatch or
	// the agent loop's intake — and the allow-list precedent in
	// pkg/channels/wecom/wecom_test.go::TestDispatchIncoming_DeniedSenderHasNoSideEffects
	// (nil/empty allow-list = allow all; a listed sender is admitted, an
	// unlisted one is dropped by BaseChannel.IsAllowedSender). This is a
	// post-hoc adoption test on the already-green f329580 tree: it passes on
	// arrival by design and is NOT a RED-before-GREEN claim. The fixture
	// swaps in a real BaseChannel allow-list ([]string{"U-allowed"}) instead
	// of stubbing IsAllowedSender, so the real guard runs; the positive
	// control proves the allow-list is active rather than the dispatcher
	// being broken. The denied path emits no ack by design, so no network
	// Send is asserted here.
	t.Run("denied_sender_stop_redirect_reaches_nothing", func(t *testing.T) {
		ch, msgBus := newTestLINEChannel("s")
		ch.BaseChannel = channels.NewBaseChannel("line", nil, msgBus, []string{"U-allowed"})
		ic := &recordingLineInterceptor{}
		ch.SetCancelInterceptor(ic)

		ch.processEvent(lineEvent{
			Type:       "message",
			ReplyToken: "rt-sr-5",
			Source:     lineSource{Type: "user", UserID: "U-denied"},
			Message:    json.RawMessage(`{"id":"m-sr-5","type":"text","text":"/stop-redirect do this"}`),
		})

		deniedRedirectCalls := ic.redirectCalls
		deniedCancelCalls := ic.cancelCalls
		deniedInbound := lineInboundCount(msgBus)
		if deniedRedirectCalls != 0 {
			t.Fatalf(
				"denied sender's /stop-redirect reached the redirect dispatch: redirectCalls = %d, want 0 (cancelCalls = %d, inbound published = %d)",
				deniedRedirectCalls, deniedCancelCalls, deniedInbound,
			)
		}
		if deniedCancelCalls != 0 {
			t.Fatalf(
				"denied sender's /stop-redirect reached the cancel dispatch: cancelCalls = %d, want 0",
				deniedCancelCalls,
			)
		}
		if deniedInbound != 0 {
			t.Fatalf(
				"denied sender's /stop-redirect reached the agent-loop intake: inbound published = %d, want 0",
				deniedInbound,
			)
		}

		// Positive control: the SAME command from the allow-listed sender must
		// still fire the redirect dispatch exactly once with the exact tuple —
		// proving the allow-list above is active (it denied U-denied), not a
		// dispatcher broken for everyone.
		ch.processEvent(lineEvent{
			Type:       "message",
			ReplyToken: "rt-sr-6",
			Source:     lineSource{Type: "user", UserID: "U-allowed"},
			Message:    json.RawMessage(`{"id":"m-sr-6","type":"text","text":"/stop-redirect do this"}`),
		})

		if ic.redirectCalls != 1 {
			t.Fatalf(
				"allowed-sender control did not fire redirect exactly once: redirectCalls = %d, want 1 (the allow-list fixture must admit U-allowed, or the dispatcher is broken)",
				ic.redirectCalls,
			)
		}
		if ic.redirectChannel != "line" ||
			ic.redirectChatID != "U-allowed" ||
			ic.redirectSender != "U-allowed" ||
			ic.redirectInstr != "do this" {
			t.Fatalf(
				"allowed-sender control redirect primitive arguments = (channel=%q, chat=%q, sender=%q, instruction=%q), want (line, U-allowed, U-allowed, do this)",
				ic.redirectChannel, ic.redirectChatID, ic.redirectSender, ic.redirectInstr,
			)
		}
		if ic.cancelCalls != 0 {
			t.Fatalf(
				"allowed-sender control triggered cancel: cancelCalls = %d, want 0",
				ic.cancelCalls,
			)
		}
		if n := lineInboundCount(msgBus); n != 0 {
			t.Fatalf(
				"allowed-sender control fell through to the agent-loop intake: inbound published = %d, want 0",
				n,
			)
		}
	})
}
