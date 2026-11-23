package gateway

// Test-owned raw fixtures for #1952. Native packages must own a prompt;
// external workers must not. Never sort or re-encode the alias member bytes.

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

const s1bAliasSoul = "Be brief."

func s1bAliasPackage(t *testing.T, raw []byte, native bool) []byte {
	t.Helper()
	manifest, parts := s1bFix3RecordParts(raw)
	if native {
		path := agentSoulPath(specSourceID)
		body := []byte(s1bAliasSoul)
		manifest.Elements[0].Files = append(manifest.Elements[0].Files, path)
		manifest.Files[path] = omnpkg.FileInfo{SHA256: sha256hex(body), Size: int64(len(body))}
		parts = append(parts, tarPart{name: path, body: body})
	}
	pkg := s1bArchive(t, manifest, parts)
	_, files := decodePackageBytes(t, pkg)
	require.Equal(t, raw, files[agentRecordPath(specSourceID)], "archive must preserve incoming alias spelling and member order")
	return pkg
}

type s1bAliasRuntime struct {
	name      string
	agentType config.AgentType
	kind      config.ExecutorKind
}

func s1bAliasRuntimes() []s1bAliasRuntime {
	return []s1bAliasRuntime{
		{"native_main", config.AgentTypeCustom, config.ExecutorKindNative},
		{"native_worker", config.AgentTypeWorker, config.ExecutorKindNative},
		{"external_worker", config.AgentTypeWorker, config.ExecutorKindExternalCLI},
	}
}

func s1bAliasRuntimeRecord(t *testing.T, runtime s1bAliasRuntime, members string) []byte {
	t.Helper()
	record := s1bWorkerRecord(false)
	record["type"] = string(runtime.agentType)
	record["subagents"] = map[string]any{"executor": map[string]any{"kind": string(runtime.kind), "cli": "codex"}}
	if members == "" {
		raw, err := json.Marshal(record)
		require.NoError(t, err)
		return raw
	}
	return s1bFix3RecordWithMembers(t, record, members)
}
