// Package realprograms is leaf test support: it must never import pkg/preview
// or its parent officepreview package, which would create a test import cycle.
package realprograms

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Roots returns every requested fixture root unchanged. Missing programs are
// fatal locally and in the dedicated job. A normal job may hand over ONLY a
// top-level test explicitly named in the committed real-program test list.
func Roots(t *testing.T, office, renderer bool) (officeRoot, rendererRoot string) {
	t.Helper()
	var missing []string
	if office {
		officeRoot = os.Getenv("OMNIPUS_TEST_OFFICE_ROOT")
		if officeRoot == "" {
			missing = append(missing, "BLOCKED: real LibreOffice fixture unavailable; set OMNIPUS_TEST_OFFICE_ROOT — required by office-preview T-10/T-17/T-30")
		}
	}
	if renderer {
		rendererRoot = os.Getenv("OMNIPUS_TEST_RENDERER_ROOT")
		if rendererRoot == "" {
			missing = append(missing, "BLOCKED: real renderer fixture unavailable; set OMNIPUS_TEST_RENDERER_ROOT — required by office-preview T-02")
		}
	}
	if len(missing) == 0 {
		return officeRoot, rendererRoot
	}
	problem := strings.Join(missing, "; ")
	job := os.Getenv("OMNIPUS_OFFICE_REAL_TESTS_JOB")
	if job == "" {
		t.Fatal(problem)
	}
	data, err := realTestsList()
	if err != nil {
		t.Fatalf("%s; cannot read real-program test list: %v; this test would otherwise run nowhere", problem, err)
	}
	top, _, _ := strings.Cut(t.Name(), "/")
	listed := false
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("%s; malformed real-program test list line %q; refusing to hand over %s", problem, line, top)
		}
		if fields[1] == top {
			listed = true
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		t.Fatalf("%s; cannot read real-program test list: %v", problem, scanErr)
	}
	if !listed {
		t.Fatalf("%s; %s is not listed in scripts/office-preview-real/real-tests.txt; this test would otherwise run nowhere", problem, top)
	}
	t.Skipf("%s belongs to job %q: %s", top, job, problem)
	return "", ""
}

// Go tests start in their package directory. Walking upward also works for
// isolated re-executions, without depending on an absolute source/build path.
func realTestsList() ([]byte, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		path := filepath.Join(dir, "scripts", "office-preview-real", "real-tests.txt")
		data, readErr := os.ReadFile(path)
		if !errors.Is(readErr, os.ErrNotExist) {
			return data, readErr
		}
		if filepath.Dir(dir) == dir {
			return nil, fmt.Errorf("real-tests.txt not found above %s: %w", wd, os.ErrNotExist)
		}
	}
}
