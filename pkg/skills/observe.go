package skills

// One managed read of one skill (skills spec r15 §5.2, §7.3, §9.2): the
// content, the displayable file list and the revision come from ONE capture
// walk taken under the registry mutation lock, so a "seen" record built from
// the result certifies exactly the bytes the read returned. Reading never
// records a version (no baseline/external_change detection, §9.4).

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// SkillFile is one displayable file of an observation (§5.2 `files`).
type SkillFile struct {
	Path string
	Size int64
	Exec bool
}

// VersionRow is one retained version as get_skill lists it (§5.2 `versions`).
type VersionRow struct {
	ID         string
	Kind       string
	Actor      string
	Tool       string
	At         string
	Paths      []string // at most maxVersionRowPaths
	PathsTotal int
}

// LastChange is §5.2's `last_change`: exactly one of the three shapes is set.
type LastChange struct {
	At, Actor, Tool string
	Paths           []string
	ShippedWith     string // built-ins
	Unknown         bool   // outside change, or no readable history
}

// VersionQuery selects the `versions` page of an observation.
type VersionQuery struct {
	Want   bool
	Before string // "" = from the newest
	Limit  int    // 1–100; the caller validates
}

// SkillObservation is the result of ObserveSkill.
type SkillObservation struct {
	Slug            string // on-disk slug (the requested slug when absent)
	Dir             string
	Present         bool
	Deleted         bool // absent with retained history
	Builtin         bool
	Revision        string
	Instruction     []byte // the captured instruction entry's bytes (frontmatter included)
	HasInstruction  bool
	Files           []SkillFile // displayable files in path order (dot segments hidden)
	HasLocalChanges bool
	// Degraded is true when the capture aborted at a §7.2 limit on a helper
	// (§9.5 checkpoint encounters (i)): the instructions were read through
	// the bounded reader, the file list comes from a metadata-only walk, and
	// no revision is certified (Revision is "").
	Degraded      bool
	LastChange    LastChange
	Versions      []VersionRow
	VersionsTotal int
	NextBefore    string // "" = no older retained versions
}

const maxVersionRowPaths = 5

// ObserveSkill reads one skill under the registry mutation lock. An absent
// folder without history returns ErrNotFound. Instruction problems return
// the §5.10 SKILL_INSTRUCTION_LIMIT / SKILL_INSTRUCTION_UNRESOLVED errors as
// *ChangeError; an unknown q.Before returns ErrUnknownVersion as *ChangeError.
func ObserveSkill(root, slug string, q VersionQuery) (*SkillObservation, error) {
	var obs *SkillObservation
	err := WithMutationLock(root, func() error {
		var err error
		obs, err = observeLocked(root, slug, q)
		return err
	})
	if err != nil {
		return nil, err
	}
	return obs, nil
}

func observeLocked(root, slug string, q VersionQuery) (*SkillObservation, error) {
	onDisk, present, err := resolveLiveFolder(root, slug)
	if err != nil {
		return nil, fmt.Errorf("read skill '%s': %w", slug, err)
	}
	obs := &SkillObservation{Slug: onDisk, Dir: filepath.Join(root, onDisk), Present: present, Builtin: IsBuiltin(onDisk)}
	hist := historyFor(root, onDisk)
	ids, err := hist.ids()
	if err != nil {
		return nil, fmt.Errorf("read skill '%s': %w", slug, err)
	}
	if !present {
		if len(ids) == 0 {
			return nil, fmt.Errorf("%w: skill %q is not installed", ErrNotFound, slug)
		}
		obs.Deleted = true
	} else if capErr := obs.capture(); capErr != nil {
		if !errors.Is(capErr, ErrSaveCheckFailed) {
			return nil, capErr
		}
		if degErr := obs.degrade(); degErr != nil {
			return nil, degErr
		}
	}
	if obs.Builtin {
		obs.LastChange = LastChange{ShippedWith: shippedWithVersion()}
		obs.HasLocalChanges = false
		// Built-ins keep no history (§5.11): the versions page stays empty.
		return obs, nil
	}
	var newest *versionManifest
	if len(ids) > 0 {
		newest, err = hist.load(ids[len(ids)-1])
		if err != nil {
			newest = nil // unreadable history: §9.2 rule 3, last change unknown
		}
	}
	obs.HasLocalChanges = localChanges(newest, obs.Present, obs.Revision)
	obs.LastChange = lastChangeOf(newest, obs.Present, obs.Revision)
	if obs.Degraded {
		// §9.5 (i): the uncheckpointable state is a local change of unknown
		// origin until a successful checkpoint records it.
		obs.HasLocalChanges = true
		obs.LastChange = LastChange{Unknown: true}
	}
	if q.Want {
		if err := obs.page(hist, ids, q); err != nil {
			return nil, err
		}
	}
	return obs, nil
}

// capture is the one walk: entries, bytes and revision of one observation.
func (o *SkillObservation) capture() error {
	res, err := captureTree(o.Dir, captureOptions{op: "capture"})
	if err != nil {
		var instr *instructionLimitError
		if errors.As(err, &instr) {
			return &ChangeError{Code: ErrSkillInstructionLimit, Text: fmt.Sprintf(
				"skill '%s' cannot be served: its instruction file exceeds the limit of %s bytes (%d bytes); reduce it outside Omnipus and try again",
				o.Slug, formatCount(MaxSkillMarkdownBytes), instr.n)}
		}
		var lim *captureLimitError
		if errors.As(err, &lim) {
			return &ChangeError{Code: ErrSaveCheckFailed, Text: fmt.Sprintf("skill '%s' cannot be read: %s", o.Slug, lim.reason)}
		}
		return fmt.Errorf("read skill '%s': %w", o.Slug, err)
	}
	if res.instr.state == instructionUnresolved {
		return &ChangeError{Code: ErrSkillInstructionUnresolved, Text: fmt.Sprintf(
			"skill '%s' cannot be served: its instruction file is a symlink to '%s', which is not a regular file captured with the skill (outside the skill, Omnipus control metadata, or missing or not a plain file); fix the instruction file outside Omnipus and try again",
			o.Slug, res.instr.target)}
	}
	o.Revision = res.revision
	if res.instr.state == instructionRegular || res.instr.state == instructionLinked {
		o.Instruction, o.HasInstruction = res.instruction, true
	}
	for _, e := range res.entries {
		if hiddenFromDisplay(e.Path) {
			continue
		}
		o.Files = append(o.Files, SkillFile{Path: e.Path, Size: e.Size, Exec: e.Exec})
	}
	return nil
}

// degrade fills a §9.5 (i) observation after a helper aborted the capture at
// a §7.2 limit: the instruction file through the bounded reader (an
// oversized instruction file is still the SKILL_INSTRUCTION_LIMIT failure)
// and the displayable files from a metadata-only walk. No revision.
func (o *SkillObservation) degrade() error {
	ref, err := resolveInstructionOnDisk(o.Dir)
	if err != nil {
		return fmt.Errorf("read skill '%s': %w", o.Slug, err)
	}
	switch ref.state {
	case instructionUnresolved:
		return &ChangeError{Code: ErrSkillInstructionUnresolved, Text: fmt.Sprintf(
			"skill '%s' cannot be served: its instruction file is a symlink to '%s', which is not a regular file captured with the skill (outside the skill, Omnipus control metadata, or missing or not a plain file); fix the instruction file outside Omnipus and try again",
			o.Slug, ref.target)}
	case instructionRegular, instructionLinked:
		raw, readErr := readActivationInstruction(o.Dir, ref)
		if readErr != nil {
			return readErr
		}
		o.Instruction, o.HasInstruction = raw, true
	}
	files, err := listDisplayFiles(o.Dir)
	if err != nil {
		return fmt.Errorf("read skill '%s': %w", o.Slug, err)
	}
	o.Files, o.Revision, o.Degraded = files, "", true
	return nil
}

// listDisplayFiles lists dir's regular files and symlinks in path order by
// Lstat only (no content is opened), hiding Omnipus control metadata and dot
// segments like the captured list.
func listDisplayFiles(dir string) ([]SkillFile, error) {
	var out []SkillFile
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if isControlEntry(rel) || hiddenFromDisplay(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, SkillFile{Path: rel, Size: info.Size(), Exec: info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0})
		return nil
	})
	return out, err
}

// hiddenFromDisplay reports a path with any dot segment (§5.2 `files`: nested
// dot-entries are hidden from the display list only, never from access).
func hiddenFromDisplay(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// localChanges is §9.2's read-time flag for a user-owned skill.
func localChanges(newest *versionManifest, present bool, liveRev string) bool {
	if newest == nil {
		return true // rule 3: history missing or unreadable
	}
	if newest.Kind != manifestInstall && newest.Kind != manifestInstallReplace {
		return true // rule 1
	}
	return present && newest.Revision != liveRev // rule 2
}

func lastChangeOf(newest *versionManifest, present bool, liveRev string) LastChange {
	if newest == nil || newest.Kind == manifestExternalChange || newest.Kind == manifestBaseline ||
		(present && newest.Revision != liveRev) {
		return LastChange{Unknown: true}
	}
	return LastChange{At: manifestTime(newest.CreatedAt), Actor: newest.Actor, Tool: newest.Tool, Paths: append([]string{}, newest.Paths...)}
}

// page fills the versions page: newest first, below q.Before when given.
func (o *SkillObservation) page(hist historyStore, ids []string, q VersionQuery) error {
	o.VersionsTotal = len(ids)
	end := len(ids) // exclusive upper bound in ascending order
	if q.Before != "" {
		end = -1
		for i, id := range ids {
			if id == q.Before {
				end = i
				break
			}
		}
		if end < 0 {
			return &ChangeError{Code: ErrUnknownVersion, Text: fmt.Sprintf("unknown version %q for skill %q", q.Before, o.Slug)}
		}
	}
	start := max(end-q.Limit, 0)
	for i := end - 1; i >= start; i-- {
		m, err := hist.load(ids[i])
		if err != nil {
			return fmt.Errorf("read skill '%s' version %s: %w", o.Slug, ids[i], err)
		}
		paths := m.Paths
		if len(paths) > maxVersionRowPaths {
			paths = paths[:maxVersionRowPaths]
		}
		total := m.PathsTotal
		if total < len(m.Paths) {
			total = len(m.Paths)
		}
		o.Versions = append(o.Versions, VersionRow{
			ID: m.ID, Kind: m.Kind, Actor: m.Actor, Tool: m.Tool, At: manifestTime(m.CreatedAt),
			Paths: append([]string{}, paths...), PathsTotal: total,
		})
	}
	if start > 0 {
		o.NextBefore = ids[start]
	}
	return nil
}

// manifestTime renders a manifest timestamp as RFC 3339 (raw when unparsable).
func manifestTime(s string) string {
	if t, err := time.Parse(manifestTimeFormat, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}

// NeedStatus is one `needs` row (§5.2): a program from
// metadata.omnipus-requires and whether PATH has it.
type NeedStatus struct {
	Program string
	Present bool
	Path    string
}

// SkillDetailMeta is the frontmatter-derived projection of an observation's
// instruction bytes (§5.2 name, description, metadata, compatibility,
// allowed_tools, needs, instructions).
type SkillDetailMeta struct {
	Name, Description string
	Metadata          map[string]string
	Compatibility     *string
	AllowedTools      []string
	Needs             []NeedStatus
	Body              string // instructions without frontmatter
}

// Meta parses the captured instruction bytes; pathEnv is the PATH used for
// the `needs` lookup. A malformed frontmatter block yields empty metadata;
// the body is still served (as the load does).
func (o *SkillObservation) Meta(pathEnv string) SkillDetailMeta {
	front, body := splitFrontmatter(string(o.Instruction))
	fm, _ := parseFrontmatter(front) // malformed: metadata empty, body served
	meta := SkillDetailMeta{Name: stringField(fm, "name"), Description: stringField(fm, "description"), Body: body, Metadata: map[string]string{}}
	nested, _ := fm["metadata"].(map[string]any)
	for k, v := range nested {
		if s, ok := v.(string); ok {
			meta.Metadata[k] = s
		}
	}
	for _, key := range []string{"license", "version", "author"} {
		if v, ok := fm[key]; ok && v != nil {
			meta.Metadata[key] = fmt.Sprint(v)
		}
	}
	if c, ok := fm["compatibility"].(string); ok {
		meta.Compatibility = &c
	}
	allowed := fm["allowed-tools"]
	if allowed == nil && nested != nil {
		allowed = nested["allowed-tools"]
	}
	meta.AllowedTools = toolList(allowed)
	if meta.AllowedTools == nil {
		meta.AllowedTools = []string{}
	}
	req, _ := nested["omnipus-requires"].(string)
	meta.Needs = []NeedStatus{}
	for _, name := range strings.Fields(req) {
		p := lookPath(name, pathEnv)
		meta.Needs = append(meta.Needs, NeedStatus{Program: name, Present: p != "", Path: p})
	}
	return meta
}

// GrantBound reports whether an agent's bindings authorize the live folder
// dir of slug (§5.13a SD-30): the binding must equal the folder generation.
// bindings is an agent record's table, so a nil table (a record without the
// field) is unbound: it authorizes nothing.
func GrantBound(dir, slug string, bindings map[string]string) (bool, error) {
	if bindings == nil {
		bindings = map[string]string{}
	}
	err := checkBinding(dir, slug, bindings)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrSkillGrantStale) {
		return false, nil
	}
	return false, err
}

// LoadableFolders returns the real paths of an agent's loadable set under root
// (§6.1 item 1): every granted name whose folder is installed and whose
// binding equals the folder's current generation (SD-30). A name that is not
// installed or whose grant is stale is simply not loadable. The first error
// reading a folder's identity is returned, so the caller fails closed instead
// of treating an unreadable stamp as "not granted" silently.
func LoadableFolders(root string, granted []string, bindings map[string]string) ([]string, error) {
	root = filepath.Clean(root)
	var out []string
	seen := map[string]bool{}
	for _, name := range granted {
		name = strings.TrimSpace(name)
		if !ValidSlug(name) || seen[SkillKey(name)] {
			continue
		}
		seen[SkillKey(name)] = true
		onDisk, present, err := resolveLiveFolder(root, name)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		dir := filepath.Join(root, onDisk)
		ok, err := GrantBound(dir, onDisk, bindings)
		if err != nil {
			return nil, fmt.Errorf("skill %q: %w", onDisk, err)
		}
		if !ok {
			continue
		}
		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return nil, fmt.Errorf("skill %q: %w", onDisk, err)
		}
		out = append(out, realDir)
	}
	return out, nil
}

// shippedWithVersion is the Omnipus version a built-in skill shipped with
// (§5.2 last_change for built-ins): built-ins are embedded in this binary.
func shippedWithVersion() string { return config.GetVersion() }
