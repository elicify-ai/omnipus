package gateway

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestCascade_PartialReported_FrameAndChannel(t *testing.T) {
	// #823 merge: both frames are published through the session hub (every
	// bound tab sees them, numbered); the requester here is NOT bound to
	// "root", so it gets the unsequenced §1.4 copy — the same bytes minus seq.
	h := makeMinimalHandler()
	wc, _ := makeForwarderTestConn(4)
	report := steer.CancelReport{
		Reached:                []string{"root", "a", "c"},
		Unreachable:            []steer.UnreachableSession{{ID: "b", Reason: "unreadable"}, {ID: "d", Reason: "missing edge"}},
		SkippedNewerGeneration: []string{"revived"},
		SkippedTerminal:        []string{"done"},
	}

	h.sendCancelReportFrame(wc, "root", "detached", report)
	h.sendCancelPartialNotice(wc, "root", report)

	// #823: numbered once in the session's journal, whether or not a tab is
	// bound — a tab attaching later catches both up.
	hub := h.hubs.lookup("root")
	if hub == nil {
		t.Fatal("the Stop report must be published through the session hub")
	}
	if got := len(journalFramesOfType(t, hub, "cancel_stage")); got != 1 {
		t.Fatalf("journaled cancel_stage frames = %d, want 1", got)
	}
	if got := len(journalFramesOfType(t, hub, "error")); got != 1 {
		t.Fatalf("journaled partial notices = %d, want 1", got)
	}

	if len(wc.sendCh) != 2 {
		t.Fatalf("frames sent = %d, want report + exactly one channel line", len(wc.sendCh))
	}
	// Unreachable is decoded into an inline id/reason-tagged struct, not
	// steer.UnreachableSession directly: that internal domain type is never
	// itself a wire type (CLAUDE.md contract-first rule) — production code
	// (sendCancelReportFrame) explicitly converts each entry into this same
	// tagged shape before marshaling, so the test decodes the real wire
	// format instead of relying on encoding/json's case-insensitive
	// fallback matching against an untagged struct.
	var frame struct {
		Type        string   `json:"type"`
		SessionID   string   `json:"session_id"`
		Stage       string   `json:"stage"`
		Reached     []string `json:"reached"`
		Unreachable []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		} `json:"unreachable"`
		SkippedNewerGeneration []string `json:"skipped_newer_generation"`
		SkippedTerminal        []string `json:"skipped_terminal"`
		Partial                bool     `json:"partial"`
	}
	if err := json.Unmarshal(<-wc.sendCh, &frame); err != nil {
		t.Fatalf("decode cancel report: %v", err)
	}
	if frame.Type != "cancel_stage" || frame.SessionID != "root" || frame.Stage != "detached" || !frame.Partial {
		t.Fatalf("cancel report envelope = %+v", frame)
	}
	if !slices.Equal(frame.Reached, report.Reached) || !slices.Equal(frame.SkippedNewerGeneration, report.SkippedNewerGeneration) || !slices.Equal(frame.SkippedTerminal, report.SkippedTerminal) {
		t.Fatalf("cancel report lists = %+v", frame)
	}
	if len(frame.Unreachable) != 2 || frame.Unreachable[0].ID != "b" || frame.Unreachable[1].ID != "d" {
		t.Fatalf("unreachable = %+v", frame.Unreachable)
	}

	var notice struct {
		Type      string  `json:"type"`
		SessionID *string `json:"session_id"`
		Message   string  `json:"message"`
	}
	if err := json.Unmarshal(<-wc.sendCh, &notice); err != nil {
		t.Fatalf("decode partial notice: %v", err)
	}
	if notice.Type != "error" || notice.SessionID == nil || *notice.SessionID != "root" {
		t.Fatalf("partial notice envelope = %+v", notice)
	}
	// The exact wording is fixed by the spec, not read off the producer:
	// adr-091-wp-d-cancel-cascade-spec.md's "Partial reported" row requires
	// "exactly one line on the originating channel: 'stopped 3 of 5; 2
	// unreachable'" — which is why this fixture reaches 3 and loses 2. The
	// notice names the partial FAILURE only; "revived" sits in
	// skipped_newer_generation on the frame above (asserted there) and is
	// deliberately absent from this line, because a revival that landed
	// after the Stop is a correct outcome, not something the cascade failed
	// to do (ADR-091 D8, "the later instruction wins").
	if strings.TrimSpace(notice.Message) != "stopped 3 of 5; 2 unreachable" {
		t.Fatalf("partial notice = %q", notice.Message)
	}
}

// TestCascade_SkippedNewerGenerationAloneIsNotPartial pins WP-D US-1/AS-9,
// whose entire stated outcome is "the registry refuses it, B's generation-2
// turn keeps running, and the report lists B under SkippedNewerGeneration" —
// no `partial: true` and no line on the originating channel. Every `partial`
// sentence in the spec (FR-D-001, US-1/AS-4, the "Partial reported" check,
// the BDD scenario, the integration-boundary table) names the unreachable
// branch and only that.
func TestCascade_SkippedNewerGenerationAloneIsNotPartial(t *testing.T) {
	h := makeMinimalHandler()
	wc, _ := makeForwarderTestConn(4)
	report := steer.CancelReport{
		Reached:                []string{"root", "a"},
		SkippedNewerGeneration: []string{"revived"},
		SkippedTerminal:        []string{"done"},
	}

	h.sendCancelReportFrame(wc, "root", "detached", report)
	h.sendCancelPartialNotice(wc, "root", report)

	if len(wc.sendCh) != 1 {
		t.Fatalf("frames sent = %d, want the report frame and NO partial notice", len(wc.sendCh))
	}
	var frame struct {
		Partial                bool     `json:"partial"`
		SkippedNewerGeneration []string `json:"skipped_newer_generation"`
	}
	if err := json.Unmarshal(<-wc.sendCh, &frame); err != nil {
		t.Fatalf("decode cancel report: %v", err)
	}
	if frame.Partial {
		t.Fatal("partial = true for a cascade that reached everything it was allowed to touch; " +
			"a session the cascade correctly left alone because a newer generation took over is not a partial Stop")
	}
	// The fact is still on the wire — partial:false hides nothing.
	if !slices.Equal(frame.SkippedNewerGeneration, []string{"revived"}) {
		t.Fatalf("skipped_newer_generation = %v, want [revived]", frame.SkippedNewerGeneration)
	}
	if got := cancelPartialSummary(report); got != "" {
		t.Fatalf("partial summary = %q, want empty — there was no partial failure to report", got)
	}
}

func TestCancelPartialSummary_CompleteReportHasNoNotice(t *testing.T) {
	report := steer.CancelReport{Reached: []string{"root", "child"}}
	if got := cancelPartialSummary(report); got != "" {
		t.Fatalf("complete report summary = %q, want empty", got)
	}
}

// TestCancelIncompleteSubtreeSummary_CoversStillRunningNodes guards the OTHER
// question — "could anything under this node still be running?" — which the
// REST delete guard (rest_sessions.go::deleteSession) asks before it destroys
// session data. That guard refuses on unreachable OR skipped-newer-generation
// and puts this string in its 500 body, so a skipped-only cascade must still
// yield a non-empty reason even though it is not `partial`.
func TestCancelIncompleteSubtreeSummary_CoversStillRunningNodes(t *testing.T) {
	clean := steer.CancelReport{Reached: []string{"root", "child"}}
	if got := cancelIncompleteSubtreeSummary(clean); got != "" {
		t.Fatalf("clean cascade summary = %q, want empty", got)
	}

	skippedOnly := steer.CancelReport{
		Reached:                []string{"root", "a"},
		SkippedNewerGeneration: []string{"revived"},
	}
	if got := cancelIncompleteSubtreeSummary(skippedOnly); got == "" {
		t.Fatal("skipped-only cascade summary is empty — the delete refusal would 500 with no reason")
	} else if !strings.Contains(got, "1 already advanced to a newer generation and are still running") {
		t.Fatalf("skipped-only cascade summary = %q, want it to name the still-running count", got)
	}

	both := steer.CancelReport{
		Reached:                []string{"root", "a", "c"},
		Unreachable:            []steer.UnreachableSession{{ID: "b", Reason: "unreadable"}, {ID: "d", Reason: "missing edge"}},
		SkippedNewerGeneration: []string{"revived"},
	}
	const want = "stopped 3 of 5; 2 unreachable; 1 already advanced to a newer generation and are still running"
	if got := cancelIncompleteSubtreeSummary(both); got != want {
		t.Fatalf("combined summary = %q, want %q", got, want)
	}
}
