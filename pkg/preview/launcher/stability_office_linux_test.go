//go:build linux

package launcher_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/preview/extracttext"
	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/elicify-ai/omnipus/tests/officepreview/realprograms"
	"github.com/stretchr/testify/require"
)

func officeHasRuntimeFatal(stderr string) bool {
	return strings.Contains(stderr, "fatal error:") && (strings.Contains(stderr, "runtime") || strings.Contains(stderr, "out of memory"))
}

// Prove a zero runtime-fatal count means something. This is the specified
// direct-under-AS reproducer, never a fabricated string passed to a matcher.
func officeProveFatalClassifier(t *testing.T, self string) {
	t.Helper()
	prlimit, err := exec.LookPath("prlimit")
	if err != nil {
		t.Fatal("BLOCKED: prlimit unavailable for real fatal-classifier control — required by office-preview T-77")
	}
	cmd := exec.Command(prlimit, "--as=536870912", "--", self, "-test.run=^$")
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "T-77: positive control must actually produce the known runtime fatal")
	require.True(t, officeHasRuntimeFatal(string(output)), "T-77: classifier must recognize the actual known fatal, got %s", output)
}

func TestRunLimitedLinuxLimitsPrecedeGoByteAndEOFStartsNothing(t *testing.T) {
	probe := officeNativeLimitProbe(t)
	proc, output := officeStartLimitProbe(t, probe, []string{"limits"}, 512<<20, 0)
	var observed map[string][2]uint64
	// Readiness recognizes EITHER old AS or required DATA installation. It
	// therefore cannot hide the old implementation by waiting only for DATA.
	require.Eventually(t, func() bool {
		limits, readErr := officeProcLimits(proc.PID)
		if readErr != nil {
			return false
		}
		observed = limits
		return limits["Max data size"][0] == 512<<20 || limits["Max address space"][0] == 512<<20
	}, 5*time.Second, 5*time.Millisecond, "launcher must reach its pre-go limit installation: %s", proc.Stderr())
	require.Equal(t, [2]uint64{512 << 20, 512 << 20}, observed["Max data size"], "T-77: limits already installed while launcher waits for go-byte")
	require.LessOrEqual(t, observed["Max stack size"][0], uint64(8<<20))
	require.Equal(t, observed["Max stack size"][0], observed["Max stack size"][1])
	require.NoError(t, proc.Stdin.Close())
	require.Error(t, officeWaitLimitProbe(t, proc))
	require.Equal(t, launcher.ExitNoGoByte, proc.ExitCode())
	require.Empty(t, output.String(), "T-77: C target prints on entry, so no output proves no target entry on EOF")
	require.False(t, officeHasRuntimeFatal(proc.Stderr()), "launcher must not crash waiting for the go-byte: %s", proc.Stderr())
}

func officeStressOne(self, target string, args []string, render bool) (string, error) {
	var output bytes.Buffer
	proc, err := launcher.Start(launcher.Spec{Self: self, Target: target, Args: args, Limits: launcher.Limits{MemoryBytes: 512 << 20, CPUSeconds: 60, FileSizeBytes: 1 << 20, OpenFiles: 64, Processes: 2}, Stdout: &output})
	if err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()
	defer func() { proc.Kill("test cleanup"); _ = proc.Stdin.Close(); _ = proc.Wait() }()
	if err = proc.Go(); err != nil {
		return proc.Stderr(), err
	}
	op := protocol.OpText
	if render {
		op = protocol.OpRender
	}
	req := protocol.Request{Op: op, FirstPage: 1, LastPage: 1, MaxLongEdge: 400, MaxInput: protocol.MaxInputPDF}
	pdf := officepreview.PDF(1)
	if err = protocol.WriteRequest(proc.Stdin, req, bytes.NewReader(pdf), int64(len(pdf))); err != nil {
		return proc.Stderr(), err
	}
	select {
	case err = <-done:
		if err != nil {
			return proc.Stderr(), err
		}
	case <-time.After(15 * time.Second):
		proc.Kill("test wall deadline")
		<-done
		return proc.Stderr(), fmt.Errorf("T-77: target hit the test wall deadline")
	}
	response, readErr := protocol.ReadResponse(bytes.NewReader(output.Bytes()), req, nil)
	if readErr != nil {
		return proc.Stderr(), readErr
	}
	if response.Meta.PageCount != 1 {
		return proc.Stderr(), fmt.Errorf("page count %d, want 1", response.Meta.PageCount)
	}
	if render {
		if len(response.Pages) != 1 || response.Pages[0].Width != 400 || response.Pages[0].Height != 200 {
			return proc.Stderr(), fmt.Errorf("renderer did not produce the independently specified 400x200 page")
		}
	} else if len(response.Text) != 1 || strings.TrimSpace(response.Text[0].Text) != "page 1" {
		return proc.Stderr(), fmt.Errorf("extractor text = %+v, want exactly page 1", response.Text)
	}
	return proc.Stderr(), nil
}

func TestRunLimitedLinuxThirteenHundredStartsHaveNoRuntimeFatals(t *testing.T) {
	_, root := realprograms.Roots(t, false, true)
	self, err := os.Executable()
	require.NoError(t, err)
	officeProveFatalClassifier(t, self)
	home := t.TempDir()
	_, err = component.RegisterRenderer(component.RegisterOptions{Home: home, Folder: root, Operator: "test-operator"})
	require.NoError(t, err)
	resolver, err := component.NewResolver(home)
	require.NoError(t, err)
	renderer, problem := resolver.Resolve(component.KindPDFRenderer)
	require.Nil(t, problem)
	type work struct{ render bool }
	type result struct {
		render bool
		stderr string
		err    error
	}
	jobs := make(chan work, 1300)
	results := make(chan result, 1300)
	for i := 0; i < 1000; i++ {
		jobs <- work{true}
	}
	for i := 0; i < 300; i++ {
		jobs <- work{false}
	}
	close(jobs)
	var workers sync.WaitGroup
	// Exactly the spec's two concurrent child lifecycles, inside ONE named
	// Go test process. These are not parallel local package test commands.
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				target, args := renderer.Executable, []string(nil)
				if !job.render {
					target, args = self, []string{extracttext.ModeArg}
				}
				stderr, runErr := officeStressOne(self, target, args, job.render)
				results <- result{job.render, stderr, runErr}
			}
		}()
	}
	workers.Wait()
	close(results)
	renders, extracts, fatals, failures := 0, 0, 0, 0
	for result := range results {
		if result.render {
			renders++
		} else {
			extracts++
		}
		if officeHasRuntimeFatal(result.stderr) {
			fatals++
		}
		if result.err != nil {
			failures++
			t.Logf("render=%v error=%v stderr=%s", result.render, result.err, result.stderr)
		}
	}
	require.Equal(t, 1000, renders)
	require.Equal(t, 300, extracts)
	require.Zero(t, fatals, "T-77: actual launcher runtime fatals, classifier positively controlled")
	require.Zero(t, failures, "all 1300 starts must complete a real protocol exchange")
}
