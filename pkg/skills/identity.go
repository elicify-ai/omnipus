package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/skillname"
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

// BindGrants is §5.13a's one write rule for an agent persist: it observes the
// granted names under root and returns the wholesale skill key → generation
// table. An installed name binds the generation read from its folder's stamp
// (an unstamped folder is stamped fresh first, then bound); a name that is
// not installed stays unbound (no entry), never a refusal. A nil table means
// no name is bound. The caller must hold root's mutation lock
// (WithMutationLock), because stamping is a write.
func BindGrants(root string, names []string) (map[string]string, error) {
	root = filepath.Clean(root)
	var out map[string]string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !ValidSlug(name) {
			continue
		}
		key := SkillKey(name)
		if _, done := out[key]; done {
			continue
		}
		onDisk, present, err := resolveLiveFolder(root, name)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		gen, err := EnsureGeneration(filepath.Join(root, onDisk))
		if err != nil {
			return nil, fmt.Errorf("bind skill %q: %w", onDisk, err)
		}
		if out == nil {
			out = map[string]string{}
		}
		out[key] = gen
	}
	return out, nil
}

// resolveLiveFolder finds the installed folder whose skill key equals slug's
// key. It returns the on-disk slug and whether it exists. An exact-name match
// wins over a case twin (§5.13 already-present case twins are left alone).
//
// slug is joined into a path only after the skill-name check right here (one
// path segment, no separators or dot entries); callers validate as well, but
// every skill folder, history and version path derives from the name this
// returns, so the containment rule sits at this one join. A name that fails
// the check resolves only to a folder actually listed under root; if none
// matches it is refused, never returned for a caller to build a path from.
func resolveLiveFolder(root, slug string) (string, bool, error) {
	if len(slug) <= skillname.MaxLength && skillname.Matcher().MatchString(slug) {
		if info, err := os.Lstat(filepath.Join(root, slug)); err == nil && info.IsDir() {
			return slug, true, nil
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return absentLiveFolder(slug)
		}
		return "", false, fmt.Errorf("list skills folder: %w", err)
	}
	key := SkillKey(slug)
	twin := ""
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !e.IsDir() {
			continue
		}
		if name == slug {
			return name, true, nil
		}
		if twin == "" && SkillKey(name) == key {
			twin = name
		}
	}
	if twin != "" {
		return twin, true, nil
	}
	return absentLiveFolder(slug)
}

// absentLiveFolder is resolveLiveFolder's answer for a name with no folder:
// a valid name is returned for the caller's absent-target path; any other
// name is refused.
func absentLiveFolder(slug string) (string, bool, error) {
	if len(slug) <= skillname.MaxLength && skillname.Matcher().MatchString(slug) {
		return slug, false, nil
	}
	return "", false, fmt.Errorf("%q is not a valid skill name", slug)
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
