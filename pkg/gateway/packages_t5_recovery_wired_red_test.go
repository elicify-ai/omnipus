// packages_t5_recovery_wired_red_test.go — start-up recovery through the boot
// calls (epic #1094 S1a, FR-062, FR-037a, D7 row 23e, D15).
//
// bootImportsThenRoster returns the labels "recover-imports" and "roster"
// without loading a roster. A source search matches a commented-out call.
// Neither is accepted as the order proof here.
package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// TestPackages_StartupRecovery_BootCallIsLive is FR-062's boot order with
// comments removed, so a commented-out call does not count.
func TestPackages_StartupRecovery_BootCallIsLive(t *testing.T) {
	raw, err := os.ReadFile("gateway.go")
	require.NoError(t, err)
	src := stripGoComments(string(raw))
	roster := strings.Index(src, "seedAndPersistAgentRoster(")
	require.GreaterOrEqual(t, roster, 0, "seedAndPersistAgentRoster( must exist")
	head := src[:roster]
	require.GreaterOrEqual(t, strings.Index(head, "bootRecoverImports("), 0,
		"FR-062: gateway boot must call bootRecoverImports before the first roster load")
}

// TestPackages_StartupRecovery_RealBoot drives a crash-produced home through
// the same two calls gateway.go makes, then the boot main-chat step.
func TestPackages_StartupRecovery_RealBoot(t *testing.T) {
	api := packageAPI(t)
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	id := createMainAgent(t, api, "Writer", "Answer in rhyme.")
	addTeamMember(t, api.homePath, ws, id)
	writeStagingJournal(t, api.homePath, "stg-boot", id, ws, omnpkg.JournalStarted)
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = nil
	frames := agentCreatedRecorder(api)

	require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)), "FR-062")
	require.Empty(t, cfg.Agents.List, "recovery must finish before the roster is loaded, and must not load it")
	require.NoError(t, seedAndPersistAgentRoster(cfg, api.homePath, filepath.Join(api.homePath, "config.json")))
	require.True(t, rosterHas(cfg, id), "the roster load after recovery lists the recovered Main")
	api.ensureBootMains()
	require.Empty(t, frames(), "start-up sends no agent_created frame")
	requireOneRecoveredMain(t, api, ws, id)
	t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", "stg-boot"))
	require.Equal(t, 1, strings.Count(readAudit(t, api.homePath), omnpkg.RecoveryFinishedMessage),
		"one finished-at-start-up audit for the recovered import")

	agentBefore := fileBytes(t, filepath.Join(api.homePath, "entities", "agents", id+".json"))
	metaBefore := fileBytes(t, recoveredMainMeta(t, api, ws, id))
	require.NoError(t, api.recoverImportsBeforeRoster(packageCtx(t)), "a second recovery of the same home is a no-op")
	require.NoError(t, seedAndPersistAgentRoster(cfg, api.homePath, filepath.Join(api.homePath, "config.json")))
	api.ensureBootMains()
	require.Equal(t, agentBefore, fileBytes(t, filepath.Join(api.homePath, "entities", "agents", id+".json")))
	require.Equal(t, metaBefore, fileBytes(t, recoveredMainMeta(t, api, ws, id)), "the recovered main chat is not rewritten")
	require.Empty(t, frames(), "the second start still sends no agent_created")
	require.Equal(t, 1, strings.Count(readAudit(t, api.homePath), omnpkg.RecoveryFinishedMessage),
		"the second recovery does not write another finished audit")
	t5RequireAbsent(t, filepath.Join(api.homePath, "backups", ".staging", "stg-boot"))
	// bootImportsThenRoster's labels are not an oracle. The call only keeps the
	// unexported helper referenced; nothing above reads what it returns.
	_, _ = api.bootImportsThenRoster(packageCtx(t))
}

func rosterHas(cfg *config.Config, id string) bool {
	for _, ac := range cfg.Agents.List {
		if ac.ID == id {
			return true
		}
	}
	return false
}

func requireOneRecoveredMain(t *testing.T, api *restAPI, ws, agent string) {
	t.Helper()
	meta := recoveredMainMeta(t, api, ws, agent)
	_, err := os.Stat(meta)
	require.NoError(t, err, "the Main has exactly one main chat at %s", meta)
	store := api.agentLoop.GetSessionStore()
	entries, err := os.ReadDir(store.BaseDir())
	require.NoError(t, err)
	var mains []string
	for _, entry := range entries {
		if strings.Contains(entry.Name(), agent) {
			mains = append(mains, entry.Name())
		}
	}
	id, err := session.MainSessionID(ws, agent)
	require.NoError(t, err)
	require.Equal(t, []string{id}, mains, "exactly one main chat for the recovered Main")
}

func recoveredMainMeta(t *testing.T, api *restAPI, ws, agent string) string {
	t.Helper()
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "the loop has no session store, so boot cannot create the recovered main chat")
	id, err := session.MainSessionID(ws, agent)
	require.NoError(t, err)
	return filepath.Join(store.BaseDir(), id, "meta.json")
}

// stripGoComments drops // and /* */ comments so a commented-out call cannot
// satisfy a source check. Strings are kept.
func stripGoComments(src string) string {
	var b strings.Builder
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += end + 4
		case src[i] == '"' || src[i] == '\'' || src[i] == '`':
			quote := src[i]
			b.WriteByte(src[i])
			i++
			if quote == '`' {
				for i < len(src) && src[i] != '`' {
					b.WriteByte(src[i])
					i++
				}
			} else {
				for i < len(src) && src[i] != quote {
					b.WriteByte(src[i])
					if src[i] == '\\' && i+1 < len(src) {
						i++
						b.WriteByte(src[i])
					}
					i++
				}
			}
			if i < len(src) {
				b.WriteByte(src[i])
				i++
			}
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String()
}
