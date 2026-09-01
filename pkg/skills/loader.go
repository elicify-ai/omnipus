package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/ast"
	"github.com/gomarkdown/markdown/parser"
	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)

const (
	MaxNameLength        = 64
	MaxDescriptionLength = 1024
)

// ValidSlug reports whether s is a syntactically valid skill slug — the same
// pattern SkillInfo's ID field and SkillWriter.resolveSkillDir enforce
// (alphanumeric segments joined by single hyphens, at most MaxNameLength
// characters). Exported so callers outside this package (ADR-072's `Skill`
// tool, pkg/tools/skill.go) can reject a malformed identifier — an empty
// string, a traversal attempt, an over-length name, or non-ASCII input —
// before ever consulting the shelf-resolution model, without duplicating the
// pattern (spec FR-021/Dataset B).
func ValidSlug(s string) bool {
	return s != "" && len(s) <= MaxNameLength && namePattern.MatchString(s)
}

// SkillMetadata holds parsed SKILL.md frontmatter fields.
// Supports both basic (name/description) and ClawHub-extended fields.
type SkillMetadata struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Author       string   `json:"author"`        // optional frontmatter: author/publisher
	Version      string   `json:"version"`       // optional frontmatter: semver string
	ArgumentHint string   `json:"argument_hint"` // ClawHub: argument-hint
	Context      string   `json:"context"`       // ClawHub: context (workspace/global/builtin)
	AllowedTools []string `json:"allowed_tools"` // ClawHub: allowed-tools
	ModelHint    string   `json:"model_hint"`    // ClawHub: model-hint
	// Extra holds unrecognized frontmatter keys for forward compatibility.
	Extra map[string]string `json:"extra,omitempty"`
}

type SkillInfo struct {
	// ID is the stable skill identifier: the skill's directory name (slug). It is
	// the value used everywhere a skill must be addressed unambiguously — DELETE,
	// activation allowlists, and built-in detection (DefaultSkillNames returns
	// slugs). It is always present and always slug-shaped (validated).
	ID string `json:"id"`
	// Name is the human-readable display name, sourced from SKILL.md frontmatter
	// `name:` (e.g. "Daily Briefing"). It falls back to the slug when no
	// frontmatter name is present. Unlike ID it is free-form and NOT slug-validated.
	Name         string `json:"name"`
	Path         string `json:"path"`
	Source       string `json:"source"`
	Description  string `json:"description"`
	Author       string `json:"author"`        // optional, from SKILL.md frontmatter
	Version      string `json:"version"`       // optional, from SKILL.md frontmatter
	ArgumentHint string `json:"argument_hint"` // optional, from SKILL.md frontmatter argument-hint
}

// validate enforces the loader's contract: the ID (slug) must be a valid skill
// identifier (alphanumeric with hyphens, within the length cap) and a
// description must be present. The display Name is intentionally NOT
// slug-validated — it is free-form (e.g. "Daily Briefing") — but it must be
// non-empty (the loader falls it back to the slug, so this only fails when both
// are empty, which cannot happen for a real on-disk skill directory).
func (info SkillInfo) validate() error {
	var errs error
	id := info.ID
	if id == "" {
		// Back-compat: callers that only populate Name (e.g. the authoring
		// validator) validate the Name as the identifier.
		id = info.Name
	}
	if id == "" {
		errs = errors.Join(errs, errors.New("name is required"))
	} else {
		if len(id) > MaxNameLength {
			errs = errors.Join(errs, fmt.Errorf("name exceeds %d characters", MaxNameLength))
		}
		if !namePattern.MatchString(id) {
			errs = errors.Join(errs, errors.New("name must be alphanumeric with hyphens"))
		}
	}

	if info.Description == "" {
		errs = errors.Join(errs, errors.New("description is required"))
	} else if len(info.Description) > MaxDescriptionLength {
		errs = errors.Join(errs, fmt.Errorf("description exceeds %d character", MaxDescriptionLength))
	}
	return errs
}

type SkillsLoader struct {
	workspace       string
	workspaceSkills string // workspace skills (project-level)
	globalSkills    string // global skills (~/.omnipus/skills)
	builtinSkills   string // builtin skills
}

// GlobalSkillsDir returns the global (user) skills directory used by this
// loader (typically ~/.omnipus/skills). This is the directory the authoring
// tools write to so that editing a built-in produces a user override rather
// than mutating the shipped built-in in place.
func (sl *SkillsLoader) GlobalSkillsDir() string { return sl.globalSkills }

// SkillRoots returns all unique skill root directories used by this loader.
// The order follows resolution priority: workspace > global > builtin.
func (sl *SkillsLoader) SkillRoots() []string {
	roots := []string{sl.workspaceSkills, sl.globalSkills, sl.builtinSkills}
	seen := make(map[string]struct{}, len(roots))
	out := make([]string, 0, len(roots))

	for _, root := range roots {
		trimmed := strings.TrimSpace(root)
		if trimmed == "" {
			continue
		}
		clean := filepath.Clean(trimmed)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}

	return out
}

func NewSkillsLoader(workspace string, globalSkills string, builtinSkills string) *SkillsLoader {
	return &SkillsLoader{
		workspace:       workspace,
		workspaceSkills: filepath.Join(workspace, "skills"),
		globalSkills:    globalSkills, // ~/.omnipus/skills
		builtinSkills:   builtinSkills,
	}
}

func (sl *SkillsLoader) ListSkills() []SkillInfo {
	skills := make([]SkillInfo, 0)
	seen := make(map[string]bool)

	addSkills := func(dir, source string) {
		if dir == "" {
			return
		}
		dirs, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				slog.Warn("skills: failed to read skills directory", "dir", dir, "source", source, "error", err)
			}
			return
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			// Defensive: never surface a dot-prefixed directory as a skill.
			// install_skill (pkg/tools/skills_install.go) stages a force=true
			// reinstall's download under a hidden ".staging" subdirectory of
			// this same skillsDir before renaming it into place, specifically
			// so a concurrent scan here never sees the in-flight download —
			// this check is a second, independent guard against that class of
			// bug even if a future staging path changes again.
			if strings.HasPrefix(d.Name(), ".") {
				continue
			}
			skillFile := filepath.Join(dir, d.Name(), "SKILL.md")
			if _, err := os.Stat(skillFile); err != nil {
				continue
			}
			info := SkillInfo{
				// ID is the stable slug — the directory name — and never
				// changes regardless of the display name in frontmatter.
				ID:     d.Name(),
				Name:   d.Name(),
				Path:   skillFile,
				Source: source,
			}
			metadata := sl.getSkillMetadata(skillFile)
			if metadata != nil {
				info.Description = metadata.Description
				// Name is the human-readable display name from frontmatter
				// (falls back to the slug). It is kept separate from ID so a
				// proper English name like "Daily Briefing" does not change the
				// addressable identifier "daily-briefing".
				if metadata.Name != "" {
					info.Name = metadata.Name
				}
				info.Author = metadata.Author
				info.Version = metadata.Version
				info.ArgumentHint = metadata.ArgumentHint
			}
			if err := info.validate(); err != nil {
				slog.Warn("invalid skill from "+source, "name", info.Name, "error", err)
				continue
			}
			// Dedup by ID (the directory slug), not by Name (the frontmatter
			// display name): two skills with the same slug but different display
			// names (e.g. a workspace "summarize" with name "summarize" and a
			// global "summarize" with name "Summarize") are the SAME skill and
			// must surface once. ID is the stable identifier — Name is human
			// text and may legitimately differ across copies.
			if seen[info.ID] {
				continue
			}
			seen[info.ID] = true
			skills = append(skills, info)
		}
	}

	// Priority: workspace > global > builtin
	addSkills(sl.workspaceSkills, "workspace")
	addSkills(sl.globalSkills, "global")
	addSkills(sl.builtinSkills, "builtin")

	return skills
}

func (sl *SkillsLoader) LoadSkill(name string) (string, bool) {
	// 1. load from workspace skills first (project-level)
	if sl.workspaceSkills != "" {
		skillFile := filepath.Join(sl.workspaceSkills, name, "SKILL.md")
		if content, err := os.ReadFile(skillFile); err == nil {
			return sl.stripFrontmatter(string(content)), true
		}
	}

	// 2. then load from global skills (~/.omnipus/skills)
	if sl.globalSkills != "" {
		skillFile := filepath.Join(sl.globalSkills, name, "SKILL.md")
		if content, err := os.ReadFile(skillFile); err == nil {
			return sl.stripFrontmatter(string(content)), true
		}
	}

	// 3. finally load from builtin skills
	if sl.builtinSkills != "" {
		skillFile := filepath.Join(sl.builtinSkills, name, "SKILL.md")
		if content, err := os.ReadFile(skillFile); err == nil {
			return sl.stripFrontmatter(string(content)), true
		}
	}

	return "", false
}

func (sl *SkillsLoader) LoadSkillsForContext(skillNames []string) string {
	if len(skillNames) == 0 {
		return ""
	}

	var parts []string
	for _, name := range skillNames {
		content, ok := sl.LoadSkill(name)
		if ok {
			parts = append(parts, fmt.Sprintf("### Skill: %s\n\n%s", name, content))
		}
	}

	return strings.Join(parts, "\n\n---\n\n")
}

func (sl *SkillsLoader) BuildSkillsSummary() string {
	return sl.BuildSkillsSummaryFunc(nil)
}

// skillSourceRank orders SkillInfo.Source values for the menu's (now purely
// cosmetic — ADR-072 D1.1) ordering: most specific first. "project" is
// ADR-072 D4.1's shelf 1 (a mount's own skills, see
// BuildSkillsSummaryFuncWithProject below); "workspace" is the pre-D4.1
// loader's own vestigial per-agent-workspace shelf, kept at the same rank for
// any caller still passing unmerged workspace-shelf skills through
// BuildSkillsSummaryFunc directly. Unknown sources sort last (defensive:
// should not occur for skills produced by ListSkills or MergeProjectSkills).
var skillSourceRank = map[string]int{
	"project":   0,
	"workspace": 0,
	"global":    1,
	"builtin":   2,
}

// BuildSkillsSummaryFunc renders the skills summary block, optionally filtered
// by an allow predicate. When allow is non-nil, only skills for which
// allow(name) is true are listed — this implements per-agent progressive
// disclosure so an agent's system prompt advertises only its allowlisted skills
// (FR-9.4/ADR-072 D4). When allow is nil, every loaded skill is eligible for
// listing.
//
// Equivalent to BuildSkillsSummaryFuncWithProject(allow, nil) — see that
// function for the full contract, including ADR-072 D1.1 (no cap, ever) and
// FR-006 (no filesystem location in any entry).
func (sl *SkillsLoader) BuildSkillsSummaryFunc(allow func(name string) bool) string {
	return sl.BuildSkillsSummaryFuncWithProject(allow, nil)
}

// BuildSkillsSummaryFuncWithProject is BuildSkillsSummaryFunc extended with a
// workspace's already-merged project shelf (ADR-072 D4.1 shelf 1 — see
// ProjectShelf/MergeProjectSkills in project.go). project may be nil, which
// reduces this to exactly BuildSkillsSummaryFunc's own behaviour; passing one
// is a later integration phase's responsibility once a workspace's mounts are
// wired through to the caller (this loader has no notion of "workspace" or
// "mount" on its own).
//
// Three things changed from the pre-D1.1/D4.1 version of this function:
//
//  1. NO CAP (D1.1). The summary used to truncate at maxSkillsInSummary and
//     append a footer pointing at find_skills — which cannot see installed
//     skills at all, so the footer was always a wrong signpost. Both the cap
//     and the footer are deleted, not resized: every eligible skill is
//     listed, always. The sort below is now purely cosmetic (stable,
//     deterministic output) rather than a survival ranking.
//  2. NO LOCATION (FR-006/N1). The old <location> line printed the skill's
//     filesystem path into the model's own context — a disclosure with no
//     legitimate use once loading goes through the `Skill` tool rather than
//     `read_file`. <source> (registry/builtin/project) stays; <location>
//     does not.
//  3. A PROJECT SHELF, gated by mount membership alone (D4.1) — allow is
//     NEVER consulted for a project entry, only for the registry/builtin
//     entries from ListSkills. D4.2's carve-out is honoured by construction:
//     a project skill whose slug already won a spot from the allow-filtered
//     registry/builtin set is dropped from the merge (that shelf's grant
//     already wins the same slug — see ResolveSkillName's identical
//     carve-out in shelf.go), so the same slug is never emitted twice.
func (sl *SkillsLoader) BuildSkillsSummaryFuncWithProject(allow func(name string) bool, project ProjectShelf) string {
	allSkills := sl.ListSkills()

	eligible := make([]SkillInfo, 0, len(allSkills)+len(project))
	seen := make(map[string]struct{}, len(allSkills)+len(project))
	for _, s := range allSkills {
		if allow != nil && !allow(s.ID) {
			continue
		}
		eligible = append(eligible, s)
		seen[strings.ToLower(s.ID)] = struct{}{}
	}
	for _, ps := range project {
		key := strings.ToLower(ps.ID)
		if _, dup := seen[key]; dup {
			// D4.2 carve-out: a granted registry/builtin slug already claimed
			// this name — the project skill of the same slug never displaces
			// it, in the menu any more than in resolution.
			continue
		}
		eligible = append(eligible, ps.SkillInfo)
		seen[key] = struct{}{}
	}
	if len(eligible) == 0 {
		return ""
	}

	// Purely cosmetic now (D1.1) — kept for stable, deterministic output
	// rather than to decide who survives a cap that no longer exists.
	sort.SliceStable(eligible, func(i, j int) bool {
		si, sj := eligible[i], eligible[j]
		ri, rj := skillSourceRank[si.Source], skillSourceRank[sj.Source]
		if ri != rj {
			return ri < rj
		}
		if si.Name != sj.Name {
			return si.Name < sj.Name
		}
		return si.ID < sj.ID
	})

	var lines []string
	lines = append(lines, "<skills>")
	for _, s := range eligible {
		// The agent invokes a skill by the slug (ID) — that is the identifier
		// ResolveSkillName/LoadSkill resolve against — so <name> carries the
		// slug. The human-readable display name is surfaced separately so the
		// model can refer to it naturally.
		escapedName := escapeXML(s.ID)
		escapedDisplay := escapeXML(s.Name)
		escapedDesc := escapeXML(s.Description)

		lines = append(lines, "  <skill>")
		lines = append(lines, fmt.Sprintf("    <name>%s</name>", escapedName))
		if s.Name != "" && s.Name != s.ID {
			lines = append(lines, fmt.Sprintf("    <display_name>%s</display_name>", escapedDisplay))
		}
		lines = append(lines, fmt.Sprintf("    <description>%s</description>", escapedDesc))
		// No <location> line (ADR-072 FR-006/N1) — see the function doc above.
		lines = append(lines, fmt.Sprintf("    <source>%s</source>", s.Source))
		lines = append(lines, "  </skill>")
	}
	lines = append(lines, "</skills>")

	return strings.Join(lines, "\n")
}

func (sl *SkillsLoader) getSkillMetadata(skillPath string) *SkillMetadata {
	content, err := os.ReadFile(skillPath)
	if err != nil {
		logger.WarnCF("skills", "Failed to read skill metadata",
			map[string]any{
				"skill_path": skillPath,
				"error":      err.Error(),
			})
		return nil
	}

	frontmatter, bodyContent := splitFrontmatter(string(content))
	dirName := filepath.Base(filepath.Dir(skillPath))
	title, bodyDescription := extractMarkdownMetadata(bodyContent)

	metadata := &SkillMetadata{
		Name:        dirName,
		Description: bodyDescription,
	}
	if title != "" && namePattern.MatchString(title) && len(title) <= MaxNameLength {
		metadata.Name = title
	}

	if frontmatter == "" {
		return metadata
	}

	// Try JSON first (for backward compatibility)
	var jsonMeta struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Author      string `json:"author"`
		Version     string `json:"version"`
	}
	if err := json.Unmarshal([]byte(frontmatter), &jsonMeta); err == nil {
		if jsonMeta.Name != "" {
			metadata.Name = jsonMeta.Name
		}
		if jsonMeta.Description != "" {
			metadata.Description = jsonMeta.Description
		}
		if jsonMeta.Author != "" {
			metadata.Author = jsonMeta.Author
		}
		if jsonMeta.Version != "" {
			metadata.Version = jsonMeta.Version
		}
		return metadata
	}

	// Fall back to simple YAML parsing
	yamlMeta := sl.parseSimpleYAML(frontmatter)
	if name := yamlMeta["name"]; name != "" {
		metadata.Name = name
	}
	if description := yamlMeta["description"]; description != "" {
		metadata.Description = description
	}
	if author := yamlMeta["author"]; author != "" {
		metadata.Author = author
	}
	if version := yamlMeta["version"]; version != "" {
		metadata.Version = version
	}
	if hint := yamlMeta["argument-hint"]; hint != "" {
		metadata.ArgumentHint = hint
	}
	if ctx := yamlMeta["context"]; ctx != "" {
		metadata.Context = ctx
	}
	if mh := yamlMeta["model-hint"]; mh != "" {
		metadata.ModelHint = mh
	}
	if tools := yamlMeta["allowed-tools"]; tools != "" {
		metadata.AllowedTools = strings.Split(tools, ",")
	}
	// Collect extra keys.
	extra := make(map[string]string)
	for k, v := range yamlMeta {
		if strings.HasPrefix(k, "extra:") {
			extra[strings.TrimPrefix(k, "extra:")] = v
		}
	}
	if len(extra) > 0 {
		metadata.Extra = extra
	}
	return metadata
}

func extractMarkdownMetadata(content string) (title, description string) {
	p := parser.NewWithExtensions(parser.CommonExtensions)
	doc := markdown.Parse([]byte(content), p)
	if doc == nil {
		return "", ""
	}

	ast.WalkFunc(doc, func(node ast.Node, entering bool) ast.WalkStatus {
		if !entering {
			return ast.GoToNext
		}

		switch n := node.(type) {
		case *ast.Heading:
			if title == "" && n.Level == 1 {
				title = nodeText(n)
				if title != "" && description != "" {
					return ast.Terminate
				}
			}
		case *ast.Paragraph:
			if description == "" {
				description = nodeText(n)
				if title != "" && description != "" {
					return ast.Terminate
				}
			}
		}
		return ast.GoToNext
	})

	return title, description
}

func nodeText(n ast.Node) string {
	var b strings.Builder
	ast.WalkFunc(n, func(node ast.Node, entering bool) ast.WalkStatus {
		if !entering {
			return ast.GoToNext
		}

		switch t := node.(type) {
		case *ast.Text:
			b.Write(t.Literal)
		case *ast.Code:
			b.Write(t.Literal)
		case *ast.Softbreak, *ast.Hardbreak, *ast.NonBlockingSpace:
			b.WriteByte(' ')
		}
		return ast.GoToNext
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

// parseSimpleYAML parses YAML frontmatter into a key→value map.
// Recognizes standard fields and ClawHub-extended fields (argument-hint,
// context, allowed-tools, model-hint). Unrecognized keys are collected
// under "extra:<key>" for forward compatibility.
func (sl *SkillsLoader) parseSimpleYAML(content string) map[string]string {
	result := make(map[string]string)

	// Unmarshal into a raw map to capture all keys.
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		slog.Warn("SKILL.md: failed to parse YAML frontmatter", "error", err)
		return result
	}

	knownKeys := map[string]bool{
		"name": true, "description": true, "argument-hint": true,
		"context": true, "allowed-tools": true, "model-hint": true,
		"author": true, "version": true,
	}

	for k, v := range raw {
		switch k {
		case "name", "description", "context", "model-hint", "author", "version":
			if s, ok := v.(string); ok && s != "" {
				result[k] = s
			}
		case "argument-hint":
			if s, ok := v.(string); ok && s != "" {
				result["argument-hint"] = s
			}
		case "allowed-tools":
			// May be a YAML sequence — join as comma-separated for map storage.
			switch vt := v.(type) {
			case []any:
				parts := make([]string, 0, len(vt))
				for _, item := range vt {
					if s, ok := item.(string); ok {
						parts = append(parts, s)
					}
				}
				result["allowed-tools"] = strings.Join(parts, ",")
			case string:
				result["allowed-tools"] = vt
			}
		default:
			if !knownKeys[k] {
				if s, ok := v.(string); ok {
					result["extra:"+k] = s
				}
			}
		}
	}

	return result
}

func (sl *SkillsLoader) extractFrontmatter(content string) string {
	frontmatter, _ := splitFrontmatter(content)
	return frontmatter
}

func (sl *SkillsLoader) stripFrontmatter(content string) string {
	_, body := splitFrontmatter(content)
	return body
}

func splitFrontmatter(content string) (frontmatter, body string) {
	normalized := string(parser.NormalizeNewlines([]byte(content)))
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", content
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return "", content
	}

	frontmatter = strings.Join(lines[1:end], "\n")
	body = strings.Join(lines[end+1:], "\n")
	body = strings.TrimLeft(body, "\n")
	return frontmatter, body
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
