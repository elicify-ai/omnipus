package agent

import (
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// The note must appear ONLY on the web surface — every other channel shows a raw
// ```mermaid fence as plain text, so steering them toward Mermaid is harmful.
func TestBuildWebRenderingNote_SurfaceGating(t *testing.T) {
	cases := []struct {
		channel  string
		wantNote bool
	}{
		{"webchat", true}, // the one surface that renders Mermaid
		{"cli", false},
		{"telegram", false},
		{"discord", false},
		{"matrix", false},
		{"", false}, // unknown/empty → SurfaceChannel → no note
	}
	for _, tc := range cases {
		got := buildWebRenderingNote(tc.channel)
		if tc.wantNote && got == "" {
			t.Errorf("buildWebRenderingNote(%q) = \"\"; want a note", tc.channel)
		}
		if !tc.wantNote && got != "" {
			t.Errorf("buildWebRenderingNote(%q) returned a note; want \"\" (Mermaid does not render on this surface)", tc.channel)
		}
	}
}

// The web note must actually encourage Mermaid and name the web chat, or it does
// not serve its purpose.
func TestBuildWebRenderingNote_Content(t *testing.T) {
	note := buildWebRenderingNote("webchat")
	for _, want := range []string{"mermaid", "web chat", "diagram"} {
		if !strings.Contains(strings.ToLower(note), want) {
			t.Errorf("web rendering note missing %q; got:\n%s", want, note)
		}
	}
}

// Layer 1: the note must carry the Mermaid syntax rules that prevent the common
// LLM failure modes (smart quotes, reserved-word ids, bracket mismatch).
func TestBuildWebRenderingNote_MermaidSyntaxRules(t *testing.T) {
	note := buildWebRenderingNote("webchat")
	for _, want := range []string{
		"straight ASCII quotes",
		"smart quotes",
		"Match bracket",
		"reserved word",
		"end",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("web rendering note missing Mermaid syntax rule %q; got:\n%s", want, note)
		}
	}
}

func TestInjectWebRenderingNote(t *testing.T) {
	sys := providers.Message{Role: "system", Content: "you are an agent"}
	user := providers.Message{Role: "user", Content: "hi"}

	t.Run("inserts at index 1 after the system prompt", func(t *testing.T) {
		out := injectWebRenderingNote([]providers.Message{sys, user}, "NOTE")
		if len(out) != 3 {
			t.Fatalf("len = %d; want 3", len(out))
		}
		if out[0].Content != sys.Content {
			t.Errorf("out[0] = %q; want the system prompt", out[0].Content)
		}
		if out[1].Role != "system" || out[1].Content != "NOTE" {
			t.Errorf("out[1] = %+v; want system/NOTE", out[1])
		}
		if out[2].Content != user.Content {
			t.Errorf("out[2] = %q; want the user message", out[2].Content)
		}
	})

	t.Run("no-op when note is empty", func(t *testing.T) {
		in := []providers.Message{sys, user}
		out := injectWebRenderingNote(in, "")
		if len(out) != 2 {
			t.Errorf("len = %d; want 2 (unchanged)", len(out))
		}
	})

	t.Run("no-op when msgs is empty", func(t *testing.T) {
		out := injectWebRenderingNote(nil, "NOTE")
		if len(out) != 0 {
			t.Errorf("len = %d; want 0", len(out))
		}
	})
}
