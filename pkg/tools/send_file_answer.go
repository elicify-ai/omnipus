// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
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
		return t.resolveAnswerRef(ctx, source)
	}
	file, err := t.resolveAnswerPath(ctx, source)
	if err != nil {
		return ReplyMedia{}, err
	}
	return ReplyMedia{
		Identity: "path:" + file.resolved, Filename: filepath.Base(file.resolved), Size: file.size, Open: file.open,
	}, nil
}

// resolveAnswerRef resolves a non-workspace media reference the agent's own
// chat can send (a registered file such as a screenshot). The stored file is
// read through send_file's own checks, like a path.
func (t *SendFileTool) resolveAnswerRef(ctx context.Context, ref string) (ReplyMedia, error) {
	if t.mediaStore == nil {
		return ReplyMedia{}, errors.New("the media store is not available")
	}
	path, meta, err := t.mediaStore.ResolveWithMeta(ref)
	if err != nil {
		return ReplyMedia{}, fmt.Errorf("the media reference could not be resolved: %w", err)
	}
	file, err := t.resolveAnswerPath(ctx, path)
	if err != nil {
		return ReplyMedia{}, err
	}
	name := meta.Filename
	if name == "" {
		name = filepath.Base(file.resolved)
	}
	return ReplyMedia{Identity: ref, Filename: name, Size: file.size, Open: file.open}, nil
}

// answerPathFile is one checked answer file: its real path, its size and the
// reader the group stages it with.
type answerPathFile struct {
	resolved string
	size     int64
	open     func() (io.ReadCloser, error)
}

// resolveAnswerPath checks raw with send_file's ResolvePath chokepoint
// (FSOpSend, read grant, send_file's allow patterns). Its open function
// resolves raw again when the group stages the file and reads it through the
// PathHandle, so the read is confined by the same check and a path that now
// resolves to a different file is refused instead of read.
func (t *SendFileTool) resolveAnswerPath(ctx context.Context, raw string) (answerPathFile, error) {
	policy, err := ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		return answerPathFile{}, fmt.Errorf("the file access rules could not be read: %w", err)
	}
	handle, err := resolveAutoCheckedPath(ctx, policy, t.Name(), FSOpSend, raw, t.allowPaths, fspolicy.PathGrantAccessRead)
	if err != nil {
		return answerPathFile{}, fmt.Errorf("the file is outside what you may send: %w", err)
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return answerPathFile{}, fmt.Errorf("the file was not found: %w", err)
	}
	if info.IsDir() {
		return answerPathFile{}, errors.New("the path is a directory, not a file")
	}
	if info.Size() > answerFileMaxBytes {
		return answerPathFile{}, fmt.Errorf("the file is %d bytes; a file can be at most %d bytes", info.Size(), answerFileMaxBytes)
	}
	resolved, err := handle.RealPath()
	if err != nil {
		return answerPathFile{}, fmt.Errorf("the file path could not be resolved: %w", err)
	}
	open := func() (io.ReadCloser, error) {
		h, err := resolveAutoCheckedPath(ctx, policy, t.Name(), FSOpSend, raw, t.allowPaths, fspolicy.PathGrantAccessRead)
		if err != nil {
			return nil, fmt.Errorf("the file is outside what you may send: %w", err)
		}
		defer h.Close()
		now, err := h.RealPath()
		if err != nil {
			return nil, fmt.Errorf("the file path could not be resolved: %w", err)
		}
		if now != resolved {
			return nil, errors.New("the file changed after it was checked")
		}
		// The opened file stays valid after the handle closes (PathHandle.Open).
		return h.Open()
	}
	return answerPathFile{resolved: resolved, size: info.Size(), open: open}, nil
}
