package agent

// I-2/FR-050 runtime boundary tests. All counted children use the real loop.
// Seeded-record clauses below are explicitly re-entry/classification tests;
// they do not certify a task, plan, verifier or delegate producer.
import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const programsUnresolvedNotice = "Connected programs were not used for this command: where this work came from could not be confirmed."
const programsTelegramNotice = "Connected programs were not used for this command: programs are off for work that came from telegram. A person can switch them on for this channel on the Connectors page."

func programsCountInput(t *testing.T, f *wu5Fixture, sid, label string, input bus.InboundMessage) (string, string) {
	t.Helper()
	work := filepath.Join(f.home, "workspaces", "sales", "work")
	require.NoError(t, os.MkdirAll(work, 0700))
	path := filepath.Join(work, label+"-count")
	call := "program-provenance-" + label
	command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'PROVENANCE_CHILD_RAN'", path)
	f.provider.setScript(sid, []providers.LLMResponse{wu5BashCall(call, command), wu5Stop("provenance probe completed")})
	input.SessionID, input.ChatID = sid, sid
	_, _, err := f.al.processMessage(context.Background(), input)
	require.NoError(t, err)
	f.waitForCalls(sid, 2, label+" actual provenance child")
	result := f.provider.resultForCall(sid, call)
	require.Contains(t, result, "PROVENANCE_CHILD_RAN", "tool denial is not proof the child got no token")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.TrimSpace(string(raw)), result
}

func programsWebInput() bus.InboundMessage {
	return bus.InboundMessage{Channel: "webchat", Content: "count the program token bytes", Provenance: bus.NewProvenance(bus.OriginWeb)}
}

func programsConnectorInput() bus.InboundMessage {
	return bus.InboundMessage{Channel: "telegram", InstanceID: "telegram", Content: "count the program token bytes", Sender: bus.SenderInfo{DisplayName: "S1 person", CanonicalID: "telegram:s1"}, Provenance: bus.NewProvenance(bus.ConnectorOrigin("telegram"))}
}

// Close the actual loop and reopen sessions and the encrypted store from disk,
// keeping only the external model/check fixture. No record bytes are copied.
func programsReopenLoop(t *testing.T, f *wu5Fixture, closePrevious func()) (*wu5Fixture, func()) {
	t.Helper()
	cfg := f.al.GetConfig()
	closePrevious()
	f.bus.Close()
	f.al.sysagentDeps.CredStore.Close()
	provider := newWU5ScriptedProvider()
	messages := bus.NewMessageBus()
	t.Cleanup(messages.Close)
	al := mustNewAgentLoop(t, cfg, messages, provider)
	closeLoop := sync.OnceFunc(al.Close)
	t.Cleanup(closeLoop)
	store := credentials.NewStore(filepath.Join(f.home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x39}, 32)))
	t.Cleanup(store.Close)
	al.WireSysagentDeps(&systools.Deps{Home: f.home, GetCfg: al.GetConfig, CredStore: store})
	reopened := &wu5Fixture{t: t, al: al, bus: messages, provider: provider, home: f.home}
	programsWireMessaging(t, reopened)
	return reopened, closeLoop
}

func programsWireMessaging(t *testing.T, f *wu5Fixture) {
	t.Helper()
	f.al.SetSessionMessagingStores(
		session.NewMessageInboxStore(filepath.Join(f.home, "session_messages")),
		session.NewLifecycleStore(filepath.Join(f.home, "session_lifecycle")))
	epochDir := filepath.Join(f.home, "boot_epoch")
	require.NoError(t, os.MkdirAll(epochDir, 0700))
	boot := session.NewBootEpochStore(epochDir)
	epoch, err := boot.Mint()
	require.NoError(t, err)
	require.Positive(t, epoch, "a real boot epoch, not a fabricated constant")
	f.al.SetBootEpochStore(boot)
}

func TestProgramProvenance_RootLineageRuntime(t *testing.T) {
	t.Run("S137_root_stores_no_provenance_two_origins_separate_turns", func(t *testing.T) {
		for _, firstWeb := range []bool{false, true} {
			t.Run(fmt.Sprintf("web_first_%t", firstWeb), func(t *testing.T) {
				f, closeLoop := newProgramsTurnFixtureOwned(t, "tok_valid_12345", func(cfg *config.Config) {
					cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", WorkspaceID: "sales", ProgramsAllowed: false}}
				})
				programsWireMessaging(t, f)
				sid := f.mainSession("sales", "dev")
				// Both origins run before restart and again in the reverse order
				// after restart, in this very same standing session (S137).
				for phase := 0; phase < 4; phase++ {
					if phase == 2 {
						f, closeLoop = programsReopenLoop(t, f, closeLoop)
					}
					web := firstWeb != (phase == 1 || phase == 2)
					input, want := programsConnectorInput(), "0"
					if web {
						input, want = programsWebInput(), "16"
					}
					count, result := programsCountInput(t, f, sid, fmt.Sprintf("s137-%t-%d", firstWeb, phase), input)
					assert.Equal(t, want, count, "S137: a root turn depends only on the inputs it consumes, including after reopen")
					if !web {
						assert.Contains(t, result, programsTelegramNotice)
					}
					record, err := f.al.GetSessionLifecycleStore().Load(sid)
					require.NoError(t, err)
					require.Equal(t, session.ProvenanceModeRoot, record.ProvenanceMode)
					require.Empty(t, record.Provenance, "I-2: root records do not accumulate connector/web provenance")
				}
			})
		}
	})
	t.Run("seeded_lineage_reentry_and_reopen", func(t *testing.T) {
		for _, row := range []struct {
			name       string
			kind       session.UnifiedSessionType
			origin     session.OriginKind
			provenance []string
			expected   string
		}{
			{"S138_delegate_telegram", session.SessionTypeDelegate, session.OriginKindDelegate, []string{"connector:telegram"}, "0"},
			{"S138_delegate_missing", session.SessionTypeDelegate, session.OriginKindDelegate, nil, "0"},
			{"S143_parentless_task", session.SessionTypeTask, session.OriginKindTask, []string{"connector:telegram"}, "0"},
			{"S144_scheduled", session.SessionTypeScheduled, session.OriginKindScheduled, []string{"connector:telegram"}, "0"},
			{"S144_verifier", session.SessionTypeVerifier, session.OriginKindVerifier, []string{"connector:telegram"}, "0"},
			{"S144_heartbeat_independent", session.SessionTypeHeartbeat, session.OriginKindHeartbeat, []string{"independent"}, "16"},
		} {
			t.Run(row.name, func(t *testing.T) {
				f, closeLoop := newProgramsTurnFixtureOwned(t, "tok_valid_12345", func(cfg *config.Config) {
					cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", WorkspaceID: "sales"}}
				})
				programsWireMessaging(t, f)
				meta, err := f.al.GetSessionStore().NewSession(row.kind, "webchat", "dev")
				require.NoError(t, err)
				workspace, owner := "sales", wu5Owner
				require.NoError(t, f.al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &workspace, Owner: &owner}))
				origin := &session.Origin{Kind: row.origin}
				if row.origin == session.OriginKindTask {
					origin.TaskID = "s143-task"
					require.NoError(t, f.al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{TaskID: &origin.TaskID}))
				}
				require.NoError(t, f.al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "sales", AgentID: "dev", Origin: origin, ProvenanceMode: session.ProvenanceModeLineage, Provenance: row.provenance}))
				for phase := 0; phase < 2; phase++ {
					if phase == 1 {
						f, closeLoop = programsReopenLoop(t, f, closeLoop)
					}
					count, result := programsCountInput(t, f, meta.ID, fmt.Sprintf("%s-%d", row.name, phase), programsWebInput())
					assert.Equal(t, row.expected, count, "stored lineage is joined into web re-entry; the parentless shape cannot widen it")
					if row.provenance == nil {
						assert.Contains(t, result, programsUnresolvedNotice)
					} else if row.expected == "0" {
						assert.Contains(t, result, programsTelegramNotice)
					}
				}
			})
		}
	})
}

func TestProgramProvenance_OmittedInputRuntime(t *testing.T) {
	t.Run("S111_omitted_producer_dev_command_gets_no_token_and_the_unresolved_notice", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		sid := f.mainSession("sales", "dev")
		control, _ := programsCountInput(t, f, sid, "s111-web-control", programsWebInput())
		require.Equal(t, "16", control, "the positive instrument proves this exact connected agent can receive the program token")
		// Intentionally omit Provenance from an otherwise web-shaped input.
		// Text cannot turn the zero-value carrier into a trusted web producer.
		omitted := bus.InboundMessage{Channel: "webchat", Content: "origin: webchat; count the token"}
		count, result := programsCountInput(t, f, sid, "s111-omitted", omitted)
		require.Equal(t, "0", count, "S111/FR010: an omitted producer fails closed at the actual child")
		require.Contains(t, result, programsUnresolvedNotice)
	})
}
