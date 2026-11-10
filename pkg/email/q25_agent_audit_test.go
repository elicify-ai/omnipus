//go:build goolm && stdjson

package email_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CR-RR1-01 / attachments spec US-2.AC-4: inspect the ACTUAL recorded event,
// not just audit_status. The pair is (top-level agent_id, details.workspace_id).
func TestQ25RegisteredAttachmentAuditIdentifiesWorkspaceAndAgentPair(t *testing.T) {
	fixture := q25RegisteredMain(t)
	args := q25AttachmentArgs(t, fixture)
	download, ok := fixture.instance.Tools.Get("download_email_attachment")
	require.True(t, ok)
	ctx := tools.WithTranscriptSessionID(fixture.ctx, "q25-audit-session")
	result := download.Execute(ctx, args)
	require.False(t, result.IsError, result.ForLLM)
	var receipt struct {
		Saved        bool   `json:"saved"`
		Path         string `json:"path"`
		AbsolutePath string `json:"absolute_path"`
		AuditStatus  string `json:"audit_status"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &receipt))
	require.True(t, receipt.Saved)
	require.Equal(t, "recorded", receipt.AuditStatus)
	actual, err := os.ReadFile(receipt.AbsolutePath)
	require.NoError(t, err)
	assert.Equal(t, q25Payload, string(actual), "retain actual-byte save control")

	// initializeAudit uses the constructor-derived system directory, and Log flushes
	// each row before returning. Read the physical JSONL without closing/replacing the logger.
	auditPath := fixture.auditPath
	raw, err := os.ReadFile(auditPath)
	require.NoError(t, err)
	var saved []audit.Entry
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var entry audit.Entry
		require.NoError(t, json.Unmarshal(line, &entry), "real audit file must remain valid JSONL")
		if entry.Event == string(audit.EventMailAttachmentSaved) {
			saved = append(saved, entry)
		}
	}
	require.Len(t, saved, 1, "US-2.AC-4: exactly one actual attachment-save event is the positive audit-search control")
	entry := saved[0]
	// CR-RR1-01: the event must identify both sides of the agent/workspace pair.
	assert.Equal(t, "q25-main", entry.AgentID, "registered mailbox owner must be attributable")
	assert.Equal(t, "workspace-a", entry.Details["workspace_id"], "recorded save must identify its destination workspace")
	assert.Equal(t, "q25-audit-session", entry.SessionID, "retain caller session attribution")
	assert.Equal(t, "download_email_attachment", entry.Tool)
	// US-2.AC-4: the event describes this real saved part and path, not another operation.
	assert.Equal(t, args["folder"], entry.Details["folder"])
	assert.Equal(t, args["message_ref"], entry.Details["message_ref"])
	assert.Equal(t, args["part_index"], entry.Details["part_index"])
	assert.Equal(t, receipt.Path, entry.Details["workspace_relative_path"])
	assert.Equal(t, "report.txt", entry.Details["original_name"])
	assert.Equal(t, "report.txt", entry.Details["final_name"])
	assert.Equal(t, float64(len(q25Payload)), entry.Details["size_bytes"])
	for _, forbidden := range []string{"attachment_bytes", "content", "subject", "password", "token", "save_operation_token"} {
		// EventMailAttachmentSaved explicitly forbids bytes, subject, credentials and save tokens.
		assert.NotContains(t, entry.Details, forbidden, "forbidden attachment audit field %s", forbidden)
	}
}
