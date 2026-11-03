package tools

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
)

// Oracles: #1449 FR-020/FR-078 require the same send_file filesystem policy
// for answer media, including registered media:// references. The security
// fix-round brief specifies the refusal text and the permitted media-temp
// case. unified-file-access-and-mounts-spec FR-3.2 protects other agents'
// files; FR-2.2/FR-2.3 deliberately allow ordinary files outside WorkDir, so
// mere location outside WorkDir would NOT be a valid refusal oracle.
// The filesystem, media store, resolver, PathHandle and answer tool are real;
// only the outward reply router is a process-edge double.
func TestSendFileAnswer_MediaRefsEnforceSendPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed bool
	}{
		{name: "protected_file_outside_workspace_and_media_temp_is_refused"},
		{name: "allowed_media_temp_is_read_through_handle", allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("OMNIPUS_HOME", filepath.Join(root, "install"))
			workspace := filepath.Join(root, "workspace")
			mediaDir := media.TempDir()
			// FR-3.2: another agent's file is in the protected set. Registering
			// it as media must not grant permission to disclose its bytes.
			outsideDir := filepath.Join(root, "install", "agents", "other-agent")
			for _, dir := range []string{workspace, mediaDir, outsideDir} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatalf("create fixture directory: %v", err)
				}
			}
			// Derive the physical allowance rather than assuming /tmp's
			// spelling is the same on every supported operating system.
			mediaDir, err := filepath.EvalSymlinks(mediaDir)
			if err != nil {
				t.Fatalf("resolve media-temp directory: %v", err)
			}
			dir := outsideDir
			if tc.allowed {
				dir = mediaDir
			}
			path := filepath.Join(dir, "source.txt")
			const content = "The answer file contains the Friday launch plan.\n"
			const filename = "prepared-answer.txt"
			if writeErr := os.WriteFile(path, []byte(content), 0o600); writeErr != nil {
				t.Fatalf("write registered file: %v", writeErr)
			}
			store := media.NewFileMediaStore()
			t.Cleanup(store.Stop)
			ref, err := store.Store(path, media.MediaMeta{
				Filename: filename, ContentType: "text/plain", CleanupPolicy: media.CleanupPolicyForgetOnly,
			}, "answer-media-policy-test")
			if err != nil {
				t.Fatalf("register media file: %v", err)
			}
			// Positive control: this is an existing, resolvable legacy ref,
			// not a nonexistent ref that would fail before the send check.
			if resolved, lookupErr := store.Resolve(ref); lookupErr != nil || resolved != path {
				t.Fatalf("registered media reference resolves to %q, err=%v; want %q", resolved, lookupErr, path)
			}
			pattern := regexp.MustCompile("^" + regexp.QuoteMeta(mediaDir) + "(?:" + regexp.QuoteMeta(string(os.PathSeparator)) + "|$)")
			files := NewSendFileTool(workspace, true, 0, store, []*regexp.Regexp{pattern})
			resolved, resolveErr := files.ResolveAnswerFile(context.Background(), ref)

			router := &answerRouterStub{reply: ReplyReceipt{Destination: "the conversation it came from"}}
			msg := NewMessageTool()
			msg.SetReplyRouter(router)
			answer := NewAnswerRequestTool(msg)
			answer.SetFileResolver(files)
			result := answer.Execute(answerCtx(), map[string]any{
				"request_id": "req-media-policy", "text": "Here is the launch plan.", "media": []string{ref},
			})
			if result == nil {
				t.Fatal("answer_request returned no tool result")
			}
			if !tc.allowed {
				const refusal = "the file is outside what you may send: "
				if resolveErr == nil || !strings.HasPrefix(resolveErr.Error(), refusal) || len(resolveErr.Error()) == len(refusal) {
					t.Errorf("outside registered media refusal = %v, want %q followed by the access reason", resolveErr, refusal)
				}
				if resolved.Open != nil || resolved.Identity != "" || resolved.Size != 0 {
					t.Errorf("refused media returned a readable file: %+v", resolved)
				}
				if !result.IsError || !strings.Contains(result.ForLLM, refusal) {
					t.Errorf("outside registered media tool result = error:%v %q, want user-facing send-policy refusal", result.IsError, result.ForLLM)
				}
				if len(router.got) != 0 || msg.HasSentInRound() {
					t.Errorf("refused media was sent: router calls=%d sent=%v, want 0 and false", len(router.got), msg.HasSentInRound())
				}
				return
			}
			if resolveErr != nil {
				t.Fatalf("allowed media-temp reference refused: %v", resolveErr)
			}
			if resolved.Identity != ref || resolved.Filename != filename || resolved.Size != int64(len(content)) || resolved.Open == nil {
				t.Fatalf("allowed media result = %+v, want original ref, filename %q, %d bytes and an opener", resolved, filename, len(content))
			}
			reader, err := resolved.Open()
			if err != nil {
				t.Fatalf("read allowed media through returned PathHandle opener: %v", err)
			}
			data, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil || string(data) != content {
				t.Errorf("allowed media bytes = %q, read=%v close=%v; want %q", data, readErr, closeErr, content)
			}
			if result.IsError || len(router.got) != 1 || !msg.HasSentInRound() {
				t.Fatalf("allowed media was not accepted once: result=%+v router calls=%d sent=%v", result, len(router.got), msg.HasSentInRound())
			}
			if len(router.got[0].Media) != 1 || router.got[0].Media[0].Identity != ref || router.got[0].Media[0].Filename != filename {
				t.Errorf("accepted answer media = %+v, want the original registered file", router.got[0].Media)
			}
		})
	}
}
