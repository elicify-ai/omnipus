package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/task"
)

type fileEffectInvocation struct {
	ctx      context.Context
	identity FileEffectContext
	tool     string
	args     map[string]any
}

func bindFileEffect(ctx context.Context, handle *PathHandle, tool string, args map[string]any) {
	identity, _ := ctx.Value(fileEffectContextKey{}).(FileEffectContext)
	handle.fileEffect = &fileEffectInvocation{ctx: ctx, identity: identity, tool: tool, args: args}
}

func fileEffectLockKey(target string) string {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		target = strings.ToLower(target)
	}
	return "file-effect:" + filepath.Clean(target)
}

// mutateFileEffect serializes the actual pre-image, witness and atomic replacement.
func mutateFileEffect(h *PathHandle, needsContent bool, transform func([]byte, bool) ([]byte, error)) error {
	lock := task.TaskFileLock.Get(fileEffectLockKey(h.abs))
	lock.Lock()
	defer lock.Unlock()
	var data []byte
	var before FileImage
	var readErr error
	inv := h.fileEffect
	ctx := context.Background()
	if inv != nil {
		ctx = inv.ctx
	}
	read, err := ResolvePath(ctx, h.policy, "file_effect_read", "", FSOpRead, h.abs)
	if err == nil {
		defer read.Close()
		if needsContent {
			data, readErr = readFileEffectBytes(read)
			before = fileImage(data, readErr == nil)
		} else {
			before, readErr = readFileEffectImage(read)
		}
	} else {
		readErr = err
	}
	if needsContent && readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return readErr
	}
	if errors.Is(readErr, fs.ErrNotExist) {
		before = FileImage{}
		readErr = nil
	}
	exists := before.Exists
	if info, statErr := h.Stat(); statErr == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("file effect: target is not a regular file")
		}
		exists = true
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return statErr
	}
	after, err := transform(data, exists)
	if err != nil {
		return err
	}
	if inv != nil && inv.identity.SessionID != "" {
		w := FileEffectWitness{Tool: inv.tool, Path: fileEffectStringArg(inv.args, "path"), Target: h.abs,
			WorkspaceID: inv.identity.WorkspaceID, WorkDir: h.policy.WorkDir,
			SessionID: inv.identity.SessionID, Assistant: inv.identity.Assistant,
			ToolCallID: inv.identity.ToolCallID, Execution: inv.identity.Execution,
			Before: before, After: fileImage(after, true)}
		encoded, err := json.Marshal(inv.args)
		if err != nil {
			return err
		}
		w.ArgumentDigest = digestBytes(encoded)
		w.Anchor = fileEffectAnchor(h)
		w.AnchorIdentity, err = fileEffectDirectoryIdentity(w.Anchor)
		if err != nil {
			w.Unreconcilable = "target identity could not be established"
		}
		if readErr != nil {
			w.Unreconcilable = "pre-image read was not authorized or stable"
		}
		if w.Assistant.EntryID == "" || w.Execution.RunID == "" || w.Execution.BootSeq == 0 {
			return fmt.Errorf("file effect: admitted occurrence identity is missing")
		}
		p, err := FileEffectWitnessPath(inv.identity.Home, w.SessionID, w.Assistant, w.ToolCallID)
		if err != nil {
			return err
		}
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("file effect: occurrence already has a durable write witness")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := saveFileEffectWitness(inv.identity.Home, w); err != nil {
			return fmt.Errorf("file effect: witness was not saved; target was not written: %w", err)
		}
		if err := fileEffectBeforeWrite(w); err != nil {
			return err
		}
	}
	return h.WriteFile(after)
}

func fileEffectAnchor(h *PathHandle) string {
	anchor := h.policy.WorkDir
	for _, root := range h.policy.AllowedRoots {
		rel, err := filepath.Rel(root, h.abs)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && len(root) > len(anchor) {
			anchor = root
		}
	}
	return anchor
}

func fileEffectStringArg(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return value
}
