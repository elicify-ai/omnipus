package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestBrowserCommandResultUsesAttachmentLifetime(t *testing.T) {
	for _, kind := range []string{"input_failure", "control_invalid", "tab_invalid", "dispatch_invalid"} {
		t.Run(kind, func(t *testing.T) {
			wc, state := newTabActionTestFixtures(t)
			t.Cleanup(func() { state.clearAttachment() })
			attachment := state.commandAttachment()
			operation, cancel := context.WithCancel(attachment.ctx)
			defer cancel()
			h := &BrowserWSHandler{}
			switch kind {
			case "input_failure":
				h.handleInputContext(operation, wc, state, attachment, "viewer", []byte(`{"type":"browser_input","kind":"text","text":"test"}`))
			case "control_invalid":
				h.handleControlContext(operation, wc, state, attachment, "viewer", "user", []byte(`{`), nil)
			case "tab_invalid":
				h.handleTabActionContext(operation, wc, state, attachment, "viewer", []byte(`{`))
			case "dispatch_invalid":
				h.dispatchBrowserCommand(wc, state, "viewer", "user", []byte(`{`), string(generated.WsFrameTypeBrowserInput), nil)
			}
			cancel() // Completion of an operation must not suppress its result.
			var result browserOutboundFrame
			select {
			case result = <-wc.sendCh:
			default:
				t.Fatal("operation did not report its failure")
			}
			var status generated.BrowserStatusFrame
			if err := json.Unmarshal(result.data, &status); err != nil {
				t.Fatal(err)
			}
			if status.State != "error" || status.OperationOnly == nil || !*status.OperationOnly {
				t.Fatalf("operation error changed attachment lifecycle: %s", result.data)
			}
			if !wc.canSendFrame(result) {
				t.Error("completed operation lost its current attachment's result")
			}
			state.beginAttach()
			if wc.canSendFrame(result) {
				t.Error("operation result remains deliverable to a replacement attachment")
			}
		})
	}
}
