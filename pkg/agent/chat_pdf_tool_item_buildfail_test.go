package agent

// Spec #1968 (read-file native PDF) — T11, T14 (tool) and T16, loop level.
// These are B-labelled "fails to build" tests: they CONSTRUCT a
// tools.ToolResult with the new InspectionPDFs field, which does not exist
// today. Keep this file held out of the package while the normal-file tests
// run; the build errors it produces name exactly the symbols the
// implementation must provide:
//
//	tools.ToolResult field  InspectionPDFs []InspectionPDF `json:"-"`
//	type tools.InspectionPDF struct {
//	    Bytes       []byte
//	    Name        string
//	    Pages       int
//	    SHA256      string
//	    HasText     bool
//	    Reauthorize func(context.Context) error
//	}
//
// (The same two symbols are declared by the pkg/tools B-file; the loop-side
// plumbing this file exercises is the presenter half of FR-15/FR-18.)
//
// Oracles: §5.2 FR-15 (six tool PDFs → one grouped tool-message block on the
// follow-up request), §5.5 FR-18 (transports group per protocol; no numeric
// limit at the transport), §5.6 M1 and the FR-13 denial note (request-only,
// never retained anywhere), FR-17 (the per-turn retry budget and its reset),
// FR-10 (per-attempt admission, reauthorized per attempt). Requests are
// observed through the recording peer (chat_pdf_1968_fixture_test.go), whose
// step shape selects the response dialect (openai-compatible, anthropic,
// bedrock); every assertion reads the RAW request bodies.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// pdf1968ReauthDeniedNote is the FR-13 fixed note a denied reauthorization
// surfaces to the model.
const pdf1968ReauthDeniedNote = "[PDF view unavailable: access denied]"

// pdf1968StubTool is a registered stub tool whose result carries native PDF
// inspections the way read_file/library_read will after the change. queue
// holds one inspection set per Execute call, in call order (the last entry
// repeats beyond the script); onCall, when set, fires before each result
// returns (T16's steering arm enqueues from inside the tool call).
type pdf1968StubTool struct {
	tools.BaseTool
	queue   [][]tools.InspectionPDF
	content string // optional persisted reader result, e.g. scanned N1 + R1
	onCall  func(call int)
	calls   atomic.Int32
}

func (d *pdf1968StubTool) Name() string { return "pdf1968_stub_tool" }
func (d *pdf1968StubTool) Description() string {
	return "Stub tool returning native PDF inspections (spec #1968)"
}
func (d *pdf1968StubTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (d *pdf1968StubTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (d *pdf1968StubTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	n := int(d.calls.Add(1))
	if d.onCall != nil {
		d.onCall(n)
	}
	idx := n - 1
	if idx >= len(d.queue) {
		idx = len(d.queue) - 1
	}
	pdfs := d.queue[idx]
	content := d.content
	if content == "" {
		content = fmt.Sprintf("%d PDF page(s) inspected natively this call", len(pdfs))
	}
	return &tools.ToolResult{
		ForLLM:         content,
		InspectionPDFs: pdfs,
	}
}

// pdf1968SixInspections builds six distinct small PDF inspections with
// filled SHA256 digests and permissive reauthorization.
func pdf1968SixInspections(t *testing.T) []tools.InspectionPDF {
	t.Helper()
	out := make([]tools.InspectionPDF, 6)
	for i := range out {
		data := pdf1968PDFOfSize(t, 2048*(i+1))
		sum := sha256.Sum256(data)
		out[i] = tools.InspectionPDF{
			Bytes:       data,
			Name:        fmt.Sprintf("page%d.pdf", i+1),
			Pages:       1,
			SHA256:      hex.EncodeToString(sum[:]),
			HasText:     true,
			Reauthorize: func(context.Context) error { return nil },
		}
	}
	return out
}

// pdf1968SingletonQueue wraps the inspection set as ONE-PDF entries so each
// stub-tool Execute call delivers exactly one singleton inspection (N2: the
// queue holds one entry per call — a single six-PDF entry repeated over six
// scripted calls would silently script 6×6 = 36 inspection items).
func pdf1968SingletonQueue(items []tools.InspectionPDF) [][]tools.InspectionPDF {
	out := make([][]tools.InspectionPDF, len(items))
	for i := range items {
		out[i] = []tools.InspectionPDF{items[i]}
	}
	return out
}

// pdf1968StubCallMap builds the OpenAI-compatible tool_calls entry the peer
// scripts for the stub tool (the peer translates it into the Converse and
// Messages dialects per step shape).
func pdf1968StubCallMap(id string) map[string]any {
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      "pdf1968_stub_tool",
			"arguments": "{}",
		},
	}
}

// pdf1968BodyHasPDFBytes reports whether the raw request body carries the
// exact PDF bytes (as their base64 data-URL payload).
func pdf1968BodyHasPDFBytes(body string, data []byte) bool {
	return strings.Contains(body, base64.StdEncoding.EncodeToString(data))
}

// ---------------------------------------------------------------------------
// Grouping walkers (F9): the FR-11 grouping is structural — all consecutive
// tool results of a round travel inside ONE wire message. These walkers
// count, per wire message, the tool-result blocks it carries, per protocol
// dialect.
// ---------------------------------------------------------------------------

// pdf1968OpenAIToolMessages counts the tool-role messages of a Chat
// Completions request body.
func pdf1968OpenAIToolMessages(t *testing.T, body string) int {
	t.Helper()
	var wire struct {
		Messages []map[string]any `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &wire), "the request must be Chat Completions JSON")
	n := 0
	for _, m := range wire.Messages {
		if role, _ := m["role"].(string); role == "tool" {
			n++
		}
	}
	return n
}

// pdf1968ChatToolResultIDs returns the tool_call_id of every tool-role
// message of a Chat Completions body, in wire order (N2: read the RESULT
// blocks themselves — the assistant's tool_calls already carry the ids, so
// a whole-body first-occurrence scan cannot detect reordered results).
func pdf1968ChatToolResultIDs(t *testing.T, body string) []string {
	t.Helper()
	var wire struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &wire), "the request must be Chat Completions JSON")
	var ids []string
	for _, m := range wire.Messages {
		if m.Role == "tool" {
			ids = append(ids, m.ToolCallID)
		}
	}
	return ids
}

// pdf1968AnthropicToolResultIDs returns the tool_use_id of every tool_result
// content block of a Messages-API body, in wire order.
func pdf1968AnthropicToolResultIDs(t *testing.T, body string) []string {
	t.Helper()
	var wire struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &wire), "the request must be Messages JSON")
	var ids []string
	for _, m := range wire.Messages {
		for _, block := range m.Content {
			if kind, _ := block["type"].(string); kind == "tool_result" {
				id, _ := block["tool_use_id"].(string)
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// pdf1968BedrockToolResultIDs returns the toolUseId of every toolResult
// content block of a Converse body, in wire order.
func pdf1968BedrockToolResultIDs(t *testing.T, body string) []string {
	t.Helper()
	var wire struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &wire), "the request must be Converse JSON")
	var ids []string
	for _, m := range wire.Messages {
		for _, block := range m.Content {
			if tr, ok := block["toolResult"].(map[string]any); ok {
				id, _ := tr["toolUseId"].(string)
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// pdf1968BedrockToolResultMessages returns, per Converse message, the count
// of toolResult blocks it carries (zero-count messages omitted).
func pdf1968BedrockToolResultMessages(t *testing.T, body string) []int {
	t.Helper()
	var wire struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &wire), "the request must be Converse JSON")
	var counts []int
	for _, m := range wire.Messages {
		n := 0
		for _, block := range m.Content {
			if _, ok := block["toolResult"]; ok {
				n++
			}
		}
		if n > 0 {
			counts = append(counts, n)
		}
	}
	return counts
}

// pdf1968AnthropicToolResultMessages returns, per Messages-API message, the
// count of tool_result blocks it carries (zero-count messages omitted).
func pdf1968AnthropicToolResultMessages(t *testing.T, body string) []int {
	t.Helper()
	var wire struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &wire), "the request must be Messages JSON")
	var counts []int
	for _, m := range wire.Messages {
		n := 0
		for _, block := range m.Content {
			if kind, _ := block["type"].(string); kind == "tool_result" {
				n++
			}
		}
		if n > 0 {
			counts = append(counts, n)
		}
	}
	return counts
}

// pdf1968ToolHarness wires one flagged agent on the given fixture row with
// the stub tool registered and granted, returning the loop, the agent map,
// the session store, the media store and the new session id. The operator
// media setting is raised above the fixtures so no arm is masked by the
// media gate.
func pdf1968ToolHarness(t *testing.T, peer *pdf1968Peer, row pdf1968ProviderRow, tool *pdf1968StubTool,
) (*AgentLoop, map[string]*AgentInstance, *session.UnifiedStore, media.MediaStore, string) {
	t.Helper()
	cat := pdf1968Catalog(t, []pdf1968ProviderRow{row})
	al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, func(c *config.Config) {
		c.PDFLimits = pdf1968LimitTable(t, nil, []pdf1968ProviderRow{row})
		c.Agents.Defaults.MaxMediaSize = 100000000
	}, pdf1968Pair{provider: row.id, model: row.models[0].id})
	al.RegisterTool(tool)
	// No-default-policy model (CLAUDE.md hard constraint 6): the stub tool
	// needs an explicit agent-level grant or it fails closed to deny.
	agents["pdf1968-agent"].StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"pdf1968_stub_tool": config.ToolPolicyAllow},
	})
	sid := pdf1968NewSession(t, store, "pdf1968-agent")
	return al, agents, store, mediaStore, sid
}

// ---------------------------------------------------------------------------
// T11 (A) — the FR-18 transport matrix at loop level (F9): six tool PDFs on
// a provider row whose data states max_documents_per_message 5 must admit
// only the newest five (oldest demoted, L2 naming the limit), keep the call
// ids and their order unchanged. Anthropic and Bedrock merge consecutive
// results into one wire message. Chat Completions keeps six tool messages;
// Responses keeps six call-correlated outputs (FR-11). With NO limits, all
// six stay native. Responses uses the real adapter through StoreProviderPool,
// not a replacement Chat implementation or a wire-translator-only surrogate.
// ---------------------------------------------------------------------------

func TestAttachPDF_WireGrouping(t *testing.T) {
	six := pdf1968SixInspections(t)

	// sixCalls scripts one tool round with six stub-tool call ids, in the
	// given dialect.
	sixCalls := func(shape string) []pdf1968Step {
		calls := make([]map[string]any, 6)
		for i := range calls {
			calls[i] = pdf1968StubCallMap(fmt.Sprintf("p%d", i+1))
		}
		return []pdf1968Step{
			{toolCalls: calls, shape: shape},
			{content: "summarised", shape: shape},
		}
	}

	// orderUnchanged asserts the tool-RESULT blocks of the body carry the
	// scripted ids in scripted order, decoded per protocol dialect (N2:
	// first-occurrence body scans cannot see reordering because the
	// assistant's tool_calls already contain every id).
	orderUnchanged := func(t *testing.T, body, shape string, ids []string) {
		t.Helper()
		var got []string
		switch shape {
		case "bedrock":
			got = pdf1968BedrockToolResultIDs(t, body)
		case "anthropic":
			got = pdf1968AnthropicToolResultIDs(t, body)
		default:
			got = pdf1968ChatToolResultIDs(t, body)
		}
		require.Equal(t, ids, got,
			"the tool results must keep their scripted ids and order on the result blocks themselves")
	}

	// sixToolPDFsPresent asserts all six fixture PDFs ride the body.
	sixToolPDFsPresent := func(t *testing.T, body string) {
		t.Helper()
		for i, p := range six {
			require.True(t, pdf1968BodyHasPDFBytes(body, p.Bytes),
				"tool PDF %d (page%d.pdf) must ride the request natively, byte-exact", i+1, i+1)
		}
	}

	// ids of the six scripted calls, p1..p6.
	ids := make([]string, 6)
	for i := range ids {
		ids[i] = fmt.Sprintf("p%d", i+1)
	}

	for _, bounded := range []bool{false, true} {
		t.Run(fmt.Sprintf("Responses six per-call outputs bounded=%v", bounded), func(t *testing.T) {
			row := pdf1968CapableRow("fx/responses-gw", "fx/responses-model", nil)
			if bounded {
				row.limits = map[string]any{"max_documents_per_message": 1}
			}
			tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(six)}
			peer := newPdf1968Peer(t, sixCalls("responses")...)
			al, agents, store, _, sid := pdf1968ToolHarness(t, peer, row, tool)
			_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
			require.NoError(t, err)
			bodies := peer.Bodies()
			require.Len(t, bodies, 2)
			require.Equal(t, "/openai/v1/responses", peer.Paths()[1], "premise: the real Responses adapter sent this request")
			var body struct {
				Input []map[string]any `json:"input"`
			}
			require.NoError(t, json.Unmarshal([]byte(bodies[1]), &body))
			var gotIDs []string
			for _, item := range body.Input {
				if item["type"] != "function_call_output" {
					continue
				}
				id, _ := item["call_id"].(string)
				gotIDs = append(gotIDs, id)
				require.LessOrEqual(t, len(gotIDs), len(six), "no extra output may consume an unscripted fixture")
				blob, err := json.Marshal(item)
				require.NoError(t, err)
				payloads := pdf1968BodyPDFs(t, string(blob))
				require.Len(t, payloads, 1, "each output contains its own PDF, not a merged six-PDF message")
				require.Equal(t, six[len(gotIDs)-1].Bytes, payloads[0])
			}
			require.Equal(t, ids, gotIDs, "six separate call-correlated outputs in result order")
			require.Len(t, pdf1968BodyPDFs(t, bodies[1]), 6)
			assert.NotContains(t, bodies[1], pdf1968L2Sentence, "one document per call fits the per-message limit")
		})
	}

	t.Run("openai-compatible no-limits baseline: six native, six tool messages", func(t *testing.T) {
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(six)}
		peer := newPdf1968Peer(t, sixCalls("")...)
		al, agents, store, _, sid := pdf1968ToolHarness(t, peer,
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil), tool)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err)
		assert.Equal(t, "summarised", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2, "one tool round then the answer")
		carried := pdf1968BodyPDFs(t, bodies[1])
		require.Len(t, carried, 6, "no limits anywhere: all six tool PDFs go native (FR-10)")
		for i, got := range carried {
			require.True(t, bytes.Equal(got, six[i].Bytes),
				"tool PDF %d must reach the wire byte-exact and in result order (%d vs %d bytes)",
				i+1, len(got), len(six[i].Bytes))
		}
		require.Equal(t, 6, pdf1968OpenAIToolMessages(t, bodies[1]),
			"FR-11: the default groups one internal message per wire message — six tool "+
				"results travel as six tool messages; only the Bedrock/Anthropic transports "+
				"merge consecutive tool results into one message")
		orderUnchanged(t, bodies[1], "", ids)
	})

	t.Run("bedrock max_documents_per_message 5: newest five, one grouped message, ids in order", func(t *testing.T) {
		row := pdf1968BedrockRow("fx/bedrock-gw", "fx/bedrock-model",
			map[string]any{"max_documents_per_message": 5})
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(six)}
		peer := newPdf1968Peer(t, sixCalls("bedrock")...)
		al, agents, store, _, sid := pdf1968ToolHarness(t, peer, row, tool)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err)
		assert.Equal(t, "summarised", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2)
		carried := pdf1968BodyPDFs(t, bodies[1])
		require.Len(t, carried, 5, "max_documents_per_message 5 admits only five of the six")
		for i, got := range carried {
			require.True(t, bytes.Equal(got, six[i+1].Bytes),
				"the NEWEST five travel, in result order (position %d = page%d.pdf)", i+1, i+2)
		}
		assert.False(t, pdf1968BodyHasPDFBytes(bodies[1], six[0].Bytes),
			"the oldest (first result) must be the demoted one")
		pdf1968AssertL2(t, "Bedrock grouped limit", bodies[1], "max_documents_per_message", []string{row.id, row.models[0].id}, 5)
		require.Equal(t, []int{6}, pdf1968BedrockToolResultMessages(t, bodies[1]),
			"all six toolResult blocks stay grouped inside ONE Converse message (FR-11)")
		orderUnchanged(t, bodies[1], "bedrock", ids)
	})

	t.Run("anthropic max_documents_per_message 5: newest five, one grouped message, ids in order", func(t *testing.T) {
		row := pdf1968ProviderRow{
			id:       "fx/anthropic-gw",
			protocol: "anthropic",
			limits:   map[string]any{"max_documents_per_message": 5},
			models:   []pdf1968ModelRow{{id: "fx/anthropic-model", modalities: []string{"text", "pdf"}}},
		}
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(six)}
		peer := newPdf1968Peer(t, sixCalls("anthropic")...)
		al, agents, store, _, sid := pdf1968ToolHarness(t, peer, row, tool)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err)
		assert.Equal(t, "summarised", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2)
		carried := pdf1968BodyPDFs(t, bodies[1])
		require.Len(t, carried, 5, "max_documents_per_message 5 admits only five of the six")
		for i, got := range carried {
			require.True(t, bytes.Equal(got, six[i+1].Bytes),
				"the NEWEST five travel, in result order (position %d = page%d.pdf)", i+1, i+2)
		}
		assert.False(t, pdf1968BodyHasPDFBytes(bodies[1], six[0].Bytes),
			"the oldest (first result) must be the demoted one")
		pdf1968AssertL2(t, "Anthropic grouped limit", bodies[1], "max_documents_per_message", []string{row.id, row.models[0].id}, 5)
		require.Equal(t, []int{6}, pdf1968AnthropicToolResultMessages(t, bodies[1]),
			"all six tool_result blocks stay grouped inside ONE Messages message (FR-11)")
		orderUnchanged(t, bodies[1], "anthropic", ids)
	})

	t.Run("bedrock no limits anywhere: six native", func(t *testing.T) {
		row := pdf1968BedrockRow("fx/bedrock-gw", "fx/bedrock-model", nil)
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(six)}
		peer := newPdf1968Peer(t, sixCalls("bedrock")...)
		al, agents, store, _, sid := pdf1968ToolHarness(t, peer, row, tool)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err)
		assert.Equal(t, "summarised", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2)
		carried := pdf1968BodyPDFs(t, bodies[1])
		require.Len(t, carried, 6, "no limits in the row data: all six go native")
		sixToolPDFsPresent(t, bodies[1])
		assert.NotContains(t, bodies[1], pdf1968L2Sentence,
			"no demotion happened, so no L2 may appear (grouping only from data)")
		require.Equal(t, []int{6}, pdf1968BedrockToolResultMessages(t, bodies[1]),
			"grouping applied from the transport's consecutive-tool-result rule (FR-11)")
		orderUnchanged(t, bodies[1], "bedrock", ids)
	})

	t.Run("anthropic no limits anywhere: six native", func(t *testing.T) {
		row := pdf1968ProviderRow{
			id:       "fx/anthropic-gw",
			protocol: "anthropic",
			models:   []pdf1968ModelRow{{id: "fx/anthropic-model", modalities: []string{"text", "pdf"}}},
		}
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(six)}
		peer := newPdf1968Peer(t, sixCalls("anthropic")...)
		al, agents, store, _, sid := pdf1968ToolHarness(t, peer, row, tool)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err)
		assert.Equal(t, "summarised", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2)
		carried := pdf1968BodyPDFs(t, bodies[1])
		require.Len(t, carried, 6, "no limits in the row data: all six go native")
		sixToolPDFsPresent(t, bodies[1])
		assert.NotContains(t, bodies[1], pdf1968L2Sentence)
		require.Equal(t, []int{6}, pdf1968AnthropicToolResultMessages(t, bodies[1]),
			"grouping applied from the transport's consecutive-tool-result rule (FR-11)")
		orderUnchanged(t, bodies[1], "anthropic", ids)
	})
}

// T13 (B): the tool-side scanned presentation matrix. The stub supplies
// exactly the persisted N1+R1 result a reader produces; the real attempt
// presenter, not the stub, must choose native bytes versus persisted text+N2.
func TestAttachPDF_ToolScannedPDF(t *testing.T) {
	data := pdf1968NoTextPDF(t, 1, true)
	sum := sha256.Sum256(data)
	persisted := "[document: scan.pdf | no extractable text (likely scanned) | readable natively by PDF-capable models; otherwise convert pages to PNG/JPEG]\n[PDF: sent natively to models that support PDFs, within their limits, this turn only; later turns see this text]"
	for _, capable := range []bool{true, false} {
		t.Run(fmt.Sprintf("capable=%v", capable), func(t *testing.T) {
			row := pdf1968CapableRow("fx/tool-scan", "fx/scan-model", nil)
			if !capable {
				row = pdf1968TextOnlyRow("fx/tool-scan", "fx/scan-model", nil)
			}
			item := tools.InspectionPDF{Bytes: data, Name: "scan.pdf", Pages: 1, SHA256: hex.EncodeToString(sum[:]), HasText: false, Reauthorize: func(context.Context) error { return nil }}
			tool := &pdf1968StubTool{queue: [][]tools.InspectionPDF{{item}}, content: persisted}
			peer := newPdf1968Peer(t, pdf1968Step{toolCalls: []map[string]any{pdf1968StubCallMap("scan-call")}}, pdf1968Step{content: "finished"})
			al, agents, store, _, sid := pdf1968ToolHarness(t, peer, row, tool)
			_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
			require.NoError(t, err)
			bodies := peer.Bodies()
			require.Len(t, bodies, 2)
			payloads := pdf1968BodyPDFs(t, bodies[1])
			if capable {
				require.Equal(t, [][]byte{data}, payloads, "scanned tool PDF is native byte-exact despite HasText=false")
				assert.Contains(t, bodies[1], pdf1968M1Marker)
				assert.NotContains(t, bodies[1], pdf1968N2Sentence)
			} else {
				require.Empty(t, payloads, "an unflagged attempt must not admit the scanned tool PDF")
				assert.Contains(t, bodies[1], pdf1968N1Sentence, "persisted scanned explanation survives")
				assert.Contains(t, bodies[1], pdf1968N2Sentence, "request explains that this model cannot read the content")
				assert.NotContains(t, bodies[1], "extracted text", "no invented extracted text")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// T16 (A) — the FR-17 per-turn retry budget across the turn's sources (F7):
// a chat-only rejection makes a later read PDF text-only; a tool rejection
// rebuilds the round with the chat PDF demoted (P1); steering admits no
// native PDF while the budget is spent; a failed text retry ends the turn
// with no third attempt; a new turn resets the state and re-admits
// natively. Every request is read off the raw bodies.
// ---------------------------------------------------------------------------

func TestPDFRecovery_TurnInvariant(t *testing.T) {
	pdfRejectBody := `{"error":{"message":"pdf input not supported for this model","type":"invalid_request_error"}}`
	six := pdf1968SixInspections(t)
	flaggedRow := pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil)

	t.Run("chat-only rejection demotes every later request of the SAME turn (F7)", func(t *testing.T) {
		// FR-17/S6: the budget is per TURN — after the chat demotion, every
		// later request of the same turn carries text, the tool round
		// included. The retry's response drives the tool call, so the read
		// happens within the very turn that spent the budget.
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(six)}
		peer := newPdf1968Peer(t,
			pdf1968Step{status: 400, errBody: pdfRejectBody}, // chat attempt rejected
			pdf1968Step{content: "reading it", toolCalls: []map[string]any{ // the text retry calls the tool
				pdf1968StubCallMap("p1")}},
			pdf1968Step{content: "seen"}) // the follow-up carries the tool results
		al, agents, store, mediaStore, sid := pdf1968ToolHarness(t, peer, flaggedRow, tool)
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", pdf1968TextPDF(t, 1, true))

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err, "the chat rejection recovers within the turn")
		bodies := peer.Bodies()
		require.Len(t, bodies, 3, "rejected attempt, text retry (with the tool call), follow-up")
		require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1, "the chat attempt carried the PDF natively")
		assert.False(t, pdf1968HasPDF(t, bodies[1]))
		assert.Contains(t, bodies[1], pdf1968P1Sentence, "the retry shows the chat demotion with P1")
		assert.False(t, pdf1968HasPDF(t, bodies[2]),
			"FR-17: after the budget is spent every later request of the turn is text — "+
				"the tool round's singleton PDF included")
		assert.Contains(t, bodies[2], pdf1968P1Sentence,
			"the budget-spent tool PDF is demoted with the P1/P2 note (§5.6)")
	})

	t.Run("tool rejection rebuilds the round with the chat PDF demoted", func(t *testing.T) {
		// One turn: chat PDF + the read. The rebuilt request after the 400
		// demotes EVERYTHING (the budget applies to the whole turn): the
		// retry shows the chat attachment's text and the tool results' text
		// with P1, no native bytes from any source.
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(six)}
		peer := newPdf1968Peer(t,
			pdf1968Step{toolCalls: []map[string]any{pdf1968StubCallMap("p1")}}, // request 1
			pdf1968Step{status: 400, errBody: pdfRejectBody},                   // request 2 (tool + chat PDFs) rejected
			pdf1968Step{content: "recovered"})                                  // request 3: text retry

		al, agents, store, mediaStore, sid := pdf1968ToolHarness(t, peer, flaggedRow, tool)
		chatBytes := pdf1968TextPDF(t, 1, true)
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", chatBytes)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err)
		assert.Equal(t, "recovered", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 3)
		require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1, "request 1 carried the chat PDF natively")
		carried := pdf1968BodyPDFs(t, bodies[1])
		require.NoError(t, pdf1968PayloadsError(carried, [][]byte{chatBytes, six[0].Bytes}),
			"N10: before any rejection both distinct chat and tool PDFs remain native, in message order")
		assert.False(t, pdf1968HasPDF(t, bodies[2]),
			"the rebuilt retry carries no native bytes from ANY source (FR-17)")
		assert.Contains(t, bodies[2], pdf1968P1Sentence,
			"the rebuilt round shows the chat PDF's demotion with P1")
		assert.Contains(t, bodies[2], "PAGE 1 text", "the chat PDF's extracted text survives")
		assert.Contains(t, bodies[2], "1 PDF page(s) inspected natively this call", "the tool's persisted text survives")
	})

	t.Run("steering with a PDF admits no native while the budget is spent (F7)", func(t *testing.T) {
		// Inject a fresh PDF only when the text retry reaches the peer: the
		// retry budget has already been spent, so this cannot exercise an
		// accidental pre-recovery attachment. Queue on the exact SessionKey.
		const steerText = "steer: focus on page three"
		var alRef *AgentLoop
		var sidRef, steerRef string
		var queued atomic.Bool
		tool := &pdf1968StubTool{queue: [][]tools.InspectionPDF{{six[0]}, {}}}
		peer := newPdf1968Peer(t,
			pdf1968Step{toolCalls: []map[string]any{pdf1968StubCallMap("p1")}},
			pdf1968Step{status: 400, errBody: pdfRejectBody},
			pdf1968Step{toolCalls: []map[string]any{pdf1968StubCallMap("p2")}, onRequest: func() {
				id, err := pdf1968QueueSteering(alRef, sidRef, steerText, steerRef)
				if err != nil || id == "" {
					t.Errorf("enqueue fresh post-recovery steering: id=%q err=%v", id, err)
					return
				}
				queued.Store(true)
			}},
			pdf1968Step{content: "done"})
		al, agents, store, mediaStore, sid := pdf1968ToolHarness(t, peer, flaggedRow, tool)
		alRef, sidRef = al, sid
		steerRef = pdf1968StorePDF(t, mediaStore, "steer.pdf", pdf1968TextPDF(t, 1, true))

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err)
		require.True(t, queued.Load(), "fresh PDF steering was enqueued after recovery")
		bodies := peer.Bodies()
		require.Len(t, bodies, 4, "tool call, rejected PDF, one text retry, same-turn continuation")
		require.Len(t, pdf1968BodyPDFs(t, bodies[1]), 1, "premise: a native tool PDF triggered recovery")
		assert.False(t, pdf1968HasPDF(t, bodies[2]), "premise: the retry already spent the budget")
		steeringBody := bodies[3]
		require.Contains(t, steeringBody, steerText, "steering must reach the SAME turn's next request")
		assert.False(t, pdf1968HasPDF(t, steeringBody),
			"FR-17: the steering request must carry no native PDF while the budget is spent — "+
				"the steering PDF included")
		assert.Contains(t, steeringBody, pdf1968P1Sentence,
			"the budget-spent steering PDF is demoted with the P1/P2 note (§5.6)")
		assert.Contains(t, bodies[2], pdf1968P1Sentence,
			"the demotion that spent the budget is visible with P1")
	})

	t.Run("a failed text retry ends the turn with no third attempt", func(t *testing.T) {
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(six)}
		peer := newPdf1968Peer(t,
			pdf1968Step{toolCalls: []map[string]any{pdf1968StubCallMap("p1")}},
			pdf1968Step{status: 400, errBody: pdfRejectBody},
			pdf1968Step{status: 400, errBody: pdfRejectBody})
		al, agents, store, _, sid := pdf1968ToolHarness(t, peer, flaggedRow, tool)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.Error(t, err, "the rejection on the text retry fails the turn")
		bodies := peer.Bodies()
		require.Len(t, bodies, 3, "exactly one demotion retry — no third attempt after it fails")
		carried := pdf1968BodyPDFs(t, bodies[1])
		require.Len(t, carried, 1, "the rebuilt request carried the tool singleton natively")
		require.True(t, bytes.Equal(carried[0], six[0].Bytes), "byte-exact singleton")
		assert.False(t, pdf1968HasPDF(t, bodies[2]))
		assert.Contains(t, bodies[2], pdf1968P1Sentence)
	})

	t.Run("a new turn resets the per-turn state and re-admits natively", func(t *testing.T) {
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(six)}
		peer := newPdf1968Peer(t,
			pdf1968Step{status: 400, errBody: pdfRejectBody}, // turn 1: chat attempt rejected
			pdf1968Step{content: "ok"},                       // turn 1: text retry
			pdf1968Step{content: "fresh"})                    // turn 2
		al, agents, store, mediaStore, sid := pdf1968ToolHarness(t, peer, flaggedRow, tool)
		firstPDF := pdf1968TextPDF(t, 1, true)
		newPDF := pdf1968TextPDF(t, 2, true)
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", firstPDF)
		newRef := pdf1968StorePDF(t, mediaStore, "new-report.pdf", newPDF)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err)
		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, newRef)
		require.NoError(t, err)
		assert.Equal(t, "fresh", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 3)
		require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1, "turn 1 carried the chat PDF natively")
		assert.False(t, pdf1968HasPDF(t, bodies[1]))
		assert.Contains(t, bodies[1], pdf1968P1Sentence)
		require.Equal(t, [][]byte{firstPDF, newPDF}, pdf1968BodyPDFs(t, bodies[2]),
			"§4.4 + FR-17: the fresh turn replays the old chat PDF and admits the new one, byte-exact in history order")
		assert.NotContains(t, bodies[2], pdf1968P1Sentence,
			"the previous turn's spent PDF budget must not leak into the fresh turn")
	})
}

// ---------------------------------------------------------------------------
// T14 (tool, A) — tool inspection PDFs are request-only (FR-14): after a
// positive native send, NO file under the session's store (canonical
// transcript archive, day archives, the derived chat-history index and its
// checkpoint) holds the raw bytes or their base64, the transcript re-read
// through a genuinely reopened store shows the same absence, and the
// per-attempt reauthorization runs exactly once per admitting attempt. The
// denial arm pins FR-13's fixed note.
// ---------------------------------------------------------------------------

func pdf1968AssertNoPrivatePDF(t *testing.T, label string, records any, items []tools.InspectionPDF) {
	t.Helper()
	payloads := make([][]byte, len(items))
	for i := range items {
		payloads[i] = items[i].Bytes
	}
	assert.NoError(t, pdf1968PrivatePDFError(records, payloads), "%s: canonical history must exclude native tool bytes", label)
}

func TestToolPDF_NotRetained(t *testing.T) {
	six := pdf1968SixInspections(t)

	t.Run("no bytes anywhere: history, transcript, archive, checkpoint, reload", func(t *testing.T) {
		// Per-item reauthorization counters (N5): FR-10 authorizes EACH
		// inspection item per admitting attempt, so each of the six items
		// must run exactly once — a shared counter could hide six calls on
		// one item and zero on the rest.
		reauthCalls := make([]atomic.Int32, len(six))
		counting := make([]tools.InspectionPDF, len(six))
		for i := range counting {
			counting[i] = six[i]
			counting[i].Reauthorize = func(context.Context) error { reauthCalls[i].Add(1); return nil }
		}
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(counting)}
		calls := make([]map[string]any, 6)
		for i := range calls {
			calls[i] = pdf1968StubCallMap(fmt.Sprintf("p%d", i+1))
		}
		peer := newPdf1968Peer(t,
			pdf1968Step{toolCalls: calls},
			pdf1968Step{content: "summarised"},
			pdf1968Step{content: "again"})
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		home := filepath.Join(t.TempDir(), "home")
		al, agents, store, _ := pdf1968NewPeerHarnessAt(t, peer, cat, cat, home, func(c *config.Config) {
			c.Agents.Defaults.MaxMediaSize = 100000000
		}, pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
		al.RegisterTool(tool)
		agents["pdf1968-agent"].StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{"pdf1968_stub_tool": config.ToolPolicyAllow},
		})
		sid := pdf1968NewSession(t, store, "pdf1968-agent")

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err)
		assert.Equal(t, "summarised", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2)
		carried := pdf1968BodyPDFs(t, bodies[1])
		require.Len(t, carried, 6, "premise: the tool PDFs went native on the follow-up request")
		for i, got := range carried {
			require.True(t, bytes.Equal(got, counting[i].Bytes),
				"premise: tool PDF %d rode the wire byte-exact", i+1)
		}
		// Request-only M1 markers (FR-14): one per attached tool PDF.
		assert.Equal(t, 6, strings.Count(bodies[1], "native PDF attached"),
			"each of the six native tool PDFs carries its request-only M1 marker")

		// Per-attempt reauthorization (FR-10): exactly one attempt carried
		// the PDFs, so EVERY item's Reauthorize ran exactly once.
		for i := range reauthCalls {
			assert.Equal(t, int32(1), reauthCalls[i].Load(),
				"reauthorization runs once per item per admitting attempt (item %d)", i+1)
		}

		// FR-14 covers the live canonical model window as well as disk.
		liveHistory := store.GetHistory(sid)
		require.NotEmpty(t, liveHistory, "premise: inspect a populated live model history")
		pdf1968AssertNoPrivatePDF(t, "live history", liveHistory, counting)
		liveArchive, err := store.ReadArchive(t.Context(), sid)
		require.NoError(t, err)
		require.NotEmpty(t, liveArchive, "premise: inspect the populated full archive")
		pdf1968AssertNoPrivatePDF(t, "live full archive", liveArchive, counting)

		// FR-14: the derived chat-history index is built on demand — the
		// readiness call materialises the checkpoint this test inspects.
		require.NoError(t, store.CheckChatHistoryReadiness(context.Background(), sid))
		// N6: the unified session store lives directly under <home>/sessions
		// (sharedSessionStoreFor → NewUnifiedStoreWithHome(<home>/sessions,
		// <home>)) — not under a workspace subfolder.
		storeRoot := filepath.Join(home, "sessions")
		sessionDir := filepath.Join(storeRoot, sid)
		require.FileExists(t, filepath.Join(sessionDir, "transcript.jsonl"),
			"the canonical transcript archive must exist")
		require.FileExists(t, filepath.Join(sessionDir, ".chat-history", "checkpoint.json"),
			"the derived chat-history checkpoint must exist after the readiness call")

		// No file under the WHOLE store root may hold raw bytes or the full
		// base64 of any tool PDF (FR-14: history, transcript, archive,
		// checkpoint — wherever under the store they live).
		sweepErr := filepath.WalkDir(storeRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			text := string(data)
			for i, p := range counting {
				assert.NotContains(t, text, string(p.Bytes),
					"%s must not retain raw tool PDF bytes (%s)", filepath.Base(path), p.Name)
				assert.NotContains(t, text, base64.StdEncoding.EncodeToString(p.Bytes),
					"%s must not retain base64 tool PDF bytes (file %d)", filepath.Base(path), i+1)
			}
			return nil
		})
		require.NoError(t, sweepErr)

		// Reload: a genuine reopen (a fresh store instance — the loop-level
		// sharedStores cache would return the same instance) reads the same
		// archive with the same absence.
		fresh, ferr := session.NewUnifiedStoreWithHome(storeRoot, home)
		require.NoError(t, ferr)
		t.Cleanup(func() { require.NoError(t, fresh.Close()) })
		reopenedHistory := fresh.GetHistory(sid)
		require.NotEmpty(t, reopenedHistory, "premise: reopened model history is populated")
		pdf1968AssertNoPrivatePDF(t, "reopened history", reopenedHistory, counting)
		reopenedArchive, aerr := fresh.ReadArchive(t.Context(), sid)
		require.NoError(t, aerr)
		require.NotEmpty(t, reopenedArchive, "premise: reopened full archive is populated")
		pdf1968AssertNoPrivatePDF(t, "reopened full archive", reopenedArchive, counting)
		entries, terr := fresh.ReadTranscript(sid)
		require.NoError(t, terr)
		require.NotEmpty(t, entries, "the reopened transcript must be readable")
		for _, e := range entries {
			for i, p := range counting {
				assert.NotContains(t, e.Content, string(p.Bytes), "reopened transcript: raw bytes")
				assert.NotContains(t, e.Content, base64.StdEncoding.EncodeToString(p.Bytes),
					"reopened transcript: base64 (file %d)", i+1)
			}
		}

		// Later turn (N6): the rebuilt history must not re-attach any tool
		// PDF either — the follow-up request is text-only.
		_, err = pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err)
		bodies = peer.Bodies()
		require.Len(t, bodies, 3)
		for i, p := range counting {
			assert.False(t, pdf1968BodyHasPDFBytes(bodies[2], p.Bytes),
				"the later turn's rebuilt history must not carry tool PDF %d natively (FR-14)", i+1)
		}
		assert.False(t, pdf1968HasPDF(t, bodies[2]),
			"the later turn's request carries no native tool PDF at all")
	})

	t.Run("every capable attempt reauthorizes and observes revoked access", func(t *testing.T) {
		var revoked atomic.Bool
		counts := make([]atomic.Int32, len(six))
		items := append([]tools.InspectionPDF(nil), six...)
		for i := range items {
			items[i].Reauthorize = func(context.Context) error {
				counts[i].Add(1)
				if revoked.Load() && i == 0 {
					return errors.New("access revoked between provider attempts")
				}
				return nil
			}
		}
		calls := make([]map[string]any, len(items))
		for i := range calls {
			calls[i] = pdf1968StubCallMap(fmt.Sprintf("p%d", i+1))
		}
		queue := append(pdf1968SingletonQueue(items), []tools.InspectionPDF{})
		tool := &pdf1968StubTool{queue: queue}
		peer := newPdf1968Peer(t,
			pdf1968Step{toolCalls: calls},
			pdf1968Step{toolCalls: []map[string]any{pdf1968StubCallMap("continue")}, onRequest: func() { revoked.Store(true) }},
			pdf1968Step{content: "finished"})
		al, agents, store, _, sid := pdf1968ToolHarness(t, peer, pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil), tool)
		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err)
		bodies := peer.Bodies()
		require.Len(t, bodies, 3, "no rejection or PDF recovery: two capable PDF-bearing attempts")
		want := make([][]byte, len(items))
		for i := range items {
			want[i] = items[i].Bytes
		}
		require.Equal(t, want, pdf1968BodyPDFs(t, bodies[1]), "premise: first admitting attempt sends all six")
		require.Equal(t, want[1:], pdf1968BodyPDFs(t, bodies[2]), "second admitting attempt omits only the now-revoked oldest item")
		assert.Contains(t, bodies[2], pdf1968ReauthDeniedNote)
		assert.NotContains(t, bodies[2], pdf1968P1Sentence, "authorization, not the PDF retry budget, caused the omission")
		observed := make([]int32, len(counts))
		for i := range counts {
			observed[i] = counts[i].Load()
		}
		assert.NoError(t, pdf1968ReauthorizationError(observed, 2), "each of six items is reauthorized on BOTH admitting attempts")
	})

	t.Run("denied reauthorization drops the attempt's PDFs with the FR-13 note", func(t *testing.T) {
		denying := make([]tools.InspectionPDF, len(six))
		for i := range denying {
			denying[i] = six[i]
			denying[i].Reauthorize = func(context.Context) error {
				return errors.New("access denied: session expired")
			}
		}
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(denying)}
		peer := newPdf1968Peer(t,
			pdf1968Step{toolCalls: []map[string]any{pdf1968StubCallMap("p1")}},
			pdf1968Step{content: "summarised"})
		al, agents, store, _, sid := pdf1968ToolHarness(t, peer,
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil), tool)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err, "a denied reauthorization must not fail the turn")
		assert.Equal(t, "summarised", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2)
		assert.False(t, pdf1968HasPDF(t, bodies[1]),
			"a denied reauthorization must send no native PDF on that attempt (FR-10)")
		assert.Contains(t, bodies[1], pdf1968ReauthDeniedNote,
			"the denial must surface the fixed FR-13 note to the model")
	})

	t.Run("mixed admission: permissive items ride natively, denied ones drop (N5)", func(t *testing.T) {
		// Items 1-2 reauthorize permissively, items 3-6 deny. The two
		// admitting items must ride the wire byte-exact while the four
		// denied ones are dropped with the FR-13 note — per-ITEM handling,
		// not an all-or-nothing gate.
		mixedCalls := make([]atomic.Int32, len(six))
		mixed := make([]tools.InspectionPDF, len(six))
		for i := range mixed {
			mixed[i] = six[i]
			if i < 2 {
				mixed[i].Reauthorize = func(context.Context) error { mixedCalls[i].Add(1); return nil }
			} else {
				mixed[i].Reauthorize = func(context.Context) error {
					mixedCalls[i].Add(1)
					return errors.New("access denied: session expired")
				}
			}
		}
		tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(mixed)}
		calls := make([]map[string]any, 6)
		for i := range calls {
			calls[i] = pdf1968StubCallMap(fmt.Sprintf("p%d", i+1))
		}
		peer := newPdf1968Peer(t,
			pdf1968Step{toolCalls: calls},
			pdf1968Step{content: "summarised"})
		al, agents, store, _, sid := pdf1968ToolHarness(t, peer,
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil), tool)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err)
		assert.Equal(t, "summarised", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2)
		carried := pdf1968BodyPDFs(t, bodies[1])
		require.Len(t, carried, 2, "only the permissive items ride the attempt")
		require.True(t, bytes.Equal(carried[0], mixed[0].Bytes), "item 1 rides byte-exact")
		require.True(t, bytes.Equal(carried[1], mixed[1].Bytes), "item 2 rides byte-exact")
		for i := 2; i < len(mixed); i++ {
			assert.False(t, pdf1968BodyHasPDFBytes(bodies[1], mixed[i].Bytes),
				"denied item %d must not reach the wire", i+1)
		}
		assert.Contains(t, bodies[1], pdf1968ReauthDeniedNote,
			"the denials surface the fixed FR-13 note beside the admitted items")
		for i := range mixedCalls {
			assert.Equal(t, int32(1), mixedCalls[i].Load(),
				"every item is reauthorized exactly once per admitting attempt (item %d)", i+1)
		}
	})
}

// ---------------------------------------------------------------------------
// T3 (tool, A) — the catalogue gate covers TOOL PDFs too (F11): the spec's
// refusal states are "loop: nil; catalog.New(); Boot with an invalid
// embedded snapshot and no persisted copy" for "chat and tool PDFs" alike.
// The chat arms live in chat_pdf_capability_test.go; these drive the stub
// tool through the same document-less loop catalogues and assert the same
// refusal on the PDF-bearing request: turn error, the follow-up never sent,
// C1 (FR-3's "requests without PDFs unaffected" keeps request 1 legitimate).
// ---------------------------------------------------------------------------

func TestToolPDF_NoCatalogDocument_Refused(t *testing.T) {
	tool := &pdf1968StubTool{queue: pdf1968SingletonQueue(pdf1968SixInspections(t))}

	cases := []struct {
		name    string
		loopDoc *catalog.Catalog
	}{
		// F11: the nil-catalogue state is a distinct refusal state — guard
		// the premise explicitly instead of assuming it follows from the
		// other two.
		{"a nil loop catalogue refuses the tool-PDF round", nil},
		{"an empty catalogue refuses the tool-PDF round", catalog.New()},
		{"boot with an invalid embedded snapshot and no persisted copy refuses the tool-PDF round",
			catalog.Boot(t.Context(), []byte("{ this is not a catalogue"),
				nil, pdf1968BootStore{readErr: fs.ErrNotExist}, nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, tc.loopDoc == nil || tc.loopDoc.Document() == nil,
				"premise: the loop catalogue under test must serve no document")
			// The gate is per PDF-bearing request (the no-PDF request goes out
			// normally), so the refusal lands on the FOLLOW-UP request after
			// the stub tool ran: request 1 (text) goes out, the tool result
			// carries the inspections, request 2 must be refused before the
			// provider sees it.
			peer := newPdf1968Peer(t,
				pdf1968Step{toolCalls: []map[string]any{pdf1968StubCallMap("p1")}},
				pdf1968Step{content: "must never be reached"})
			procCat := pdf1968Catalog(t, []pdf1968ProviderRow{
				pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
			})
			home := filepath.Join(t.TempDir(), "home")
			al, agents, store, _ := pdf1968NewPeerHarnessAt(t, peer, procCat, tc.loopDoc, home,
				func(c *config.Config) {
					c.Agents.Defaults.MaxMediaSize = 100000000
				}, pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
			al.RegisterTool(tool)
			agents["pdf1968-agent"].StoreToolPolicy(&tools.ToolPolicyCfg{
				Policies: map[string]config.ToolPolicy{"pdf1968_stub_tool": config.ToolPolicyAllow},
			})
			events := pdf1968Capture(al)
			sid := pdf1968NewSession(t, store, "pdf1968-agent")

			_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
			require.Error(t, err, "a tool-PDF round without a catalogue document must fail the turn")
			assert.Equal(t, 1, peer.CallCount(),
				"only the initial text request may go out; the tool-PDF follow-up must be refused")
			errs := events.modelListErrors()
			require.Len(t, errs, 1, "exactly one model_list_unavailable error event expected, got %+v", events.all())
			pdf1968AssertC1(t, errs[0].Message, "fx/tiny-haiku-pdf")
			pdf1968AssertStoredC1(t, store, sid, "fx/tiny-haiku-pdf")

			// F11: the persisted transcript entry carries the error code and
			// the C1 notice, and a genuinely reopened store replays the same
			// refusal (S8's "transcript replays" for the tool side).
			entries, terr := store.ReadTranscript(sid)
			require.NoError(t, terr)
			var persisted bool
			for _, e := range entries {
				if e.ErrorCode == "model_list_unavailable" {
					persisted = true
					assert.Equal(t, "error", e.Status, "the refusal persists as an error entry")
					pdf1968AssertC1(t, e.Content, "fx/tiny-haiku-pdf")
				}
			}
			require.True(t, persisted, "the refusal must persist with the model_list_unavailable code")

			fresh, ferr := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
			require.NoError(t, ferr)
			replayed, rerr := fresh.ReadTranscript(sid)
			require.NoError(t, rerr)
			var replayedRefusal bool
			for _, e := range replayed {
				if e.ErrorCode == "model_list_unavailable" {
					replayedRefusal = true
					pdf1968AssertC1(t, e.Content, "fx/tiny-haiku-pdf")
				}
			}
			require.True(t, replayedRefusal, "the reopened transcript must replay the refusal")
		})
	}
}
