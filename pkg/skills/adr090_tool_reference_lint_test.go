package skills_test

import (
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// FR-013 (ADR-090 agent-configuration-and-skills-spec): "Add catalog lint for
// explicitly marked tool references in every prompt/skill; all exact names
// must exist."
//
// THE MARKER. A tool reference is explicitly marked by the literal prefix
// "tool:" immediately followed by the exact catalog name — `tool:read_file`
// in Markdown (backticked for rendering), tool:read_file in the Go prompt
// literals. The marker is DOCUMENTATION AND LINT ONLY: no runtime code parses
// it, and unmarked content is simply not linted. That is what keeps external
// packages exempt: the four pinned elicify-* document skills are written
// harness-agnostically ("the execution tool", "the harness's attachment
// mechanism"), carry zero marks, and pass — nothing outside this repo is
// required to adopt the convention, and user-authored skills are explicitly
// NOT taught it (skill-authoring's step 3 keeps their content portable).
//
// WHY A DEDICATED PREFIX. The embedded skills already use plain backticks for
// non-tool tokens (`awaiting_supervision`, `Retry-After`), so linting bare
// backticked identifiers would false-positive on them or need a growing
// allowlist. Only a tool:-prefixed span is a claim about the catalog.
//
// WHY package skills_test. The FR-013 capability lint
// (adr090_capability_lint_test.go) resolves effective policies through
// pkg/tools' real compositor, which imports pkg/skills — an in-package test
// could not import it without a cycle. Both lint files therefore live in the
// external test package and read the embedded content through the PUBLIC
// first-boot seeder (skills.SeedDefaults into a temp dir + os.DirFS), so the
// lint sees the exact bytes a fresh install actually ships, marker file
// included, without any new production API.

// markedToolRef matches every explicitly marked tool reference. The capture
// is the claimed catalog name; anything else in the text is out of scope.
var markedToolRef = regexp.MustCompile(`tool:([A-Za-z0-9_]+)`)

// lintCatalogSnapshot returns the static builtin tool-name catalog as a set.
// AllStaticToolNames is the same universe the seeds and the boot-time
// coverage validator enumerate against, so "exists" here means "exists" there.
func lintCatalogSnapshot(t *testing.T) map[string]bool {
	t.Helper()
	catalog := map[string]bool{}
	for _, name := range coreagent.AllStaticToolNames() {
		catalog[name] = true
	}
	if len(catalog) == 0 {
		t.Fatal("coreagent.AllStaticToolNames() is empty — the lint would be vacuous")
	}
	return catalog
}

// seededSkillFile is one file of the ACTUAL embedded content as materialized
// by the real first-boot seeder — not the embed.FS directly — so both FR-013
// lints read what a fresh install actually ships.
type seededSkillFile struct {
	relPath   string // e.g. "interview/SKILL.md"
	pkg       string // top-level package directory, e.g. "interview"
	isSkillMD bool
	text      string
}

// seededEmbeddedFiles seeds the embedded default skills into a fresh temp
// directory via the public skills.SeedDefaults and walks the result.
// Structural guards: every DefaultSkillNames package must be present in the
// walk (SeedDefaults must have seeded, not skipped, all of them into the
// fresh dir), so a newly added package is linted without anyone updating a
// list — the property a hand-copied file list cannot provide.
func seededEmbeddedFiles(t *testing.T) []seededSkillFile {
	t.Helper()
	root := t.TempDir()
	res, err := skills.SeedDefaults(root)
	if err != nil {
		t.Fatalf("SeedDefaults into temp dir: %v", err)
	}
	names := skills.DefaultSkillNames()
	if len(names) == 0 {
		t.Fatal("skills.DefaultSkillNames() is empty — the lint would be vacuous")
	}
	if len(res.Seeded) != len(names) || len(res.Skipped) != 0 {
		t.Fatalf("SeedDefaults into a fresh temp dir seeded %d, skipped %d; want seeded=%d skipped=0 — the walk would not cover the full default set",
			len(res.Seeded), len(res.Skipped), len(names))
	}
	sys := os.DirFS(root)
	var files []seededSkillFile
	err = fs.WalkDir(sys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := fs.ReadFile(sys, p)
		if rerr != nil {
			return rerr
		}
		files = append(files, seededSkillFile{
			relPath:   p,
			pkg:       strings.SplitN(p, "/", 2)[0],
			isSkillMD: path.Base(p) == "SKILL.md",
			text:      string(data),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk seeded skills: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.pkg] = true
	}
	for _, name := range names {
		if !seen[name] {
			t.Fatalf("the walk missed seeded package %q — lint coverage is incomplete", name)
		}
	}
	return files
}

// unknownMarkedToolNames returns the marked names in text that are NOT in the
// catalog, deduplicated and sorted. The deliberate-unknown detection this
// function provides is proved by mutation in
// TestADR090_PromptSkillToolReferences_RejectsUnknownMarkedNames — never by
// reading expected names off production output.
func unknownMarkedToolNames(text string, catalog map[string]bool) []string {
	seen := map[string]bool{}
	for _, m := range markedToolRef.FindAllStringSubmatch(text, -1) {
		if !catalog[m[1]] {
			seen[m[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TestADR090_PromptSkillToolReferences_MarkedNamesExistInCatalog lints every
// built-in prompt and every embedded skill file. Inventory coverage is
// structural, not a list: the prompts come from coreagent.All() (the same
// roster init()'s compiled-prompt invariant walks), the system-agent souls
// from SystemAgents(), and the skill files from the public first-boot seeder
// walk — so a newly added role, rubric, skill, or knowledge file is linted
// without anyone updating this test.
func TestADR090_PromptSkillToolReferences_MarkedNamesExistInCatalog(t *testing.T) {
	catalog := lintCatalogSnapshot(t)

	texts := map[string]string{} // lint-surface label -> full text

	// Ordinary role prompts (all seven; the worker's prompt exists today and
	// init() only exempts it from the MUST, so an empty non-worker prompt is
	// a fatality and an empty worker prompt is skipped, mirroring init()).
	for _, a := range coreagent.All() {
		prompt := coreagent.GetPrompt(string(a.ID))
		if prompt == "" {
			if !coreagent.IsWorkerID(a.ID) {
				t.Fatalf("role %q has no compiled prompt to lint", a.ID)
			}
			continue
		}
		texts["prompt:"+string(a.ID)] = prompt
	}

	// System-agent souls (the Judge and Plan Supervisor rubrics).
	for _, sa := range coreagent.SystemAgents() {
		soul := coreagent.SystemAgentDefaultSoul(sa.ID)
		if soul == "" {
			t.Fatalf("system agent %q has no default soul to lint", sa.ID)
		}
		texts["soul:"+string(sa.ID)] = soul
	}

	// Every file of every seeded package — SKILL.md, knowledge/*, scripts,
	// fixtures, and the seeder's own .omnipus-builtin provenance marker. The
	// four elicify-* document packages are IN this walk (their
	// harness-agnostic prose yields zero marks, which is correct); the walk
	// also means a mark that ever appears there would be validated too.
	skillMDMarks := map[string]int{} // package dir -> marks in its SKILL.md
	for _, f := range seededEmbeddedFiles(t) {
		texts[f.relPath] = f.text
		if f.isSkillMD {
			skillMDMarks[f.pkg] = len(markedToolRef.FindAllString(f.text, -1))
		}
	}

	// Coverage completeness: every embedded package must have had a SKILL.md
	// processed by the walk — a package the walk somehow skipped is a package
	// this lint does not cover, which is exactly the silent miss it exists to
	// prevent.
	if len(skillMDMarks) == 0 {
		t.Fatal("the seeded-skills walk processed no SKILL.md at all — lint coverage is vacuous")
	}

	labels := make([]string, 0, len(texts))
	for label := range texts {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	for _, label := range labels {
		for _, name := range unknownMarkedToolNames(texts[label], catalog) {
			t.Errorf("%s: marked tool reference %q does not exist in coreagent.AllStaticToolNames() — fix the name (typo/rename drift) or drop the tool: mark",
				label, name)
		}
	}

	// Non-vacuousness: a lint that nothing invokes proves nothing. Every
	// ordinary prompt and every system soul carries at least one mark, and
	// every NON-pinned package's SKILL.md does too. The elicify-* packages
	// are the deliberate exemption: their bytes are pinned to the upstream
	// elicify-ai/elicify-Skills revision (provenance inventory +
	// TestADR090_DocumentPackagesSeedPinnedAssets) and are not edited to add
	// repo-local markers.
	for _, label := range labels {
		kind, _, _ := strings.Cut(label, ":")
		if kind == "prompt" || kind == "soul" {
			if len(markedToolRef.FindAllString(texts[label], -1)) == 0 {
				t.Errorf("%s carries no explicitly marked tool reference — the FR-013 lint cannot see this surface; mark its tool names",
					label)
			}
		}
	}
	dirs := make([]string, 0, len(skillMDMarks))
	for dir := range skillMDMarks {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if !strings.HasPrefix(dir, "elicify-") && skillMDMarks[dir] == 0 {
			t.Errorf("embedded skill %q carries no explicitly marked tool reference — mark its tool names (elicify-* pinned packages are the only exemption)",
				dir)
		}
	}
}

// TestADR090_PromptSkillToolReferences_RejectsUnknownMarkedNames proves the
// detector catches the drift classes it exists for, on a FIXED synthetic
// corpus — never on production text, so the test cannot inherit whatever the
// production files happen to say. Each mutation injects one deliberate
// unknown name; the lint must report exactly that name.
func TestADR090_PromptSkillToolReferences_RejectsUnknownMarkedNames(t *testing.T) {
	catalog := lintCatalogSnapshot(t)

	const sample = "Standard tools include tool:read_file and tool:write_file.\n" +
		"Do not use tool:bash here. Load deferred tools through tool:ToolSearch and\n" +
		"skill content through tool:Skill. Plain backticks without the prefix —\n" +
		"`awaiting_supervision`, `Retry-After` — and prose like \"the execution tool.\"\n" +
		"carry no mark and must never be flagged."

	// Control: the unmutated corpus is clean, which is also the proof that
	// unmarked lookalikes are out of the extractor's scope.
	if got := unknownMarkedToolNames(sample, catalog); len(got) != 0 {
		t.Fatalf("control corpus reported unknown names %v — the extractor is over-broad", got)
	}

	mutations := []struct {
		name     string
		mutated  string
		expected string // the exact unknown name the lint must report
	}{
		{"typo", strings.Replace(sample, "tool:read_file", "tool:read_fil", 1), "read_fil"},
		{"plural drift", strings.Replace(sample, "tool:write_file", "tool:write_files", 1), "write_files"},
		{"invented tool", sample + " Also probe tool:browser_search.", "browser_search"},
		{"dead catalog name", sample + " Or schedule tool:cron.", "cron"},
		{"case-sensitive drift", strings.Replace(sample, "tool:ToolSearch", "tool:toolsearch", 1), "toolsearch"},
	}
	for _, m := range mutations {
		got := unknownMarkedToolNames(m.mutated, catalog)
		if len(got) != 1 || got[0] != m.expected {
			t.Errorf("mutation %q: lint reported %v, want exactly [%s] — the lint is blind to this drift class",
				m.name, got, m.expected)
		}
	}
}
