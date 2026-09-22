package gateway

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestCascade_PartialReported_FrameAndChannel(t *testing.T) {
	wc := &wsConn{sendCh: make(chan []byte, 4)}
	report := steer.CancelReport{
		Reached:                []string{"root", "a", "c"},
		Unreachable:            []steer.UnreachableSession{{ID: "b", Reason: "unreadable"}, {ID: "d", Reason: "missing edge"}},
		SkippedNewerGeneration: []string{"revived"},
		SkippedTerminal:        []string{"done"},
	}

	sendCancelReportFrame(wc, "root", "detached", report)
	sendCancelPartialNotice(wc, "root", report)

	if len(wc.sendCh) != 2 {
		t.Fatalf("frames sent = %d, want report + exactly one channel line", len(wc.sendCh))
	}
	var frame struct {
		Type                   string                     `json:"type"`
		SessionID              string                     `json:"session_id"`
		Stage                  string                     `json:"stage"`
		Reached                []string                   `json:"reached"`
		Unreachable            []steer.UnreachableSession `json:"unreachable"`
		SkippedNewerGeneration []string                   `json:"skipped_newer_generation"`
		SkippedTerminal        []string                   `json:"skipped_terminal"`
		Partial                bool                       `json:"partial"`
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
	if strings.TrimSpace(notice.Message) != "stopped 3 of 5; 2 unreachable" {
		t.Fatalf("partial notice = %q", notice.Message)
	}
}

func TestCancelPartialSummary_CompleteReportHasNoNotice(t *testing.T) {
	report := steer.CancelReport{Reached: []string{"root", "child"}}
	if got := cancelPartialSummary(report); got != "" {
		t.Fatalf("complete report summary = %q, want empty", got)
	}
}
