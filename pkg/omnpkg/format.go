package omnpkg

import "time"

// Spec-fixed format identity and limits (FR-002, FR-008).
const (
	FormatName          = "omnpkg"
	FormatVersion       = 1
	AgentSchemaVersion  = 1
	MaxDecodedBytes     = 256 << 20 // 256 MiB decoded stream (headers and padding included)
	MaxEntries          = 10000
	MaxManifestBytes    = 1 << 20 // 1 MiB
	MaxAgentFileBytes   = 1 << 20 // 1 MiB per agent-owned file
	MaxDecoderWindow    = 8 << 20 // 8 MiB zstd decoder max window (also the writer's window)
	MaxDecoderMemory    = 256 << 20
	MaxStagedImports    = 5
	StagingReadyTTL     = 30 * time.Minute
	MaxAgentNameLen     = 100
	ManifestEntry       = "manifest.json"
	AgentsPrefix        = "omnipus/agents/"
	AgentRecordName     = "agent.json"
	AgentSoulName       = "SOUL.md"
	AgentInstructionsNm = "AGENT.md"
)

// Element types and scopes written by S1.
const (
	ElementTypeAgent = "agent"
	ScopeCustom      = "custom"
)

// Manifest mirrors contracts/components/schemas/PackageManifest.yaml as written
// into the package's first entry (unknown manifest fields are ignored, FR-004).
//
// The package file is not a gateway/SPA boundary type; the
// preview carries the manifest through the generated PackageManifest type.
type Manifest struct {
	Format           string              `json:"format"`
	FormatVersion    int                 `json:"format_version"`
	Scope            string              `json:"scope"`
	Elements         []ManifestElement   `json:"elements"`
	Counts           map[string]int      `json:"counts"`
	Files            map[string]FileInfo `json:"files"`
	SourceBuild      string              `json:"source_build"`
	CreatedAt        time.Time           `json:"created_at"`
	IncludesSessions bool                `json:"includes_sessions"`
	NeedsSecrets     []NeedsSecret       `json:"needs_secrets"`
}

// ManifestElement is one elements[] row.
type ManifestElement struct {
	Type     string   `json:"type"`
	Schema   int      `json:"schema"`
	SourceID string   `json:"source_id"`
	Name     string   `json:"name"`
	Files    []string `json:"files"`
}

// FileInfo is the per-file SHA-256 and size (FR-003).
type FileInfo struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// NeedsSecret is one needs_secrets[] row (name, used_by, kind).
type NeedsSecret struct {
	Name   string `json:"name"`
	UsedBy string `json:"used_by"`
	Kind   string `json:"kind"`
}

// agentRecordPath / agentSoulPath / agentInstructionsPath are the agent
// layout entries for one source ID (ADR D2).
func agentRecordPath(id string) string { return AgentsPrefix + id + "/" + AgentRecordName }
func agentSoulPath(id string) string   { return AgentsPrefix + id + "/" + AgentSoulName }
func agentInstructionsPath(id string) string {
	return AgentsPrefix + id + "/" + AgentInstructionsNm
}
