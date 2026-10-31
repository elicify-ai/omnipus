package channels

// T-04 (part 2) — S-54: all 13 adapters record rejections (channels-spec-r8,
// S-54, FR-060; US-7 AS1).
//
// Oracle (spec): every adapter rejection branch listed in the spec's
// "Symbols involved" table — discord.go:369, telegram.go:641,
// slack.go:374/480/555 (3 branches), qq.go:663/743 (2 branches),
// weixin.go:420, wecom.go:643, feishu_64.go:513, irc/handler.go:73,
// googlechat.go:636, line.go:423, whatsapp_native.go:749, matrix.go:796,
// dingtalk.go:236 — must call the shared recordRejected memory, so an
// unlisted sender is counted and surfaced on every adapter (US-7 AS1),
// not silently dropped.
//
// This is the spec's supplementary source-scan drift pattern (the same
// pattern S-43 pins the producer table with): a behavioural per-adapter
// fixture per platform is beyond RED scope (each needs a live adapter
// harness); the scan pins that each listed branch site carries the call.
// Line numbers drift, so the scan counts recordRejected call sites per
// adapter file against the branch count the spec names.
//
// RED expectation: no adapter calls recordRejected today (WP-0 shell), so
// every row fails.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// adapterRejectionBranches is the spec's S-54 table: adapter source file →
// number of rejection branches that must call recordRejected.
var adapterRejectionBranches = map[string]int{
	"discord/discord.go":                 1,
	"telegram/telegram.go":               1,
	"slack/slack.go":                     3,
	"qq/qq.go":                           2,
	"weixin/weixin.go":                   1,
	"wecom/wecom.go":                     1,
	"feishu/feishu_64.go":                1,
	"irc/handler.go":                     1,
	"googlechat/googlechat.go":           1,
	"line/line.go":                       1,
	"whatsapp_native/whatsapp_native.go": 1,
	"matrix/matrix.go":                   1,
	"dingtalk/dingtalk.go":               1,
}

func TestS54_AllAdapterRejectionBranchesRecordRejected(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "instrument: runtime.Caller must resolve this test file")
	// <root>/pkg/channels/adapter_rejection_branches_test.go → repo root.
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))

	for adapterFile, wantBranches := range adapterRejectionBranches {
		t.Run(adapterFile, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(repoRoot, "pkg", "channels", filepath.FromSlash(adapterFile)))
			require.NoError(t, err, "instrument: the spec's branch file must exist")

			got := strings.Count(string(data), "recordRejected(")
			require.GreaterOrEqual(t, got, wantBranches,
				"S-54: %s must call recordRejected at each of its %d rejection branch(es); found %d — unlisted senders are silently dropped there today",
				adapterFile, wantBranches, got)
		})
	}
}
