// unified_conv_agentstores.go: the per-agent half of the one-time CONV cutover
// (session-core U2, DEL-10 step 1; ARCHITECT-ANSWER-DEL10.md). Before the
// cutover every agent had its own session store at <agent home>/sessions; after
// it there is ONE shared store, so what the per-agent stores hold moves there
// under the same ids, before any runtime reader of the per-agent stores is
// removed. Nothing here is a permanent importer: once the sources are retired
// there is nothing left to find.
package session

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// convAgentSources returns the distinct per-agent session directories to take
// in, excluding the shared directory itself.
func convAgentSources(sharedDir string, dirs []string) []string {
	shared := filepath.Clean(sharedDir)
	seen := map[string]bool{shared: true}
	var out []string
	for _, d := range dirs {
		d = filepath.Clean(d)
		if d == "" || d == "." || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// convMoveAgentSessionDirs moves every full session directory (one with a
// meta.json) of a per-agent store into the shared store under the same id. A
// directory that already exists in the shared store is a conflict: the cutover
// refuses visibly and the source stays where it is. A directory without a
// meta.json is not a session this pass can place; it is left alone with a WARN.
func convMoveAgentSessionDirs(sharedDir, agentDir string) error {
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("conversion: read per-agent store %s: %w", agentDir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		src := filepath.Join(agentDir, name)
		if _, statErr := os.Stat(filepath.Join(src, "meta.json")); statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				slog.Warn("conversion: per-agent directory is not a saved session; left in place", "dir", src)
				continue
			}
			return fmt.Errorf("conversion: per-agent session %q: %w", name, statErr)
		}
		dst := filepath.Join(sharedDir, name)
		if _, statErr := os.Stat(dst); statErr == nil {
			return fmt.Errorf("conversion: per-agent session %q (%s) already exists in the shared store (%s); refusing cutover, source retained",
				name, src, dst)
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return fmt.Errorf("conversion: per-agent session %q: stat destination: %w", name, statErr)
		}
		if err := os.MkdirAll(sharedDir, 0o700); err != nil {
			return fmt.Errorf("conversion: create shared store: %w", err)
		}
		if err := os.Rename(src, dst); err != nil {
			// A rename across devices copies, publishes, then retires the source.
			if cerr := convCopyTree(src, dst); cerr != nil {
				_ = os.RemoveAll(dst)
				return fmt.Errorf("conversion: move per-agent session %q: %w", name, errors.Join(err, cerr))
			}
			if rerr := os.RemoveAll(src); rerr != nil {
				return fmt.Errorf("conversion: retire per-agent session %q: %w", name, rerr)
			}
		}
	}
	return nil
}

func convCopyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		in, oerr := os.Open(path)
		if oerr != nil {
			return oerr
		}
		defer in.Close()
		out, cerr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if cerr != nil {
			return cerr
		}
		if _, copyErr := io.Copy(out, in); copyErr != nil {
			_ = out.Close()
			return copyErr
		}
		return out.Close()
	})
}
