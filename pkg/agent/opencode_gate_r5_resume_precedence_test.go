package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TEST-ANALYSIS gap 3 / B16a: losing a started conversation wins over an
// independently proven program or preserved-workspace refusal. These are
// deterministic lost-runtime fixtures, not claims of an OS restart.
func TestOpenCodeR5_LostConversationWinsOverBadProgramAndChangedWorkspace(t *testing.T) {
	for _, competing := range []string{"bad_settings_program", "changed_preserved_workspace"} {
		t.Run(competing, func(t *testing.T) {
			al, _ := opencodeR4Loop(t, "opencode")
			driver, processes := opencodeR4ProcessEdge(t, al)
			child := launchDelegateTo(t, al, delegateExtCLIAgentID, "R5 competing refusal")
			rec := opencodeR4PriorStart(t, al, child.SessionID)
			worker, ok := al.registry.GetAgent(delegateExtCLIAgentID)
			require.True(t, ok)
			ts := newTurnState(worker, processOptions{
				SessionKey: child.SessionID, TranscriptSessionID: child.SessionID,
				TranscriptStore: al.ResolveSessionStore(child.SessionID), WorkspaceID: rec.WorkspaceID,
				Channel: "webchat", ChatID: child.SessionID, ExternalCLIResume: true,
			}, al.newTurnEventScope(worker.ID, child.SessionID))
			ts.generation = child.Generation
			switch competing {
			case "bad_settings_program":
				badPath := filepath.Join(t.TempDir(), "program-does-not-exist")
				_, statErr := os.Stat(badPath)
				require.ErrorIs(t, statErr, os.ErrNotExist, "instrument: no executable exists at this Settings path")
				if al.GetConfig().Sandbox.ThirdPartyAgents == nil {
					al.GetConfig().Sandbox.ThirdPartyAgents = &config.ThirdPartyAgentsConfig{}
				}
				al.GetConfig().Sandbox.ThirdPartyAgents.CLIs = map[string]config.ThirdPartyCLIConfig{"opencode": {Path: badPath}}
				_, refusal := al.runProgramFor("opencode")
				require.NotEmpty(t, refusal, "instrument: the real saved-program checker detects this competing failure")
			case "changed_preserved_workspace":
				// First demonstrate the competing mismatch through actual dispatch
				// with a retained driver. It refuses before any process call. Then
				// release the driver: both faults coexist in the tested dispatch.
				holder := al.externalRunSession(child.SessionID)
				former := t.TempDir()
				current, _, resolveErr := resolveTurnWorkDirAndWorkspaceOrRefuse(context.Background(), worker.ID, worker.Home, rec.WorkspaceID)
				require.NoError(t, resolveErr)
				require.NotEqual(t, filepath.Clean(former), filepath.Clean(current))
				holder.mu.Lock()
				holder.started, holder.driver = true, driver
				holder.workDir, holder.workspaceID = former, rec.WorkspaceID
				holder.mu.Unlock()
				_, controlErr := runExternalCLISubTurn(context.Background(), al, ts, "workspace-only instrument")
				require.ErrorIs(t, controlErr, errExternalWorkspaceChanged, "instrument: real dispatch can detect the competing preserved-workspace failure")
				holder.mu.Lock()
				holder.driver = nil
				holder.mu.Unlock()
			}
			// Snapshot AFTER any workspace-only instrument's own refusal.
			// The combined-fault call below may write no further refusal/turn.
			before := qaReceiptReopenTranscript(t, al.ResolveSessionStore(child.SessionID), child.SessionID)
			rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			observed := opencodeR4Observe(t, al, child.SessionID)
			result, runErr := runExternalCLISubTurn(context.Background(), al, ts, "combined lost-conversation and configuration/workspace failure")
			opencodeR4Classified(t, runErr, opencodeR4Chat("OpenCode"))
			require.NotNil(t, result, "gap 3: lost-conversation result must retain its neutral parent form")
			assert.ErrorIs(t, result.Err, errExternalResumeUnavailable)
			assert.Equal(t, "("+opencodeR4Code+") "+opencodeR4Neutral("OpenCode"), result.ContentForLLM())
			assert.Equal(t, before, qaReceiptReopenTranscript(t, al.ResolveSessionStore(child.SessionID), child.SessionID), "gap 3: early B16a must not write a competing program/workspace failure")
			after, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			require.NoError(t, err)
			assert.Equal(t, rec, after)
			assert.Empty(t, observed.take(), "gap 3: no competing turn-error event before the caller receives B16a")
			assert.Zero(t, processes.Load(), "gap 3: no replacement external process")
			driver.mu.Lock()
			runs, resumes := len(driver.runOpts), len(driver.resumeOpts)
			driver.mu.Unlock()
			assert.Zero(t, runs)
			assert.Zero(t, resumes)
		})
	}
}
