// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED tests for the D14 debug-log fix (spec §16 item 16, §15 Group F
// "The debug log carries no reasoning text or signature material", FR-016,
// SC-003): a thinking turn's reasoning text must appear ONLY in the LLM
// context file — never in any log — and the debug record must shape reasoning
// as lengths/counts only.
//
// RED status: pins no new symbol; the sweep fails today because the debug
// log DOES carry raw reasoning ("reasoning": rr...response.Reasoning in
// pkg/agent/loop_run_turn_response.go's LLM-response debug record), so the
// "zero log matches" oracle is the failing reason.

func TestDebugLog_ReasoningSweep_ZeroLogMatches(t *testing.T) {
	home := t.TempDir()
	logPath := filepath.Join(home, "logs", "agent.log")

	logger.SetLevel(logger.DEBUG)
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(logger.INFO)
	})

	require.NoError(t, logger.EnableFileLogging(logPath))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         home,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	prov := &thinkingStreamProvider{rounds: []thinkingRound{
		{
			reasoning: "Sweep probe: the key is " + thinkingSentinel + " — do not leak.",
			answer:    "Done.",
		},
	}}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), prov)
	w := newSessionWorker("wpc-sweep", al, func() {})
	w.processTurn(context.Background(), bus.InboundMessage{
		Channel: "test",
		Sender:  bus.SenderInfo{CanonicalID: "user-a"},
		ChatID:  "chat-a",
		Content: "sweep probe turn",
		Peer:    bus.Peer{Kind: bus.PeerDirect, ID: "user-a"},
	})

	require.NoError(t, logger.DisableFileLogging())

	// Sweep every file under the test home.
	type hit struct {
		path      string
		isLogFile bool
	}
	var hits []hit
	var logFiles []string
	err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), thinkingSentinel) {
			hits = append(hits, hit{
				path:      path,
				isLogFile: strings.HasSuffix(path, ".log") || strings.Contains(path, "/logs/"),
			})
		}
		if strings.HasSuffix(path, ".log") || strings.Contains(path, "/logs/") {
			logFiles = append(logFiles, path)
		}
		return nil
	})
	require.NoError(t, err)

	// Instrument check (rule 6): the sentinel MUST appear somewhere — the
	// LLM context file records raw reasoning by design (D15). Zero hits would
	// mean the turn never wrote it and the sweep proves nothing.
	require.NotEmpty(t, hits, "positive control failed: the reasoning sentinel never reached disk — "+
		"the turn's context file should carry it (D15)")
	for _, h := range hits {
		assert.True(t, strings.HasSuffix(h.path, "context.jsonl"),
			"reasoning sentinel found in %s — D15 allows ONLY the LLM context file; log files are forbidden (FR-016)", h.path)
		assert.False(t, h.isLogFile, "reasoning sentinel must never land in a log file; found in %s", h.path)
	}

	// Zero log matches, stated positively: the log file exists (the debug
	// record really ran) and does not contain the sentinel.
	require.FileExists(t, logPath)
	logData, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.NotContains(t, string(logData), thinkingSentinel,
		"the debug log must carry no reasoning text (FR-016 / D14)")

	// Field shape: every JSON log line whose fields mention reasoning shapes
	// it as a length or count — numeric or boolean only, never a string.
	sc := bufio.NewScanner(bytes.NewReader(logData))
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	sawReasoningField := false
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if jsonErr := json.Unmarshal(line, &rec); jsonErr != nil {
			continue // non-JSON lines carry no structured fields to leak
		}
		for k, v := range rec {
			lk := strings.ToLower(k)
			if !strings.Contains(lk, "reason") {
				continue
			}
			switch v.(type) {
			case float64, bool:
				// length / count / boolean — the D14-legal shapes
				sawReasoningField = true
			default:
				t.Errorf("debug log line carries reasoning field %q as %T (%v) — D14 allows "+
					"lengths/counts only (FR-016); line: %s", k, v, v, line)
			}
		}
	}
	assert.False(t, sawReasoningField, "instrument check: no reasoning-keyed field was found in the log — "+
		"the LLM-response debug record must still report reasoning as a length/count (FR-016)")
}
