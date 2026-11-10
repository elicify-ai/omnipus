//go:build goolm && stdjson

package agent

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// cwQuietChildEnv marks the re-exec'd child test process. The retained-heap
// meter reads process-wide runtime.MemStats.HeapAlloc, so any memory that
// earlier tests in the shared pkg/agent binary leave reachable counts against
// the measurement (CI run 2026-10-04: baseline 163,624 B, peak 12,658,480 B
// under -race, against an 8 MiB allowance). Running the MIN-001 memory
// subtests in a fresh child process that executes ONLY that subtest makes
// the process quiet by construction instead of by comment.
const cwQuietChildEnv = "OMNIPUS_CW_QUIET_CHILD"

// cwInQuietChild returns a subtest body that runs body in a child process
// built from this very test binary (same -race / build tags). In the child
// (env flag set) it just runs body. Same re-exec idiom as processHookHelperCommand.
func cwInQuietChild(body func(*testing.T)) func(*testing.T) {
	return func(t *testing.T) {
		if os.Getenv(cwQuietChildEnv) == "1" {
			body(t)
			return
		}
		parts := strings.Split(t.Name(), "/")
		for i, p := range parts {
			parts[i] = "^" + regexp.QuoteMeta(p) + "$"
		}
		args := []string{"-test.run=" + strings.Join(parts, "/"), "-test.v", "-test.count=1"}
		cmd := exec.Command(os.Args[0], args...)
		cmd.Env = append(os.Environ(), cwQuietChildEnv+"=1")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		// Check the instrument: a filter that matched nothing exits 0 too, so
		// require the named PASS line for this exact subtest.
		if err != nil || !strings.Contains(out.String(), "--- PASS: "+t.Name()+" ") {
			t.Fatalf("quiet child process failed or did not run %q: err=%v\n%s", t.Name(), err, out.String())
		}
		t.Logf("quiet child output:\n%s", out.String())
	}
}

var cwHeapProfileFrame = regexp.MustCompile(`^#\t0x[0-9a-f]+\t(\S+)\+0x[0-9a-f]+\t(\S+)`)
var cwHeapProfileHead = regexp.MustCompile(`^\d+: (\d+) \[\d+: \d+\] @`)

// cwHeapHolders names who holds the live heap: goroutine count plus the top
// allocation sites by in-use bytes from the heap profile (as of the last GC).
// Evidence for whoever debugs a future failure; never part of an assertion.
func cwHeapHolders() string {
	runtime.GC()
	var buf bytes.Buffer
	if err := pprof.Lookup("heap").WriteTo(&buf, 1); err != nil {
		return fmt.Sprintf("goroutines=%d; heap profile unavailable: %v", runtime.NumGoroutine(), err)
	}
	inuse := map[string]uint64{}
	var pending uint64
	var haveHead bool
	for _, line := range strings.Split(buf.String(), "\n") {
		if m := cwHeapProfileHead.FindStringSubmatch(line); m != nil {
			pending, _ = strconv.ParseUint(m[1], 10, 64)
			haveHead = true
			continue
		}
		if haveHead {
			if m := cwHeapProfileFrame.FindStringSubmatch(line); m != nil {
				inuse[m[1]+" "+m[2]] += pending
				haveHead = false
			}
		}
	}
	type site struct {
		where string
		bytes uint64
	}
	sites := make([]site, 0, len(inuse))
	for k, v := range inuse {
		sites = append(sites, site{k, v})
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].bytes > sites[j].bytes })
	var b strings.Builder
	fmt.Fprintf(&b, "goroutines=%d; top in-use allocation sites (sampled, as of last GC):", runtime.NumGoroutine())
	for i, s := range sites {
		if i == 8 {
			break
		}
		fmt.Fprintf(&b, "\n  %10d B  %s", s.bytes, s.where)
	}
	return b.String()
}
