// NOTE: this tag applies to every file in pkg/gateway — it is a package-wide
// constraint enforcing CGO_ENABLED=0 for the single-binary open-source build.

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
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
// Retention: retentionSweep is called by gateway.executeSweepTick on the same
// schedule as the transcript sweep, using the same retentionDays config.
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
// write error (callers should fall back to inline or truncated behavior).
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

// retentionSweep deletes tool-result files whose mtime is older than
// retentionDays*24h. Returns the count of files deleted.
//
// Mirrors session.RetentionSweep semantics:
//   - retentionDays <= 0 → no-op (0, nil)
//   - per-file delete errors are logged at Warn and the sweep continues
//   - an error is returned only if the base directory walk cannot start
//   - session subdirectories that contain zero remaining files are removed
//
// Called by gateway.executeSweepTick alongside the transcript sweep.
func (s *toolResultStore) retentionSweep(retentionDays int) (int, error) {
	if s == nil || s.homePath == "" || retentionDays <= 0 {
		return 0, nil
	}

	base := filepath.Join(s.homePath, "tool_results")
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return 0, nil
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	removed := 0
	touchedDirs := make(map[string]struct{})

	err := filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == base {
				return walkErr
			}
			slog.Warn("tool_result_store: retention_sweep: walk error", "path", path, "error", walkErr)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			slog.Warn("tool_result_store: retention_sweep: stat failed", "file", path, "error", err)
			return nil
		}
		if info.ModTime().Before(cutoff) {
			if rmErr := os.Remove(path); rmErr != nil {
				slog.Warn("tool_result_store: retention_sweep: delete failed", "file", path, "error", rmErr)
			} else {
				removed++
				touchedDirs[filepath.Dir(path)] = struct{}{}
			}
		}
		return nil
	})
	if err != nil {
		return removed, err
	}

	// Sweep empty per-session dirs.
	for dir := range touchedDirs {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			continue
		}
		if len(entries) > 0 {
			continue
		}
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			slog.Warn("tool_result_store: retention_sweep: dir remove failed", "dir", dir, "error", rmErr)
		}
	}
	return removed, nil
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
// it returns (nil, false) signaling "keep original".
//
// Callers must check the bool; when false, the original result value should be
// used unchanged.
//
// When the store write fails (e.g. disk full, EPERM), this function emits a
// TruncatedResult sentinel instead of re-inlining the large body.  Re-inlining
// would regress to the pre-offload behavior and risk OOM on constrained
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

// structuredFailureDiscriminators are the fixed `error` values that mark a
// tool result string as a STRUCTURED failure payload rather than prose.
//
// Each corresponds to a contract schema whose producer writes it as the tool's
// ForLLM verbatim, so the calling agent and the SPA read the same bytes:
//   - "delegation_denied" → DelegationFailure   (pkg/tools.DelegationDeniedResult)
//   - "file_exists"       → FileExistsRefusal   (pkg/tools.FileExistsRefusalResult)
//
// A discriminator that is not in this set is left as prose and forwarded
// unchanged, which is why an unrelated `{"error":"timeout"}` from some other
// tool is not mistaken for one of these.
var structuredFailureDiscriminators = map[string]struct{}{
	tools.DelegationDeniedCode:  {},
	tools.FileExistsRefusalCode: {},
}

// unparseableFailureWarns counts how many unparseable structured payloads we
// have seen, so the warning below can be logged sparsely.
var unparseableFailureWarns atomic.Uint64

// warnUnparseableStructuredFailure logs the 1st, 10th, 100th… occurrence.
//
// Sparse on purpose. This fires from two hot paths: the per-CONNECTION event
// forwarder (three open tabs on one chat means three identical warns per tool
// result) and the replay reconstruction, which walks every persisted tool call
// on every attach — and attach happens on page load, session switch, and each
// WS reconnect. A session holding 200 bad payloads would emit 200 lines per
// reconnect, into a log this project already has a documented flooding hazard
// with. The signal is identical every time, so the first one is the whole
// message; the rest is volume.
func warnUnparseableStructuredFailure(err error, payload string) {
	n := unparseableFailureWarns.Add(1)
	if n != 1 && n%10 != 0 {
		return
	}
	slog.Warn("gateway: structured tool-failure payload failed to parse; rendering as prose",
		"error", err,
		"len", len(payload),
		// truncateRunesForFrame, not payload[:60] — a byte slice can cut a
		// multi-byte rune in half and put invalid UTF-8 into a log field.
		"prefix", truncateRunesForFrame(payload, 60),
		"occurrences", n)
}

// parseStructuredToolFailure inspects a tool result string and, when it is one
// of the structured failure payloads above, returns the parsed object (for the
// frame's `result` field), the human-readable reason (for the frame's `error`
// field), and true. Otherwise it returns (nil, "", false) and the caller
// forwards the raw string unchanged.
//
// This is what stops the SPA from rendering a raw JSON blob where it used to
// render a sentence: the payload becomes a typed object the client can match
// on, and the prose reason is lifted into the field that renderers already
// show. BOTH the live path (websocket.go) and the replay reconstruction
// (replay.go) must call it — a codebase that maintains live/replay parity
// deliberately cannot afford one of them parsing and the other not.
func parseStructuredToolFailure(result string) (obj map[string]any, reason string, ok bool) {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" || trimmed[0] != '{' {
		return nil, "", false
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		// A string that OPENS like one of our payloads but does not parse is
		// almost certainly one that got truncated downstream — the producers
		// bound their fields precisely so this cannot happen, so if it does,
		// something moved. Silence here would render a JSON fragment to the
		// user with no trace of why, which is how this class stays invisible.
		if strings.HasPrefix(trimmed, `{"error":"`) {
			warnUnparseableStructuredFailure(err, trimmed)
		}
		return nil, "", false
	}
	disc, _ := parsed["error"].(string)
	if _, known := structuredFailureDiscriminators[disc]; !known {
		return nil, "", false
	}
	reason, _ = parsed["reason"].(string)
	return parsed, reason, true
}
