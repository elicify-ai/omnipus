package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// #1452 A2.8 (founder decision 2026-10-30): the streamed display read must
// decode exactly like encoding/json and fail closed, never show wrong text.

// historyLowerStandIns lowers the stand-in sizes so small random documents
// exercise every stand-in path. keep >= threshold, as in production.
func historyLowerStandIns(t *testing.T, threshold, keep, encKeep int) {
	t.Helper()
	a, b, c := historyStandInThreshold, historyStandInDecodedKeep, historyStandInEncodedKeep
	historyStandInThreshold, historyStandInDecodedKeep, historyStandInEncodedKeep = threshold, keep, encKeep
	t.Cleanup(func() { historyStandInThreshold, historyStandInDecodedKeep, historyStandInEncodedKeep = a, b, c })
}

var historyGenUnits = []string{
	"a", "Z", "0", " ", `\"`, `\\`, `\/`, `\b`, `\f`, `\n`, `\r`, `\t`,
	`\u0001`, `\u001f`, `\u003c`, `\u00e9`, `\u20ac`, `\uffff`, `\ud83d\ude00`,
	`\ud800x`, `\udc00`, `\ud800A`, `\udbff\udfff`, `\ud800\ud800`, `\ud800\u0041`,
	"\u00e9", "\u20ac", "\U0001F600", "\u2028", "\u2029", "<", ">", "&", "\x7f",
	"\xff", "\xc3", "\xe2\x82", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xc0\xaf",
	`\"session_id\"`, `session_id`,
}

var historyGenNumbers = []string{"0", "-0", "1", "-17", "3.25", "1e10", "1E+2", "2.5e-3", "12345678901234567890", "1e400", "-1e-400", "0.000000000000000000000001"}

var historyGenKeys = []string{"text", "media", "session_id", "model_message", "Model_Message", "content", "a", "", `kA`, `é`}

type historyGen struct {
	r *rand.Rand
	b bytes.Buffer
}

func (g *historyGen) ws() {
	g.b.WriteString([]string{"", "", " ", "\n", "\t ", "\r\n"}[g.r.Intn(6)])
}

func (g *historyGen) str(units int) {
	g.b.WriteByte('"')
	for i := 0; i < units; i++ {
		g.b.WriteString(historyGenUnits[g.r.Intn(len(historyGenUnits))])
	}
	g.b.WriteByte('"')
}

func (g *historyGen) value(depth int) {
	n := g.r.Intn(10)
	if depth > 3 && n < 6 {
		n += 4
	}
	switch {
	case n < 2:
		g.object(depth + 1)
	case n < 3:
		g.b.WriteByte('[')
		for i, k := 0, g.r.Intn(4); i < k; i++ {
			if i > 0 {
				g.b.WriteByte(',')
			}
			g.ws()
			g.value(depth + 1)
			g.ws()
		}
		g.b.WriteByte(']')
	case n < 5:
		g.str(g.r.Intn(6))
	case n < 7:
		g.str(10 + g.r.Intn(60)) // around and above the lowered threshold
	case n < 8:
		g.b.WriteString(historyGenNumbers[g.r.Intn(len(historyGenNumbers))])
	default:
		g.b.WriteString([]string{"true", "false", "null"}[g.r.Intn(3)])
	}
}

func (g *historyGen) object(depth int) {
	g.b.WriteByte('{')
	for i, k := 0, g.r.Intn(5); i < k; i++ {
		if i > 0 {
			g.b.WriteByte(',')
		}
		g.ws()
		if g.r.Intn(3) == 0 {
			g.str(g.r.Intn(4))
		} else {
			g.b.WriteString(`"` + historyGenKeys[g.r.Intn(len(historyGenKeys))] + `"`)
		}
		g.ws()
		g.b.WriteByte(':')
		g.ws()
		g.value(depth)
		g.ws()
	}
	g.b.WriteByte('}')
}

func historyGenDoc(r *rand.Rand) []byte {
	g := &historyGen{r: r}
	g.ws()
	g.object(0)
	g.ws()
	return append([]byte(nil), g.b.Bytes()...)
}

func historyEncodeJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// historyCheckStandIns compares the decoded reduced value with the value
// encoding/json decodes from the whole document.
func historyCheckStandIns(t *testing.T, ins *historyStandIns, want, got any, private bool, top bool, path string) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		require.True(t, ok, path)
		require.Len(t, g, len(w), path)
		for k, wv := range w {
			gv, ok := g[k]
			require.True(t, ok, path+"/"+k)
			historyCheckStandIns(t, ins, wv, gv, private || (top && strings.EqualFold(k, "model_message")), false, path+"/"+k)
		}
	case []any:
		g, ok := got.([]any)
		require.True(t, ok, path)
		require.Len(t, g, len(w), path)
		for i := range w {
			historyCheckStandIns(t, ins, w[i], g[i], private, false, fmt.Sprintf("%s[%d]", path, i))
		}
	case string:
		g, ok := got.(string)
		require.True(t, ok, path)
		enc := historyEncodeJSON(t, w)
		if len(enc) <= historyStandInThreshold {
			require.Equal(t, w, g, "%s: a string at or below the threshold is kept exactly", path)
			return
		}
		l, ok := ins.lookup(g)
		require.True(t, ok, "%s: a string above the threshold becomes a stand-in", path)
		sum := sha256.Sum256([]byte(w))
		require.Equal(t, ins.token(sum[:]), g, "%s: token is content-addressed", path)
		require.Equal(t, len(w), l.decodedLen, path)
		require.Equal(t, len(enc), l.encodedLen, "%s: exact canonical encoding size", path)
		require.Equal(t, strings.Contains(w, `"session_id"`), l.namesSessionID, path)
		require.True(t, strings.HasPrefix(w, string(l.decodedPrefix)), "%s: decoded prefix", path)
		keep, encKeep := historyStandInDecodedKeep, historyStandInEncodedKeep
		if private {
			keep, encKeep = historyStandInThreshold, 0
		}
		require.GreaterOrEqual(t, len(l.decodedPrefix), min(len(w), keep), path)
		require.Equal(t, string(enc[1:1+min(len(enc)-2, encKeep)]), string(l.encodedPrefix), "%s: encoded prefix", path)
	default:
		require.Equal(t, want, got, path)
	}
}

func TestHistoryStreamScanner_MatchesEncodingJSONOnRandomDocuments(t *testing.T) {
	historyLowerStandIns(t, 48, 64, 20)
	r := rand.New(rand.NewSource(1452_2026_10_30))
	var standIns, decodeErrors int
	for i := 0; i < 400; i++ {
		doc := historyGenDoc(r)
		var want any
		wantErr := json.Unmarshal(doc, &want)
		ins := newHistoryStandIns()
		reduced, err := historyStreamRecord(bytes.NewReader(append(append([]byte(nil), doc...), '\n')), ins)
		require.Equal(t, json.Valid(doc), err == nil, "doc %d: syntax verdict differs from encoding/json: %q", i, doc)
		if err != nil {
			continue
		}
		var got any
		gotErr := json.Unmarshal(reduced, &got)
		require.Equal(t, wantErr == nil, gotErr == nil, "doc %d: decode verdict differs: %v / %v", i, wantErr, gotErr)
		if wantErr != nil {
			decodeErrors++
			continue
		}
		standIns += len(ins.values)
		historyCheckStandIns(t, ins, want, got, false, true, fmt.Sprintf("doc%d", i))

		// The prefix encoder over stand-ins equals the real encoding's
		// prefix and exact size, for every displayed (non-private) member.
		for k, gv := range got.(map[string]any) {
			if strings.EqualFold(k, "model_message") {
				continue
			}
			enc := historyEncodeJSON(t, want.(map[string]any)[k])
			w := &historyPrefixWriter{limit: historyStandInEncodedKeep}
			require.NoError(t, historyEncodePrefix(w, gv, ins))
			require.Equal(t, len(enc), w.total, "doc %d member %q: exact encoded size", i, k)
			require.Equal(t, string(enc[:min(len(enc), w.limit)]), string(w.buf), "doc %d member %q: encoded prefix", i, k)
			if !ins.inValue(gv) {
				whole := &historyPrefixWriter{limit: 1 << 20}
				require.NoError(t, historyEncodePrefix(whole, gv, nil))
				require.Equal(t, string(enc), string(whole.buf), "without stand-ins the encoder is json.Marshal")
			}
		}
	}
	t.Logf("history_stream_property docs=400 stand_ins=%d decode_errors=%d", standIns, decodeErrors)
	require.Positive(t, standIns, "positive control: the corpus exercises stand-ins")
	require.Positive(t, decodeErrors, "positive control: the corpus exercises decode errors (1e400)")
}

func TestHistoryStreamScanner_SyntaxVerdictMatchesEncodingJSONOnMutations(t *testing.T) {
	historyLowerStandIns(t, 48, 64, 20)
	r := rand.New(rand.NewSource(1452_2026_10_31))
	const noise = "{}[],:\"\\ u0x1e.-+tfn\x01\x1f\xff\n"
	var invalid int
	for i := 0; i < 600; i++ {
		doc := historyGenDoc(r)
		for m, k := 0, 1+r.Intn(3); m < k && len(doc) > 0; m++ {
			at := r.Intn(len(doc))
			switch r.Intn(4) {
			case 0:
				doc = append(doc[:at:at], doc[at+1:]...)
			case 1:
				doc = append(doc[:at:at], append([]byte{noise[r.Intn(len(noise))]}, doc[at:]...)...)
			case 2:
				doc[at] = noise[r.Intn(len(noise))]
			default:
				doc = doc[:at]
			}
		}
		_, err := historyStreamRecord(bytes.NewReader(append(append([]byte(nil), doc...), '\n')), newHistoryStandIns())
		valid := json.Valid(doc)
		require.Equal(t, valid, err == nil, "mutation %d: syntax verdict differs from encoding/json: %q", i, doc)
		if !valid {
			invalid++
			require.ErrorIs(t, err, errHistoryStreamUnreadable)
		}
	}
	t.Logf("history_stream_mutations docs=600 invalid=%d", invalid)
	require.Positive(t, invalid, "positive control: mutations produce invalid documents")
}

func TestHistoryStreamScanner_FramingDepthAndKeys(t *testing.T) {
	read := func(doc string) error {
		_, err := historyStreamRecord(strings.NewReader(doc), newHistoryStandIns())
		return err
	}
	require.NoError(t, read("{}\n"))
	require.ErrorIs(t, read("{}"), errHistoryStreamUnreadable, "an unframed record is not read")
	require.ErrorIs(t, read("{} \n x"), errHistoryStreamUnreadable)
	deep := func(n int) string { return `{"a":` + strings.Repeat("[", n-1) + strings.Repeat("]", n-1) + "}" }
	for _, n := range []int{historyStreamMaxDepth, historyStreamMaxDepth + 1} {
		require.Equal(t, json.Valid([]byte(deep(n))), read(deep(n)+"\n") == nil, "nesting %d follows encoding/json's limit", n)
	}
	// A key larger than the stand-in keep is accepted by encoding/json but
	// not held by the stream: that record fails closed.
	bigKey := `{"` + strings.Repeat("k", historyStandInDecodedKeep+1) + `":1}`
	require.True(t, json.Valid([]byte(bigKey)))
	require.ErrorIs(t, read(bigKey+"\n"), errHistoryStreamUnreadable)
}

// T62/T63b companion: the streamed page entry is identical to "reduce fully
// (the full-entry route), then shorten" for escapes, invalid UTF-8, the 4 KiB
// and 64 KiB UTF-8 boundaries, media and child-session facts, numbers,
// settle, projection and retract, and values shown whole.
func TestChatHistoryPage_StreamedEntryEqualsFullEntryShortened(t *testing.T) {
	at := time.Date(2026, 10, 30, 5, 0, 0, 0, time.UTC)
	escapes := strings.Repeat("q\"\\\n\t<>&\u2028\u0001\u00e9\u20ac\U0001F600/", 6000)
	build := func(t *testing.T, s *UnifiedStore, sid string, fallback bool) {
		app := func(e TranscriptEntry) ArchiveAddress {
			e.AgentID, e.Timestamp, e.ViewMembership = "mia", at, ViewMembershipChat
			a, err := s.AppendTranscriptAddressed(sid, e)
			require.NoError(t, err)
			return a
		}
		call := func(id string, tc ToolCall) ArchiveAddress {
			tc.ID = ToolCallID(id)
			if tc.Tool == "" {
				tc.Tool = "read_file"
			}
			return app(TranscriptEntry{ID: id, Type: EntryTypeToolCall, Role: "assistant", ToolCalls: []ToolCall{tc}})
		}
		if fallback {
			// Values shown whole and larger than a stand-in: the projection
			// reruns with whole reads and must still equal the full route.
			app(TranscriptEntry{ID: "whole-system", Type: EntryTypeSystem, Role: "system", Content: strings.Repeat("s<", 60000)})
			call("whole-params", ToolCall{Status: "success", Parameters: map[string]any{"content": strings.Repeat("p", 100000)}, Result: map[string]any{"text": "ok"}})
			call("whole-error", ToolCall{Status: "error", Error: strings.Repeat("e&", 40000)})
			return
		}
		app(TranscriptEntry{ID: "text-escapes", Role: "user", Content: escapes})
		app(TranscriptEntry{ID: "text-whole-from-standin", Role: "assistant", Content: strings.Repeat("<", 60000)})
		app(TranscriptEntry{ID: "text-invalid-utf8", Role: "assistant", Content: strings.Repeat("a\xffb\xe2\x82", 20000)})
		app(TranscriptEntry{ID: "text-64k-boundary", Role: "user", Content: strings.Repeat("x", 65534) + "€" + strings.Repeat("y", 100)})
		app(TranscriptEntry{ID: "text-small", Role: "user", Content: "hello"})
		// `{"text":"` is 9 bytes: the euro occupies encoded bytes 4095..4097.
		call("result-4k-boundary", ToolCall{Status: "success", Result: map[string]any{"text": strings.Repeat("a", 4086) + "€" + strings.Repeat("b", 60000)}})
		call("result-facts", ToolCall{Tool: "spawn", Status: "success", Result: map[string]any{
			"media": []any{map[string]any{"path": "/x.png", "mime_type": "image/png"}}, "session_id": "child-1",
			"text": strings.Repeat("t", 70000) + `{"session_id":"child-1"}`,
		}})
		call("result-numbers", ToolCall{Status: "success", Result: map[string]any{"n": 12345678901234567890.0, "f": 1e300, "nested": map[string]any{"arr": []any{escapes, 1, true, nil}}}})
		call("result-invalid-utf8", ToolCall{Status: "success", Result: map[string]any{"text": strings.Repeat("\xff\xc3", 40000)}})
		call("params-whole", ToolCall{Status: "success", Parameters: map[string]any{"content": strings.Repeat("w", 60000)}, Result: map[string]any{"text": "small"}})
		call("error-whole", ToolCall{Status: "error", Error: strings.Repeat("e", 60000)})
		settled := call("settled", ToolCall{Status: "pending"})
		_, err := s.SettleToolCall(sid, settled, "pending", ToolCall{ID: "settled", Tool: "read_file", Status: "success", Result: map[string]any{"text": escapes}})
		require.NoError(t, err)
		projected := call("projected", ToolCall{Status: "success", Result: map[string]any{"text": "original", "keep": "me"}})
		_, err = s.ProjectToolCalls(sid, []ToolCallProjectionEdit{{Target: projected, ToolCallID: "projected", ContentState: "capped", Text: strings.Repeat("P", 70000)}})
		require.NoError(t, err)
		retracted := call("retracted", ToolCall{Status: "success", Result: map[string]any{"text": strings.Repeat("R", 52000)}})
		p1, err := s.ProjectToolCalls(sid, []ToolCallProjectionEdit{{Target: retracted, ToolCallID: "retracted", ContentState: "emptied", Text: strings.Repeat("X", 80000)}})
		require.NoError(t, err)
		_, err = s.RetractToolCallEffects(sid, []ArchiveAddress{p1})
		require.NoError(t, err)
		errProjected := call("error-projected", ToolCall{Status: "error", Error: "short failure"})
		_, err = s.ProjectToolCalls(sid, []ToolCallProjectionEdit{{Target: errProjected, ToolCallID: "error-projected", ContentState: "capped", Text: strings.Repeat("E", 55000)}})
		require.NoError(t, err)
	}
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("fallback_%t", fallback), func(t *testing.T) {
			s, sid := newEffectsSession(t)
			build(t, s, sid, fallback)
			require.NoError(t, s.Close())
			opened := map[ArchiveAddress]int{}
			prev := chatHistoryReadHook
			chatHistoryReadHook = func(_ context.Context, e chatHistoryReadEvent) error {
				if e.Phase == "record_opened" && e.Stream == "archive" && e.Address != nil {
					opened[*e.Address]++
				}
				return nil
			}
			page := historyRead(t, s, sid, historyOptions(50))
			chatHistoryReadHook = prev
			require.NotEmpty(t, page.Entries)
			if !fallback {
				// A whole-read rerun opens the owner record again. (The reducer
				// itself may read one effect for two purposes, e.g. a retract.)
				owners := map[string]bool{}
				for _, e := range page.Entries {
					owners[e.Entry.ID] = true
				}
				var twice []string
				for a, n := range opened {
					if n > 1 && owners[a.EntryID] {
						twice = append(twice, fmt.Sprintf("%s x%d", a.EntryID, n))
					}
				}
				require.Empty(t, twice, "streamed read opens each owner once (no whole-read rerun)")
			}
			for _, got := range page.Entries {
				require.False(t, got.Entry.DisplayUnavailable(), got.Entry.ID)
				full, err := s.ReadChatHistoryEntry(context.Background(), sid, got.Position)
				require.NoError(t, err)
				shortened, err := historyShortenForDisplay(full.Entry)
				require.NoError(t, err)
				full.Entry = shortened
				require.Equal(t, full, got, "%s: streamed entry equals reduce-then-shorten", got.Entry.ID)
				require.Equal(t, string(historyEncodeJSON(t, full.Entry)), string(historyEncodeJSON(t, got.Entry)), got.Entry.ID)
				for i := range full.Entry.ToolCalls {
					a, aok := full.Entry.ToolCalls[i].HistoryDisplayPreview()
					b, bok := got.Entry.ToolCalls[i].HistoryDisplayPreview()
					require.Equal(t, aok, bok, got.Entry.ID)
					require.Equal(t, a, b, "%s: byte-identical preview and facts", got.Entry.ID)
				}
			}
			if !fallback {
				ids := historyIDs(page)
				require.Contains(t, ids, "result-facts")
				for _, e := range page.Entries {
					switch e.Entry.ID {
					case "result-4k-boundary":
						d, ok := e.Entry.ToolCalls[0].HistoryDisplayPreview()
						require.True(t, ok)
						require.Len(t, d.Preview, 4095, "positive control: the euro straddles the 4 KiB cut")
					case "result-facts":
						d, ok := e.Entry.ToolCalls[0].HistoryDisplayPreview()
						require.True(t, ok)
						require.True(t, d.TextNamesSessionID, "positive control: the quoted key lies beyond the stand-in prefix")
						require.True(t, d.HasMedia)
						require.Equal(t, "child-1", d.ChildSessionID)
					case "text-64k-boundary":
						require.Len(t, e.Entry.Content, 65534, "positive control: the euro straddles the 64 KiB cut")
					}
				}
			}
		})
	}
}

// A record the stream cannot hold (a key larger than the stand-in keep) is
// one D4 placeholder row on the page; the full-content route still reads it.
func TestChatHistoryPage_StreamUnreadableRecordFailsClosed(t *testing.T) {
	s, sid := newEffectsSession(t)
	key := strings.Repeat("k", historyStandInDecodedKeep+1)
	_, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: "huge-key", Type: EntryTypeToolCall, Role: "assistant", AgentID: "mia", ViewMembership: ViewMembershipChat, Timestamp: time.Date(2026, 10, 30, 5, 0, 0, 0, time.UTC),
		ToolCalls: []ToolCall{{ID: "c", Tool: "read_file", Status: "success", Parameters: map[string]any{key: "UNREADABLE_CANARY"}}}})
	require.NoError(t, err)
	require.NoError(t, s.Close())
	page := historyRead(t, s, sid, historyOptions(20))
	require.Len(t, page.Entries, 1)
	got := page.Entries[0]
	require.True(t, got.Entry.DisplayUnavailable())
	require.Equal(t, "huge-key", got.Entry.ID)
	require.Equal(t, 1, got.RowCount)
	require.NotContains(t, string(historyEncodeJSON(t, got.Entry)), "UNREADABLE_CANARY", "never guessed text")
	full, err := s.ReadChatHistoryEntry(context.Background(), sid, got.Position)
	require.NoError(t, err)
	require.False(t, full.Entry.DisplayUnavailable())
	require.Equal(t, "UNREADABLE_CANARY", full.Entry.ToolCalls[0].Parameters[key], "the full-content route is unchanged")
}
