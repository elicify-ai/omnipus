package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// skillIdentityFileName holds a skill folder's generation (§5.13a SD-30): one
// UUID-format line minted at the skill's creation event. It is Omnipus
// control metadata — excluded from the content revision, from capture, from
// restore and from the secret check, and never a writable path.
const skillIdentityFileName = ".omnipus-identity"

// skillOriginFile is the retired install-origin sidecar; still excluded from
// the content revision so legacy folders hash identically.
const skillOriginFile = ".skill-origin.json"

// SkillKey returns the one skill identity of a slug (§5.13): two slugs are
// the same skill when their keys are equal.
func SkillKey(slug string) string { return strings.ToLower(slug) }

// IsBuiltin reports whether slug's skill key is the key of a current built-in
// skill (§8.1). Case-insensitive.
func IsBuiltin(slug string) bool {
	key := SkillKey(slug)
	for _, name := range DefaultSkillNames() {
		if SkillKey(name) == key {
			return true
		}
	}
	return false
}

// RetiredBuiltinNames returns the retired built-in skill names (§8.4).
func RetiredBuiltinNames() []string {
	return []string{
		"agent-authoring",
		"channel-setup",
		"daily-briefing",
		"define-done",
		"delegation-graph",
		"doctor",
		"handoff",
		"mcp-install",
		"provider-setup",
		"skill-authoring",
		"skill-mapping",
		"summarize",
		"tool-mapping",
		"workspace-team",
	}
}

// GrantEditor is the one-method seam the gateway implements over the agent
// roster (§8.2). pkg/skills imports neither the gateway nor the agent store.
type GrantEditor interface {
	// RemoveGrant removes name (by skill key) from every agent's grant list,
	// in memory and in each stored record, changing only the skills list.
	// Idempotent.
	RemoveGrant(name string) error
}

// isControlEntry reports whether a top-level folder entry is Omnipus control
// metadata excluded from the content revision (§7.3).
func isControlEntry(rel string) bool {
	switch rel {
	case versionsDir, builtinMarkerFile, skillOriginFile, skillIdentityFileName:
		return true
	}
	return strings.HasPrefix(rel, versionsDir+"/")
}

// ReadGeneration returns the generation stamped in dir ("" when the folder is
// unstamped). A stamp that exists but cannot be read is an error.
func ReadGeneration(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, skillIdentityFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read skill generation: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// newGeneration mints a fresh, never-reused generation value.
func newGeneration() string { return uuid.NewString() }

// writeGeneration stamps dir with gen (one line).
func writeGeneration(dir, gen string) error {
	if err := fileutil.WriteFileAtomic(filepath.Join(dir, skillIdentityFileName), []byte(gen+"\n"), 0o644); err != nil {
		return fmt.Errorf("write skill generation: %w", err)
	}
	return nil
}

// EnsureGeneration returns dir's generation, stamping a fresh one first when
// the folder is unstamped (§5.13a: the grant path binds an outside-created
// folder by stamping it at that moment). The caller must hold the mutation
// lock of the folder's skills root.
func EnsureGeneration(dir string) (string, error) {
	gen, err := ReadGeneration(dir)
	if err != nil || gen != "" {
		return gen, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("skill %q is not a folder", dir)
	}
	gen = newGeneration()
	if err := writeGeneration(dir, gen); err != nil {
		return "", err
	}
	return gen, nil
}

// resolveLiveFolder finds the installed folder whose skill key equals slug's
// key. It returns the on-disk slug and whether it exists. An exact-name match
// wins over a case twin (§5.13 already-present case twins are left alone).
func resolveLiveFolder(root, slug string) (string, bool, error) {
	if info, err := os.Lstat(filepath.Join(root, slug)); err == nil && info.IsDir() {
		return slug, true, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return slug, false, nil
		}
		return slug, false, fmt.Errorf("list skills folder: %w", err)
	}
	key := SkillKey(slug)
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !e.IsDir() {
			continue
		}
		if SkillKey(name) == key {
			return name, true, nil
		}
	}
	return slug, false, nil
}

// ResolveInstalledSlug returns the on-disk slug of the installed skill whose
// key equals slug's key, or false when none is installed.
func ResolveInstalledSlug(root, slug string) (string, bool, error) {
	return resolveLiveFolder(root, slug)
}

// isStampedBuiltin reports whether dir carries the built-in stamp.
func isStampedBuiltin(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, builtinMarkerFile))
	return err == nil
}
