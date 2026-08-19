// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two entries were missing from the secret set until review found them, and
// both were live credential disclosures reachable in ONE tool call:
//
//	$OMNIPUS_HOME/backups/*.tar.gz  a tarball of the ENTIRE vault. createTarGz
//	                                (pkg/gateway/rest_settings.go) excludes only
//	                                logs/ and backups/, so the archive carries
//	                                master.key, credentials.json, config.json,
//	                                cli.token, auth.json, entities/, agents/,
//	                                workspaces/.
//	$OMNIPUS_HOME/auth.json         per-provider OAuth access and refresh tokens
//	                                in PLAINTEXT (pkg/auth/store.go).
//
// Both became reachable when FR-2.2 opened reads: before that, FSScopeConfined
// refused them for a reason that had nothing to do with secrecy, and opening
// reads removed that accidental protection. send_file makes it worse — it uses
// FSOpSend, which by the operator's own decision carries NO path restriction,
// so the carve-out check is the only thing between an agent and a chat upload
// of the whole vault.
//
// These tests drive the REAL enforcement points: ReadFileTool.Execute
// end-to-end for read_file, and the exact ResolvePathAllowingPatterns call
// send_file.go makes for send_file. Every case is paired with a control on an
// ordinary file in the SAME parent directory, so a test that passed because
// $OMNIPUS_HOME as a whole became unreachable would fail rather than look
// like protection.

// secretSetHome lays out a realistic $OMNIPUS_HOME plus the agent home that
// serves as the turn's work dir, and points config.OmnipusHomeDir at it.
//
// Returns the resolved home and the agent home. The home is symlink-resolved
// because on macOS t.TempDir() lives under /var -> /private/var, and the
// carve-out comparison is done on realpaths.
func secretSetHome(t *testing.T) (home, agentHome string) {
	t.Helper()

	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	home = base

	agentHome = filepath.Join(home, "agents", "mia")
	require.NoError(t, os.MkdirAll(agentHome, 0o700))

	// The secrets.
	require.NoError(t, os.MkdirAll(filepath.Join(home, "backups"), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(home, "backups", "backup-20260812T090000Z.tar.gz"),
		[]byte("VAULT-TARBALL-BYTES"), 0o600))

	authJSON, err := json.Marshal(map[string]any{
		"credentials": map[string]any{
			"anthropic": map[string]string{
				"access_token":  "OAUTH-ACCESS-TOKEN",
				"refresh_token": "OAUTH-REFRESH-TOKEN",
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "auth.json"), authJSON, 0o600))

	// The controls: ordinary, non-secret files in the same parents.
	//
	// notes.txt sits directly in $OMNIPUS_HOME, the parent of both backups/
	// and auth.json. If it stopped being readable, these tests would be
	// proving "the home is unreachable" rather than "these two entries are".
	require.NoError(t, os.WriteFile(filepath.Join(home, "notes.txt"), []byte("ORDINARY-HOME-FILE"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentHome, "own.txt"), []byte("OWN-WORKDIR-FILE"), 0o600))

	t.Setenv(config.EnvHome, home)
	return home, agentHome
}

// secretSetTargets is the deny/allow matrix every test below walks.
func secretSetTargets(home, agentHome string) []struct {
	name       string
	path       string
	wantDenied bool
	marker     string
} {
	return []struct {
		name       string
		path       string
		wantDenied bool
		marker     string
	}{
		{"backups tarball", filepath.Join(home, "backups", "backup-20260812T090000Z.tar.gz"), true, "VAULT-TARBALL-BYTES"},
		{"backups directory itself", filepath.Join(home, "backups"), true, ""},
		{"auth.json", filepath.Join(home, "auth.json"), true, "OAUTH-REFRESH-TOKEN"},
		{"control: ordinary file in $OMNIPUS_HOME", filepath.Join(home, "notes.txt"), false, "ORDINARY-HOME-FILE"},
		{"control: file in the agent's own work dir", filepath.Join(agentHome, "own.txt"), false, "OWN-WORKDIR-FILE"},
	}
}

// TestSecretSet_ReadFileToolCannotReachBackupsOrAuthJSON drives read_file
// end-to-end, the way an agent actually reaches these files.
func TestSecretSet_ReadFileToolCannotReachBackupsOrAuthJSON(t *testing.T) {
	home, agentHome := secretSetHome(t)

	// restrict=false is the WORST case for this boundary: it is the
	// unrestricted posture in which every path-based excuse for refusing is
	// gone and only the carve-out remains. If the deny holds here it holds
	// under confined too.
	tool := NewReadFileTool(agentHome, false, 0)

	for _, tc := range secretSetTargets(home, agentHome) {
		t.Run(tc.name, func(t *testing.T) {
			res := tool.Execute(context.Background(), map[string]any{"path": tc.path})
			body := readFileResultText(t, res)

			if tc.wantDenied {
				require.True(t, res.IsError,
					"read_file must be denied for %s, got success: %s", tc.path, body)
				// Guarded: assert.NotContains against "" is vacuously false, so
				// the directory case (which has no content marker) would fail
				// on an assertion that never had anything to check.
				if tc.marker != "" {
					assert.NotContains(t, body, tc.marker, "secret content leaked into the tool result")
				}
				return
			}

			require.False(t, res.IsError,
				"control must stay readable — a deny that also blocks %s is over-broad, not protection: %s", tc.path, body)
			assert.Contains(t, body, tc.marker)
		})
	}
}

// TestSecretSet_SendFileResolutionCannotReachBackupsOrAuthJSON drives the exact
// resolver call send_file.go makes (FSOpSend via ResolvePathAllowingPatterns).
//
// This is the one that matters most: FSOpSend is documented as carrying no path
// restriction of its own, so nothing else in send_file's own body would stop
// the upload. Everything before this call in send_file.Execute is channel and
// media-store plumbing, so exercising the resolver directly tests the whole of
// the access decision without standing up a chat channel.
func TestSecretSet_SendFileResolutionCannotReachBackupsOrAuthJSON(t *testing.T) {
	home, agentHome := secretSetHome(t)

	ctx := context.Background()
	policy, err := ResolveTurnFSPolicy(ctx, agentHome, false)
	require.NoError(t, err)

	for _, tc := range secretSetTargets(home, agentHome) {
		t.Run(tc.name, func(t *testing.T) {
			handle, err := ResolvePathAllowingPatterns(ctx, policy, "send_file", "", FSOpSend, tc.path, nil)

			if tc.wantDenied {
				require.Error(t, err, "send_file must not resolve %s — that is a one-call vault upload", tc.path)
				require.ErrorIs(t, err, ErrCarveOut,
					"the refusal must come from the secret set, not incidentally from scope")
				assert.Nil(t, handle)
				return
			}

			require.NoError(t, err, "control must still resolve for send_file: %s", tc.path)
			defer handle.Close()
			data, readErr := handle.ReadFile()
			require.NoError(t, readErr)
			assert.Contains(t, string(data), tc.marker)
		})
	}
}

// TestSecretSet_OperatorAllowPatternCannotReopenBackupsOrAuthJSON covers the
// bypass an operator could plausibly configure by accident.
//
// ResolvePathAllowingPatterns forces FSScopeUnrestricted and adds the matched
// path to AllowedRoots when an AllowReadPaths regex matches. Neither of those
// can reopen a carve-out — ResolvePath checks IsCarveOut before it consults
// either — but that is a property worth asserting rather than inferring from
// two doc comments, because the whole point of the regex axis is to grant
// access the scope would otherwise refuse.
func TestSecretSet_OperatorAllowPatternCannotReopenBackupsOrAuthJSON(t *testing.T) {
	home, agentHome := secretSetHome(t)

	// A pattern an operator might genuinely write to let an agent fetch its
	// own artefacts out of the home directory.
	patterns := []*regexp.Regexp{regexp.MustCompile("^" + regexp.QuoteMeta(home) + "/.*")}

	ctx := context.Background()
	policy, err := ResolveTurnFSPolicy(ctx, agentHome, true)
	require.NoError(t, err)

	for _, tc := range secretSetTargets(home, agentHome) {
		t.Run(tc.name, func(t *testing.T) {
			handle, err := ResolvePathAllowingPatterns(ctx, policy, "read_file", "", FSOpRead, tc.path, patterns)

			if tc.wantDenied {
				require.Error(t, err, "an allow-list pattern must not reopen the secret set for %s", tc.path)
				require.ErrorIs(t, err, ErrCarveOut)
				return
			}

			require.NoError(t, err, "control must still resolve through the allow-list: %s", tc.path)
			handle.Close()
		})
	}
}

// readFileResultText flattens a ToolResult into everything an agent or the user
// could see. Both surfaces are concatenated deliberately: a leak into ForUser
// would be just as bad as one into ForLLM, and asserting on only one of them
// would let the other through.
func readFileResultText(t *testing.T, res *ToolResult) string {
	t.Helper()
	require.NotNil(t, res)

	var b strings.Builder
	b.WriteString(res.ForLLM)
	b.WriteString("\n")
	b.WriteString(res.ForUser)
	if res.Err != nil {
		b.WriteString("\n")
		b.WriteString(res.Err.Error())
	}
	return b.String()
}
