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

// countTopLevelChromeProcesses counts chrome-headless-shell processes belonging
// to profileDir (their cmdline carries --user-data-dir=<profileDir>) whose parent
// is NOT itself such a process (G8). One coordinator launch = exactly ONE such
// top-level process (its renderer/GPU/zygote children don't carry --user-data-dir
// and are descendants anyway, so they don't count). Two leaked coordinators would
// make this 2.
//
// The --user-data-dir scoping (CRIT-001 hardening) makes the count HERMETIC: only
// the shared Chrome launched for THIS test — over the CDP pipe, into this test's
// unique temp profile dir — is counted. An UNRELATED omnipus gateway running on
// the same host (a real dev-pod case: a live preview gateway's Chrome under a
// different profile dir) is correctly excluded, while a genuine coordinator leak
// (a second Chrome sharing this profile dir) is still caught. Linux-only; returns
// 0 elsewhere.
func countTopLevelChromeProcesses(profileDir string) int {
	if runtime.GOOS != "linux" {
		return 0
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
		isChrome := (strings.Contains(cmd, "chrome-headless-shell") || strings.Contains(cmd, "headless_shell")) &&
			strings.Contains(cmd, "--user-data-dir="+profileDir)
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

func TestFiveAgents_ConcurrentStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test needs a real Chrome")
	}
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
	coord := NewBrowserCoordinator(home, cfg, 30)
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
		mgr.AttachSharedChrome(coord, agentID)

		wg.Add(1)
		go func(i int, agentID string, mgr *BrowserManager) {
			defer wg.Done()
			<-start
			tabCtx, err := mgr.Session(defaultSessionID)
			if err != nil {
				results[i] = result{agentID: agentID, err: fmt.Errorf("Session: %w", err)}
				return
			}
			tabCtx, cancel := context.WithTimeout(tabCtx, 30*time.Second)
			defer cancel()
			var body string
			if err := chromedp.Run(tabCtx,
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
	// G8: tighten the RSS bound from the loose 6 GB sanity cap to the
	// documented 4 GB ceiling. Five light http pages + their contexts should
	// sit comfortably under this.
	if rssMB > 4096 {
		t.Errorf("5-agent browsing RSS %d MB exceeds the 4 GB documented cap", rssMB)
	}

	// G8: assert exactly ONE top-level chrome-headless-shell process for THIS
	// test's profile dir — the headline "one Chrome for N agents" acceptance. A
	// second such process would mean a coordinator leak (two Chrome instances).
	// Scoped to cfg.ProfileDir so an unrelated omnipus gateway's Chrome on the
	// same host (different profile dir) does not pollute the count (CRIT-001).
	topLevel := countTopLevelChromeProcesses(cfg.ProfileDir)
	t.Logf("top-level chrome-headless-shell processes: %d", topLevel)
	if topLevel != 1 {
		t.Errorf("expected exactly 1 top-level chrome-headless-shell process (one shared Chrome); got %d", topLevel)
	}
}
