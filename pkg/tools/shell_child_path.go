package tools

import (
	"context"
	"os"

	"github.com/elicify-ai/omnipus/pkg/documentruntime"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// The PATH an agent's bash child gets, for checks that must agree with what
// that child can run — the skills `needs` line (skills spec r15 §5.7, FR-003).

var ctxKeyAgentBashPATH = &toolCtxKey{"agentBashPATH"}

// WithAgentBashPATH returns a child context whose AgentBashPATH resolves the
// PATH of the turn agent's bash child through resolve. The agent loop sets it
// once per turn.
func WithAgentBashPATH(ctx context.Context, resolve func(context.Context) string) context.Context {
	if resolve == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyAgentBashPATH, resolve)
}

// AgentBashPATH returns the PATH the turn agent's bash child gets. Without a
// resolver on ctx it returns the PATH of the scrubbed baseline environment a
// child inherits — never the gateway process PATH unfiltered.
func AgentBashPATH(ctx context.Context) string {
	if resolve, ok := ctx.Value(ctxKeyAgentBashPATH).(func(context.Context) string); ok {
		return resolve(ctx)
	}
	return baselineChildPATH()
}

// BashChildPATH returns the PATH the bash tool in reg would give a child for
// this turn's ctx, or the baseline child PATH when reg has no bash tool.
func BashChildPATH(ctx context.Context, reg *ToolRegistry) string {
	if reg != nil {
		if tool, ok := reg.Get("bash"); ok {
			if exec, ok := tool.(*ExecTool); ok {
				return exec.ChildPATH(ctx)
			}
		}
	}
	return baselineChildPATH()
}

// ChildPATH composes the PATH a foreground child of this turn gets, with the
// same layers executeRun composes (generic runtimes, the managed document
// runtime when it is ready, then the inherited tail). It creates nothing: the
// per-turn document cache is a run-time concern that does not change PATH.
func (t *ExecTool) ChildPATH(ctx context.Context) string {
	baseDir := t.workingDir
	if d := TurnWorkspaceDir(ctx); d != "" {
		baseDir = d
	}
	var doc documentEnvLayer
	if t.documentRuntime != nil {
		layout := t.turnDocumentLayout(baseDir)
		if _, err := documentruntime.ConverterServiceOverride(layout); err == nil {
			doc = documentEnvLayer{layout: layout, ok: true}
		}
	}
	// An unresolved runtime layer contributes nothing, exactly as in
	// executeRun (which reports it as a notice and keeps running).
	rt, _ := runtimeEnvTurn(baseDir)
	var base []string
	if t.godMode {
		base = scrubbedEnv(os.Environ())
	}
	env := composeExecutionEnv(base, doc, rt)
	if env == nil {
		return baselineChildPATH()
	}
	path, _ := envValue(env, "PATH")
	return path
}

// baselineChildPATH is the PATH of the scrubbed environment a sandboxed child
// inherits when nothing is composed over it.
func baselineChildPATH() string {
	path, _ := envValue(sandbox.ScrubGatewayEnv(), "PATH")
	return path
}
