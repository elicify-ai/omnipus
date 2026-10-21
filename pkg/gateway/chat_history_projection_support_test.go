package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func pagingProjectionTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%o:%d:%s", info.Mode().Perm(), info.ModTime().UnixNano(), b)
		return nil
	}))
	return out
}

func pagingProjectionFixture(t *testing.T) (*session.UnifiedStore, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	s, err := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	meta, err := s.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	return s, meta.ID, home
}

func pagingProjectionProbe(t *testing.T, mode string) {
	t.Helper()
	s, sid, home := pagingProjectionFixture(t)
	if strings.HasSuffix(mode, "_ws_divergence") {
		tools := newToolResultStore(home)
		r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20}, historyPageProjectionDeps{ToolStore: tools})
		require.NoError(t, err, "real shared prepared projector factory, never a counting replay sink")
		require.NotNil(t, r)
		if strings.HasPrefix(mode, "tool_") {
			pagingSeedTools(t, s, sid, 21, 200*1024)
		} else {
			for i := 1; i <= 21; i++ {
				require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: fmt.Sprintf("special-%03d", i), Type: session.EntryTypeProviderFallback, AgentID: "mia", UnavailableModel: "unavailable-model", Model: "used-model", UnavailableCode: "rate_limit", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}))
			}
		}
		require.NoError(t, s.FlushSessionStats(sid))
		before := pagingProjectionTree(t, home)
		offloadBefore := tools.offloadFailureTotal.Load()
		prepared, err := r.Read(context.Background(), s)
		require.NoError(t, err)
		require.Equal(t, before, pagingProjectionTree(t, home), "weighing/final preparation must not offload results, change cards/lifecycle, publish roots or write actions")
		require.Equal(t, offloadBefore, tools.offloadFailureTotal.Load())
		require.Len(t, prepared.Response.Entries, 20)
		require.Len(t, prepared.Page.Entries, 20)
		for i, e := range prepared.Response.Entries {
			require.NotNil(t, e.HistoryPosition)
			require.Equal(t, string(prepared.Page.Entries[i].Position), *e.HistoryPosition)
		}
		require.Equal(t, prepared.Page.PageInfo.ProjectedBytes, prepared.Response.PageInfo.ProjectedBytes)
		var wsBytes int64
		origins := map[string]bool{}
		for _, raw := range prepared.ReplayFrames {
			wsBytes += int64(len(raw))
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &fields))
			var typ string
			require.NoError(t, json.Unmarshal(fields["type"], &typ))
			if typ == "done" || typ == "replay_warning" {
				continue
			}
			var origin generated.HistoryEntryOrigin
			require.NotEmpty(t, fields["history_origin"], "every prepared entry-derived frame is locally owned: %s", typ)
			require.NoError(t, json.Unmarshal(fields["history_origin"], &origin))
			origins[origin.Position] = true
		}
		require.Len(t, origins, 20, "prepared frames cover exactly selected occurrences, not the unselected lookahead")
		entries, err := json.Marshal(prepared.Response.Entries)
		require.NoError(t, err)
		bodyLower := int64(len(entries))
		if wsBytes > bodyLower {
			bodyLower = wsBytes
		}
		require.GreaterOrEqual(t, prepared.Response.PageInfo.ProjectedBytes, bodyLower, "larger carrier's real encoded content, never REST-only guess")
		require.Less(t, prepared.Response.PageInfo.ProjectedBytes, bodyLower+8192, "D-W1 frozen small envelope/context overhead, no repeated batch accounting")
		return
	}
	pagingSeed(t, s, sid, "weigh", 21)
	opts := session.ChatHistoryPageOptions{Limit: 20, MinRows: 1}
	switch mode {
	case "nil_weigher":
		opts.Weigh = nil
	case "error_weigher":
		opts.Weigh = func(context.Context, session.ChatHistoryPage) (session.ChatHistoryWeight, error) {
			return session.ChatHistoryWeight{}, errors.New("private-weigh-canary")
		}
	case "negative_weight":
		opts.Weigh = func(context.Context, session.ChatHistoryPage) (session.ChatHistoryWeight, error) {
			return session.ChatHistoryWeight{RESTBytes: -1, WSBytes: 1, ProjectedBytes: 1}, nil
		}
	case "overflow_weight":
		opts.Weigh = func(context.Context, session.ChatHistoryPage) (session.ChatHistoryWeight, error) {
			n := int64(math.MaxInt64)
			n++
			return session.ChatHistoryWeight{RESTBytes: n, WSBytes: 1, ProjectedBytes: 1}, nil
		}
	case "inconsistent_dependency":
		opts.Weigh = func(context.Context, session.ChatHistoryPage) (session.ChatHistoryWeight, error) {
			return session.ChatHistoryWeight{RESTBytes: 10, WSBytes: 20, ProjectedBytes: 10}, nil
		}
	case "cancel_unselected_lookahead":
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		opts.SoftByteBudget = 1024
		opts.Weigh = func(_ context.Context, p session.ChatHistoryPage) (session.ChatHistoryWeight, error) {
			n := int64(400 + 500*len(p.Entries))
			if len(p.Entries) >= 2 {
				cancel()
			}
			return session.ChatHistoryWeight{RESTBytes: n, WSBytes: n, ProjectedBytes: n}, nil
		}
		_, err := s.ReadChatHistoryPage(ctx, sid, opts)
		require.ErrorIs(t, err, context.Canceled, "discard canceled candidate/preparation, no partial success")
		return
	default:
		t.Fatalf("unhandled independent weigh case %s", mode)
	}
	_, err := s.ReadChatHistoryPage(context.Background(), sid, opts)
	require.ErrorIs(t, err, session.ErrHistoryProjection)
	require.NotContains(t, err.Error(), "private-weigh-canary")
}

// Remote-only heavy data case:100 admitted records just below8MiB. The real
// prepared factory is required BEFORE allocating/seeding this synthetic corpus.
func pagingNearAdmissionProbe(t *testing.T) {
	t.Helper()
	s, sid, home := pagingProjectionFixture(t)
	r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 100, MinRows: 100}, historyPageProjectionDeps{ToolStore: newToolResultStore(home)})
	require.NoError(t, err, "real near-admission factory")
	require.NotNil(t, r)
	payload := strings.Repeat("x", 8*1024*1024-4096)
	for i := 0; i < 100; i++ {
		e := session.TranscriptEntry{ID: fmt.Sprintf("near-%03d", i), Type: session.EntryTypeToolCall, AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{{ID: session.ToolCallID(fmt.Sprintf("call-%03d", i)), Tool: "read_file", Status: "success", Parameters: map[string]any{}, Result: map[string]any{"text": payload}}}}
		encoded, err := json.Marshal(session.ArchiveRecord{TranscriptEntry: e})
		require.NoError(t, err)
		require.Less(t, len(encoded), 8*1024*1024)
		require.Greater(t, len(encoded), 8*1024*1024-4096)
		require.NoError(t, s.AppendTranscriptStrict(sid, e), "actual8-MiB canonical admission, not guessed payload multiplication")
	}
	p, err := r.Read(context.Background(), s)
	require.NoError(t, err)
	require.Len(t, p.Response.Entries, 100)
	require.Equal(t, 100, p.Response.PageInfo.RowCount)
	require.True(t, p.Response.PageInfo.ByteBudgetExceeded)
	require.NotNil(t, p.Response.PageInfo.BudgetOverride)
	require.Equal(t, "height_floor", *p.Response.PageInfo.BudgetOverride)
	require.Contains(t, []string{"row_target", "source_cap"}, p.Response.PageInfo.StopReason)
	var actual int64
	for _, raw := range p.ReplayFrames {
		actual += int64(len(raw))
	}
	rest, err := json.Marshal(p.Response.Entries)
	require.NoError(t, err)
	if int64(len(rest)) > actual {
		actual = int64(len(rest))
	}
	require.Greater(t, actual, int64(4194304))
	require.GreaterOrEqual(t, p.Response.PageInfo.ProjectedBytes, actual)
}
