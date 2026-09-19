package tools

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/documentruntime"
)

// appendDocumentRuntimeGuidance adds the Omnipus-specific runtime location and
// the self-serve dependency recovery route to a just-loaded first-party
// document skill. Portable elicify-* package text is left unchanged
// (ADR-090 §6.5/L1: the packaged skills carry the portable dependency
// requirements; Omnipus supplies setup guidance rather than rewriting the
// package).
//
// The route is CALLER-INDEPENDENT (ADR-090 §6.5: any permitted native agent
// uses the same route) and follows the ES-FR-05 workflow with the generic
// command/script installation tool:
//
//	probe own environment (bash) → if missing, prepare the installation
//	command or inline script from the skill/task-plan dependency guidance →
//	discover environment_setup by exact name → ordinary Ask approval (shows
//	the exact command) → background session (prompt session id; poll and read
//	the outcome; use the returned prefix/PATH; starting and exit 0 are not
//	readiness) → rerun the probe in the agent's OWN sandbox (ES-FR-04)
//	→ generate/validate/convert/render → read real images → fix → deliver.
//
// The former FR-011 Admin handoff, parent routing, finalization guidance, and
// the retired structured-dependency request format are deliberately gone: the
// generic tool encodes no packages, and no caller is handed an invented hop.
func appendDocumentRuntimeGuidance(_ context.Context, content, skillName string, layout documentruntime.Layout) string {
	root := filepath.Join(layout.Skills, skillName)
	probe := strings.Join(documentruntime.ProbeArgv(layout), " ")
	var b strings.Builder
	b.WriteString(content)
	b.WriteString("\n\n## Omnipus runtime location\n")
	b.WriteString("Resolve every relative knowledge/ and scripts/ reference above under this authorized absolute package root: `")
	b.WriteString(root)
	b.WriteString("`. The document probe command is `")
	b.WriteString(probe)
	b.WriteString("`.")
	b.WriteString("\n\n## Omnipus document runtime\n")
	b.WriteString("Probe, with bash in YOUR environment, what this workflow actually needs: the imports, commands, and renderers you will call, treating the returned prefix and PATH entries as part of that environment. The document probe command published above is a ready-made check for the classic bundled runtime; it does not gate generic installation — a failure there alone is not proof a dependency is missing, and its passing alone is not readiness for what your script installed. Then carry out the requested document workflow: render its pages, sheets, or slides to PNG/JPEG and inspect those rendered files with read_file; reading document text is not visual inspection, and you do not claim visual contents from generated text.")
	b.WriteString(" If your probe reports something missing: recover it yourself through environment_setup — discover it by its exact name through ToolSearch if it is not currently callable; a prompt mention does not load or grant it. The tool itself knows no packages: you choose and supply the installation command or inline script, taking the concrete command from this skill's dependency guidance and your task plan, plus a short purpose. Scope defaults to this workspace; request shared only for a genuinely shared runtime need, and name a target workspace only when you are authorized to use it.")
	b.WriteString(" The ordinary approval flow shows your exact command/script and gates the call. If approval is denied or the tool is unavailable, report the precise limitation and leave the task resumable; do not repeat the request or look for another route.")
	b.WriteString(" An approved call starts a background session: the result returns a session id promptly, and you poll and read that session for progress and the final result — starting the job is not installation success. Your script sees the documented environment variables OMNIPUS_ENV_PREFIX, OMNIPUS_ENV_CACHE, and OMNIPUS_ENV_TMP, and the result returns the actual prefix paths; use those returned prefix and PATH entries in your own later commands — the application does not silently know what your script installed, and exit 0 is only the command's exit status, not readiness.")
	b.WriteString(" When the session reports completion, rerun the same probe in YOUR OWN environment; installer success is not readiness, and readiness means the check passes where you will actually execute.")
	b.WriteString(" Only then create the document, validate it independently, convert Office output to a temporary PDF, render every page, sheet, or slide to images, and inspect the actual images before delivering.")
	return b.String()
}
