package skills

// The §5.7 `Skill` load result: the activation header and the bounded
// instruction body. A user-owned load runs step-1 outside-change detection
// first (§9.4) under the mutation lock; a load whose detection capture
// aborted degrades (§9.5) — no revision, no version, a truthful last-change
// line — but still serves valid instructions through the bounded reader.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// maxHeaderFiles caps the header's file list (§5.7).
const maxHeaderFiles = 200

// LoadActivationOptions carries what the caller knows about the loading agent.
type LoadActivationOptions struct {
	// BuiltinRoot is the built-in shelf folder ("" = none).
	BuiltinRoot string
	// EditAllowed is true when the agent's effective edit_skill policy is not
	// deny (§5.7 `edit:` clause).
	EditAllowed bool
	// PATHEnv is the PATH the agent's bash gets (`needs` lookup).
	PATHEnv string
	// Bindings is the agent's skill key → generation table (§5.13a SD-30).
	Bindings map[string]string
}

// ActivationResult is one §5.7 load: the rendered text plus what the caller
// records — the on-disk slug, the shelf and the certified revision ("" for a
// built-in or a degraded load, which certify none; §7.3).
type ActivationResult struct {
	Content  string
	Slug     string
	Shelf    string // "registry" | "builtin"
	Revision string
}

// LoadForActivation renders the §5.7 load result for slug.
func LoadForActivation(skillsRoot, slug string, opts LoadActivationOptions) (string, error) {
	res, err := LoadActivation(skillsRoot, slug, opts)
	return res.Content, err
}

// LoadActivation is LoadForActivation with the load's identity and revision.
func LoadActivation(skillsRoot, slug string, opts LoadActivationOptions) (ActivationResult, error) {
	if !ValidSlug(slug) {
		return ActivationResult{}, fmt.Errorf("%w: %q is not a valid skill name", ErrNotFound, slug)
	}
	root := filepath.Clean(skillsRoot)
	onDisk, present, err := resolveLiveFolder(root, slug)
	if err != nil {
		return ActivationResult{}, err
	}
	builtin := IsBuiltin(slug)
	dir := filepath.Join(root, onDisk)
	if opts.BuiltinRoot != "" && !present {
		if bOnDisk, ok, bErr := resolveLiveFolder(opts.BuiltinRoot, slug); bErr == nil && ok {
			dir, onDisk, present, builtin = filepath.Join(opts.BuiltinRoot, bOnDisk), bOnDisk, true, true
		}
	}
	if !present {
		return ActivationResult{}, fmt.Errorf("%w: '%s'", ErrNotFound, slug)
	}
	// The instruction file is resolved first (Lstat/Readlink only, no target
	// is opened), so a link onto control metadata such as the generation stamp
	// is reported as unresolved rather than read as an identity.
	ref, err := resolveInstructionOnDisk(dir)
	if err != nil {
		return ActivationResult{}, err
	}
	switch ref.state {
	case instructionMissing:
		return ActivationResult{}, fmt.Errorf("%w: skill '%s' has no SKILL.md", ErrNotFound, onDisk)
	case instructionUnresolved:
		return ActivationResult{}, fmt.Errorf("%w: skill '%s': SKILL.md is a symlink to '%s', which is not a regular file of the skill; fix the link or replace it with the file", ErrSkillInstructionUnresolved, onDisk, ref.target)
	}
	if err = checkBinding(dir, onDisk, opts.Bindings); err != nil {
		return ActivationResult{}, err
	}

	var view loadView
	if builtin {
		raw, readErr := readActivationInstruction(dir, ref)
		if readErr != nil {
			return ActivationResult{}, readErr
		}
		view = loadView{slug: onDisk, shelf: "builtin", dir: dir, raw: raw, opts: opts,
			lastChange: "shipped with Omnipus " + config.GetVersion()}
	} else if view, err = loadRegistry(root, onDisk, dir, ref, opts); err != nil {
		return ActivationResult{}, err
	}
	content, err := renderLoad(view)
	if err != nil {
		return ActivationResult{}, err
	}
	return ActivationResult{Content: content, Slug: onDisk, Shelf: view.shelf, Revision: view.revision}, nil
}

// checkBinding is SD-30's identity half: the agent's binding for the key must
// equal the folder's current generation. An agent's table (non-nil, possibly
// empty) is strict: a missing binding or an unstamped folder matches nothing
// (§5.13a "unbound = inert", "an unstamped folder matches no binding"). A nil
// table means the caller supplied no agent identity at all (a direct
// LoadForActivation without an agent); then only an unstamped folder loads.
// Every agent caller passes its table (ContextBuilder.SkillBindings,
// GrantBound), so the nil case never authorizes an agent.
func checkBinding(dir, slug string, bindings map[string]string) error {
	gen, err := ReadGeneration(dir)
	if err != nil {
		return err
	}
	if bindings == nil {
		if gen == "" {
			return nil
		}
	} else if gen != "" && bindings[SkillKey(slug)] == gen {
		return nil
	}
	return StaleGrantError(slug)
}

// StaleGrantError is the §5.10 SKILL_GRANT_STALE refusal for slug: the agent
// holds the name, but its binding matches no live generation.
func StaleGrantError(slug string) error {
	return &ChangeError{Code: ErrSkillGrantStale, Text: fmt.Sprintf("skill '%s' is not available: your grant is for a previous skill with this name that no longer exists; ask the owner to re-grant the current '%s'", slug, slug)}
}

// loadRegistry runs detection, then renders the user-owned header.
func loadRegistry(root, slug, dir string, ref instructionRef, opts LoadActivationOptions) (loadView, error) {
	view := loadView{slug: slug, shelf: "registry", dir: dir, opts: opts}
	var degraded bool
	err := WithMutationLock(root, func() error {
		p := &pipeline{root: root, req: ChangeRequest{Actor: ChangeActor{Kind: "unknown"}}, now: time.Now().UTC()}
		st, err := p.observe(slug)
		if err != nil {
			return err
		}
		f := p.captureCheckpoint(st, "activation")
		if f != nil {
			var instr *instructionLimitError
			var lim *captureLimitError
			switch {
			case errors.As(f.err, &instr):
				return instructionLimit(slug, instr, f.suffix)
			case f.special != nil, errors.As(f.err, &lim):
				degraded = true
				return nil
			default:
				return fmt.Errorf("load skill '%s': %w%s", slug, f.err, f.suffix)
			}
		}
		raw, _, err := instructionBytes(st.liveMap())
		if err != nil {
			var instr *instructionLimitError
			if errors.As(err, &instr) {
				return instructionLimit(slug, instr, "")
			}
			return err
		}
		view.raw = raw
		view.revision = st.rev
		if st.head != nil {
			view.version = st.head.ID
			view.lastChange = lastChangeLine(st.head)
		}
		return nil
	})
	if err != nil {
		return loadView{}, err
	}
	if degraded {
		raw, err := readActivationInstruction(dir, ref)
		if err != nil {
			return loadView{}, err
		}
		view.raw, view.lastChange = raw, "unknown (changed outside Omnipus)"
	}
	return view, nil
}

func instructionLimit(slug string, e *instructionLimitError, suffix string) error {
	return fmt.Errorf("%w: skill '%s' was not loaded: %s%s", ErrSkillInstructionLimit, slug, e.Error(), suffix)
}

// readActivationInstruction reads the resolved instruction entry through the
// bounded reader (op "instruction").
func readActivationInstruction(dir string, ref instructionRef) ([]byte, error) {
	raw, err := readBoundedInstruction("instruction", filepath.Join(dir, filepath.FromSlash(ref.rel)), ref.rel)
	if err != nil {
		var instr *instructionLimitError
		if errors.As(err, &instr) {
			return nil, instructionLimit(filepath.Base(dir), instr, "")
		}
		return nil, err
	}
	return raw, nil
}

// lastChangeLine renders §5.7's `last change` from the newest manifest.
func lastChangeLine(m *versionManifest) string {
	if m.Kind == manifestExternalChange || m.Kind == manifestBaseline {
		return "unknown (changed outside Omnipus)"
	}
	when := m.CreatedAt
	if t, err := time.Parse(manifestTimeFormat, m.CreatedAt); err == nil {
		when = t.UTC().Format("2006-01-02 15:04") + " UTC"
	}
	who := m.Actor
	switch m.Actor {
	case "person", "system", "unknown":
	default:
		who = "agent " + m.Actor
	}
	detail := m.Kind
	if m.Tool != "" {
		detail = m.Tool
	}
	if len(m.Paths) > 0 {
		paths := m.Paths
		more := ""
		if len(paths) > 3 {
			paths, more = paths[:3], " …"
		}
		detail += " " + strings.Join(paths, ", ") + more
	}
	return fmt.Sprintf("%s by %s (%s)", when, who, detail)
}

// loadView is what the renderer needs.
type loadView struct {
	slug, shelf, dir  string
	revision, version string
	lastChange        string
	raw               []byte
	opts              LoadActivationOptions
}

func renderLoad(v loadView) (string, error) {
	front, body := splitFrontmatter(string(v.raw))
	fm, _ := parseFrontmatter(front) // a malformed block renders no metadata; the body is still served
	folder := v.dir
	if abs, err := filepath.Abs(folder); err == nil {
		folder = abs
	}
	if resolved, err := filepath.EvalSymlinks(folder); err == nil {
		folder = resolved
	}
	files, total, err := displayFiles(v.dir)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<skill slug="%s" shelf="%s"`, v.slug, v.shelf)
	if v.shelf == "registry" && v.revision != "" {
		fmt.Fprintf(&b, ` revision="%s"`, v.revision)
		if v.version != "" {
			fmt.Fprintf(&b, ` version="%s"`, v.version)
		}
	}
	b.WriteString(">\n")
	fmt.Fprintf(&b, "folder: %s\n", folder)
	if d := stringField(fm, "description"); d != "" {
		fmt.Fprintf(&b, "description: %s\n", d)
	}
	if m := metadataLine(fm); m != "" {
		fmt.Fprintf(&b, "metadata: %s\n", m)
	}
	if c := stringField(fm, "compatibility"); c != "" {
		fmt.Fprintf(&b, "compatibility: %q\n", c)
	}
	shown := files
	if len(shown) > maxHeaderFiles {
		shown = shown[:maxHeaderFiles]
	}
	fmt.Fprintf(&b, "files (%d, %s): %s", len(shown), formatSize(total), strings.Join(shown, " · "))
	if extra := len(files) - len(shown); extra > 0 {
		fmt.Fprintf(&b, " · … %d more — use list_directory", extra)
	}
	b.WriteString("\n")
	if needs := needsLine(fm, v.opts.PATHEnv); needs != "" {
		fmt.Fprintf(&b, "needs: %s\n", needs)
	}
	if v.lastChange != "" {
		fmt.Fprintf(&b, "last change: %s\n", v.lastChange)
	}
	switch {
	case v.shelf == "builtin":
		b.WriteString("you may: read all files · run scripts with bash · edit: no (built-in; copy it to change it)\n")
	case v.opts.EditAllowed:
		b.WriteString("you may: read all files · run scripts with bash (your bash permission applies) · edit: yes\n")
	default:
		b.WriteString("you may: read all files · run scripts with bash (your bash permission applies) · edit: no\n")
	}
	b.WriteString("</skill>\n<instructions>\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n</instructions>")
	return b.String(), nil
}

// displayFiles lists the folder's files for the header: control metadata
// excluded, dot-entries hidden at any depth, lexical order. No file is opened.
func displayFiles(dir string) ([]string, int64, error) {
	var files []string
	var total int64
	err := walkSkillTree(dir, func(rel, _ string, _ fs.DirEntry, fi os.FileInfo) error {
		for _, part := range strings.Split(rel, "/") {
			if strings.HasPrefix(part, ".") {
				return nil
			}
		}
		files = append(files, rel)
		if fi.Mode().IsRegular() {
			total += fi.Size()
		}
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list skill files: %w", err)
	}
	return files, total, nil
}

func formatSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%d KB", (n+1023)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// metadataLine renders name, version, author, license and allowed-tools from
// the top level or the `metadata` map. omnipus-requires is never echoed.
func metadataLine(fm map[string]any) string {
	nested, _ := fm["metadata"].(map[string]any)
	pick := func(key string) any {
		if v, ok := fm[key]; ok {
			return v
		}
		if nested != nil {
			return nested[key]
		}
		return nil
	}
	var parts []string
	for _, key := range []string{"name", "version", "author", "license"} {
		if v := pick(key); v != nil {
			parts = append(parts, fmt.Sprintf("%s=%v", key, v))
		}
	}
	if tools := toolList(pick("allowed-tools")); len(tools) > 0 {
		parts = append(parts, "allowed-tools=["+strings.Join(tools, ", ")+"]")
	}
	return strings.Join(parts, " · ")
}

func toolList(v any) []string {
	switch t := v.(type) {
	case string:
		return strings.Fields(t)
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	return nil
}

// needsLine checks metadata.omnipus-requires (space-separated program names)
// by PATH lookup over pathEnv.
func needsLine(fm map[string]any, pathEnv string) string {
	nested, _ := fm["metadata"].(map[string]any)
	req, _ := nested["omnipus-requires"].(string)
	names := strings.Fields(req)
	if len(names) == 0 {
		return ""
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		if p := lookPath(name, pathEnv); p != "" {
			parts = append(parts, fmt.Sprintf("%s — present (%s)", name, p))
		} else {
			parts = append(parts, name+" — MISSING")
		}
	}
	return strings.Join(parts, " · ")
}

// lookPath finds an executable named name in pathEnv ("" when absent).
func lookPath(name, pathEnv string) string {
	if strings.ContainsAny(name, `/\`) {
		return ""
	}
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{".exe", ".cmd", ".bat", ".com", ""}
	}
	for _, d := range filepath.SplitList(pathEnv) {
		if d == "" {
			continue
		}
		for _, ext := range exts {
			p := filepath.Join(d, name+ext)
			fi, err := os.Stat(p)
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			if runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
				continue
			}
			return p
		}
	}
	return ""
}
