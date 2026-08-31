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

// maxSkillsInSummary caps how many skills BuildSkillsSummaryFunc renders in
// full: the summary used to dump every allowed skill's full XML entry into
// the system prompt every turn with no limit, so an install with a large or
// growing skill catalog paid an unbounded, ever-increasing per-turn cost.
// The cap keeps the block bounded; truncated skills are still discoverable
// via find_skills (mentioned in the footer appended when truncation occurs).
const maxSkillsInSummary = 20

// skillSourceRank orders SkillInfo.Source values by the same precedence
// ListSkills uses to resolve name collisions: workspace overrides global
// overrides builtin. Unknown sources sort last (defensive: should not occur
// for skills produced by ListSkills).
var skillSourceRank = map[string]int{
	"workspace": 0,
	"global":    1,
	"builtin":   2,
}

// BuildSkillsSummaryFunc renders the skills summary block, optionally filtered
// by an allow predicate. When allow is non-nil, only skills for which
// allow(name) is true are listed — this implements per-agent progressive
// disclosure so an agent's system prompt advertises only its allowlisted skills
// (FR-9.4). When allow is nil, every loaded skill is eligible for listing.
//
// The eligible set is capped at maxSkillsInSummary. Previously the
// cap kept the most-recently-modified skills, ranked by an os.Stat of each
// skill's SKILL.md inside the sort comparator — an uncached syscall on every
// system-prompt build (this runs every turn), and a meaningless ordering to
// begin with: a git clone, cp -r, rsync, or container rebuild rewrites mtimes
// wholesale (often to identical values), so which skills survived the cap
// could change silently between deploys with no code change at all.
//
// The cap now keeps the same precedence ListSkills already uses to resolve
// slug collisions — workspace skills first, then global, then builtin, tied
// broken by display name then by slug — which is both a meaningful ordering
// (more specific overrides survive) and free: it is computed once via
// decorate-sort-undecorate over fields SkillInfo already carries, with no
// filesystem access at all. A footer names how many were cut and points at
// find_skills to search the full catalog, including anything not shown here.
func (sl *SkillsLoader) BuildSkillsSummaryFunc(allow func(name string) bool) string {
	allSkills := sl.ListSkills()
	if len(allSkills) == 0 {
		return ""
	}

	// Filter to the allowed set first, then rank by the same
	// workspace>global>builtin precedence ListSkills uses, tie-broken by
	// name then slug for full determinism.
	eligible := make([]SkillInfo, 0, len(allSkills))
	for _, s := range allSkills {
		if allow != nil && !allow(s.ID) {
			continue
		}
		eligible = append(eligible, s)
	}
	if len(eligible) == 0 {
		return ""
	}

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

	shown := eligible
	truncated := 0
	if len(eligible) > maxSkillsInSummary {
		shown = eligible[:maxSkillsInSummary]
		truncated = len(eligible) - maxSkillsInSummary
	}

	var lines []string
	lines = append(lines, "<skills>")
	emitted := 0
	for _, s := range shown {
		// The agent invokes a skill by the slug (ID) — that is the identifier
		// ResolveSkillName/LoadSkill resolve against — so <name> carries the
		// slug. The human-readable display name is surfaced separately so the
		// model can refer to it naturally.
		escapedName := escapeXML(s.ID)
		escapedDisplay := escapeXML(s.Name)
		escapedDesc := escapeXML(s.Description)
		escapedPath := escapeXML(s.Path)

		lines = append(lines, "  <skill>")
		lines = append(lines, fmt.Sprintf("    <name>%s</name>", escapedName))
		if s.Name != "" && s.Name != s.ID {
			lines = append(lines, fmt.Sprintf("    <display_name>%s</display_name>", escapedDisplay))
		}
		lines = append(lines, fmt.Sprintf("    <description>%s</description>", escapedDesc))
		lines = append(lines, fmt.Sprintf("    <location>%s</location>", escapedPath))
		lines = append(lines, fmt.Sprintf("    <source>%s</source>", s.Source))
		lines = append(lines, "  </skill>")
		emitted++
	}
	lines = append(lines, "</skills>")

	if emitted == 0 {
		return ""
	}
	out := strings.Join(lines, "\n")
	if truncated > 0 {
		noun := "skills"
		if truncated == 1 {
			noun = "skill"
		}
		out += fmt.Sprintf("\n\n%d more installed %s not shown above — call find_skills to search the full catalog.", truncated, noun)
	}
	return out
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
