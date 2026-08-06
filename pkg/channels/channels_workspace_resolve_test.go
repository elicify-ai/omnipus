package channels_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
)

func TestChannels_ResolveWithCallerWorkspace(t *testing.T) {
	const ws = "ws-channel"
	lib, err := library.New(t.TempDir(), ws)
	if err != nil {
		t.Fatal(err)
	}
	ref, _, err := lib.UploadFixture("channel.bin", strings.NewReader("channel media"))
	if err != nil {
		t.Fatal(err)
	}
	store := media.NewFileMediaStore()
	store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
		if id != ws {
			return nil, media.ErrCrossWorkspaceRef
		}
		return lib, nil
	})
	channels := []string{
		"telegram", "discord", "slack", "matrix", "feishu",
		"irc", "googlechat", "whatsapp_native", "weixin", "qq", "wecom",
	}
	for _, channel := range channels {
		t.Run(channel, func(t *testing.T) {
			if channel == "irc" || channel == "googlechat" || channel == "whatsapp_native" {
				t.Skip("text-only channel; no media outbound path")
			}
			path, _, err := store.ResolveWithCallerWorkspace(ref, ws)
			if err != nil || path == "" {
				t.Fatalf("resolve: path=%q err=%v", path, err)
			}
			if _, _, err := store.ResolveWithCallerWorkspace(ref, "other-workspace"); !errors.Is(
				err,
				media.ErrCrossWorkspaceRef,
			) {
				t.Fatalf("cross-workspace resolve: got %v", err)
			}
			if _, err := os.Stat(filepath.Clean(path)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
