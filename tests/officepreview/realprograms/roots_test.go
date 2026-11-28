package realprograms

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// Q61/R1 is the oracle. These are real Roots calls in isolated processes,
// with real environment reads and a synthetic on-disk list. The parent
// verifies fatal/skip/run separately; an expected child skip is never a PASS
// claim for the product test, and no parent test is skipped.
func TestRoots(t *testing.T) {
	if os.Getenv("OMNIPUS_TEST_ROOTS_CHILD") == "1" {
		rootsChild(t)
		return
	}
	for _, row := range []rootsCase{
		{name: "all_requested_roots_set_runs_without_reading_list", office: true, renderer: true, officeSet: true, rendererSet: true, job: "Office preview real programs", unreadable: true, outcome: "run"},
		{name: "local_missing_office_is_fatal", office: true, renderer: true, rendererSet: true, outcome: "fatal", message: "BLOCKED: real LibreOffice fixture unavailable; set OMNIPUS_TEST_OFFICE_ROOT — required by office-preview T-10/T-17/T-30"},
		{name: "dedicated_missing_renderer_is_fatal", office: true, renderer: true, officeSet: true, outcome: "fatal", message: "BLOCKED: real renderer fixture unavailable; set OMNIPUS_TEST_RENDERER_ROOT — required by office-preview T-02"},
		{name: "listed_top_level_hands_nested_test_to_named_job", office: true, renderer: true, listed: true, job: "Office preview real programs", outcome: "skip", message: `belongs to job "Office preview real programs"`},
		{name: "unlisted_name_is_fatal_not_a_prefix_or_column_one_match", office: true, job: "Office preview real programs", outcome: "fatal", message: "is not listed"},
		{name: "unreadable_list_is_fatal", renderer: true, job: "Office preview real programs", unreadable: true, outcome: "fatal", message: "cannot read real-program test list"},
		{name: "unrequested_office_does_not_block_renderer", renderer: true, rendererSet: true, job: "Office preview real programs", unreadable: true, outcome: "run"},
		{name: "neither_program_requested_returns_empty_roots", job: "Office preview real programs", unreadable: true, outcome: "run"},
	} {
		t.Run(row.name, func(t *testing.T) { runRootsCase(t, row) })
	}
}

type rootsCase struct {
	name                   string
	office, renderer       bool
	officeSet, rendererSet bool
	listed, unreadable     bool
	job, outcome, message  string
}

func runRootsCase(t *testing.T, row rootsCase) {
	t.Helper()
	root := t.TempDir()
	list := filepath.Join(root, "scripts", "office-preview-real", "real-tests.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(list), 0700))
	if row.unreadable {
		// A directory cannot be read as a file, also on elevated CI accounts.
		require.NoError(t, os.Mkdir(list, 0700))
	} else {
		contents := "# no partial or first-column matches\nTestRoots TestOther\npkg/example TestRootsLonger\n"
		if row.listed {
			contents += "pkg/example TestRoots\n"
		}
		require.NoError(t, os.WriteFile(list, []byte(contents), 0600))
	}
	officeRoot, rendererRoot := "", ""
	if row.officeSet {
		officeRoot = filepath.Join(root, "Office root with spaces")
	}
	if row.rendererSet {
		rendererRoot = filepath.Join(root, "renderer-root")
	}
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, "-test.v", "-test.count=1", "-test.run=^"+regexp.QuoteMeta("TestRoots")+"$")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"OMNIPUS_TEST_ROOTS_CHILD=1",
		"OMNIPUS_TEST_ROOTS_WANT_OFFICE="+strconv.FormatBool(row.office),
		"OMNIPUS_TEST_ROOTS_WANT_RENDERER="+strconv.FormatBool(row.renderer),
		"OMNIPUS_TEST_OFFICE_ROOT="+officeRoot,
		"OMNIPUS_TEST_RENDERER_ROOT="+rendererRoot,
		"OMNIPUS_OFFICE_REAL_TESTS_JOB="+row.job)
	output, runErr := cmd.CombinedOutput()
	text := string(output)
	t.Logf("Roots child outcome=%s:\n%s", row.outcome, text)
	if row.outcome == "fatal" {
		var exit *exec.ExitError
		require.ErrorAs(t, runErr, &exit)
		require.Equal(t, 1, exit.ExitCode())
		require.Contains(t, text, "--- FAIL: TestRoots/nested/roots (")
		require.NotContains(t, text, "--- SKIP:")
		require.NotContains(t, text, "ROOTS_RETURNED")
	} else {
		require.NoError(t, runErr, "%s", text)
		if row.outcome == "skip" {
			require.Contains(t, text, "--- SKIP: TestRoots/nested/roots (")
			require.NotContains(t, text, "ROOTS_RETURNED")
		} else {
			require.Contains(t, text, "ROOTS_RETURNED")
			require.NotContains(t, text, "--- SKIP:")
		}
	}
	if row.message != "" {
		require.Contains(t, text, row.message)
	}
}

func rootsChild(t *testing.T) {
	t.Helper()
	office := os.Getenv("OMNIPUS_TEST_ROOTS_WANT_OFFICE") == "true"
	renderer := os.Getenv("OMNIPUS_TEST_ROOTS_WANT_RENDERER") == "true"
	t.Run("nested/roots", func(t *testing.T) {
		officeRoot, rendererRoot := Roots(t, office, renderer)
		wantOffice, wantRenderer := "", ""
		if office {
			wantOffice = os.Getenv("OMNIPUS_TEST_OFFICE_ROOT")
		}
		if renderer {
			wantRenderer = os.Getenv("OMNIPUS_TEST_RENDERER_ROOT")
		}
		require.Equal(t, wantOffice, officeRoot)
		require.Equal(t, wantRenderer, rendererRoot)
		fmt.Println("ROOTS_RETURNED")
	})
}
