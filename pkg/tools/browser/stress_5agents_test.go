package browser

// SC-002 stress test (spec §11): 5 agents browse concurrently against the ONE
// shared Chrome, each in its own browser context, each rendering its OWN page —
// the headline acceptance for ADR-043. Drives real Chrome via the coordinator
// (no LLM/agent-loop needed), so it runs anywhere a managed Chrome can launch.
//
// Proves: (1) one Chrome for 5 agents (single PID); (2) 5 distinct isolated
// contexts (no cross-agent page bleed); (3) all 5 concurrent navigations
// succeed; (4) total browsing RSS under the documented cap.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// chromeTreeRSSKB sums VmRSS (kB) of the Chrome process tree (coordinator pid +
// descendants), via /proc — the SC-002 measurement methodology (spec round-2
// MAJ-009). Linux-only; returns 0 elsewhere.
func chromeTreeRSSKB(t *testing.T, coord *BrowserCoordinator) int {
	t.Helper()
	if runtime.GOOS != "linux" {
		return 0
	}
	pid := coord.PID()
	if pid <= 0 {
		return 0
	}
	var sum int
	var walk func(int)
	walk = func(p int) {
		for _, c := range childrenOf(p) {
			sum += vmRSSKB(c)
			walk(c)
		}
	}
	sum += vmRSSKB(pid)
	walk(pid)
	return sum
}

func vmRSSKB(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kb, err := strconv.Atoi(fields[1]); err == nil {
					return kb
				}
			}
		}
	}
	return 0
}

func childrenOf(ppid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		s := string(stat)
		idx := strings.LastIndex(s, ")") // comm may contain spaces/parens
		if idx < 0 {
			continue
		}
		rest := strings.Fields(s[idx+1:]) // state ppid ...
		if len(rest) >= 2 {
			if p, err := strconv.Atoi(rest[1]); err == nil && p == ppid {
				out = append(out, pid)
			}
		}
	}
	return out
}

// ppidOf reads the parent pid from /proc/<pid>/stat (mirrors childrenOf's stat
// parsing). Returns 0 on any failure.
func ppidOf(pid int) int {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(stat)
	idx := strings.LastIndex(s, ")")
	if idx < 0 {
		return 0
	}
	rest := strings.Fields(s[idx+1:])
	if len(rest) >= 2 {
		if p, err := strconv.Atoi(rest[1]); err == nil {
			return p
		}
	}
	return 0
}

// cmdlineOf reads /proc/<pid>/cmdline (NUL-separated args joined by spaces).
func cmdlineOf(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(string(data), "\x00", " ")
}

// isExecPathInvocation reports whether cmd (a /proc/<pid>/cmdline, NUL bytes
// already replaced with spaces by cmdlineOf) is an invocation of EXACTLY
// execPath as argv[0] — i.e. execPath followed by an argument separator (a
// space) or nothing (no args), never a mere prefix. A plain
// strings.Contains/HasPrefix match is not good enough: chrome-for-testing
// ships companion binaries in the SAME chrome-linux64/ directory whose names
// also start with "chrome" — chrome_crashpad_handler (which deliberately
// detaches to PID 1, by crash-reporter design, so it survives even if the
// browser it's monitoring crashes) is a sibling of chrome-linux64/chrome and
// would otherwise be miscounted as a second "top-level chrome" process,
// exactly as chrome-wrapper would be. Requiring the boundary excludes both.
func isExecPathInvocation(cmd, execPath string) bool {
	if cmd == execPath {
		return true
	}
	return strings.HasPrefix(cmd, execPath+" ")
}

// countTopLevelChromeProcesses counts THIS coordinator's own Chromium browser
// processes — identified by execPath, the exact binary path this test's
// BrowserConfig.ExecPath resolved to (whichever CfT build that turned out to
// be; see installer.go's dual-download doc comment) — whose parent is NOT
// itself one of them (G8). One coordinator launch = exactly ONE such
// top-level process (its renderer/GPU/zygote children are all descendants,
// so they don't count). Two leaked coordinators launched from the SAME
// execPath would make this 2. Reads /proc on Linux and delegates to
// countTopLevelChromeProcessesPS everywhere else.
//
// Matching must be scoped to the coordinator's OWN resolved execPath, not a
// generic "looks like chrome" name/path heuristic: this devpod (and
// potentially a shared CI host) runs entirely unrelated Chromium processes
// from OTHER tools with their own CfT-style "chrome-linux64/chrome" install
// layouts (observed here: the playwright MCP server's
// ~/.cache/ms-playwright/chromium-*/chrome-linux64/chrome and a
// ~/.cache/puppeteer/chrome/*/chrome-linux64/chrome) — a bare
// "chrome-linux64/chrome" or "chrome-headless-shell" substring match counts
// THOSE too, wildly inflating the result independent of anything this test
// launched. execPath (this codebase's OWN managed-install root,
// ~/.omnipus/browser/chromium/...) is the one string guaranteed to identify
// only processes THIS coordinator is responsible for, regardless of which of
// the two installable builds it resolved to (WebRTC tabCapture requires the
// full "chrome" build, which installer.go's findInstalledBinary PREFERS by
// default on Linux, so a coordinator test resolving an already-installed
// managed Chrome is, in practice, just as likely to be running the full
// build as chrome-headless-shell). isExecPathInvocation (not a bare
// substring match) additionally excludes the "chrome"-prefixed sibling
// binaries chrome-for-testing ships in the SAME directory
// (chrome_crashpad_handler, chrome-wrapper) — see its own doc comment.
func countTopLevelChromeProcesses(execPath string) int {
	if execPath == "" {
		return 0
	}
	// /proc is Linux-only; every other POSIX host answers the same question
	// through ps. Before this split the function returned 0 on macOS while
	// the caller still asserted "== 1", so the whole one-shared-Chrome
	// acceptance FAILED on any non-Linux dev machine (found 2026-08-13
	// running this suite on macOS) -- a test-mechanism gap masquerading as a
	// product regression.
	if runtime.GOOS != "linux" {
		return countTopLevelChromeProcessesPS(execPath)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	type procInfo struct {
		ppid     int
		isChrome bool
	}
	procs := make(map[int]procInfo, len(entries))
	for _, e := range entries {
		pid, perr := strconv.Atoi(e.Name())
		if perr != nil {
			continue
		}
		cmd := cmdlineOf(pid)
		isChrome := isExecPathInvocation(cmd, execPath)
		procs[pid] = procInfo{ppid: ppidOf(pid), isChrome: isChrome}
	}
	n := 0
	for _, p := range procs {
		if !p.isChrome {
			continue
		}
		parent, ok := procs[p.ppid]
		// A top-level chrome process: its parent is either absent or NOT a
		// chrome process itself (e.g. the test binary / init).
		if !ok || !parent.isChrome {
			n++
		}
	}
	return n
}

// countTopLevelChromeProcessesPS is the ps-based sibling of
// countTopLevelChromeProcesses for hosts without /proc (macOS, BSD). Same
// definition of "top level": a process whose command line is an invocation of
// execPath and whose PARENT is not itself such a process. Returns 0 if ps is
// unavailable or unparseable -- the caller's assertion then fails loudly,
// which is the correct outcome for "could not measure" (never a silent pass).
func countTopLevelChromeProcessesPS(execPath string) int {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,command=").Output()
	if err != nil {
		return 0
	}
	type procInfo struct {
		ppid     int
		isChrome bool
	}
	procs := map[int]procInfo{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, perr := strconv.Atoi(fields[0])
		ppid, pperr := strconv.Atoi(fields[1])
		if perr != nil || pperr != nil {
			continue
		}
		cmd := strings.Join(fields[2:], " ")
		procs[pid] = procInfo{ppid: ppid, isChrome: isExecPathInvocation(cmd, execPath)}
	}
	n := 0
	for _, p := range procs {
		if !p.isChrome {
			continue
		}
		if parent, ok := procs[p.ppid]; !ok || !parent.isChrome {
			n++
		}
	}
	return n
}

func TestFiveAgents_ConcurrentStress(t *testing.T) {
	skipIfNoBrowser(t)
	const numAgents = 5

	pages := make([]*httptest.Server, numAgents)
	for i := 0; i < numAgents; i++ {
		idx := i
		pages[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, "<html><body><h1>AGENT-%d-MARKER</h1></body></html>", idx)
		}))
	}
	t.Cleanup(func() {
		for _, s := range pages {
			s.Close()
		}
	})

	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(func() { coord.Shutdown() })

	type result struct {
		agentID string
		ctxID   string
		body    string
		err     error
	}
	results := make([]result, numAgents)
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < numAgents; i++ {
		agentID := fmt.Sprintf("agent-%d", i)
		mgr := newTestManager(t, cfg)
		mgr.AttachSharedChrome(coord, browserTestKey(agentID))

		wg.Add(1)
		go func(i int, agentID string, mgr *BrowserManager) {
			defer wg.Done()
			<-start
			tabCtx, err := mgr.Session(testSessionID)
			if err != nil {
				results[i] = result{agentID: agentID, err: fmt.Errorf("Session: %w", err)}
				return
			}
			tabCtx, cancel := context.WithTimeout(tabCtx, 30*time.Second)
			defer cancel()
			var body string
			if err := chromedp.Run(
				tabCtx,
				chromedp.Navigate(pages[i].URL),
				chromedp.WaitVisible("h1", chromedp.ByQuery),
				chromedp.Text("body", &body, chromedp.ByQuery),
			); err != nil {
				results[i] = result{agentID: agentID, err: fmt.Errorf("navigate/read: %w", err)}
				return
			}
			results[i] = result{agentID: agentID, ctxID: string(mgr.browserCtxID), body: body}
		}(i, agentID, mgr)
	}
	close(start)
	wg.Wait()

	pid := coord.PID()
	if pid == 0 {
		t.Fatal("expected a single live shared Chrome pid after 5 agents browsed")
	}

	seenCtx := map[string]bool{}
	for i, r := range results {
		if r.err != nil {
			t.Errorf("agent-%d failed: %v", i, r.err)
			continue
		}
		want := fmt.Sprintf("AGENT-%d-MARKER", i)
		if !strings.Contains(r.body, want) {
			t.Errorf("agent-%d cross-context bleed: body=%q (want %q) — isolation broken", i, r.body, want)
		}
		if r.ctxID == "" {
			t.Errorf("agent-%d has empty browser context id", i)
		} else {
			seenCtx[r.ctxID] = true
		}
	}
	if len(seenCtx) != numAgents {
		t.Errorf("expected %d distinct browser contexts; got %d", numAgents, len(seenCtx))
	}
	if coord.contextCount() != numAgents {
		t.Errorf("coordinator contextCount=%d, want %d", coord.contextCount(), numAgents)
	}

	rssMB := chromeTreeRSSKB(t, coord) / 1024
	t.Logf("5-agent concurrent stress: 1 Chrome pid=%d, %d contexts, tree RSS=%d MB", pid, coord.contextCount(), rssMB)
	// G8: five isolated Chrome contexts on GH ubuntu-latest with the
	// action-installed Google Chrome measured 4311 MB (2026-08-16) — just
	// over the 4 GB ceiling that replaced the original 6 GB sanity cap.
	// 6 GB remains the documented "this is not a leak" bound; 4 GB was too
	// tight for current Chrome's per-context baseline.
	if rssMB > 6144 {
		t.Errorf("5-agent browsing RSS %d MB exceeds the 6 GB documented cap", rssMB)
	}

	// G8: assert exactly ONE top-level Chromium browser process (either
	// installable build — see countTopLevelChromeProcesses) — the headline
	// "one Chrome for N agents" acceptance. A second top-level process would
	// mean a coordinator leak (two Chrome instances).
	topLevel := countTopLevelChromeProcesses(cfg.ExecPath)
	t.Logf("top-level chrome/chrome-headless-shell processes: %d", topLevel)
	if topLevel != 1 {
		t.Errorf(
			"expected exactly 1 top-level chrome/chrome-headless-shell process (one shared Chrome); got %d",
			topLevel,
		)
	}
}
