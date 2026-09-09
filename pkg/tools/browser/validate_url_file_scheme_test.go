package browser

// FR-019 — the file:// refusal has to point somewhere.
//
// An agent that has just written an HTML file and wants to look at it reaches
// for file://. "Blocked for security reasons" is true and useless: it leaves
// the agent with no next move, and the usual outcome is that it tries the
// same thing again with a different path. There IS a way to view a local
// file, so the error names it.
//
// Pure string assertions — no browser needed.

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func newValidatorForTest(t *testing.T) *BrowserManager {
	t.Helper()
	mgr, err := NewBrowserManager(BrowserConfig{Headless: true}, security.NewSSRFChecker(nil))
	if err != nil {
		t.Fatalf("NewBrowserManager: %v", err)
	}
	return mgr
}

func TestValidateURL_FileScheme_NamesServeWeb(t *testing.T) {
	mgr := newValidatorForTest(t)

	err := mgr.ValidateURL(context.Background(), "file:///tmp/report.html")
	if err == nil {
		t.Fatal("file:// must still be blocked")
	}
	msg := err.Error()

	// The tool's REAL name. There is no tool called "web_serve"; naming one
	// would send the agent looking for something that does not exist.
	if !strings.Contains(msg, tools.ToolNameWebServe) {
		t.Errorf("the file:// error must name the %s tool; got %q", tools.ToolNameWebServe, msg)
	}
	if strings.Contains(msg, "web_serve") {
		t.Errorf("the file:// error names a tool that does not exist (\"web_serve\"); the tool is %q. Got %q",
			tools.ToolNameWebServe, msg)
	}
	// The URL shape the agent then navigates to.
	if !strings.Contains(msg, "/preview/") {
		t.Errorf("the file:// error must name the /preview/ route the agent navigates to; got %q", msg)
	}
	// The condition under which that route exists at all.
	if !strings.Contains(msg, "gateway.preview_enabled") {
		t.Errorf("the file:// error must name gateway.preview_enabled, so an operator whose install has it off can tell why the suggestion did not work; got %q", msg)
	}
}

// The other four blocked schemes are unchanged. javascript: is not an agent
// trying to view its own work — it is a scheme with no legitimate use here,
// and sending an agent to serve_web over it would be noise.
func TestValidateURL_JavascriptSchemeUnchanged(t *testing.T) {
	mgr := newValidatorForTest(t)

	for _, raw := range []string{
		"javascript:alert(1)",
		"data:text/html,<h1>x</h1>",
		"chrome://settings/",
		"chrome-extension://id/page.html",
	} {
		err := mgr.ValidateURL(context.Background(), raw)
		if err == nil {
			t.Fatalf("%s must still be blocked", raw)
		}
		msg := err.Error()
		if !strings.Contains(msg, "blocked for security reasons") {
			t.Errorf("%s: the pre-existing refusal wording must be unchanged; got %q", raw, msg)
		}
		if strings.Contains(msg, tools.ToolNameWebServe) || strings.Contains(msg, "/preview/") {
			t.Errorf("%s: only file:// gets the serve_web pointer; got %q", raw, msg)
		}
	}
}
