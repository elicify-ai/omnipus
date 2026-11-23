// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// ProgramSource resolves the connected programs of the turn in ctx for one
// child (connected programs FR-010, FR-011, FR-012, FR-024, FR-027).
// privateNames are the agent-private variables the same child receives, so a
// variable two sources set is set by neither. The agent loop supplies it from
// the trusted turn context; it never fails the command: every failure is a
// value-free notice on the result. Nil = no programs.
type ProgramSource func(ctx context.Context, privateNames []string) *programs.TurnPrograms

// resolveTurnPrograms is the nil-safe call of a ProgramSource.
func resolveTurnPrograms(ctx context.Context, src ProgramSource, private []PrivateVariable) *programs.TurnPrograms {
	if src == nil {
		return nil
	}
	names := make([]string, 0, len(private))
	for _, v := range private {
		names = append(names, v.Name)
	}
	return src(ctx, names)
}

// withoutProgramConflicts drops the private variables a connected program
// also sets for this command (FR-024: two sources set one variable, so
// neither is set). The program side is already left out of the resolution.
func withoutProgramConflicts(p *programs.TurnPrograms, private []PrivateVariable) []PrivateVariable {
	if p == nil {
		return private
	}
	out := make([]PrivateVariable, 0, len(private))
	for _, v := range private {
		if !p.Withholds(v.Name) {
			out = append(out, v)
		}
	}
	return out
}

// programNotices joins the resolution's notices into one paragraph block.
func programNotices(p *programs.TurnPrograms) string {
	if p == nil {
		return ""
	}
	notice := ""
	for _, n := range p.Notices {
		notice = joinNotice(notice, n)
	}
	return notice
}

// programSegment is one shell segment and the program words the shell runs in
// it (commandWordRoles: wrappers, assignments and absolute paths resolved).
type programSegment struct {
	text  string
	words []string
}

// commandProgramSegments splits a command into its segments and their
// program words. ok is false when the command cannot be classified (unbalanced
// quotes, a substitution, a line continuation): then no word is "visible"
// and no site is granted automatically (FR-015 grants only for visible words).
func commandProgramSegments(command string) ([]programSegment, bool) {
	c := newPathUseClassifier(command)
	if !c.classifiable || c.lineContinuation {
		return nil, false
	}
	var out []programSegment
	for i := 0; i < len(c.cmd); {
		start, end := c.segmentBounds(i)
		if end > start {
			roles, _ := c.commandWordRoles(start, end)
			seg := programSegment{text: c.cmd[start:end]}
			for _, w := range c.segmentCommandWords(start, end) {
				if roles[w[0]] {
					seg.words = append(seg.words, stripShellQuotes(c.cmd[w[0]:w[1]]))
				}
			}
			if strings.TrimSpace(seg.text) != "" {
				out = append(out, seg)
			}
		}
		i = end + 1
	}
	return out, true
}

// allProgramWords returns every visible program word of a command.
func allProgramWords(command string) []string {
	segs, ok := commandProgramSegments(command)
	if !ok {
		return nil
	}
	var words []string
	for _, s := range segs {
		words = append(words, s.words...)
	}
	return words
}

// programSiteSplit is FR-015's split of a command: the union of the network
// sites of the eligible entries whose program word a segment shows, and the
// remaining segments, which are classified for network access as today.
func programSiteSplit(p *programs.TurnPrograms, command string) (sites []string, residual string) {
	if p == nil || len(p.Entries) == 0 {
		return nil, command
	}
	segs, ok := commandProgramSegments(command)
	if !ok {
		return nil, command
	}
	var matched []string
	var rest []string
	for _, s := range segs {
		hit := false
		for _, w := range s.words {
			if p.MatchesProgramWord(w) {
				hit = true
				matched = append(matched, w)
			}
		}
		if !hit {
			rest = append(rest, s.text)
		}
	}
	return p.SitesFor(matched), strings.Join(rest, "\n")
}

type programRunKey struct{}

// withTurnPrograms carries one command's resolved programs to the permission
// pre-flight (the network split of FR-015).
func withTurnPrograms(ctx context.Context, p *programs.TurnPrograms) context.Context {
	if p == nil {
		return ctx
	}
	return context.WithValue(ctx, programRunKey{}, p)
}

func turnProgramsFrom(ctx context.Context) *programs.TurnPrograms {
	p, _ := ctx.Value(programRunKey{}).(*programs.TurnPrograms)
	return p
}

// childPATH is the PATH the command's child will see: the composed
// environment's, else the sandbox baseline's (the foreground path inherits it).
func childPATH(execEnv []string, lim sandbox.Limits) string {
	if v, ok := envValue(execEnv, "PATH"); ok {
		return v
	}
	v, _ := envValue(sandboxLimitsEnv(lim), "PATH")
	return v
}

// gitVersionProbe runs `<git> --version` as an ordinary child of the same turn
// and sandbox (FR-014): through sandbox.Run with this command's limits and
// environment, its output masked through the same Attach.
func gitVersionProbe(ctx context.Context, p *programs.TurnPrograms, execEnv []string, lim sandbox.Limits) programs.GitProbe {
	return func(gitPath string) (string, error) {
		probeLim := lim
		probeLim.TimeoutSeconds = gitProbeTimeoutSeconds
		mask := p.MaskOnly()
		probeLim.Programs = &mask
		res, err := sandbox.Run(ctx, []string{gitPath, "--version"}, execEnv, probeLim)
		if err != nil {
			return "", err
		}
		return string(res.Stdout), nil
	}
}

// gitProbeTimeoutSeconds bounds the version probe (R2-M2: 10 s).
const gitProbeTimeoutSeconds = 10

// programAttachment decides the child's program set for one bash command:
// the variables, and the managed git entries when the git the command will
// run supports them (FR-013, FR-014). A probe failure adds no git entries
// and says so; it never blocks the command. The GIT_CONFIG_* names are
// reserved (FR-013): whenever managed entries are added, any inherited ones
// (God Mode's gateway environment, or a private variable) are removed first,
// with a notice.
func programAttachment(ctx context.Context, p *programs.TurnPrograms, command, cwd string, execEnv []string, lim sandbox.Limits) (programs.ResolvedPrograms, []string, string) {
	if p == nil {
		return programs.ResolvedPrograms{}, execEnv, ""
	}
	notice := ""
	gitEnv, gitNotice, gitErr := p.GitSupport(allProgramWords(command), childPATH(execEnv, lim), cwd,
		gitVersionProbe(ctx, p, execEnv, lim))
	if gitErr != nil {
		gitNotice = "Connected git logins were not used for this command: the git version could not be checked."
	}
	notice = joinNotice(notice, gitNotice)
	if len(gitEnv) > 0 {
		var replaced bool
		execEnv, replaced = programs.StripGitConfig(execEnv)
		if replaced {
			notice = joinNotice(notice, programs.MsgInheritedGitConfig)
		}
	}
	return p.Attachment(gitEnv), execEnv, notice
}
