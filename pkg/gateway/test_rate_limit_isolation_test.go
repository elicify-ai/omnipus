// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// test_rate_limit_isolation_test.go — one test's traffic must never be
// charged to another test's rate-limit budget.
//
// THIS PACKAGE HAS TWO KINDS OF LIMITER, KEYED DIFFERENTLY, AND EACH NEEDS
// ITS OWN ISOLATION. Applying the wrong one isolates nothing.
//
//  1. IP-keyed limiters — rest_auth.go's `var` block, plus the few declared
//     beside their own routes (smokeTestLimiter, the libraryPreview*
//     limiters). Keyed on the CLIENT IP. Isolated by isolateRateLimit below,
//     which gives each test its own source address.
//
//  2. The knowledge limiter — knowledgeRESTLimiter in rest_knowledge.go.
//     Keyed on the WORKSPACE ID (knowledgeRateKey); it never reads the
//     address, so isolateRateLimit does nothing for it. Isolated by
//     ulidLikeID below, which hands every test workspace an ID no other call
//     in the process can receive.
//
// THE TRAP, FIRST KEY. Every IP-keyed limiter is a process-global sliding
// window keyed on the CLIENT IP, and httptest.NewRequest gives every request it builds the
// same RemoteAddr: 192.0.2.1:1234. So by default the whole package shares one
// bucket per limiter. Nothing goes wrong while the per-limiter request count
// across the package stays under the ceiling — and then someone adds a test,
// or merely makes the suite slower or faster, the requests re-cluster inside
// one 60-second window, and an unrelated test is refused 429 for traffic it
// never sent.
//
// That is not hypothetical. It landed on
// TestSignIn_CopilotDispatchDoesNotLeakToOtherProviders, which asserts 200 on
// POST /providers/openai-chatgpt/sign-in and got
// `429 rate limit exceeded, retry after 49 seconds`. Its own three requests
// are nowhere near signInStartLimiter's ceiling of 10/minute; the package's
// other fifteen POST .../sign-in builders share its address and drained the
// bucket first. The failure is ordering- and timing-dependent, which is worse
// than a deterministic one: it passes in a narrow -run subset and fails in
// the full-package run, so a scoped green result cannot clear it.
//
// The fix is isolation, never a bigger ceiling and never an assertion that
// tolerates 429: the limiters are the product behaviour under test elsewhere
// in this package, and loosening one to make an unrelated test pass would
// throw away the control to protect the artifact.
//
// WHY THIS LIVES HERE AND NOT IN ONE FILE. It has happened before, to the
// SAME test, and was fixed file-locally: rest_auth_envtoken_asymmetry_test.go
// has its own uniqueTestSourceIP() and a comment describing this failure in
// detail. That fix was correct and it did not make the package durable —
// it isolated the file that happened to notice, leaving every other caller on
// the shared default, so the next change to land re-created the identical
// failure on the identical test. Isolation applied at the chokepoint (every
// test entry into HandleProviders) is what makes it stick.
//
// uniqueTestSourceIP is left as it is: it hands out an address per REQUEST
// rather than per test, which is what that file's assertions rely on, and its
// 203.0.113.x range cannot collide with the 198.18.x.y handed out here.
//
// THE SAME TRAP, SECOND KEY (CI on dd25339bf). ulidLikeID used to return the
// current second plus 'A'+counter%26, so the 27th workspace ID made inside
// one second equalled the 1st. Each test has its own temp home, so nothing on
// disk collided — only the in-memory knowledge limiter key did, and nothing
// reported it. TestVaultSearch_RateLimiterOrderingClosesTheScopeProbeOracle
// drains its workspace's knowledge budget; exactly 26 IDs later in the
// package's fixed test order, TestRecordWrite_VersionTokenOnCreateIsRefused
// received the SAME ID whenever the tests between finished inside one
// second, and its first and only request was refused `429 too many knowledge
// requests` for traffic it never sent. It passed on slow runs and failed on
// fast ones with no code change. TestUlidLikeID_NeverRepeatsWithinOneSecond
// pins the fix.

package gateway

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// httptestDefaultRemoteAddr is the address net/http/httptest stamps on every
// request built by httptest.NewRequest. It is the shared default that makes
// the whole package one bucket, and it is also the signal that a test has NOT
// deliberately chosen an address of its own.
const httptestDefaultRemoteAddr = "192.0.2.1:1234"

// testWorkspaceIDCounter numbers every test workspace ID this process hands
// out. It never wraps and is safe to advance from parallel tests.
var testWorkspaceIDCounter atomic.Uint64

// ulidLikeID returns a path-safe workspace ID that no other call in this test
// process returns.
//
// Uniqueness comes from the counter ALONE. The time prefix is kept only so an
// ID in failure output says roughly when it was made; it is not what keeps
// two IDs apart, and nothing may rely on it for that — a one-second prefix
// plus a short repeating suffix is exactly the shape that collided.
func ulidLikeID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%sX%06d", time.Now().Format("20060102150405"), testWorkspaceIDCounter.Add(1))
}

var (
	testClientIPMu   sync.Mutex
	testClientIPSeen = map[string]string{}
	testClientIPNext uint32
)

// testClientIP returns a source address unique to this test (or subtest),
// stable across every call within it. Addresses are handed out from a counter
// rather than hashed from t.Name(): a hash over ~200 test names in a 16-bit
// space collides with better-than-even odds, and a collision silently
// recreates exactly the shared-bucket problem this helper exists to remove.
//
// The range is 198.18.0.0/15 (RFC 2544 benchmark-test space, never a real
// client). It is deliberately distinct from httptest's 192.0.2.1 default and
// from the 198.51.100.x addresses the provider pre-auth tests pick by hand,
// so no automatic address can ever collide with a hand-chosen one.
func testClientIP(t *testing.T) string {
	t.Helper()
	testClientIPMu.Lock()
	defer testClientIPMu.Unlock()
	if ip, ok := testClientIPSeen[t.Name()]; ok {
		return ip
	}
	testClientIPNext++
	n := testClientIPNext
	ip := fmt.Sprintf("198.18.%d.%d", (n>>8)&0xff, n&0xff)
	testClientIPSeen[t.Name()] = ip
	return ip
}

// isolateRateLimit gives r this test's own source address so the package's
// per-IP limiters bill it to its own budget, and returns r for inline use:
//
//	api.HandleProviders(w, isolateRateLimit(t, req))
//
// A request whose RemoteAddr has already been set to something other than
// httptest's default is left ALONE. Several tests in this package choose an
// address on purpose — to exercise X-Forwarded-For handling, to prove a
// limiter actually refuses the 61st request, or to keep two halves of one
// test in separate buckets — and silently overwriting that would break the
// very thing they assert.
func isolateRateLimit(t *testing.T, r *http.Request) *http.Request {
	t.Helper()
	if r.RemoteAddr == httptestDefaultRemoteAddr {
		r.RemoteAddr = testClientIP(t) + ":54321"
	}
	return r
}

// TestRateLimitIsolation_UniquePerTestAndStable pins the two properties the
// helper above is relied on for.
func TestRateLimitIsolation_UniquePerTestAndStable(t *testing.T) {
	first := testClientIP(t)
	assert2 := testClientIP(t)
	if first != assert2 {
		t.Fatalf("testClientIP must be stable within one test: %q then %q", first, assert2)
	}
	if first == httptestDefaultRemoteAddr {
		t.Fatalf("testClientIP must never hand back httptest's shared default")
	}

	t.Run("subtests get their own address", func(t *testing.T) {
		sub := testClientIP(t)
		if sub == first {
			t.Fatalf("a subtest must not share its parent's rate-limit bucket (both %q)", sub)
		}
	})
}

// TestRateLimitIsolation_RespectsADeliberateAddress pins the carve-out: a
// test that chose its own source address keeps it.
func TestRateLimitIsolation_RespectsADeliberateAddress(t *testing.T) {
	r, err := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	r.RemoteAddr = "203.0.113.77:1111"
	if got := isolateRateLimit(t, r).RemoteAddr; got != "203.0.113.77:1111" {
		t.Fatalf("a deliberately chosen RemoteAddr must survive; got %q", got)
	}

	r.RemoteAddr = httptestDefaultRemoteAddr
	if got := isolateRateLimit(t, r).RemoteAddr; got == httptestDefaultRemoteAddr {
		t.Fatalf("httptest's shared default must be replaced; got %q", got)
	}
}

// TestUlidLikeID_NeverRepeatsWithinOneSecond is the guard for the dd25339bf
// flake (see this file's header). 200 IDs made back to back all land inside a
// second or two, which is exactly the burst the old helper could not survive:
// with a one-second prefix and a suffix of 26 letters it can produce at most
// 26 distinct IDs per second, so this fails on it deterministically — however
// the burst falls across a second boundary, one side holds 100 or more calls.
//
// The assertion is on the KNOWLEDGE LIMITER KEY, not only the raw ID, because
// the key is what two tests actually share; a future change that made IDs
// distinct but mapped them to one bucket would still be the same defect.
func TestUlidLikeID_NeverRepeatsWithinOneSecond(t *testing.T) {
	const n = 200
	seenID := make(map[string]int, n)
	seenKey := make(map[string]int, n)
	for i := 0; i < n; i++ {
		id := ulidLikeID(t)
		if err := validateEntityID(id); err != nil {
			t.Fatalf("call %d returned %q, which the gateway would refuse as a workspace ID: %v", i, id, err)
		}
		if prev, dup := seenID[id]; dup {
			t.Fatalf("call %d returned %q, already returned by call %d — two tests would share one workspace's knowledge budget", i, id, prev)
		}
		seenID[id] = i
		key := knowledgeRateKey(id)
		if prev, dup := seenKey[key]; dup {
			t.Fatalf("call %d maps to knowledge limiter key %q, already used by call %d", i, key, prev)
		}
		seenKey[key] = i
	}
}

// TestUlidLikeID_UniqueUnderConcurrentUse pins the other half of the helper's
// contract: parallel tests may call it at the same moment. Under -race the
// old unguarded int counter is also a reported data race.
func TestUlidLikeID_UniqueUnderConcurrentUse(t *testing.T) {
	const workers, perWorker = 8, 100
	ids := make(chan string, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				ids <- ulidLikeID(t)
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := make(map[string]struct{}, workers*perWorker)
	for id := range ids {
		if _, dup := seen[id]; dup {
			t.Fatalf("concurrent callers were both handed %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("expected %d distinct IDs, got %d", workers*perWorker, len(seen))
	}
}
