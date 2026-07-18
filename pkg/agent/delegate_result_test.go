// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// W4 regression coverage, sync path. The shipped wave4_delegate_result_test.go
// only exercises the ASYNC pattern (it pre-seeds a placeholder record, then
// checks spawnSubTurn's defer corrects it). Live UAT found the SYNCHRONOUS path
// unpersisted: that defer no-ops for sync (record doesn't exist yet, retry is
// async-only), and the turn loop's own tool_call write only populated Result
// for media — so a sync delegate's text result was dropped on reload. loop.go
// now calls buildSyncDelegateResult on the non-media branch; this pins its
// contract so the {"text":…}(+"error") shape stays identical to the async
// defer's and stays scoped to the "delegate" tool only.

package agent

import (
	"reflect"
	"testing"
)

func TestBuildSyncDelegateResult(t *testing.T) {
	tests := []struct {
		name          string
		toolName      string
		contentForLLM string
		isError       bool
		isAsync       bool
		want          map[string]any
	}{
		{
			name:          "non-delegate tool returns nil (no behavior change for other tools)",
			toolName:      "web_search",
			contentForLLM: "some result text",
			isError:       false,
			want:          nil,
		},
		{
			name:          "non-delegate errored tool still returns nil",
			toolName:      "bash",
			contentForLLM: "",
			isError:       true,
			want:          nil,
		},
		{
			name:          "delegate success persists the sub-turn text (the bug: was dropped)",
			toolName:      "delegate",
			contentForLLM: "Subagent task completed: Result: PONG",
			isError:       false,
			want:          map[string]any{"text": "Subagent task completed: Result: PONG"},
		},
		{
			name:          "delegate success trims surrounding whitespace",
			toolName:      "delegate",
			contentForLLM: "  PONG\n",
			isError:       false,
			want:          map[string]any{"text": "PONG"},
		},
		{
			name:          "delegate error persists text AND an error flag",
			toolName:      "delegate",
			contentForLLM: "SubTurn failed: boom",
			isError:       true,
			want:          map[string]any{"text": "SubTurn failed: boom", "error": true},
		},
		{
			name:          "delegate error with no output gets fallback text + error flag",
			toolName:      "delegate",
			contentForLLM: "   ",
			isError:       true,
			want:          map[string]any{"text": "sub-turn reported an error", "error": true},
		},
		{
			name:          "delegate empty non-error returns nil (nothing worth persisting)",
			toolName:      "delegate",
			contentForLLM: "",
			isError:       false,
			want:          nil,
		},
		{
			// Regression guard for the review-caught async interaction: an ASYNC
			// delegate's immediate "running in background" ack must NOT be
			// persisted here — spawnSubTurn's defer owns async result
			// persistence. Persisting the ack would leave a terminal-status
			// delegate whose result wrongly claims it is still running whenever
			// the sub-turn finishes with empty output.
			name:          "async delegate ack returns nil (owned by the async defer, never persisted here)",
			toolName:      "delegate",
			contentForLLM: "Delegated task — running in background; check with delegate(status).",
			isError:       false,
			isAsync:       true,
			want:          nil,
		},
		{
			name:          "async delegate error also returns nil (async never persists here)",
			toolName:      "delegate",
			contentForLLM: "SubTurn dispatch rejected: boom",
			isError:       true,
			isAsync:       true,
			want:          nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSyncDelegateResult(tt.toolName, tt.contentForLLM, tt.isError, tt.isAsync)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildSyncDelegateResult(%q, %q, isError=%v, isAsync=%v) = %+v, want %+v",
					tt.toolName, tt.contentForLLM, tt.isError, tt.isAsync, got, tt.want)
			}
		})
	}
}
