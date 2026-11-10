package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// SG-03 (final-gate security): the addressed-message tool's result - the text
// the model reads, the tool transcript keeps and the WebSocket result frame
// carries - holds fixed wording for a server-side storage failure. The ledger
// path, its internal file name and the parse/OS cause go to the error log only.

var sg03Fragments = []string{
	"requests.jsonl", ".jsonl", "unreadable", "permission denied", "no such file", "/var/", "/tmp", "/private/",
	"invalid character", "unexpected end of JSON", "ledger", "meta.json", "transcript",
}

func sg03Clean(t *testing.T, res *tools.ToolResult, extra ...string) {
	t.Helper()
	texts := []string{res.ForLLM, res.ForUser}
	if res.Err != nil {
		texts = append(texts, res.Err.Error())
	}
	for _, text := range texts {
		lower := strings.ToLower(text)
		for _, frag := range append(append([]string{}, sg03Fragments...), extra...) {
			assert.NotContains(t, lower, strings.ToLower(frag), "tool result must not carry %q: %q", frag, text)
		}
	}
}

func sg03Tool(f *addrFixture) *tools.MessageTool {
	tool := tools.NewMessageTool()
	tool.SetPeerRouter(f.r)
	tool.SetReplyRouter(f.r)
	tool.SetSendCallback(func(_, _, _ string, _ tools.SendOrigin) error { return nil })
	return tool
}

func sg03Ctx(agentID, session string) context.Context {
	ctx := tools.WithToolContext(context.Background(), "webchat", "chat-1")
	ctx = tools.WithAgentID(ctx, agentID)
	ctx = tools.WithWorkspaceID(ctx, addrWS)
	return tools.WithTranscriptSessionID(ctx, session)
}

// A malformed line in the receiver's request ledger: the legitimate answer is
// refused, with fixed text.
func TestSG03_Reply_DamagedLedgerIsFixedText(t *testing.T) {
	f := newAddrFixture(t)
	recv, src := conversationSetup(t, f, addressing.Pair{})
	ledger := filepath.Join(f.al.GetSessionStore().BaseDir(), recv, "requests.jsonl")
	fh, err := os.OpenFile(ledger, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = fh.WriteString("{this line is torn\n")
	require.NoError(t, err)
	require.NoError(t, fh.Close())

	res := sg03Tool(f).Execute(sg03Ctx(addrReceiver, recv), map[string]any{"content": "the answer", "reply_to": "q7"})

	require.True(t, res.IsError, "the reply is still refused: %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "nothing was sent", "the refusal stays actionable")
	sg03Clean(t, res, recv, src, filepath.Dir(ledger))
	entries, _ := f.al.GetSessionStore().ReadTranscript(src)
	assert.Empty(t, entries, "nothing may be sent into the source conversation")
}

// An unwritable source conversation: the answer cannot be saved; fixed text.
func TestSG03_Reply_UnwritableSourceIsFixedText(t *testing.T) {
	f := newAddrFixture(t)
	recv, src := conversationSetup(t, f, addressing.Pair{})
	srcDir := filepath.Join(f.al.GetSessionStore().BaseDir(), src)
	require.NoError(t, os.Chmod(srcDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(srcDir, 0o700) })

	res := sg03Tool(f).Execute(sg03Ctx(addrReceiver, recv), map[string]any{"content": "the answer", "reply_to": "q7"})

	require.True(t, res.IsError, res.ForLLM)
	assert.Contains(t, res.ForLLM, "nothing was sent")
	sg03Clean(t, res, recv, src, srcDir)
}

// A peer request whose receiver main cannot take the ledger entry: fixed text.
func TestSG03_Peer_UnwritableReceiverMainIsFixedText(t *testing.T) {
	f := newAddrFixture(t)
	main, err := f.al.GetSessionStore().GetOrCreateMainSession(addrWS, addrReceiver)
	require.NoError(t, err)
	mainDir := filepath.Join(f.al.GetSessionStore().BaseDir(), main.ID)
	require.NoError(t, os.Chmod(mainDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(mainDir, 0o700) })

	res := sg03Tool(f).Execute(sg03Ctx("ray", "sender-session"), map[string]any{
		"content": "please review X", "workspace_id": addrWS, "agent_id": addrReceiver})

	require.True(t, res.IsError, "the request is still refused: %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "nothing was admitted")
	sg03Clean(t, res, main.ID, mainDir)
}

// Controls: a healthy ledger still routes the answer, and unknown/discarded
// correlations keep their own actionable wording.
func TestSG03_Reply_ControlsKeepTheirWording(t *testing.T) {
	f := newAddrFixture(t)
	recv, src := conversationSetup(t, f, addressing.Pair{})
	tool := sg03Tool(f)

	unknown := tool.Execute(sg03Ctx(addrReceiver, recv), map[string]any{"content": "x", "reply_to": "no-such"})
	require.True(t, unknown.IsError)
	assert.Contains(t, unknown.ForLLM, "no such request is waiting for an answer")

	ok := tool.Execute(sg03Ctx(addrReceiver, recv), map[string]any{"content": "the answer", "reply_to": "q7"})
	require.False(t, ok.IsError, ok.ForLLM)
	entries, err := f.al.GetSessionStore().ReadTranscript(src)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}
