// skill_document_guidance_test.go — ADR-090 §6.5 / ES-FR-05 (self-serve
// environment_setup route for document workflows).
//
// The portable elicify-* SKILL bodies stay unchanged (hash-pinned upstream
// bytes). Omnipus-specific routing is appended only when Skill loads a
// first-party document package AND a document runtime is wired. The appended
// guidance is CALLER-INDEPENDENT (ES-FR-05: any permitted native agent uses
// the same route) and must carry the full recovery workflow:
//
//   probe own environment (bash) → discover environment_setup by exact name
//   → ordinary Ask approval → background session (start returns promptly;
//   poll/read outcome; starting is not installed) → RERUN the probe in the
//   agent's OWN sandbox (installer success is not readiness, ES-FR-04)
//   → generate/validate/convert/render → read real images (read_file)
//   → fix and rerender → deliver.
//
// No Admin handoff, no parent routing, no "keep pending" machinery (spec §3
// replaces the former Admin handoff and finalization route).
//
// Spec oracles are catalog tool names and phrase literals, not values derived
// from the helper under test.

package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/documentruntime"
)

func documentGuidanceLayout(t *testing.T, workerID string) documentruntime.Layout {
	t.Helper()
	layout, err := documentruntime.ResolveLayout(t.TempDir(), documentruntime.ManifestRevision, workerID)
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func loadSkillAs(t *testing.T, agentID, slug string, runtime *documentruntime.Layout, body string) *ToolResult {
	t.Helper()
	tool := NewSkillTool(5)
	tool.SetResolver(func(context.Context, string) SkillLoadOutcome {
		return SkillLoadOutcome{Status: SkillLoadLoaded, Content: body, CanonicalSlug: slug}
	}, func(context.Context, string) bool { return true }, nil)
	if runtime != nil {
		tool.SetDocumentRuntime(*runtime)
	}
	ctx := WithAgentID(WithToolContext(context.Background(), "cli", ""), agentID)
	return tool.Execute(ctx, map[string]any{"name": slug})
}

// assertSelfServeGuidance pins the full self-serve recovery workflow that
// ES-FR-05 and ES-FR-04 require for every executing caller.
func assertSelfServeGuidance(t *testing.T, text string) {
	t.Helper()

	// Discovery + invocation route (ES-FR-05: guidance points to
	// environment_setup instead of Admin; ADR-090 §6.5: discoverable through
	// ToolSearch outside the upfront set).
	if !strings.Contains(text, "environment_setup") {
		t.Fatalf("guidance must name environment_setup: %s", text)
	}
	if !strings.Contains(text, "ToolSearch") {
		t.Fatalf("guidance must point to exact-name ToolSearch discovery: %s", text)
	}

	// Ordinary approval, and honest limitation on denial (ES-FR-01: the
	// existing approval is the only approval; ES-BDD-07: denied tool must
	// yield a clear limitation, no unsafe retry).
	if !strings.Contains(text, "approval") {
		t.Fatalf("guidance must state the ordinary approval applies: %s", text)
	}
	if !strings.Contains(text, "denied") {
		t.Fatalf("guidance must state the denied/unavailable case reports a limitation: %s", text)
	}

	// Background session lifecycle (ES-FR-02: asynchronous background job
	// returns a session_id promptly; ES-FR-03: poll/read/kill; starting a job
	// is not installation success).
	if !strings.Contains(text, "background") {
		t.Fatalf("guidance must describe the background session: %s", text)
	}
	if !strings.Contains(text, "poll") {
		t.Fatalf("guidance must describe polling/reading the session result: %s", text)
	}

	// Readiness is verified in the caller's own sandbox, after setup
	// (ES-FR-04: a host-only install or installer-side test is insufficient).
	if !strings.Contains(text, "rerun the same probe") {
		t.Fatalf("guidance must require rerunning the probe in the caller's own environment after setup: %s", text)
	}

	// GENERIC INSTALL (current decision, supersedes structured dependency
	// requests): the agent chooses and supplies the installation command or
	// inline script, with a short purpose shown at the ordinary approval.
	if !strings.Contains(text, "command") || !strings.Contains(text, "script") {
		t.Fatalf("guidance must teach the agent to supply the installation command or script itself: %s", text)
	}
	if !strings.Contains(text, "purpose") {
		t.Fatalf("guidance must name the short purpose shown at approval: %s", text)
	}

	// The tool encodes no package catalogue: dependency knowledge comes from
	// the skill/task plan, not from Omnipus.
	if !strings.Contains(text, "knows no packages") {
		t.Fatalf("guidance must state the tool itself knows no packages: %s", text)
	}

	// Returned prefix/PATH usage: the application does not silently know what
	// an arbitrary script installed, so the agent must consume the returned
	// paths in its own later commands. The documented prefix env var
	// (OMNIPUS_ENV_PREFIX, generic-tool/storage interface agreement) is named
	// so the script and the later verification both use the same prefix.
	if !strings.Contains(text, "prefix") || !strings.Contains(text, "PATH") {
		t.Fatalf("guidance must teach using the returned prefix/PATH entries: %s", text)
	}
	if !strings.Contains(text, "OMNIPUS_ENV_PREFIX") || !strings.Contains(text, "OMNIPUS_ENV_CACHE") || !strings.Contains(text, "OMNIPUS_ENV_TMP") {
		t.Fatalf("guidance must name the documented OMNIPUS_ENV_PREFIX/CACHE/TMP variables: %s", text)
	}

	// Actual-needs probing discipline (interface convergence): the classic
	// fixed-prefix documentruntime probe is published as a convenience only —
	// it must not become a prerequisite for generic installation success.
	if !strings.Contains(text, "actually needs") {
		t.Fatalf("guidance must teach probing what the workflow actually needs: %s", text)
	}
	if !strings.Contains(text, "does not gate") {
		t.Fatalf("guidance must state the classic probe does not gate generic installation: %s", text)
	}

	// The retired structured-dependency request wording (ecosystem/kind/name/
	// version) must be gone. Scoped to the document-runtime section: the
	// location section embeds tenant paths that may legitimately contain any
	// substring.
	idx := strings.Index(text, "## Omnipus document runtime")
	if idx < 0 {
		t.Fatalf("missing Omnipus document runtime section: %s", text)
	}
	runtimeLower := strings.ToLower(text[idx:])
	for _, banned := range []string{"ecosystem", "structured dependencies"} {
		if strings.Contains(runtimeLower, banned) {
			t.Fatalf("appended guidance must not carry the retired %q request wording: %s", banned, text)
		}
	}

	// Honest result semantics: exit 0 is the command's exit, not application
	// readiness.
	if !strings.Contains(text, "exit 0") {
		t.Fatalf("guidance must state generic exit 0 is not readiness: %s", text)
	}

	// Visual inspection discipline (ES-FR-05: render page/sheet/slide images,
	// read and inspect images; reading document text is not visual
	// inspection).
	if !strings.Contains(text, "read_file") {
		t.Fatalf("guidance must require reading rendered images with read_file: %s", text)
	}

	// No legacy hop survives in the routing prose (spec §3 replaces the Admin
	// handoff and finalization route). The scan is scoped to the document
	// runtime section: it is pure guidance text, while the location section
	// embeds tenant paths that may legitimately contain any substring.
	lower := runtimeLower
	for _, banned := range []string{"switch_agent", "message_parent", "admin", "keep this document task pending"} {
		if strings.Contains(lower, banned) {
			t.Fatalf("appended guidance must not carry the legacy %q route: %s", banned, text)
		}
	}
}

func TestDocumentSkillLoad_MiaSelfServesMissingDependencies(t *testing.T) {
	layout := documentGuidanceLayout(t, "mia")
	result := loadSkillAs(t, "mia", "elicify-docx", &layout, "portable document instructions")
	if result.IsError {
		t.Fatalf("load failed: %+v", result)
	}
	text := result.ForLLM
	if !strings.Contains(text, "portable document instructions") {
		t.Fatalf("portable body dropped: %s", text)
	}
	if !strings.Contains(text, filepath.Join(layout.Skills, "elicify-docx")) {
		t.Fatalf("missing authorized package root: %s", text)
	}
	probe := strings.Join(documentruntime.ProbeArgv(layout), " ")
	if !strings.Contains(text, probe) {
		t.Fatalf("missing exact probe command %q in %s", probe, text)
	}
	if !strings.Contains(text, "bash") {
		t.Fatalf("Mia must run the probe with bash in her own environment: %s", text)
	}
	assertSelfServeGuidance(t, text)
}

func TestDocumentSkillLoad_WorkerSelfServesMissingDependencies(t *testing.T) {
	layout := documentGuidanceLayout(t, "worker")
	result := loadSkillAs(t, "worker", "elicify-pptx", &layout, "portable slides instructions")
	if result.IsError {
		t.Fatalf("load failed: %+v", result)
	}
	text := result.ForLLM
	assertSelfServeGuidance(t, text)
	if !strings.Contains(text, "bash") {
		t.Fatalf("worker must run the probe with bash in its own environment: %s", text)
	}
}

func TestDocumentSkillLoad_AdminCallerGetsSameSelfServeRoute(t *testing.T) {
	layout := documentGuidanceLayout(t, "admin")
	result := loadSkillAs(t, "admin", "elicify-xlsx", &layout, "portable xlsx instructions")
	if result.IsError {
		t.Fatalf("load failed: %+v", result)
	}
	assertSelfServeGuidance(t, result.ForLLM)
}

func TestDocumentSkillLoad_UnknownCallerGetsSameRouteWithLimitationPath(t *testing.T) {
	layout := documentGuidanceLayout(t, "custom-doc")
	result := loadSkillAs(t, "custom-writer", "elicify-xlsx", &layout, "portable xlsx")
	text := result.ForLLM
	if !strings.Contains(text, strings.Join(documentruntime.ProbeArgv(layout), " ")) {
		t.Fatalf("still publishes the probe: %s", text)
	}
	assertSelfServeGuidance(t, text)
}

func TestDocumentSkillLoad_AllFirstPartyDocumentSlugsGetGuidance(t *testing.T) {
	layout := documentGuidanceLayout(t, "mia")
	for _, slug := range []string{"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"} {
		result := loadSkillAs(t, "mia", slug, &layout, "body")
		if result.IsError || !strings.Contains(result.ForLLM, "environment_setup") {
			t.Fatalf("slug %s: %+v", slug, result)
		}
		if !strings.Contains(result.ForLLM, filepath.Join(layout.Skills, slug)) {
			t.Fatalf("slug %s missing own package root", slug)
		}
	}
}

func TestDocumentSkillLoad_DoesNotInjectOnOtherSkillPackages(t *testing.T) {
	layout := documentGuidanceLayout(t, "mia")
	result := loadSkillAs(t, "mia", "interview", &layout, "interview body only")
	if result.IsError {
		t.Fatalf("load failed: %+v", result)
	}
	if result.ForLLM != "interview body only" {
		t.Fatalf("non-document skill must be returned unchanged, got %q", result.ForLLM)
	}
}

func TestDocumentSkillLoad_NoInjectionWithoutRuntime(t *testing.T) {
	result := loadSkillAs(t, "mia", "elicify-docx", nil, "portable only")
	if result.ForLLM != "portable only" {
		t.Fatalf("unwired runtime must not append guidance, got %q", result.ForLLM)
	}
}

func TestDocumentSkillSearch_DoesNotAppendRuntimeGuidance(t *testing.T) {
	layout := documentGuidanceLayout(t, "mia")
	tool := NewSkillTool(5)
	tool.SetResolver(
		func(context.Context, string) SkillLoadOutcome { return SkillLoadOutcome{} },
		func(context.Context, string) bool { return true },
		func(context.Context) []SkillSearchDoc {
			return []SkillSearchDoc{{Slug: "elicify-docx", Description: "Word documents"}}
		},
	)
	tool.SetDocumentRuntime(layout)
	result := tool.Execute(WithAgentID(context.Background(), "mia"), map[string]any{"query": "Word documents"})
	if strings.Contains(result.ForLLM, "environment_setup") || strings.Contains(result.ForLLM, "Omnipus runtime location") {
		t.Fatalf("search must not inject runtime guidance: %s", result.ForLLM)
	}
}
