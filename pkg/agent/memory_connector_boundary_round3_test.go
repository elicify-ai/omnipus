package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// The reconciled HTTP tests cover saved binding + old archive preservation.
// This drives actual bound-connector admission to verify the destination too.
func TestMemoryRound3ConnectorDestination(t *testing.T) {
	for _, initiallyBound := range []bool{false, true} {
		name := "FirstBinding"
		if initiallyBound {
			name = "RebindToExistingMain"
		}
		t.Run(name, func(t *testing.T) {
			f := boundConnFixture(t)
			if !initiallyBound {
				cfg := f.al.GetConfig()
				initial := cfg.Channels[connInst]
				initial.WorkspaceID, initial.Identity = "", nil
				cfg.Channels[connInst] = initial
			}
			store := f.al.GetSessionStore()
			old, err := store.NewChannelSession("telegram", connInst, "chat-1", addrReceiver, "original")
			require.NoError(t, err)
			if initiallyBound {
				ws := addrWS
				require.NoError(t, store.SetMeta(old.ID, session.MetaPatch{WorkspaceID: &ws}))
			}
			require.NoError(t, store.AddMessage(old.ID, "assistant", "ORIGINAL_A_RECALLED_NOTE"))
			oldMeta, err := store.GetMeta(old.ID)
			require.NoError(t, err)
			oldArchive, err := store.ReadArchive(t.Context(), old.ID)
			require.NoError(t, err)
			require.Len(t, oldArchive, 1)
			bws := "ws-2"
			bMain, err := store.GetOrCreateMainSession(bws, addrReceiver)
			require.NoError(t, err)
			require.NoError(t, store.AppendTranscriptStrict(bMain.ID, session.TranscriptEntry{
				ID: "round3-b-existing", Role: "user", AgentID: addrReceiver, Content: "B_EXISTING_HISTORY", Timestamp: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC),
			}))
			cfg := f.al.GetConfig()
			binding := cfg.Channels[connInst]
			binding.WorkspaceID = bws
			binding.Identity = &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: addrReceiver}
			cfg.Channels[connInst] = binding
			f.deps.eligible[bws+"/"+addrReceiver] = true
			for _, text := range []string{"CURRENT_B", "FOLLOWUP_B"} {
				msg := connMsg("chat-1", text)
				refusal, admitErr := f.al.admitBoundConnectorInputCtx(t.Context(), &msg)
				require.NoError(t, admitErr)
				require.Empty(t, refusal)
				require.Equal(t, bMain.ID, msg.SessionID, "use B's established main, never A's old session")
				require.NotEqual(t, old.ID, msg.SessionID)
				require.Equal(t, bws, msg.Metadata["workspace_id"])
				capture, captureErr := f.al.RequestLedger().Resolve(msg.SessionID, addressing.Pair{WorkspaceID: bws, AgentID: addrReceiver}, msg.TranscriptEntryID)
				require.NoError(t, captureErr)
				require.Equal(t, addressing.Pair{WorkspaceID: bws, AgentID: addrReceiver}, capture.Source.Owner)
			}
			metaAfter, err := store.GetMeta(old.ID)
			require.NoError(t, err)
			require.Equal(t, oldMeta, metaAfter)
			archiveAfter, err := store.ReadArchive(t.Context(), old.ID)
			require.NoError(t, err)
			require.Equal(t, oldArchive, archiveAfter)
			entries, err := store.ReadTranscript(bMain.ID)
			require.NoError(t, err)
			contents := make([]string, len(entries))
			for i, entry := range entries {
				contents[i] = entry.Content
			}
			require.Equal(t, []string{"B_EXISTING_HISTORY", "CURRENT_B", "FOLLOWUP_B"}, contents, "B keeps its own history, gains only B input and reuses its identity")
		})
	}
}
