//go:build goolm && stdjson

package email_test

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/stretchr/testify/require"
)

const q25Mailbox = "qa-mailbox@example.test"
const q25Payload = "Q25 normally registered attachment bytes\n"

func q25StartMailbox(t *testing.T) (string, int) {
	t.Helper()
	memory := imapmemserver.New()
	user := imapmemserver.NewUser(q25Mailbox, "fixture-password")
	require.NoError(t, user.Create("INBOX", nil))
	memory.AddUser(user)
	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memory.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		require.NoError(t, server.Close())
		require.NoError(t, <-done)
	})
	client, err := imapclient.DialInsecure(listener.Addr().String(), nil)
	require.NoError(t, err)
	require.NoError(t, client.Login(q25Mailbox, "fixture-password").Wait())
	message := "From: sender@example.test\r\nTo: " + q25Mailbox + "\r\nSubject: Q25 attachment fixture\r\n" +
		"Date: Mon, 02 Mar 2026 10:00:00 +0000\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=q25-boundary\r\n\r\n" +
		"--q25-boundary\r\nContent-Type: text/plain\r\n\r\nBody\r\n" +
		"--q25-boundary\r\nContent-Type: text/plain; name=report.txt\r\n" +
		"Content-Disposition: attachment; filename=report.txt\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		base64.StdEncoding.EncodeToString([]byte(q25Payload)) + "\r\n--q25-boundary--\r\n"
	appendCommand := client.Append("INBOX", int64(len(message)), nil)
	_, err = appendCommand.Write([]byte(message))
	require.NoError(t, err)
	require.NoError(t, appendCommand.Close())
	_, err = appendCommand.Wait()
	require.NoError(t, err)
	require.NoError(t, client.Close())
	// Only the network dial is substituted. Real client MIME/ref handling,
	// agent registration, transfer factory, authorization and filesystem run.
	email.Q25UseIMAPDialForTest(t, func(_ context.Context, address string, _ *tls.Config) (*imapclient.Client, error) {
		return imapclient.DialInsecure(address, nil)
	})
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return host, port
}

type q25AgentFixture struct {
	workA, workB string
	auditPath    string // constructor-derived physical audit file, not the transfer service home
	ctx          context.Context
	instance     *agent.AgentInstance
}

func q25RegisteredMain(t *testing.T) q25AgentFixture {
	t.Helper()
	host, port := q25StartMailbox(t)
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("Q25_MAIL_PASSWORD", "fixture-password")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.RestrictToWorkspace = true
	cfg.Sandbox.AuditLog = true
	main := config.AgentConfig{ID: "q25-main", Name: "Q25 Main", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}
	cfg.Agents.List = []config.AgentConfig{main}
	cfg.Mailboxes = config.MailboxesConfig{main.ID: {"workspace-a": {
		Enabled: true, WorkspaceID: "workspace-a", Username: q25Mailbox, PasswordRef: "Q25_MAIL_PASSWORD",
		IMAPHost: host, IMAPPort: port, SMTPHost: host, SMTPPort: port,
	}}}
	for _, id := range []string{"workspace-a", "workspace-b"} {
		require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: id, Name: id, Status: "active", CoreTeam: []string{main.ID}}))
		require.NoError(t, os.MkdirAll(workspace.WorkDir(home, id), 0o755))
	}
	loop, err := agent.NewAgentLoop(cfg, bus.NewMessageBus(), testutil.NewScenario().WithText("unused"))
	require.NoError(t, err)
	t.Cleanup(loop.Close)
	instance, ok := loop.GetRegistry().GetAgent(main.ID)
	require.True(t, ok, "normal loop construction must publish the real Main instance")
	_, ok = instance.Tools.Get("download_email_attachment")
	require.True(t, ok, "SEC-03: save must be the normally registered tool, not a manually constructed/replaced tool")
	ctx := tools.WithWorkspaceID(context.Background(), "workspace-a")
	ctx = tools.WithAgentID(ctx, main.ID)
	ctx = tools.WithTurnWorkspaceDir(ctx, workspace.WorkDir(home, "workspace-a"))
	t.Logf("Q25 normal registry ready: agent=%s workspace=workspace-a mailbox=%s", main.ID, q25Mailbox)
	// initializeAudit derives its home from the parent of AgentHomeBasePath.
	return q25AgentFixture{workA: workspace.WorkDir(home, "workspace-a"), workB: workspace.WorkDir(home, "workspace-b"), auditPath: filepath.Join(filepath.Dir(cfg.AgentHomeBasePath()), "system", "audit.jsonl"), ctx: ctx, instance: instance}
}

func q25AttachmentArgs(t *testing.T, fixture q25AgentFixture) map[string]any {
	t.Helper()
	inbox, ok := fixture.instance.Tools.Get("read_inbox")
	require.True(t, ok)
	result := inbox.Execute(fixture.ctx, map[string]any{"limit": float64(1)})
	require.False(t, result.IsError, "configured normal mailbox positive control: %s", result.ForLLM)
	var messages []email.Message
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &messages))
	require.Len(t, messages, 1)
	require.Equal(t, "Q25 attachment fixture", messages[0].Subject)
	// The normal read_message path, not the envelope-only inbox list, issues
	// the authoritative attachment reference (transport.go::ReadMessage).
	readMessage, ok := fixture.instance.Tools.Get("read_message")
	require.True(t, ok)
	detail := readMessage.Execute(fixture.ctx, map[string]any{"uid": float64(messages[0].UID)})
	require.False(t, detail.IsError, "normal message-read positive control: %s", detail.ForLLM)
	var fullMessage email.Message
	require.NoError(t, json.Unmarshal([]byte(detail.ForLLM), &fullMessage))
	require.NotEmpty(t, fullMessage.MessageRef, "consume a real issued reference, never synthesize one")
	list, ok := fixture.instance.Tools.Get("list_email_attachments")
	require.True(t, ok)
	parts := list.Execute(fixture.ctx, map[string]any{"folder": "inbox", "message_ref": fullMessage.MessageRef})
	require.False(t, parts.IsError, "normal attachment listing positive control: %s", parts.ForLLM)
	var listing struct {
		Attachments []struct {
			Filename  string `json:"filename"`
			PartIndex int    `json:"part_index"`
		} `json:"attachments"`
	}
	require.NoError(t, json.Unmarshal([]byte(parts.ForLLM), &listing))
	require.Len(t, listing.Attachments, 1)
	require.Equal(t, "report.txt", listing.Attachments[0].Filename)
	return map[string]any{"folder": "inbox", "message_ref": fullMessage.MessageRef, "part_index": float64(listing.Attachments[0].PartIndex)}
}
