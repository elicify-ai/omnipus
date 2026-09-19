//go:build linux || darwin

package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReadImage_NamedFIFOIsRefusedWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/stream.png"
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := NewReadFileTool(dir, true, MaxReadFileSize).Execute(ctx, map[string]any{"path": "stream.png"})
	if !result.IsError || !strings.Contains(result.ForLLM, "image source must be a regular file") {
		t.Fatalf("FIFO result=%#v", result)
	}
}
