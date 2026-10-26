package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// FileImage is a private full-file digest, not file content or a wire format.
// not-wire-format: recovery persistence only.
type FileImage struct {
	Exists bool   `json:"exists"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// FileEffectWitness records an intended byte effect before atomic replacement.
// not-wire-format: private recovery persistence, never a tool response.
type FileEffectWitness struct {
	Tool           string                    `json:"tool"`
	Path           string                    `json:"path"`
	Target         string                    `json:"target"`
	WorkspaceID    string                    `json:"workspace_id"`
	WorkDir        string                    `json:"work_dir"`
	Anchor         string                    `json:"anchor"`
	AnchorIdentity string                    `json:"anchor_identity"`
	SessionID      string                    `json:"session_id"`
	Assistant      session.ArchiveAddress    `json:"assistant"`
	ToolCallID     string                    `json:"tool_call_id"`
	Execution      session.ExecutionIdentity `json:"execution"`
	ArgumentDigest string                    `json:"argument_digest"`
	Before         FileImage                 `json:"before"`
	After          FileImage                 `json:"after"`
	Unreconcilable string                    `json:"unreconcilable,omitempty"`
	ReadConfined   bool                      `json:"read_confined,omitempty"`
}

// FileEffectObservation proves current bytes, never a lost historical return.
// not-wire-format: private recovery evidence only.
type FileEffectObservation struct {
	Disposition string    `json:"disposition"`
	Reason      string    `json:"reason,omitempty"`
	Image       FileImage `json:"image"`
}

const (
	FileEffectPresent = "effect_present"
	FileEffectAbsent  = "effect_absent"
	FileEffectUnknown = "unknown"
)

type FileEffectReconciler interface {
	ReconcileFileEffect(context.Context, FileEffectWitness) FileEffectObservation
}

// FileEffectContext is supplied by the admitted execution, never model arguments.
type FileEffectContext struct {
	Home, SessionID, WorkspaceID, ToolCallID string
	Assistant                                session.ArchiveAddress
	Execution                                session.ExecutionIdentity
}

type fileEffectContextKey struct{}

func WithFileEffectContext(ctx context.Context, value FileEffectContext) context.Context {
	return context.WithValue(ctx, fileEffectContextKey{}, value)
}

var fileEffectHook struct {
	sync.RWMutex
	beforeWrite func(FileEffectWitness) error
}

// SetFileEffectBeforeWriteHook observes the durable witness before target mutation.
// A returned error prevents the write. Restore the observer after use.
func SetFileEffectBeforeWriteHook(hook func(FileEffectWitness) error) func() {
	fileEffectHook.Lock()
	previous := fileEffectHook.beforeWrite
	fileEffectHook.beforeWrite = hook
	fileEffectHook.Unlock()
	return func() { fileEffectHook.Lock(); fileEffectHook.beforeWrite = previous; fileEffectHook.Unlock() }
}

func fileEffectBeforeWrite(w FileEffectWitness) error {
	fileEffectHook.RLock()
	hook := fileEffectHook.beforeWrite
	fileEffectHook.RUnlock()
	if hook != nil {
		return hook(w)
	}
	return nil
}

func FileEffectKey(assistant session.ArchiveAddress, callID string) string {
	return digestBytes([]byte(assistant.EntryID + "\x00" + callID))
}

func FileEffectWitnessPath(home, sessionID string, assistant session.ArchiveAddress, callID string) (string, error) {
	if sessionID == "" || sessionID == "." || sessionID == ".." || strings.ContainsAny(sessionID, `/\\`) || !filepath.IsAbs(home) {
		return "", fmt.Errorf("file recovery: invalid storage identity")
	}
	return filepath.Join(home, "sessions", sessionID, "file_effects", FileEffectKey(assistant, callID)+".json"), nil
}

func ReadFileEffectWitness(home, sessionID string, assistant session.ArchiveAddress, callID string) (FileEffectWitness, error) {
	p, err := FileEffectWitnessPath(home, sessionID, assistant, callID)
	if err != nil {
		return FileEffectWitness{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return FileEffectWitness{}, err
	}
	var w FileEffectWitness
	if err := json.Unmarshal(data, &w); err != nil {
		return w, err
	}
	if err := fileEffectWitnessImagesPresent(data); err != nil {
		return w, err
	}
	if w.SessionID != sessionID || w.Assistant != assistant || w.ToolCallID != callID || w.Execution.RunID == "" || w.Execution.BootSeq == 0 || !filepath.IsAbs(w.Target) {
		return w, fmt.Errorf("file recovery: witness identity is invalid")
	}
	return w, nil
}

// fileEffectWitnessImagesPresent rejects a witness whose before/after images
// are absent, null, or structurally incomplete (no exists flag). JSON decoding
// alone cannot distinguish a missing image from recorded nonexistence — a
// missing/null/empty image unmarshals to the zero FileImage, which is exactly
// what proven nonexistence looks like — and DESIGN 2 forbids treating absent
// evidence as proof of an absent effect. Such a witness is corrupt evidence
// and must reconcile to Unknown, never to effect_absent.
func fileEffectWitnessImagesPresent(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, field := range []string{"before", "after"} {
		value, ok := raw[field]
		if !ok || string(value) == "null" {
			return fmt.Errorf("file recovery: witness %s image is missing or null", field)
		}
		var image struct {
			Exists *bool `json:"exists"`
		}
		if err := json.Unmarshal(value, &image); err != nil {
			return fmt.Errorf("file recovery: witness %s image is malformed and fails to decode: %w", field, err)
		}
		if image.Exists == nil {
			return fmt.Errorf("file recovery: witness %s image has no exists flag", field)
		}
	}
	return nil
}

func saveFileEffectWitness(home string, w FileEffectWitness) error {
	p, err := FileEffectWitnessPath(home, w.SessionID, w.Assistant, w.ToolCallID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(w)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomicSyncDir(p, data, 0o600)
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileImage(data []byte, exists bool) FileImage {
	if !exists {
		return FileImage{}
	}
	return FileImage{Exists: true, Size: int64(len(data)), SHA256: digestBytes(data)}
}

// FileEffectArgumentDigest binds a witness to the canonical encoded tool arguments.
func FileEffectArgumentDigest(encoded []byte) string { return digestBytes(encoded) }
