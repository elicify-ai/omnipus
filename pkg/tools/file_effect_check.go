package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func readFileEffectImage(h *PathHandle) (FileImage, error) {
	f, err := h.OpenRegularNonBlocking()
	if errors.Is(err, fs.ErrNotExist) {
		return FileImage{}, nil
	}
	if err != nil {
		return FileImage{}, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return FileImage{}, err
	}
	hash := sha256.New()
	n, err := io.CopyBuffer(hash, f, make([]byte, 32*1024))
	if err != nil {
		return FileImage{}, err
	}
	after, err := f.Stat()
	if err != nil {
		return FileImage{}, err
	}
	current, err := h.Stat()
	if err != nil {
		return FileImage{}, err
	}
	if !os.SameFile(before, after) || !os.SameFile(after, current) || before.Size() != n || after.Size() != n || !before.ModTime().Equal(after.ModTime()) {
		return FileImage{}, fmt.Errorf("file recovery: source changed while hashing")
	}
	return FileImage{Exists: true, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func readFileEffectBytes(h *PathHandle) ([]byte, error) {
	f, err := h.OpenRegularNonBlocking()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	current, err := h.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) || !os.SameFile(after, current) || before.Size() != int64(len(data)) || after.Size() != before.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("file recovery: pre-image changed while reading")
	}
	return data, nil
}

func reconcileFileEffect(ctx context.Context, agentHome string, restrict bool, tool string, w FileEffectWitness) FileEffectObservation {
	unknown := func(reason string) FileEffectObservation {
		return FileEffectObservation{Disposition: FileEffectUnknown, Reason: reason}
	}
	if w.Tool != tool || w.Unreconcilable != "" || w.Before == w.After {
		return unknown("missing, unreconcilable or indistinguishable file effect")
	}
	if ToolWorkspaceID(ctx) != w.WorkspaceID {
		return unknown("original workspace is no longer selected")
	}
	if w.ReadConfined {
		ctx = WithReadConfined(ctx, true) // Retain an original restriction, never a grant.
	}
	policy, err := ResolveTurnFSPolicy(ctx, agentHome, restrict)
	if err != nil || policy.WorkDir != w.WorkDir {
		return unknown("original workspace identity changed")
	}
	h, err := ResolvePath(ctx, policy, "file_effect_recovery", "", FSOpRead, w.Path)
	if err != nil {
		return unknown("current read authorization refused the target")
	}
	defer h.Close()
	if h.abs != w.Target || fileEffectAnchor(h) != w.Anchor {
		return unknown("target or mount changed")
	}
	identity, err := fileEffectDirectoryIdentity(w.Anchor)
	if err != nil || identity == "" || identity != w.AnchorIdentity {
		return unknown("target anchor identity changed")
	}
	lock := task.TaskFileLock.Get(fileEffectLockKey(h.abs))
	lock.Lock()
	defer lock.Unlock()
	if competing, err := otherPendingFileEffects(config.OmnipusHomeDir(), w); err != nil || competing {
		return unknown("unresolved-writer identity is missing, corrupt or competing")
	}
	image, err := readFileEffectImage(h)
	if err != nil {
		return unknown("target is unreadable, non-regular or unstable")
	}
	identityAfter, err := fileEffectDirectoryIdentity(w.Anchor)
	if err != nil || identityAfter != identity {
		return unknown("target anchor changed during readback")
	}
	switch image {
	case w.After:
		return FileEffectObservation{Disposition: FileEffectPresent, Image: image}
	case w.Before:
		return FileEffectObservation{Disposition: FileEffectAbsent, Image: image}
	default:
		return FileEffectObservation{Disposition: FileEffectUnknown, Reason: "current image differs from both expected images", Image: image}
	}
}

func (t *AppendFileTool) ReconcileFileEffect(ctx context.Context, w FileEffectWitness) FileEffectObservation {
	return reconcileFileEffect(ctx, t.agentHome, t.restrict, t.Name(), w)
}
func (t *EditFileTool) ReconcileFileEffect(ctx context.Context, w FileEffectWitness) FileEffectObservation {
	return reconcileFileEffect(ctx, t.agentHome, t.restrict, t.Name(), w)
}
func (t *WriteFileTool) ReconcileFileEffect(ctx context.Context, w FileEffectWitness) FileEffectObservation {
	return reconcileFileEffect(ctx, t.agentHome, t.restrict, t.Name(), w)
}
