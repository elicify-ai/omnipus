//go:build !cgo

// NOTE: this tag applies to every file in pkg/gateway — it is a package-wide
// constraint enforcing CGO_ENABLED=0 for the single-binary open-source build.

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// InlineToolResultMaxBytes is the threshold above which a tool result is offloaded to disk and replaced with a ToolResultRef sentinel in the WS frame.
const InlineToolResultMaxBytes = 50 * 1024 // 50 KiB

// toolResultPreviewBytes is the number of bytes of the raw JSON body that are
// included as the preview field in the generated.ToolResultRef sentinel.
const toolResultPreviewBytes = 4 * 1024 // 4 KiB

// ErrToolResultNotFound is returned by readByRef when the requested ref does
// not exist under the given session directory.
var ErrToolResultNotFound = errors.New("tool result not found")

// toolResultStore handles persistence of tool results that exceed InlineToolResultMaxBytes.
//
// Files are written to $OMNIPUS_HOME/tool_results/<session_id>/<ref>.json (mode 0600).
// TODO(retention): files accumulate indefinitely; wire into session retention sweep.
//
// A nil *toolResultStore is valid; all methods are no-ops and return appropriate zero/fallback values.
type toolResultStore struct {
	homePath string
	// offloadFailureTotal counts saveJSON failures since process start.
	offloadFailureTotal atomic.Int64
}

// newToolResultStore creates a store whose root is $homePath/tool_results/.
// If homePath is empty the store is disabled (all writes are silently skipped).
func newToolResultStore(homePath string) *toolResultStore {
	return &toolResultStore{homePath: homePath}
}

// saveJSON writes body (raw JSON bytes) to disk and returns the opaque ref ID.
// Returns ("", false) when the store is disabled (homePath == "") or on any
// write error (callers should fall back to inline or truncated behaviour).
func (s *toolResultStore) saveJSON(sessionID string, body []byte) (ref string, ok bool) {
	if s == nil || s.homePath == "" {
		return "", false
	}

	// Generate a 16-byte (32-char hex) random ref.
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		slog.Warn("tool_result_store: failed to generate ref",
			"event", "tool_result_ref_gen_error",
			"session_id", sessionID,
			"error", err,
		)
		return "", false
	}
	ref = hex.EncodeToString(raw[:])

	dir := filepath.Join(s.homePath, "tool_results", sessionID)
	path := filepath.Join(dir, ref+".json")

	if err := fileutil.WriteFileAtomic(path, body, 0o600); err != nil {
		slog.Warn("tool_result_store: failed to write result body",
			"event", "tool_result_write_error",
			"session_id", sessionID,
			"ref", ref,
			"error", err,
		)
		return "", false
	}
	return ref, true
}

// readByRef reads $homePath/tool_results/<sessionID>/<ref>.json and returns the
// file contents.  Both sessionID and ref are validated by the caller; this
// method adds a belt-and-braces path-escape check before the stat.
//
// Returns ErrToolResultNotFound when the file does not exist.
func (s *toolResultStore) readByRef(sessionID, ref string) ([]byte, error) {
	if s == nil || s.homePath == "" {
		return nil, fmt.Errorf("tool result store not configured")
	}
	if err := validateEntityID(ref); err != nil {
		return nil, fmt.Errorf("invalid ref: %w", err)
	}
	if err := validateEntityID(sessionID); err != nil {
		return nil, fmt.Errorf("invalid session id: %w", err)
	}

	dir := filepath.Join(s.homePath, "tool_results", sessionID)
	target := filepath.Join(dir, ref+".json")
	// Belt-and-braces: ensure the resolved path is under the expected directory.
	if !filepath.IsAbs(target) || !hasPathPrefix(target, dir) {
		return nil, ErrToolResultNotFound
	}

	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrToolResultNotFound
		}
		return nil, fmt.Errorf("tool_result_store: read %s: %w", ref, err)
	}
	return data, nil
}

// hasPathPrefix reports whether p is rooted at prefix (both must be clean absolute paths).
func hasPathPrefix(p, prefix string) bool {
	p = filepath.Clean(p)
	prefix = filepath.Clean(prefix)
	if p == prefix {
		return true
	}
	return len(p) > len(prefix) && p[len(prefix)] == filepath.Separator && p[:len(prefix)] == prefix
}

// maybeOffloadResult checks whether the JSON-encoded size of result exceeds
// InlineToolResultMaxBytes.  If so, it persists the body to disk and returns a
// generated.ToolResultRef sentinel.  If the result is within the inline limit
// it returns (nil, false) signalling "keep original".
//
// Callers must check the bool; when false, the original result value should be
// used unchanged.
//
// When the store write fails (e.g. disk full, EPERM), this function emits a
// TruncatedResult sentinel instead of re-inlining the large body.  Re-inlining
// would regress to the pre-offload behaviour and risk OOM on constrained
// clients (iPad, mobile SPA).
func maybeOffloadResult(
	store *toolResultStore,
	sessionID string,
	encoded []byte,
) (sentinel any, offloaded bool) {
	if len(encoded) <= InlineToolResultMaxBytes {
		return nil, false
	}
	// Size is in (InlineToolResultMaxBytes, replayMaxResultBytes]:
	// persist the body and emit a ToolResultRef sentinel.
	// When the store is nil/disabled, fall through to inline.
	// The result will be picked up by truncateResult if it also exceeds
	// replayMaxResultBytes (the 1 MiB hard cap).
	if store == nil || store.homePath == "" {
		return nil, false
	}

	preview := encoded
	if len(preview) > toolResultPreviewBytes {
		preview = encoded[:toolResultPreviewBytes]
	}

	ref, ok := store.saveJSON(sessionID, encoded)
	if !ok {
		// Write failed (disk full, EPERM, etc.).  Do NOT re-inline the large body —
		// that would regress to the pre-offload OOM scenario on mobile clients.
		// Emit a TruncatedResult sentinel so the user sees a clear message.
		store.offloadFailureTotal.Add(1)
		slog.Warn("tool_result_store: offload failed — emitting truncated sentinel",
			"event", "tool_result_offload_failure",
			"session_id", sessionID,
			"size_bytes", len(encoded),
			"failure_total", store.offloadFailureTotal.Load(),
		)
		return map[string]any{
			"_truncated":          true,
			"original_size_bytes": len(encoded),
			"preview":             string(preview),
		}, true
	}

	slog.Info("tool_result_store: offloaded oversized result",
		"event", "tool_result_offloaded",
		"session_id", sessionID,
		"ref", ref,
		"size_bytes", len(encoded),
	)

	// Use the generated ToolResultRef type (hard constraint #8).
	return generated.ToolResultRef{
		IsRef:             true,
		Ref:               ref,
		OriginalSizeBytes: len(encoded),
		Preview:           string(preview),
	}, true
}
