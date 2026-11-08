//go:build darwin

package preview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// T-11 / §3.4 D8c, D8d; MV-3; B-69, B-70: the macOS sandbox profile of a
// real converter start equals the converter's own read list plus the D8d
// look-up lines and the socket posture — compared against the exact profile
// the start used, read from the captured `-p` argument of that very start
// (the accepted seam pkg/preview/confine.go::testHookConverterStart, §3.4
// D8d ruling), never a profile the test builds. The start is a real confined
// LibreOffice start under the real launcher; one conversion is run to
// produce it. The conversion result is not this row's oracle: at the D8c
// state on macOS LibreOffice aborts at start-up (DeploymentException — the
// parent-folder look-up gap D8d closes); the START happened, and the profile
// is what this row reads.
//
// Held oracle (from MV-3 and §3.4 D8c + D8d, not from the code):
//
//   - every rule of the captured policy renders: read rules as
//     `(allow file-read* (subpath "<resolved>"))`, execute as
//     `(allow process-exec (subpath …))`, write as
//     `(allow file-write* (subpath …))`, for the resolved real path — and
//     nothing beyond those rules, the D8d look-up lines, the 3 socket rules
//     and the preamble appears in the policy section: no rule from the
//     turn's policy, no extra folder;
//   - D8d: exactly one `(allow file-read-metadata (literal "<folder>"))`
//     line per folder above every listed folder, computed independently by
//     this test from the captured policy's real paths, `/` excluded; no
//     `file-read*`, `file-write*` or `process-exec` line covers any of those
//     folders (look-up only);
//   - the 3 socket rules for this call's socket folder (read, write,
//     bind+connect); `(deny network*)`; Mach lookups exactly 2 (the macOS
//     preamble's two);
//   - ReadsOpen and ExecOpen are false and the policy carries no deny list
//     (D8c), so no blanket read/execute line appears.
//
// Not a pass if: the profile is one the test assembled; the hook was never
// called (zero captured starts fails, never skips); the start ran
// unconfined; the expected look-up list is copied from the code under test.
func TestPreviewConverterProfileMatchesOwnReadList(t *testing.T) {
	primeKernelSandbox(t)
	officeRoot := os.Getenv("OMNIPUS_TEST_OFFICE_ROOT")
	if officeRoot == "" {
		t.Fatal("BLOCKED: real LibreOffice fixture unavailable; set OMNIPUS_TEST_OFFICE_ROOT — required by T-11 (the profile compared is the one a real LibreOffice start used)")
	}
	home := t.TempDir()
	opts := component.RegisterOptions{Home: home, Operator: "office-preview-test", Folder: officeRoot}
	_, err := component.RegisterOffice(opts)
	require.NoError(t, err, "instrument: the real LibreOffice fixture registers")
	self, err := os.Executable()
	require.NoError(t, err)
	comp, problem := (&component.Resolver{Home: home, Executable: self}).Resolve(component.KindOffice)
	require.Nil(t, problem, "instrument: the registered real LibreOffice resolves through the real load and verifies")
	require.NotEqual(t, "", comp.Registry, "instrument: the macOS office component has a filtered registry folder for the read-only rule to cover")

	var starts []converterStart
	testHookConverterStart = func(cs converterStart) { starts = append(starts, cs) }
	t.Cleanup(func() { testHookConverterStart = nil })

	conv, c := fakeOfficeCall(t, home, "valid")
	conv.registry = comp.Registry
	conv.executable = comp.Executable
	conv.root = comp.Root
	defer c.finish()
	_, unconfined, convErr := conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
	_ = convErr // tolerated: the D8d-pending start-up crash; see the doc comment
	require.Equal(t, "", unconfined, "T-11: the start this row reads ran confined — an unconfined run proves nothing about the profile")

	// Zero captured starts fails, never skips (seam ruling, §3.4 D8d).
	require.NotEmpty(t, starts, "T-11: the hook captured zero converter starts — a test expecting a confined start fails, never skips")
	cs := starts[len(starts)-1]
	profile := seatbeltProfileOf(t, cs)
	marker := strings.Index(profile, ";; --- Filesystem allows")
	require.NotEqual(t, -1, marker, "T-11: the profile marks its policy section")
	section := profile[marker:]

	// The expected look-up folders, computed independently from the captured
	// policy's real paths — never copied from the code under test (T-11
	// false-pass note).
	parents := previewLookupParentFolders(t, cs.policy)
	for _, parent := range parents {
		require.Contains(t, section, previewAllowLineExact(parent),
			"T-11 §3.4 D8d/MV-3: the profile carries exactly one look-up line for %s", parent)
	}

	// The expected rule set from the captured policy: every filesystem rule's
	// rendered lines, and the socket rules. Anything else in the policy
	// section is a rule the converter's own list does not name — a
	// turn-policy rule or an extra folder — and fails.
	type ruleKey struct {
		verb string
		path string
	}
	expected := map[ruleKey]bool{}
	for _, rule := range cs.policy.FilesystemRules {
		resolved, linkErr := filepath.EvalSymlinks(rule.Path)
		require.NoError(t, linkErr, "instrument: a captured rule path resolves: %s", rule.Path)
		if rule.Access&sandbox.AccessExecute != 0 {
			expected[ruleKey{"process-exec", resolved}] = true
		}
		if rule.Access&(sandbox.AccessRead|sandbox.AccessExecute) != 0 {
			expected[ruleKey{"file-read*", resolved}] = true
		}
		if rule.Access&sandbox.AccessWrite != 0 {
			expected[ruleKey{"file-write*", resolved}] = true
		}
	}
	for _, rule := range cs.policy.UnixSocketRules {
		resolved, linkErr := filepath.EvalSymlinks(rule.Path)
		require.NoError(t, linkErr, "instrument: the captured socket folder resolves")
		if rule.Bind {
			expected[ruleKey{"network-bind", resolved}] = true
		}
		if rule.Connect {
			expected[ruleKey{"network-outbound", resolved}] = true
		}
	}

	metadataCount := 0
	for _, line := range previewParseAllows(section) {
		if line.verb == "file-read-metadata" && line.path != "" {
			metadataCount++
			require.Contains(t, parents, line.path,
				"T-11 §3.4 D8d: every look-up line names a folder above a listed folder, never anything else — found %s", line.path)
			continue
		}
		require.True(t, expected[ruleKey{line.verb, line.path}],
			"T-11/MV-3: the profile carries a rule the converter's own list does not name — no turn-policy rule and no extra folder may appear: %s", line.raw)
		for _, parent := range parents {
			require.NotEqual(t, parent, line.path,
				"T-11 §3.4 D8d/MV-3: no %s line covers the look-up folder %s (look-up only)", line.verb, parent)
		}
	}
	require.Equal(t, len(parents), metadataCount,
		"T-11 §3.4 D8d/MV-3: exactly one look-up line per folder above the listed folders, %d expected", len(parents))

	require.False(t, cs.policy.ReadsOpen, "T-11 §3.4 D8c/MV-3: ReadsOpen is false — no blanket read line may appear")
	require.False(t, cs.policy.ExecOpen, "T-11 §3.4 D8c/MV-3: ExecOpen is false — no blanket execute line may appear")
	require.Empty(t, cs.policy.DeniedPaths, "T-11 §3.4 D8c/MV-3: the converter policy carries no deny list")
	require.Empty(t, cs.policy.DeniedNodes, "T-11 §3.4 D8c/MV-3: the converter policy carries no deny list")
	require.Empty(t, cs.policy.DeniedPathPrefixes, "T-11 §3.4 D8c/MV-3: the converter policy carries no deny list")
	require.Empty(t, cs.policy.WriteDeniedPaths, "T-11 §3.4 D8c/MV-3: the converter policy carries no deny list")
	require.Len(t, cs.policy.UnixSocketRules, 1, "T-11/MV-3: the converter carries exactly the socket folder rule (the 3 socket rules: read, write, bind+connect)")
	assertConverterProfilePosture(t, cs, profile)

	// The captured start's command line is the wrapped sandbox-exec line, so
	// the profile read above is the exact one this start used.
	require.Equal(t, "/usr/bin/sandbox-exec", cs.args[0],
		"T-11: the captured start is a sandbox-exec wrap (the profile came from its -p argument)")
}
