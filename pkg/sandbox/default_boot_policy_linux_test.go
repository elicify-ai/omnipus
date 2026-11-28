// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package sandbox

// T-123 and T-124 (office-preview spec §3.8 BR-2.1…BR-2.3; FR-065; founder
// Q100 A, 2026-11-20) — the confined Linux startup rules' content and
// refusal rules.
//
// CONTRACT the developer implements to (BR-2.2, team-lead ruling): a new
// sibling of DefaultPolicyForModel, used only by the gateway startup:
//
//	func DefaultBootPolicyForModel(
//		model FilesystemModel,
//		homePath string,
//		allowedPaths []string,
//		allowedExecPaths []string,
//		warnFn func(msg string, path string),
//		bindPorts []uint16,
//		startupGrants []string,
//	) SandboxPolicy
//
// DefaultPolicyForModel keeps its current signature and delegates with
// startupGrants = nil; basePolicyForModel never sees the grants, so
// DeriveKernelPolicy and the tool policies structurally cannot carry them.
// Grants become rules exactly like buildExecPathRules makes them
// (AccessRead|AccessExecute, refusals reused per BR-2.3), appended after the
// G = nil rules, in G's order, deduplicated, refused entries dropped with one
// warn naming them. The open model, macOS and Windows ignore the input
// byte-identically (twins: default_boot_policy_darwin_test.go,
// default_boot_policy_windows_test.go).
//
// RED on 24f2f912c: the sibling does not exist, so this file does not compile
// (spec §13: stub first, then a failing-assertion red).

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// t123GrantRights is BR-2.1's fixed access: read and execute, never write.
const t123GrantRights = AccessRead | AccessExecute

// Compile checks (T-123(g) and the sibling's own shape): the pre-amendment
// signature must not change and the sibling must take the grants last.
var (
	_ func(FilesystemModel, string, []string, []string, func(string, string), []uint16) SandboxPolicy           = DefaultPolicyForModel
	_ func(FilesystemModel, string, []string, []string, func(string, string), []uint16, []string) SandboxPolicy = DefaultBootPolicyForModel
)

// t123DebGrants is T-123(a)'s G; t123InstallerGrants is T-123(b)'s. Both are
// copied from the spec rows, not from any code constant.
var (
	t123DebGrants = []string{
		"/usr/share/omnipus/office",
		"/usr/share/omnipus/pdfium",
		"/usr/bin/omnipus",
	}
	t123InstallerGrants = []string{
		"/usr/local/share/omnipus/office",
		"/usr/local/share/omnipus/pdfium",
		"/usr/local/bin/omnipus",
	}
)

func t123BannedPaths() []string {
	return []string{"/usr/share/omnipus", "/usr/share", "/usr/local/bin"}
}

// TestT123_GrantsAppendAfterNilRulesInOrder (a, b): with G set, the rules
// equal the G = nil rules followed by exactly G's rules, in order, each
// read+execute; no parent or bin rule; no write bit.
func TestT123_GrantsAppendAfterNilRulesInOrder(t *testing.T) {
	for name, grants := range map[string][]string{
		"deb":       t123DebGrants,
		"installer": t123InstallerGrants,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			base := DefaultBootPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil, nil)
			got := DefaultBootPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil, grants)

			want := make([]PathRule, 0, len(base.FilesystemRules)+len(grants))
			want = append(want, base.FilesystemRules...)
			for _, p := range grants {
				want = append(want, PathRule{Path: p, Access: t123GrantRights})
			}
			require.True(t, reflect.DeepEqual(want, got.FilesystemRules),
				"T-123(a/b): rules must be base rules then exactly G's read+execute rules in order")

			index := q1bIndex(got.FilesystemRules)
			for _, banned := range t123BannedPaths() {
				_, ok := index[banned]
				require.False(t, ok, "T-123(a): no rule may name %s", banned)
			}
			for _, p := range grants {
				rule, ok := index[p]
				require.True(t, ok, "T-123(a): %s must be granted", p)
				require.Zero(t, rule.Access&AccessWrite, "T-123(a): %s must never carry write", p)
			}
		})
	}
}

// TestT123_NilGrantsMatchDefaultPolicyForModel (c): G = nil is deep-equal to
// DefaultPolicyForModel with the same arguments, which still carries exactly
// the three BR-1 reads (T-121 keeps its meaning through the delegating
// sibling).
func TestT123_NilGrantsMatchDefaultPolicyForModel(t *testing.T) {
	home := t.TempDir()
	boot := DefaultBootPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil, nil)
	classic := DefaultPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil)
	require.True(t, reflect.DeepEqual(classic, boot),
		"T-123(c): G = nil must be byte-identical to DefaultPolicyForModel")
	q1bAssertThreeReadRights(t, boot.FilesystemRules)
}

// TestT123_OpenModelIgnoresGrants (d): the open model is byte-identical with
// and without G.
func TestT123_OpenModelIgnoresGrants(t *testing.T) {
	home := t.TempDir()
	withGrants := DefaultBootPolicyForModel(FilesystemModelOpen, home, nil, nil, nil, nil, t123DebGrants)
	without := DefaultBootPolicyForModel(FilesystemModelOpen, home, nil, nil, nil, nil, nil)
	require.True(t, reflect.DeepEqual(without, withGrants),
		"T-123(d): the open model must ignore the grants byte-identically")
}

// TestT123_DuplicateGrantYieldsOneRule (f): a path listed twice is granted
// once.
func TestT123_DuplicateGrantYieldsOneRule(t *testing.T) {
	home := t.TempDir()
	g := []string{t123DebGrants[0], t123DebGrants[0], t123DebGrants[1], t123DebGrants[2]}
	got := DefaultBootPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil, g)
	count := 0
	for _, rule := range got.FilesystemRules {
		if rule.Path == t123DebGrants[0] {
			count++
			require.Equal(t, t123GrantRights, rule.Access)
		}
	}
	require.Equal(t, 1, count, "T-123(f): a path listed twice is granted exactly once")
}

// t124NotUnder asserts the setup precondition that path lies neither inside
// base nor covers it — T-124(h)'s declared link must be decoupled from every
// writable folder by DECLARED paths alone, so only the resolved target can
// trigger the refusal.
func t124NotUnder(t *testing.T, path, base, what string) {
	t.Helper()
	under := path == base || strings.HasPrefix(path, base+string(filepath.Separator))
	covers := base == path || strings.HasPrefix(base, path+string(filepath.Separator))
	require.False(t, under, "T-124(h) precondition: the declared link must not lie inside %s", what)
	require.False(t, covers, "T-124(h) precondition: the declared link must not contain %s", what)
}

// TestT124_RefusedEntriesGetNoRuleAndOneWarn — BR-2.3's refusal classes,
// each with the control /usr/share/omnipus/office still granted. The refused
// entry gets no rule, warn fires exactly once for it, and the control keeps
// its read+execute rule.
func TestT124_RefusedEntriesGetNoRuleAndOneWarn(t *testing.T) {
	home := t.TempDir()
	writable := filepath.Join(home, "agent-write-root")
	require.NoError(t, os.Mkdir(writable, 0700))
	tmpdir := filepath.Join(home, "custom-tmp")
	require.NoError(t, os.Mkdir(tmpdir, 0700))

	insideHome := filepath.Join(home, "system", "components", "office", "x")
	require.NoError(t, os.MkdirAll(insideHome, 0700))

	// (h) a symlink whose DECLARED path lies outside every writable folder —
	// the data folder, /tmp, the custom temp folder and every configured
	// writable root — with only its RESOLVED TARGET under /tmp. The refusal
	// must follow the link: a comparison that only matches declared paths
	// would grant this entry (CHECK F2's mutant), so the fixture asserts
	// the preconditions before the production code sees the entry. The
	// link root cannot be t.TempDir(), which itself lives under /tmp; the
	// package work directory is the scratch root outside every writable
	// folder instead.
	work, workErr := os.Getwd()
	require.NoError(t, workErr)
	linkRoot, mkErr := os.MkdirTemp(work, "t124-links-")
	require.NoError(t, mkErr)
	t.Cleanup(func() {
		if err := os.RemoveAll(linkRoot); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	linkDir := filepath.Join(linkRoot, "links")
	require.NoError(t, os.MkdirAll(linkDir, 0700))
	linkTarget := filepath.Join("/tmp", "t124-office-link-target")
	require.NoError(t, os.RemoveAll(linkTarget))
	require.NoError(t, os.MkdirAll(linkTarget, 0700))
	t.Cleanup(func() {
		if err := os.RemoveAll(linkTarget); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	link := filepath.Join(linkDir, "office-link")
	require.NoError(t, os.Symlink(linkTarget, link))
	cleanLink := filepath.Clean(link)
	homeReal, homeErr := filepath.EvalSymlinks(home)
	require.NoError(t, homeErr)
	tmpReal, tmpErr := filepath.EvalSymlinks("/tmp")
	require.NoError(t, tmpErr)
	t124NotUnder(t, cleanLink, filepath.Clean(home), "the data folder")
	t124NotUnder(t, cleanLink, homeReal, "the resolved data folder")
	t124NotUnder(t, cleanLink, "/tmp", "/tmp (declared)")
	t124NotUnder(t, cleanLink, tmpReal, "/tmp (resolved)")
	t124NotUnder(t, cleanLink, filepath.Clean(tmpdir), "the custom temp folder")
	t124NotUnder(t, filepath.Clean(home), cleanLink, "contain the data folder")
	t124NotUnder(t, "/tmp", cleanLink, "contain /tmp")
	resolvedLink, resolveErr := filepath.EvalSymlinks(link)
	require.NoError(t, resolveErr, "T-124(h) precondition: the target must exist so the refusal can resolve the link")
	require.True(t, resolvedLink == tmpReal || strings.HasPrefix(resolvedLink, tmpReal+string(filepath.Separator)),
		"T-124(h) precondition: only the RESOLVED target may lie under /tmp; got %s", resolvedLink)

	// "contains a writable allowed path": the entry is a PARENT of an
	// allowed path, in a second root with no rule of its own — home itself
	// already carries a base rule, so a parent of a writable path under
	// home could not tell the refusal from the base rule (finding D5).
	otherRoot := t.TempDir()
	writableAllowed := filepath.Join(otherRoot, "agent-write", "office")
	require.NoError(t, os.MkdirAll(writableAllowed, 0700))
	containsWritable := filepath.Dir(writableAllowed)
	require.NotEqual(t, home, containsWritable, "instrument: the entry must not be home itself, which has a base rule")

	cases := []struct {
		name  string
		entry string
		// allowedPaths feeds the policy; nil elsewhere.
		allowedPaths []string
		// tmpdir pins $TMPDIR for case (e); empty keeps the environment clean.
		setTmpdir string
	}{
		{name: "root", entry: "/"},
		{name: "restricted_system_path", entry: "/etc/omnipus/office"},
		{name: "inside_data_folder", entry: insideHome},
		{name: "inside_tmp", entry: "/tmp/omnipus-office"},
		{name: "inside_custom_tmpdir", entry: filepath.Join(tmpdir, "office"), setTmpdir: tmpdir},
		{name: "contains_data_folder", entry: filepath.Dir(home)},
		{name: "inside_writable_allowed_path", entry: filepath.Join(writable, "office"), allowedPaths: []string{writable}},
		{name: "contains_writable_allowed_path", entry: containsWritable, allowedPaths: []string{writableAllowed}},
		{name: "resolves_into_writable", entry: link},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setTmpdir != "" {
				t.Setenv("TMPDIR", tc.setTmpdir)
			}
			type warnRecord struct{ path, msg string }
			var warns []warnRecord
			warn := func(msg, path string) {
				warns = append(warns, warnRecord{path: path, msg: msg})
			}
			got := DefaultBootPolicyForModel(FilesystemModelConfined, home, tc.allowedPaths, nil, warn, nil, []string{tc.entry, "/usr/share/omnipus/office"})

			index := q1bIndex(got.FilesystemRules)
			_, refused := index[tc.entry]
			require.False(t, refused, "T-124: the refused entry %s must get no rule", tc.entry)
			control, ok := index["/usr/share/omnipus/office"]
			require.True(t, ok, "T-124: the control entry must stay granted (the refusal is per entry, not of the whole list)")
			require.Equal(t, t123GrantRights, control.Access, "T-124: the control keeps read+execute")

			// The warning names the refused entry in one of the forms the
			// rule compares (BR-2.3 judges declared and resolved paths); its
			// message is non-empty. Anything else is not a warning about it.
			resolved, resolveErr := filepath.EvalSymlinks(tc.entry)
			if resolveErr != nil {
				resolved = ""
			}
			named := 0
			for _, w := range warns {
				if w.path == tc.entry || w.path == filepath.Clean(tc.entry) || (resolved != "" && w.path == resolved) {
					named++
					require.NotEmpty(t, strings.TrimSpace(w.msg), "T-124: the refusal warning carries a reason")
				}
			}
			require.Equal(t, 1, named, "T-124: exactly one warning must name the refused entry %s; got %+v", tc.entry, warns)
		})
	}
}
