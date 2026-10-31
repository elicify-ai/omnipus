// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// answerFileMaxBytes is the per-file limit of an answer's files (TD-3): the
// same 100 MB as one POST /upload part.
const answerFileMaxBytes = int64(100 << 20)

var _ AnswerFileResolver = (*SendFileTool)(nil)

// ResolveAnswerFile resolves one answer_request file that is not a workspace
// reference (#1449 FR-020): a media reference of the agent's own chat, or a
// path the agent could send to its own chat with send_file — the same
// filesystem policy, the same read grant, the same audit operation. The bytes
// are not read here; the group stages them once, at acceptance.
func (t *SendFileTool) ResolveAnswerFile(ctx context.Context, source string) (ReplyMedia, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return ReplyMedia{}, errors.New("the file is empty")
	}
	if strings.HasPrefix(source, "media://") {
		return t.resolveAnswerRef(source)
	}
	policy, err := ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		return ReplyMedia{}, fmt.Errorf("the file access rules could not be read: %w", err)
	}
	handle, err := resolveAutoCheckedPath(ctx, policy, t.Name(), FSOpSend, source, t.allowPaths, fspolicy.PathGrantAccessRead)
	if err != nil {
		return ReplyMedia{}, fmt.Errorf("the file is outside what you may send: %w", err)
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return ReplyMedia{}, fmt.Errorf("the file was not found: %w", err)
	}
	if info.IsDir() {
		return ReplyMedia{}, errors.New("the path is a directory, not a file")
	}
	if info.Size() > answerFileMaxBytes {
		return ReplyMedia{}, fmt.Errorf("the file is %d bytes; a file can be at most %d bytes", info.Size(), answerFileMaxBytes)
	}
	resolved, err := handle.RealPath()
	if err != nil {
		return ReplyMedia{}, fmt.Errorf("the file path could not be resolved: %w", err)
	}
	return ReplyMedia{
		Identity: "path:" + resolved, Filename: filepath.Base(resolved), Size: info.Size(),
		Open: func() (io.ReadCloser, error) { return os.Open(resolved) },
	}, nil
}

// resolveAnswerRef resolves a non-workspace media reference the agent's own
// chat can send (a registered file such as a screenshot).
func (t *SendFileTool) resolveAnswerRef(ref string) (ReplyMedia, error) {
	if t.mediaStore == nil {
		return ReplyMedia{}, errors.New("the media store is not available")
	}
	path, meta, err := t.mediaStore.ResolveWithMeta(ref)
	if err != nil {
		return ReplyMedia{}, fmt.Errorf("the media reference could not be resolved: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return ReplyMedia{}, fmt.Errorf("the media file was not found: %w", err)
	}
	if info.IsDir() || info.Size() > answerFileMaxBytes {
		return ReplyMedia{}, fmt.Errorf("the media file is not a file of at most %d bytes", answerFileMaxBytes)
	}
	name := meta.Filename
	if name == "" {
		name = filepath.Base(path)
	}
	return ReplyMedia{
		Identity: ref, Filename: name, Size: info.Size(),
		Open: func() (io.ReadCloser, error) { return os.Open(path) },
	}, nil
}
