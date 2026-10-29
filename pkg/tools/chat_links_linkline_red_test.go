// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for chat-links link lines (BS-1.7, BS-1.7b, BS-1.8, BS-1.9, BS-4.3,
// BS-5.3, SEC-15, FR-010, FR-011).
//
// Expected hrefs are derived from the spec's link grammar, not from a run of
// the tools: omnipus://file/<ws>/<seg> with each segment percent-encoded UTF-8,
// and the encoder also escapes '(', ')', '[', ']', space, '<', '>'.
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
)

const chatLinksWorkspaceID = "W"

// chatLinksHref is the spec encoder's output for one workspace-relative path.
// Segments are encoded individually so '/' stays a separator.
func chatLinksHref(rel string) string {
	parts := strings.Split(rel, "/")
	enc := make([]string, len(parts))
	for i, seg := range parts {
		enc[i] = chatLinksEncodeSeg(seg)
	}
	return "link: omnipus://file/" + chatLinksWorkspaceID + "/" + strings.Join(enc, "/")
}

func chatLinksEncodeSeg(seg string) string {
	var b strings.Builder
	for _, r := range seg {
		switch r {
		case ' ', '(', ')', '[', ']', '<', '>', '%', '#', '?':
			b.WriteString(chatLinksPct(byte(r)))
		default:
			if r < 0x80 && (r == '-' || r == '.' || r == '_' || r == '~' ||
				(r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
				b.WriteRune(r)
				continue
			}
			// Percent-encoded UTF-8 (spec link grammar). string(r) is one rune's UTF-8.
			for _, c := range []byte(string(r)) {
				b.WriteString(chatLinksPct(c))
			}
		}
	}
	return b.String()
}

func chatLinksPct(c byte) string {
	const hex = "0123456789abcdef"
	return "%" + string([]byte{hex[c>>4], hex[c&0x0f]})
}

func chatLinksWebCtx(dir string) context.Context {
	ctx := WithToolContext(context.Background(), "webchat", "chat-1")
	ctx = WithWorkspaceID(ctx, chatLinksWorkspaceID)
	ctx = WithTurnWorkspaceDir(ctx, dir)
	ctx = WithAgentID(ctx, "mia")
	ctx = WithTranscriptSessionID(ctx, "sess-1")
	ctx = WithToolRootChatSessionID(ctx, "sess-1")
	return ctx
}

func TestEncodeLibraryLink_GoldenVectors(t *testing.T) {
	// BS-1.8 / FR-010. Observed through write_file, which must append the one
	// encoder's link line on web chat (FR-011). A hand-built unencoded link fails.
	dir := t.TempDir()
	tool := NewWriteFileTool(dir, true)
	ctx := chatLinksWebCtx(dir)

	cases := []struct {
		name string
		rel  string
	}{
		{"parentheses and space", "report (final).md"},
		{"brackets", "a[1].md"},
		{"percent space hash question", "100% done #1?.md"},
		{"unicode", "uber/ü ñ.txt"},
		{"space", "a b.md"},
		{"plain", "notes/plan.md"},
		{"255-char segment", strings.Repeat("a", 255)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := filepath.Dir(tc.rel)
			if parent != "." {
				if err := os.MkdirAll(filepath.Join(dir, parent), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
			}
			res := tool.Execute(ctx, map[string]any{
				"path":    tc.rel,
				"content": "x",
			})
			if res.IsError {
				t.Fatalf("write_file %q failed before a link could be checked: %s", tc.rel, res.ForLLM)
			}
			want := chatLinksHref(tc.rel)
			if !strings.Contains(res.ForLLM, want) {
				t.Fatalf("write_file result = %q, want it to contain %q (BS-1.8 / FR-010 / FR-011)", res.ForLLM, want)
			}
		})
	}
}

func TestWriteTools_LinkLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := chatLinksWebCtx(dir)
	want := chatLinksHref("notes/plan.md")

	t.Run("write_file web chat", func(t *testing.T) {
		res := NewWriteFileTool(dir, true).Execute(ctx, map[string]any{
			"path":    "notes/plan.md",
			"content": "plan",
		})
		if res.IsError {
			t.Fatalf("write failed: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("write_file result = %q, want %q (BS-1.7)", res.ForLLM, want)
		}
	})

	t.Run("edit_file web chat", func(t *testing.T) {
		res := NewEditFileTool(dir, true).Execute(ctx, map[string]any{
			"path":     "notes/plan.md",
			"old_text": "plan",
			"new_text": "plan v2",
		})
		if res.IsError {
			t.Fatalf("edit failed: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("edit_file result = %q, want %q (BS-1.7)", res.ForLLM, want)
		}
	})

	t.Run("append_file web chat", func(t *testing.T) {
		res := NewAppendFileTool(dir, true).Execute(ctx, map[string]any{
			"path":    "notes/plan.md",
			"content": "\nmore",
		})
		if res.IsError {
			t.Fatalf("append failed: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("append_file result = %q, want %q (BS-1.7)", res.ForLLM, want)
		}
	})

	for _, channel := range []string{"telegram", "email"} {
		t.Run("no link on "+channel, func(t *testing.T) {
			off := WithToolContext(context.Background(), channel, "chat-9")
			off = WithWorkspaceID(off, chatLinksWorkspaceID)
			off = WithTurnWorkspaceDir(off, dir)
			res := NewWriteFileTool(dir, true).Execute(off, map[string]any{
				"path":      "notes/other.md",
				"content":   "y",
				"overwrite": true,
			})
			if res.IsError {
				t.Fatalf("%s write failed: %s", channel, res.ForLLM)
			}
			if strings.Contains(res.ForLLM, "omnipus://") || strings.Contains(res.ForLLM, "link:") {
				t.Fatalf("%s result leaked a link: %q (BS-1.7b / SEC-15 / D-A)", channel, res.ForLLM)
			}
		})
	}
}

func TestSendFile_ChannelNoLink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)

	t.Run("web chat includes the link line", func(t *testing.T) {
		tool := NewSendFileTool(dir, true, 0, store)
		res := tool.Execute(chatLinksWebCtx(dir), map[string]any{"path": "notes.md"})
		if res.IsError {
			t.Fatalf("send_file failed: %s", res.ForLLM)
		}
		want := chatLinksHref("notes.md")
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("send_file result = %q, want %q (BS-5.1 / FR-011)", res.ForLLM, want)
		}
	})

	t.Run("telegram delivers the file and says to use the name", func(t *testing.T) {
		tool := NewSendFileTool(dir, true, 0, store)
		ctx := WithToolContext(context.Background(), "telegram", "chat-ext")
		ctx = WithWorkspaceID(ctx, chatLinksWorkspaceID)
		ctx = WithTurnWorkspaceDir(ctx, dir)
		res := tool.Execute(ctx, map[string]any{"path": "notes.md"})
		if res.IsError {
			t.Fatalf("telegram send_file failed: %s", res.ForLLM)
		}
		if len(res.Media) != 1 {
			t.Fatalf("telegram send_file media = %v, want one delivered ref (BS-5.3)", res.Media)
		}
		if strings.Contains(res.ForLLM, "omnipus://") || strings.Contains(res.ForLLM, "link:") {
			t.Fatalf("telegram result leaked a link: %q (BS-5.3 / SEC-15)", res.ForLLM)
		}
		if !strings.Contains(strings.ToLower(res.ForLLM), "by name") {
			t.Fatalf("telegram result = %q, want it to tell the agent to refer to the file by name (BS-5.3)", res.ForLLM)
		}
	})
}
