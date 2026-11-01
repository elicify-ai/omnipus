// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Skills spec r15 §5.5 and §10.1 item 7: a file-tool write into an installed
// skill folder is a skill change. ResolvePath hands back a handle whose
// WriteFile delegates to skills.ApplyChange — never raw I/O — and only when
// the agent loop's composition step put a marker on the call's context that
// names the same skill key. Without it the write is refused with
// SKILL_WRITE_NOT_APPROVED before the pipeline runs.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

type skillWriteApprovalKey struct{}

// WithSkillWriteApproval marks ctx as carrying the loop's composed skill
// write decision for skillKey (§10.1 item 7). Only the agent loop sets it,
// after composing the file tool's policy with edit_skill.
func WithSkillWriteApproval(ctx context.Context, skillKey string) context.Context {
	return context.WithValue(ctx, skillWriteApprovalKey{}, skills.SkillKey(skillKey))
}

// SkillWriteApproval returns the skill key the loop composed for this call
// ("" when none).
func SkillWriteApproval(ctx context.Context) string {
	v, _ := ctx.Value(skillWriteApprovalKey{}).(string)
	return v
}

// RegistrySkillKeyForPath reports the skill key a real path inside
// <home>/skills/<slug>/ belongs to. ok is false for any path outside a skill
// folder (including the skills root itself and its control entries).
func RegistrySkillKeyForPath(realAbs string) (key string, ok bool) {
	_, slug, _, ok := registrySkillTarget(realAbs)
	if !ok {
		return "", false
	}
	return skills.SkillKey(slug), true
}

// registrySkillTarget splits a real path under the registry into the skills
// root, the first component (the skill slug as written) and the path inside
// the folder ("" for the folder itself).
func registrySkillTarget(realAbs string) (root, slug, rel string, ok bool) {
	root = registrySkillsRoot()
	clean := filepath.Clean(realAbs)
	if !fspolicy.CoversForDeny(root, clean) {
		return "", "", "", false
	}
	r, err := filepath.Rel(root, clean)
	if err != nil || r == "." || !filepath.IsLocal(r) {
		return "", "", "", false
	}
	parts := strings.SplitN(filepath.ToSlash(r), "/", 2)
	slug = parts[0]
	if strings.HasPrefix(slug, ".") || !skills.ValidSlug(slug) {
		return "", "", "", false
	}
	if len(parts) == 2 {
		rel = parts[1]
	}
	return root, slug, rel, true
}

// skillWriteTarget is carried on a PathHandle that names a file inside an
// installed skill folder. All fields are captured at ResolvePath time.
type skillWriteTarget struct {
	root        string
	slug        string
	rel         string
	approvedKey string
	actor       skills.ChangeActor
	// message is the pipeline's success line after a committed write.
	message string
}

func newSkillWriteTarget(ctx context.Context, toolName, realAbs string) (*skillWriteTarget, bool) {
	root, slug, rel, ok := registrySkillTarget(realAbs)
	if !ok || rel == "" {
		return nil, false
	}
	return &skillWriteTarget{
		root: root, slug: slug, rel: rel,
		approvedKey: SkillWriteApproval(ctx),
		actor: skills.ChangeActor{
			Kind: "agent", AgentID: ToolAgentID(ctx), SessionID: ToolTranscriptSessionID(ctx),
			WorkspaceID: ToolWorkspaceID(ctx), Tool: toolName,
		},
	}, true
}

// write applies data through the change pipeline per the §5.5 write table.
func (s *skillWriteTarget) write(data []byte) error {
	key := skills.SkillKey(s.slug)
	if s.approvedKey != key {
		return &skills.ChangeError{Code: skills.ErrSkillWriteNotApproved,
			Text: fmt.Sprintf("this write targets skill '%s' but was not checked as a skill change; retry the call", s.slug)}
	}
	onDisk, present, err := skills.ResolveInstalledSlug(s.root, s.slug)
	if err != nil {
		return fmt.Errorf("could not read skill '%s': %w", s.slug, err)
	}
	session := s.actor.SessionID
	req := skills.ChangeRequest{Slug: onDisk, Content: data, Actor: s.actor}
	if present {
		rev, seen := skills.SeenRevision(session, onDisk)
		if !seen {
			return &skills.ChangeError{Code: skills.ErrSkillNotRead,
				Text: fmt.Sprintf("read skill '%s' first (Skill, get_skill, read_file or list_directory), then retry", onDisk)}
		}
		req.Kind, req.Path, req.Expect = skills.KindEdit, s.rel, skills.Expectation{Revision: rev}
	} else {
		if s.rel != "SKILL.md" {
			return &skills.ChangeError{Code: skills.ErrInvalidInput, Text: "create a skill by writing its SKILL.md first"}
		}
		req.Kind, req.Expect = skills.KindCreate, skills.Expectation{Absent: true}
	}
	out, err := skills.ApplyChange(s.root, req)
	if err != nil {
		if errors.Is(err, skills.ErrSkillPartiallySaved) {
			skills.ForgetSeen(session, onDisk)
		}
		return err
	}
	skills.RecordSeen(session, onDisk, out.Revision) // the session's own committed change (§7.3)
	s.message = out.Message
	if out.Warning != "" {
		s.message = out.Warning
	}
	return nil
}

// SkillChangeNote returns the version line a committed skill write appends to
// the file tool's success text (§5.5 closing rule), or "" for every other
// write — which keeps its text byte for byte.
func (h *PathHandle) SkillChangeNote() string {
	if h == nil || h.skillWrite == nil || h.skillWrite.message == "" {
		return ""
	}
	return "\n" + h.skillWrite.message
}

// recordSkillSeen records the session's observation of the skill a read or
// listing handle lies in (§7.3: read_file / list_directory inside the folder
// set "seen"). The revision comes from one captured observation of the
// folder. A failed observation records nothing and clears any earlier record:
// a read that could not observe the state cannot vouch for a revision.
func recordSkillSeen(ctx context.Context, realAbs string) {
	session := ToolTranscriptSessionID(ctx)
	if session == "" {
		return
	}
	root, slug, _, ok := registrySkillTarget(realAbs)
	if !ok {
		return
	}
	onDisk, present, err := skills.ResolveInstalledSlug(root, slug)
	if err != nil || !present || skills.IsBuiltin(onDisk) {
		skills.ForgetSeen(session, slug)
		return
	}
	obs, err := skills.ObserveSkill(root, onDisk, skills.VersionQuery{})
	if err != nil || obs == nil || obs.Revision == "" {
		skills.ForgetSeen(session, onDisk)
		return
	}
	skills.RecordSeen(session, onDisk, obs.Revision)
}
