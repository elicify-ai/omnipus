// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-092 D3: a wrapper's own Deny remains binding in Ask and God Mode.
// Evaluation and enforcement both run before assertions; nothing is executed.
func TestEnforceShellPermissionMode_WrapperOwnDenyPreserved(t *testing.T) {
	bin := t11PolicyMetadata(t)
	inner, err := filepath.EvalSymlinks(filepath.Join(bin, "printf"))
	require.NoError(t, err, "fixture: resolve the inner metadata path")
	for _, mode := range []ShellMode{ShellModeAsk, ShellModeGod} {
		for _, c := range t11WrapperToolPolicyCases(t) {
			t.Run(string(mode)+"/"+c.name, func(t *testing.T) {
				tool, ctx, requester := t11WrapperPolicyTool(t, mode, c.rules)
				verdict := tool.EvaluateCommandRules(c.command)
				perm, refusal := tool.enforceShellPermissionMode(ctx, c.command)
				var actualMode ShellMode
				if perm != nil {
					actualMode = perm.mode
				}
				record, err := json.Marshal(map[string]any{
					"name": c.name, "mode": mode, "command": c.command, "rules": c.rules, "want_action": c.want,
					"verdict": verdict, "permission_present": perm != nil, "permission_mode": actualMode,
					"refusal": refusal, "approval_calls": requester.callCount(),
				})
				require.NoError(t, err, "fixture: record actual evaluation and permission disposition")
				t.Logf("WRAPPER_MODE_CASE %s", record)
				require.Len(t, verdict.Segments, 1, "wrapper policy: one command segment")
				assert.Equal(t, c.want, verdict.Action, "wrapper policy: the configured Deny must survive unwrapping")
				seg := verdict.Segments[0]
				assert.Equal(t, c.want, seg.Action, "wrapper policy: the segment retains its rule action")
				require.False(t, seg.Blind, "wrapper policy: this is not an unrelated blind decision")
				require.Empty(t, seg.BlindReason, "wrapper policy: no blind reason is expected")
				require.Equal(t, 0, requester.callCount(), "wrapper policy: neither Deny nor no-rule handling prompts here")
				if c.want == shellrule.ActionDeny {
					require.NotNil(t, refusal, "wrapper policy: an operator Deny requires a hard refusal in this mode")
					require.Nil(t, perm, "wrapper policy: a refused command cannot receive usable permission")
					require.True(t, refusal.IsError, "wrapper policy: the refusal is an error result")
					require.Contains(t, refusal.ForLLM, "operator rule", "wrapper policy: identify the refusal cause")
					require.Contains(t, refusal.ForLLM, "binary=\""+c.rules[0].Binary+"\"", "wrapper policy: identify the configured rule")
					require.Equal(t, &c.rules[0], seg.MatchedRule, "wrapper policy: the exact configured rule must match")
				} else {
					require.Nil(t, refusal, "no-rule control: do not manufacture a refusal")
					require.NotNil(t, perm, "no-rule control: ordinary permission handling stays reachable")
					require.Equal(t, mode, perm.mode, "no-rule control: retain the selected mode")
					require.Nil(t, seg.MatchedRule, "no-rule control: no rule is configured")
					require.Equal(t, inner, seg.ResolvedPath, "no-rule control: retain the inner program's resolved path")
				}
				require.False(t, verdict.FullyAllowed(), "wrapper policy: no allow rule is configured")
			})
		}
	}
}

type t11WrapperToolPolicyCase struct {
	name, command string
	rules         []shellrule.Rule
	want          shellrule.Action
}

func t11WrapperToolPolicyCases(t *testing.T) []t11WrapperToolPolicyCase {
	t.Helper()
	// Two spellings, two wrapper depths and three independent rule classes.
	cases := make([]t11WrapperToolPolicyCase, 0, 2*2*3)
	for _, spelling := range []string{"sudo", "ſudo"} {
		for _, outer := range []string{"", "env "} {
			depth := "direct"
			if outer != "" {
				depth = "nested"
			}
			command := outer + spelling + " printf q43-control"
			cases = append(cases,
				t11WrapperToolPolicyCase{depth + "/wrapper_deny/" + spelling, command, []shellrule.Rule{{Action: shellrule.ActionDeny, Binary: spelling}}, shellrule.ActionDeny},
				t11WrapperToolPolicyCase{depth + "/inner_deny/" + spelling, command, []shellrule.Rule{{Action: shellrule.ActionDeny, Binary: "printf"}}, shellrule.ActionDeny},
				t11WrapperToolPolicyCase{depth + "/no_rule/" + spelling, command, nil, shellrule.ActionNone},
			)
		}
	}
	require.Len(t, cases, 12, "fixture: retain all 2*2*3 policy rows")
	return cases
}

func t11PolicyMetadata(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	names := []string{"sudo", "ſudo", "env", "printf", "sh", "ſh", "shish", "ſhish", "reviewer"}
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("inert metadata; never executed\n"), 0o700), "fixture: create %q", name)
	}
	t.Setenv("PATH", bin)
	for _, name := range names {
		resolved, err := shellrule.ResolveBinary(name, bin)
		require.NoError(t, err, "fixture: %q must resolve before policy assertions", name)
		t.Logf("POLICY_RESOLVE name=%q path=%q", name, resolved)
	}
	return bin
}

func t11WrapperPolicyTool(t *testing.T, mode ShellMode, rules []shellrule.Rule) (*ExecTool, context.Context, *fakeShellApprovalRequester) {
	t.Helper()
	requester := &fakeShellApprovalRequester{approve: true}
	tool, err := NewExecToolWithDeps(t.TempDir(), true, nil, ExecToolDeps{
		GodMode: mode == ShellModeGod, ShellMode: fakeShellModeResolver{mode: mode},
		ApprovalRequester: requester, ApprovalGrants: security.NewApprovalGrantStore(), CommandRules: rules,
	})
	require.NoError(t, err, "fixture: construct the real tool with command rules")
	ctx := WithTurnID(WithTranscriptSessionID(WithAgentID(context.Background(), "t11-policy"), "t11-policy-session"), "t11-policy-turn")
	require.Equal(t, mode, tool.resolveShellMode(ctx), "fixture: exercise the selected mode")
	return tool, ctx, requester
}

// ADR-092 D4: recognized shell heads are exact-grant-only. Script and inline
// forms are checked through the public builder; near names stay ordinary.
func TestBashPrefixGrant_ShellSpellingExclusions(t *testing.T) {
	bin := t11PolicyMetadata(t)
	cases := []struct {
		name, command, program string
		shell, inline          bool
	}{
		{"plain_script", "sh sample.sh", "sh", true, false},
		{"folded_script", "ſh sample.sh", "ſh", true, false},
		{"plain_inline", `sh -c "printf q43-control"`, "sh", true, true},
		{"folded_inline", `ſh -c "printf q43-control"`, "ſh", true, true},
		{"plain_near_name", "shish sample.sh", "shish", false, false},
		{"folded_near_name", "ſhish sample.sh", "ſhish", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := map[string]any{"command": c.command, "cwd": "folder-a", "run_in_background": false}
			head, expansion, normalized := shellCommandHeadDetailed(c.command)
			excluded := isPrefixGrantWrapperHead(head)
			opts := shellRuleOptions()
			prefix, prefixOK := shellrule.SuggestedPrefix(c.command, opts.Platform, opts.HeadResolver)
			grant, ok := BashPrefixGrantFor(args)
			wantGrant := !c.shell && runtime.GOOS != "windows"
			record, err := json.Marshal(map[string]any{
				"name": c.name, "command": c.command, "head": head, "head_excluded": excluded, "shell": c.shell, "inline": c.inline,
				"prefix": prefix, "prefix_ok": prefixOK, "grant": grant, "grant_ok": ok, "want_grant": wantGrant,
			})
			require.NoError(t, err, "fixture: record both prefix consumers and the actual grant")
			t.Logf("SHELL_GRANT_CASE %s", record)
			require.False(t, expansion, "fixture: use a plain resolved program name")
			require.False(t, normalized, "fixture: the selected spelling reaches the grant-head consumer unchanged")
			if !c.inline {
				assert.Equal(t, c.shell, excluded, "shell grant: the normalized script-call head has the specified exclusion")
			}
			assert.Equal(t, wantGrant, ok, "shell grant: shell forms are exact-only; ordinary prefixes remain available on POSIX")
			if !wantGrant {
				require.Equal(t, security.ShellPrefixGrant{}, grant, "shell grant: a rejected prefix returns no usable grant")
			} else {
				binary, err := filepath.EvalSymlinks(filepath.Join(bin, c.program))
				require.NoError(t, err, "fixture: canonicalize the ordinary metadata path")
				require.Equal(t, security.ShellPrefixGrant{Binary: binary, ArgPrefix: "sample.sh", RunInBackground: false}, grant, "control: derive the exact ordinary prefix")
			}
		})
	}
}
