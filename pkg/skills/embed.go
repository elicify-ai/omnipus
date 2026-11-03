package skills

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// embeddedSkills holds the default skill set compiled into the binary so a fresh
// install ships with a working set of skills without any external files
// (FR-9.3). The directory layout mirrors a skills root: embedded/<name>/SKILL.md.
//
//go:embed all:embedded
var embeddedSkills embed.FS

// embeddedRoot is the prefix under which skills live inside embeddedSkills.
const embeddedRoot = "embedded"

// DefaultSkillNames returns the names of the skills embedded into the binary.
// Order is deterministic (directory order from the embed FS).
func DefaultSkillNames() []string {
	entries, err := fs.ReadDir(embeddedSkills, embeddedRoot)
	if err != nil {
		// embed.FS is built at compile time; a read error here is a programmer
		// error, not a runtime condition.
		slog.Error("skills: failed to read embedded skills root", "error", err)
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// ExportEmbeddedPackage materializes one compiled-in skill package at destDir.
// It is the runtime bridge for helpers and knowledge files that the ordinary
// SKILL.md resolver deliberately does not expose by host path.
func ExportEmbeddedPackage(name, destDir string) error {
	if !ValidSlug(name) {
		return fmt.Errorf("export embedded skill: invalid name %q", name)
	}
	info, err := fs.Stat(embeddedSkills, embeddedRoot+"/"+name)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("export embedded skill %q: not found", name)
	}
	if _, err := os.Lstat(destDir); err == nil {
		return fmt.Errorf("export embedded skill %q: destination exists", name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return copyEmbeddedSkill(name, destDir)
}

// copyEmbeddedSkill copies every file under embedded/<name>/ into destSkillDir.
// The copy is staged into a temporary sibling directory and renamed into place
// so a partial copy is never observable as a real skill directory.
func copyEmbeddedSkill(name, destSkillDir string) error {
	srcRoot := embeddedRoot + "/" + name

	parent := filepath.Dir(destSkillDir)
	tmpDir, err := os.MkdirTemp(parent, ".seed-"+name+"-*")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	// Best-effort cleanup if we fail before the rename.
	committed := false
	defer func() {
		if !committed {
			if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
				slog.Warn("skills: failed to clean up staging dir", "dir", tmpDir, "error", rmErr)
			}
		}
	}()

	walkErr := fs.WalkDir(embeddedSkills, srcRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(srcRoot, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(tmpDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := embeddedSkills.ReadFile(p)
		if readErr != nil {
			return fmt.Errorf("read embedded file %q: %w", p, readErr)
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if walkErr != nil {
		return walkErr
	}

	// UAT batch3 S68 (see authoring.go's builtinMarkerFile doc comment): stamp
	// the pristine-builtin provenance marker alongside SKILL.md, inside the
	// same staging directory that gets atomically renamed into place below —
	// so the marker and the seeded content always appear together, never one
	// without the other, and EditSkill can tell "this is still the shipped
	// built-in, never edited" from "this is a genuine prior user override"
	// without relying on mere file existence.
	if err := os.WriteFile(filepath.Join(tmpDir, builtinMarkerFile), []byte{}, 0o644); err != nil {
		return fmt.Errorf("write builtin marker: %w", err)
	}

	if err := os.Rename(tmpDir, destSkillDir); err != nil {
		return fmt.Errorf("commit staged skill: %w", err)
	}
	committed = true
	return nil
}
