package gateway

import (
	"context"
	"testing"
)

func TestDedicatedControlAcknowledgesOnlyActualSuccess(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	a := f.state.commandAttachment()
	for _, tc := range []struct {
		name, kind, raw string
		success         bool
	}{
		{"release", "control", `{"type":"browser_control","action":"release"}`, true},
		{"disabled-take", "control", `{"type":"browser_control","action":"take"}`, false},
		{"invalid-tab", "tab", `{"type":"browser_tab_action","action":"switch","index":999}`, false},
		{"resize", "viewport", `{"type":"browser_viewport","width":640,"height":480}`, true},
		{"bad-navigation", "input", `{"type":"browser_input","kind":"navigate","url":"javascript:alert(1)"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runBrowserInputControl(a.ctx, func(ctx context.Context) {
				switch tc.kind {
				case "control":
					f.cfg.Tools.Browser.TakeControlEnabled = false
					f.handler.handleControlContext(ctx, f.conn, f.state, a, "fixture-viewer", "user", []byte(tc.raw), f.cfg)
				case "tab":
					f.handler.handleTabActionContext(ctx, f.conn, f.state, a, "fixture-viewer", []byte(tc.raw))
				case "viewport":
					f.handler.handleViewportContext(ctx, f.conn, f.state, a, "fixture-viewer", []byte(tc.raw))
				case "input":
					f.handler.handleInputContext(ctx, f.conn, f.state, a, "fixture-viewer", []byte(tc.raw))
				}
			})
			if got != tc.success {
				t.Fatalf("control success=%v want=%v", got, tc.success)
			}
		})
	}
}
