package skills

// §7.2 validation per operation, §7.4 secret check, §9.5 restoration
// eligibility (the one complete restore gate).

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/pathsafe"
)

// MaxCompatibilityLength bounds the frontmatter `compatibility` field.
const MaxCompatibilityLength = 500

// refusal is a §7.2 refusal: the code sentinel plus the reason text that
// follows "the change to '<slug>' was not saved: ".
type refusal struct {
	code   error
	reason string
}

func (r *refusal) Error() string { return r.reason }

func saveCheck(reason string) *refusal { return &refusal{code: ErrSaveCheckFailed, reason: reason} }

// validateRelPath applies §7.2's path rules to one relative path: no
// traversal, no absolute path, Windows-safe components on every platform
// (pkg/pathsafe), and — for authoring paths — no dot-component.
func validateRelPath(rel string, authoring bool) *refusal {
	if rel == "" {
		return saveCheck("the path is empty")
	}
	slash := filepath.ToSlash(rel)
	if filepath.IsAbs(rel) || path.IsAbs(slash) || filepath.VolumeName(rel) != "" {
		return saveCheck(fmt.Sprintf("'%s' is not a relative path inside the skill", rel))
	}
	if path.Clean(slash) != slash {
		return saveCheck(fmt.Sprintf("'%s' is not a normalized path inside the skill", rel))
	}
	for _, comp := range strings.Split(slash, "/") {
		if comp == ".." || comp == "." || comp == "" {
			return saveCheck(fmt.Sprintf("'%s' leaves the skill folder", rel))
		}
		if authoring && strings.HasPrefix(comp, ".") {
			return saveCheck(fmt.Sprintf("'%s' has a dot-component; skill paths cannot start a component with '.'", rel))
		}
		if strings.Contains(comp, ":") {
			return saveCheck(fmt.Sprintf("'%s' contains ':'", rel))
		}
		if err := pathsafe.WindowsRules.ValidateComponent(comp); err != nil {
			return saveCheck(fmt.Sprintf("'%s' is not a portable file name (%v)", rel, err))
		}
	}
	if err := pathsafe.WindowsRules.ValidateRelPathLength(slash); err != nil {
		return saveCheck(fmt.Sprintf("'%s' is too long (%v)", rel, err))
	}
	first := strings.SplitN(slash, "/", 2)[0]
	if isControlEntry(first) {
		return saveCheck(fmt.Sprintf("'%s' is Omnipus control metadata and cannot be written", rel))
	}
	return nil
}

// caseFoldConflict reports an existing entry that differs from rel only by
// letter case (#1040: a second skill.md beside SKILL.md is refused).
func caseFoldConflict(entries map[string]treeEntry, rel string) string {
	if _, ok := entries[rel]; ok {
		return ""
	}
	if strings.EqualFold(rel, "SKILL.md") && rel != "SKILL.md" {
		return "SKILL.md"
	}
	for p := range entries {
		if strings.EqualFold(p, rel) {
			return p
		}
	}
	return ""
}

// checkLiveWritePath enforces the live-disk half of the write path rules:
// the written path stays inside after symlink resolution, is not a symlink
// and is not hard-linked.
func checkLiveWritePath(liveDir, rel string) *refusal {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	cur := liveDir
	for i, comp := range parts {
		cur = filepath.Join(cur, comp)
		fi, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return saveCheck(fmt.Sprintf("'%s' cannot be inspected (%v)", rel, err))
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return saveCheck(fmt.Sprintf("'%s' is a symlink; a skill write never goes through a link", rel))
		}
		last := i == len(parts)-1
		if !last && !fi.IsDir() {
			return saveCheck(fmt.Sprintf("'%s' is not inside a folder", rel))
		}
		if last {
			if fi.IsDir() {
				return saveCheck(fmt.Sprintf("'%s' is a folder", rel))
			}
			if !fi.Mode().IsRegular() {
				return saveCheck(fmt.Sprintf("'%s' is not a regular file", rel))
			}
			if hardLinkCount(fi) > 1 {
				return saveCheck(fmt.Sprintf("'%s' is hard-linked; a skill write never goes through a hard link", rel))
			}
		}
	}
	return nil
}

// instructionBytes returns the bytes of the would-be state's instruction
// entry, resolved within the entry table (§9.5 captured-domain rule).
func instructionBytes(entries map[string]treeEntry) ([]byte, instructionRef, error) {
	ref := resolveInstructionInEntries(entries)
	if ref.state != instructionRegular && ref.state != instructionLinked {
		return nil, ref, nil
	}
	e := entries[ref.rel]
	if e.Size > MaxSkillMarkdownBytes {
		return nil, ref, &instructionLimitError{rel: ref.rel, n: e.Size}
	}
	b, err := entryBytes(e)
	return b, ref, err
}

// entryBytes reads an entry's content (in-memory bytes or its blob file),
// verifying the blob hash on read.
func entryBytes(e treeEntry) ([]byte, error) {
	if e.content != nil {
		return e.content, nil
	}
	if e.Type == entryTypeSymlink {
		return []byte(e.LinkTarget), nil
	}
	if e.blob == "" {
		return nil, fmt.Errorf("no stored bytes for '%s'", e.Path)
	}
	b, err := os.ReadFile(e.blob)
	if err != nil {
		return nil, fmt.Errorf("read stored bytes for '%s': %w", e.Path, err)
	}
	if sha256Hex(b) != e.SHA256 {
		return nil, fmt.Errorf("stored bytes for '%s' do not match their recorded hash", e.Path)
	}
	return b, nil
}

// validateInstruction checks a SKILL.md for §7.2: frontmatter present, name
// equal to the slug, a valid description, compatibility at most 500
// characters, metadata values all strings.
func validateInstruction(slug string, content []byte) string {
	if int64(len(content)) > MaxSkillMarkdownBytes {
		return fmt.Sprintf("SKILL.md exceeds the instruction limit of %s bytes (%d bytes)", formatCount(MaxSkillMarkdownBytes), len(content))
	}
	front, _ := splitFrontmatter(string(content))
	if front == "" && !bytes.HasPrefix(content, []byte("---\n---")) {
		return "SKILL.md must start with a YAML frontmatter block (--- name/description ---)"
	}
	fm, err := parseFrontmatter(front)
	if err != nil {
		return fmt.Sprintf("the SKILL.md frontmatter is not valid YAML (%v)", err)
	}
	name, _ := fm["name"].(string)
	if name != slug {
		return fmt.Sprintf("the SKILL.md frontmatter name must be '%s'", slug)
	}
	desc, _ := fm["description"].(string)
	if err := ValidateSkillDescription(slug, desc); err != nil {
		return err.Error()
	}
	if raw, ok := fm["compatibility"]; ok && raw != nil {
		s, isStr := raw.(string)
		if !isStr {
			return "compatibility must be a string"
		}
		if n := len([]rune(s)); n > MaxCompatibilityLength {
			return fmt.Sprintf("compatibility must be at most %d characters (got %d)", MaxCompatibilityLength, n)
		}
	}
	if raw, ok := fm["metadata"]; ok && raw != nil {
		m, isMap := raw.(map[string]any)
		if !isMap {
			return "metadata must be a map of strings"
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, isStr := m[k].(string); !isStr {
				return fmt.Sprintf("metadata values must be strings (metadata.%s is not)", k)
			}
		}
	}
	return ""
}

// validateState runs §7.2's content checks over a whole would-be state:
// a valid instruction file, sizes and file count.
func validateState(slug string, entries map[string]treeEntry) *refusal {
	content, ref, err := instructionBytes(entries)
	if err != nil {
		var lim *instructionLimitError
		if errors.As(err, &lim) {
			return saveCheck(fmt.Sprintf("SKILL.md exceeds the instruction limit of %s bytes (%d bytes)", formatCount(MaxSkillMarkdownBytes), lim.n))
		}
		return &refusal{code: ErrStorage, reason: err.Error()}
	}
	switch ref.state {
	case instructionMissing:
		return saveCheck("the skill has no SKILL.md; write a valid SKILL.md")
	case instructionUnresolved:
		return saveCheck(fmt.Sprintf("SKILL.md is a symlink to '%s', which is not a regular file of the skill", ref.target))
	}
	if reason := validateInstruction(slug, content); reason != "" {
		return saveCheck(reason)
	}
	return validateSizes(entries)
}

func validateSizes(entries map[string]treeEntry) *refusal {
	if len(entries) > MaxSkillFiles {
		return saveCheck(fileCountReason())
	}
	var total int64
	paths := sortedPaths(entries)
	for _, p := range paths {
		e := entries[p]
		if e.Type != entryTypeFile {
			continue
		}
		if e.Size > MaxSkillFileBytes {
			return saveCheck(perFileLimitReason(p, e.Size))
		}
		total += e.Size
	}
	if total > MaxSkillFolderBytes {
		return saveCheck(totalLimitReason(total))
	}
	return nil
}

func sortedPaths(entries map[string]treeEntry) []string {
	out := make([]string, 0, len(entries))
	for p := range entries {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// secretSet is one decrypted enumeration of the store, used for one check
// and dropped afterwards (§7.4: values never kept).
type secretSet struct {
	values []credentials.NamedValue
}

// loadSecrets enumerates the store. A provider error fails closed with
// SECRET_CHECK_UNAVAILABLE naming the reason (§5.10) — damaged entry names
// only, never values.
func loadSecrets() (*secretSet, *refusal) {
	provider := SecretValuesProvider
	if provider == nil {
		return &secretSet{}, nil
	}
	values, err := provider()
	if err != nil {
		// §5.10 text form exactly: (locked | unreadable | N damaged entries:
		// NAMES). The underlying cause goes to the log for the operator, never
		// into the refusal text (it can carry paths).
		slog.Warn("skills: secret check unavailable; the change is refused", "error", err)
		reason := "the secret check could not be completed (" + secretUnavailableReason(err) + ")"
		return nil, &refusal{code: ErrSecretCheckUnavailable, reason: reason}
	}
	kept := values[:0:0]
	for _, v := range values {
		if len(v.Value) > 3 {
			kept = append(kept, v)
		}
	}
	return &secretSet{values: kept}, nil
}

func secretUnavailableReason(err error) string {
	var damaged *credentials.DamagedEntriesError
	switch {
	case errors.Is(err, credentials.ErrStoreLocked):
		return "locked"
	case errors.As(err, &damaged):
		return fmt.Sprintf("%d damaged entries: %s", len(damaged.Names), strings.Join(damaged.Names, ", "))
	default:
		return "unreadable"
	}
}

// scan refuses when any regular-file content or symlink target text of the
// state contains a stored value longer than 3 bytes (§7.4).
func (s *secretSet) scan(entries map[string]treeEntry) *refusal {
	if s == nil || len(s.values) == 0 {
		return nil
	}
	for _, p := range sortedPaths(entries) {
		e := entries[p]
		if e.Type == entryTypeSpecial {
			continue
		}
		b, err := entryBytes(e)
		if err != nil {
			return &refusal{code: ErrStorage, reason: err.Error()}
		}
		for _, v := range s.values {
			if bytes.Contains(b, []byte(v.Value)) {
				return &refusal{code: ErrSecretInSkill, reason: fmt.Sprintf("%s contains the value of stored secret %s", p, v.Name)}
			}
		}
	}
	return nil
}

// structuralIneligibility is the snapshot half of the restore gate (§9.5):
// every entry a regular file or an in-skill relative symlink, no special
// entry, a valid SKILL.md, sizes.
func structuralIneligibility(slug string, entries map[string]treeEntry) string {
	for _, p := range sortedPaths(entries) {
		e := entries[p]
		switch e.Type {
		case entryTypeSpecial:
			return "unsupported entry " + p
		case entryTypeSymlink:
			if !symlinkInside(p, e.LinkTarget) {
				return "symlink target outside the skill"
			}
		}
	}
	content, ref, err := instructionBytes(entries)
	if err != nil || (ref.state != instructionRegular && ref.state != instructionLinked) {
		return "no valid SKILL.md"
	}
	if validateInstruction(slug, content) != "" {
		return "no valid SKILL.md"
	}
	if r := validateSizes(entries); r != nil {
		return r.reason
	}
	return ""
}

// symlinkInside reports whether a link at rel with target text resolves
// inside the skill folder (relative, never escaping).
func symlinkInside(rel, target string) bool {
	if target == "" || filepath.IsAbs(target) || path.IsAbs(filepath.ToSlash(target)) || filepath.VolumeName(target) != "" {
		return false
	}
	joined := path.Clean(path.Join(path.Dir(rel), filepath.ToSlash(target)))
	return joined != ".." && !strings.HasPrefix(joined, "../") && joined != "."
}

// restoreGate evaluates the complete §7.2 restore gate for a version:
// structural eligibility, per-path rules over the recorded paths (no
// dot-component rule), and the secret check against the current store.
// It returns "" when the version passes, else the refusal.
func restoreGate(slug string, entries map[string]treeEntry, secrets *secretSet) *refusal {
	if reason := structuralIneligibility(slug, entries); reason != "" {
		return saveCheck(reason)
	}
	for _, p := range sortedPaths(entries) {
		if r := validateRelPath(p, false); r != nil {
			return r
		}
	}
	return secrets.scan(entries)
}

func entryMapOf(entries []treeEntry) map[string]treeEntry {
	m := make(map[string]treeEntry, len(entries))
	for _, e := range entries {
		m[e.Path] = e
	}
	return m
}

func entryListOf(m map[string]treeEntry) []treeEntry {
	out := make([]treeEntry, 0, len(m))
	for _, p := range sortedPaths(m) {
		out = append(out, m[p])
	}
	return out
}
