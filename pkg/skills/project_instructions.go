package skills

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxInstructionsBytes bounds the composed project-instructions block
// (ADR-072 D7, FR-043). It deliberately matches
// pkg/workspace/instructions.go's unexported maxInstructionsBytes (262144) —
// the same cap the workspace's own AGENT.md already enforces via
// ReadInstructions — but is its own constant here: this cap applies to the
// mounts' combined contribution (a distinct, second source on the same rail,
// D7), not to the workspace's own file, which enforces its cap independently.
const MaxInstructionsBytes = 262144

// projectInstructionFileNames are the recognised root instruction file
// names, in precedence order — CLAUDE.md wins when both exist (D7: "Root
// file only, both CLAUDE.md and AGENTS.md accepted, CLAUDE.md winning if
// both exist").
var projectInstructionFileNames = []string{"CLAUDE.md", "AGENTS.md"}

// ProjectInstructionMount is the narrow view of a mount that instruction
// composition needs: its operator-chosen name (the FR-044 ordering key and
// the label the composed block carries) and its root directory.
type ProjectInstructionMount struct {
	Name string
	Root string
}

// SelectProjectInstructionFile picks the single root instruction file for one
// mount (D7: root file only, not per-subdirectory) and returns its content.
// The choice is made by EXISTENCE, deterministically: CLAUDE.md is tried
// first and wins if present, regardless of whether AGENTS.md is also
// present; only when CLAUDE.md is absent is AGENTS.md tried.
//
// A chosen file that exists but cannot be read (Dataset D row 7: "file
// present but unreadable") contributes nothing — ok=false — and does NOT
// fall back to the other name: the selection is by name existence, not by
// readability, so a permission error on the winning name is not silently
// papered over by promoting the loser.
func SelectProjectInstructionFile(mountRoot string) (content, fileName string, ok bool) {
	for _, name := range projectInstructionFileNames {
		path := filepath.Join(mountRoot, name)
		if _, statErr := os.Lstat(path); statErr != nil {
			continue // this name is not present here — try the next one
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			slog.Warn("skills: project instruction file present but unreadable; contributing nothing",
				"path", path, "error", readErr)
			return "", "", false
		}
		return string(data), name, true
	}
	return "", "", false
}

// ComposeProjectInstructions builds ADR-072 D7's second source on the
// existing per-turn workspace-instructions rail: one labelled block per
// mount (its single chosen root instruction file, via
// SelectProjectInstructionFile), ordered by the same byte-wise ascending
// mount-name order FR-029/FR-044 use, so the result is deterministic
// regardless of input order.
//
// The composed block — the mounts' combined contribution, NOT the workspace's
// own instructions, which are capped separately upstream — is capped at
// MaxInstructionsBytes. When the cap binds, the content is cut at the budget
// and a visible marker is appended stating that truncation occurred
// (D7: "silently truncating instructions is worse than not loading them").
//
// A mount contributing no readable instruction file is silently omitted
// (FR-039-style silence) — this never fails the turn.
func ComposeProjectInstructions(mounts []ProjectInstructionMount) (composed string, truncated bool) {
	sorted := make([]ProjectInstructionMount, len(mounts))
	copy(sorted, mounts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var blocks []string
	for _, m := range sorted {
		content, _, ok := SelectProjectInstructionFile(m.Root)
		if !ok {
			continue
		}
		trimmed := strings.TrimRight(content, "\n")
		if trimmed == "" {
			continue
		}
		blocks = append(blocks, fmt.Sprintf("### %s\n\n%s", m.Name, trimmed))
	}
	if len(blocks) == 0 {
		return "", false
	}

	composed = strings.Join(blocks, "\n\n---\n\n")
	if len(composed) <= MaxInstructionsBytes {
		return composed, false
	}

	marker := fmt.Sprintf("\n\n[project instructions truncated at %d bytes — content past this point was cut]", MaxInstructionsBytes)
	cut := MaxInstructionsBytes - len(marker)
	if cut < 0 {
		cut = 0
	}
	if cut > len(composed) {
		cut = len(composed)
	}
	// Never cut mid-rune (ADR-072 Finding C). composed can contain multi-byte
	// UTF-8 characters (em-dash, curly quotes, non-ASCII names) from a
	// mount's CLAUDE.md/AGENTS.md, and a raw byte-offset slice can land
	// inside one of them — producing invalid UTF-8 injected directly into
	// the per-turn prompt block. Walk the cut point backward to the nearest
	// rune boundary: composed[:cut] is only ever shortened further, never
	// grown, so the byte budget (MaxInstructionsBytes) is never exceeded.
	for cut > 0 && cut < len(composed) && !utf8.RuneStart(composed[cut]) {
		cut--
	}
	composed = composed[:cut] + marker
	return composed, true
}
