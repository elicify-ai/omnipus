// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestFileGrep_BoundsMatrix pins MV-3/DS-2: every engineered bound produces
// the correct truncated_reason token and the matching Stats field, using
// small injected Options.Limits — never a large real corpus.
func TestFileGrep_BoundsMatrix(t *testing.T) {
	t.Run("max_files", func(t *testing.T) {
		files := map[string]string{}
		for i := 0; i < 6; i++ {
			files[fmt.Sprintf("f%d.txt", i)] = "hello\n"
		}
		res := mustSearch(t, oneRoot(buildFS(files)), Options{
			Query:  "nomatch",
			Limits: Limits{Files: 3},
		})
		if !res.Truncated || res.TruncatedReason != ReasonMaxFiles {
			t.Fatalf("want truncated max_files, got truncated=%v reason=%v", res.Truncated, res.TruncatedReason)
		}
		if res.Stats.FilesVisited != 4 {
			t.Fatalf("want FilesVisited=4 (visits the file that trips the cap), got %d", res.Stats.FilesVisited)
		}
	})

	t.Run("max_bytes", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 50; i++ {
			b.WriteString("this is a filler line of ordinary text content\n")
		}
		res := mustSearch(t, oneRoot(buildFS(map[string]string{"big.txt": b.String()})), Options{
			Query:  "nomatch",
			Limits: Limits{Bytes: 100},
		})
		if !res.Truncated || res.TruncatedReason != ReasonMaxBytes {
			t.Fatalf("want truncated max_bytes, got truncated=%v reason=%v", res.Truncated, res.TruncatedReason)
		}
		if res.Stats.BytesScanned <= 100 {
			t.Fatalf("want BytesScanned > 100 (the bound was exceeded), got %d", res.Stats.BytesScanned)
		}
		// Overshoot is bounded to at most one extra line (~48 bytes here).
		if res.Stats.BytesScanned > 100+64 {
			t.Fatalf("BytesScanned overshoot too large: %d", res.Stats.BytesScanned)
		}
	})

	t.Run("max_matches", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 10; i++ {
			b.WriteString("this line has the needle in it\n")
		}
		res := mustSearch(t, oneRoot(buildFS(map[string]string{"m.txt": b.String()})), Options{
			Query:  "needle",
			Limits: Limits{Matches: 3},
		})
		if !res.Truncated || res.TruncatedReason != ReasonMaxMatches {
			t.Fatalf("want truncated max_matches, got truncated=%v reason=%v", res.Truncated, res.TruncatedReason)
		}
		if len(res.Hits) != 3 {
			t.Fatalf("want exactly 3 hits, got %d", len(res.Hits))
		}
	})

	t.Run("max_matches exactly met is not truncated", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(map[string]string{
			"m.txt": "this line has the needle in it\n",
		})), Options{
			Query:  "needle",
			Limits: Limits{Matches: 1},
		})
		if res.Truncated {
			t.Fatalf("want truncated=false (exactly Matches hits and nothing more), got truncated=%v reason=%v", res.Truncated, res.TruncatedReason)
		}
		if len(res.Hits) != 1 {
			t.Fatalf("want exactly 1 hit, got %d", len(res.Hits))
		}
	})

	t.Run("max_matches_per_file", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 20; i++ {
			b.WriteString("this line has the needle in it\n")
		}
		res := mustSearch(t, oneRoot(buildFS(map[string]string{"m.txt": b.String()})), Options{
			Query:  "needle",
			Limits: Limits{MatchesPerFile: 5, Matches: 1000},
		})
		if res.Truncated {
			t.Fatalf("per-file cap alone must not set a request-level truncation, got reason=%v", res.TruncatedReason)
		}
		if len(res.Hits) != 5 {
			t.Fatalf("want exactly 5 hits (per-file cap), got %d", len(res.Hits))
		}
		if res.Stats.HitsCappedPerFile != 1 {
			t.Fatalf("want HitsCappedPerFile=1, got %d", res.Stats.HitsCappedPerFile)
		}
	})

	t.Run("max_depth", func(t *testing.T) {
		files := map[string]string{
			"a/b/c/d/deep.txt": "hello\n",
			"shallow.txt":      "hello\n",
		}
		res := mustSearch(t, oneRoot(buildFS(files)), Options{
			Query:  "nomatch",
			Limits: Limits{Depth: 2},
		})
		if !res.Truncated || res.TruncatedReason != ReasonMaxDepth {
			t.Fatalf("want truncated max_depth, got truncated=%v reason=%v", res.Truncated, res.TruncatedReason)
		}
	})

	t.Run("max_output", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 50; i++ {
			b.WriteString("this line has the needle in it and quite a bit of extra padding text\n")
		}
		res := mustSearch(t, oneRoot(buildFS(map[string]string{"m.txt": b.String()})), Options{
			Query:  "needle",
			Limits: Limits{OutputBytes: 200, Matches: 1000},
		})
		if !res.Truncated || res.TruncatedReason != ReasonMaxOutput {
			t.Fatalf("want truncated max_output, got truncated=%v reason=%v", res.Truncated, res.TruncatedReason)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		files := map[string]string{
			"a.txt": "hello\n",
			"b.txt": "hello\n",
			"c.txt": "hello\n",
		}
		slow := slowFS{FS: buildFS(files), delay: 20 * time.Millisecond}
		res := mustSearch(t, oneRoot(slow), Options{
			Query:  "nomatch",
			Limits: Limits{Deadline: 1 * time.Millisecond},
		})
		if !res.Truncated || res.TruncatedReason != ReasonDeadline {
			t.Fatalf("want truncated deadline, got truncated=%v reason=%v", res.Truncated, res.TruncatedReason)
		}
	})

	t.Run("per_file_content_cap_is_a_counted_skip_not_a_truncation", func(t *testing.T) {
		// PerFileContentCap (4 MiB) is a fixed engine constant, not part of
		// Limits (spec: the per-request override list is files/bytes/
		// matches/matches_per_file/depth/deadline_ms/output_bytes — the
		// per-file cap is deliberately not among them), so this bound can
		// only be exercised with real content past the cap, not injection.
		var b strings.Builder
		line := strings.Repeat("x", 1000) + "\n"
		for int64(b.Len()) < PerFileContentCap+int64(len(line)) {
			b.WriteString(line)
		}
		b.WriteString("needle appears only after the per-file cap\n")

		res := mustSearch(t, oneRoot(buildFS(map[string]string{"huge.txt": b.String()})), Options{
			Query:  "needle",
			Limits: Limits{Bytes: DefaultMaxBytes},
		})
		if res.Truncated {
			t.Fatalf("per-file cap alone must not set a request-level truncation, got reason=%v", res.TruncatedReason)
		}
		if res.Stats.FilesSkippedFileCap != 1 {
			t.Fatalf("want FilesSkippedFileCap=1, got %d", res.Stats.FilesSkippedFileCap)
		}
		if len(res.Hits) != 0 {
			t.Fatalf("the needle sits past the per-file cap, want 0 hits, got %d", len(res.Hits))
		}
	})
}

// TestFileGrep_DeadlineAndContextCancel pins US-2 AS-8 / spec test 6:
// both an internal deadline AND an externally cancelled parent context stop
// the walk, reporting ReasonDeadline either way.
func TestFileGrep_DeadlineAndContextCancel(t *testing.T) {
	t.Run("internal deadline stops the walk", func(t *testing.T) {
		files := map[string]string{"a.txt": "hello\n", "b.txt": "hello\n"}
		slow := slowFS{FS: buildFS(files), delay: 20 * time.Millisecond}
		res := mustSearch(t, oneRoot(slow), Options{
			Query:  "nomatch",
			Limits: Limits{Deadline: 1 * time.Millisecond},
		})
		if !res.Truncated || res.TruncatedReason != ReasonDeadline {
			t.Fatalf("want truncated deadline, got truncated=%v reason=%v", res.Truncated, res.TruncatedReason)
		}
	})

	t.Run("external cancel stops the walk", func(t *testing.T) {
		// Finding F-H: an externally CANCELED context (client disconnect, a
		// stopped agent turn, …) is a different event from a request that
		// genuinely ran past its own DEADLINE, and must be reported as
		// such rather than conflated into "deadline" — see ctxStopReason.
		files := map[string]string{"a.txt": "hello\n", "b.txt": "hello\n"}
		slow := slowFS{FS: buildFS(files), delay: 20 * time.Millisecond}
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(2 * time.Millisecond)
			cancel()
		}()
		res, err := Search(ctx, oneRoot(slow), Options{Query: "nomatch"})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if !res.Truncated || res.TruncatedReason != ReasonCanceled {
			t.Fatalf("want truncated canceled on external cancel, got truncated=%v reason=%v", res.Truncated, res.TruncatedReason)
		}
	})
}
