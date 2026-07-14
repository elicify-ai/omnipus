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
	if rssMB > 6144 {
		t.Errorf("5-agent browsing RSS %d MB exceeds the 6 GB sanity bound", rssMB)
	}
}
