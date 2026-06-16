package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// BundleManifestFilename is the canonical on-disk filename for a bundle
// manifest. It lives at the root of an installable bundle directory.
//
// NAME chosen to match the v0.1.0 Workspaces concept (`.preview-doc/plugins.html`,
// the `omnipus-plugin.json` wire block) and ADR-019 FR-10 ("component-level-hybrid
// bundle manifest SHAPE").
const BundleManifestFilename = "omnipus-plugin.json"

// semverPattern validates a semantic version string (MAJOR.MINOR.PATCH with an
// optional pre-release / build suffix). The manifest version field MUST be a
// semver (ADR-019 NFR-7: the version field's full schema is pinned now so the
// later installer adds no format change).
var semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-.]+)?(?:\+[0-9A-Za-z-.]+)?$`)

// BundleManifest is the component-level-hybrid bundle manifest
// (ADR-019 FR-10.2, NFR-7; `.preview-doc/plugins.html` Part 2).
//
// WIRE-VS-ON-DISK: this is a PURELY ON-DISK file format
// (`omnipus-plugin.json`, shipped inside an installable bundle directory). It is
// never serialized across the gateway/SPA boundary — there is no REST/WS
// endpoint or persisted-state file the SPA reads that carries it (the installer
// and Marketplaces UI are DEFERRED per FR-10.2). It is therefore correctly a Go
// struct + validator and is NOT defined in contracts/ (Constraint #8 applies
// only to bytes crossing the gateway boundary). If/when an installer endpoint
// surfaces this to the SPA, that future work MUST add the schema to
// contracts/components/schemas/ at that time.
//
// A bundle is "component-level hybrid": it reuses the universal unit formats
// VERBATIM (SKILL.md, .mcp.json) so skills and MCP servers from ClawHub / Claude
// Code marketplaces drop straight in, while agents/channels/providers use
// Omnipus-native shapes. Component fields are RELATIVE PATHS inside the bundle
// directory, not inline definitions — the (deferred) installer fans each path
// out to the subsystem that already owns it.
//
// NFR-7 — every field below ships INERT in v0.1.0 (the installer is deferred)
// but has its complete schema/type PINNED NOW, so enabling the install behaviour
// later adds no format change.
type BundleManifest struct {
	// Name is the bundle's unique identifier (required). Same lexical rules as a
	// skill name: alphanumeric segments joined by single hyphens, <= 64 chars.
	Name string `json:"name"`

	// Version is the bundle's semantic version (required, semver MAJOR.MINOR.PATCH).
	Version string `json:"version"`

	// Signature is the detached bundle signature (optional, inert in v0.1.0).
	// Reserved for the deferred installer's signed/verified path
	// (`.preview-doc/decisions.html` — "signed/verified + explicit consent").
	Signature string `json:"signature,omitempty"`

	// Skills is the relative path to a directory of SKILL.md skills carried by
	// the bundle (optional). Reused VERBATIM — ecosystem-portable.
	Skills string `json:"skills,omitempty"`

	// MCPServers is the relative path to an .mcp.json file (optional). Reused
	// VERBATIM — ecosystem-portable.
	MCPServers string `json:"mcpServers,omitempty"`

	// Agents is the relative path to a directory of native agents
	// (AGENT.md + SOUL.md + HEARTBEAT.md) (optional).
	Agents string `json:"agents,omitempty"`

	// Channels is the relative path to a native channels.json that binds a
	// compiled-in channel (optional).
	Channels string `json:"channels,omitempty"`

	// Providers is the relative path to a native providers.json carrying
	// per-tool provider config (optional).
	Providers string `json:"providers,omitempty"`

	// Permissions declares the capabilities the bundle requests (optional,
	// inert in v0.1.0). Deny-by-default: an empty Permissions grants nothing.
	Permissions *BundlePermissions `json:"permissions,omitempty"`
}

// BundlePermissions declares the capabilities a bundle requests
// (`.preview-doc/plugins.html` — `"permissions": { "tools": [...], "egress": [...] }`).
// Inert in v0.1.0; pinned per NFR-7. Deny-by-default — the deferred installer
// grants only what is listed here, behind explicit consent.
type BundlePermissions struct {
	// Tools is the list of tool names the bundle's components may invoke.
	Tools []string `json:"tools,omitempty"`
	// Egress is the list of network hosts the bundle's executable parts
	// (MCP servers / sidecars) may reach.
	Egress []string `json:"egress,omitempty"`
}

// bundleNamePattern reuses the skill name rule: alphanumeric segments joined by
// single hyphens (lowercase or uppercase). namePattern from loader.go matches
// the same shape; aliased for an independent, manifest-specific error message.
var bundleNamePattern = namePattern

// Validate checks the manifest against the required shape, returning a joined
// error describing every problem (so a malformed manifest reports all its
// faults at once rather than one-at-a-time).
//
// Rejection rules:
//   - name: required, <= 64 chars, alphanumeric-with-hyphens.
//   - version: required, valid semver.
//   - component paths (skills/mcpServers/agents/channels/providers): when
//     present, MUST be bundle-local relative paths (no absolute paths, no
//     "..", no leading "/") — they are resolved inside the bundle directory.
//   - kind: a manifest MUST declare at least one component
//     (skills/mcpServers/agents/channels/providers); a manifest with no
//     components has no recognizable bundle kind and is rejected.
func (m *BundleManifest) Validate() error {
	var errs error

	// name
	switch {
	case strings.TrimSpace(m.Name) == "":
		errs = errors.Join(errs, errors.New("name is required"))
	case len(m.Name) > MaxNameLength:
		errs = errors.Join(errs, fmt.Errorf("name exceeds %d characters", MaxNameLength))
	case !bundleNamePattern.MatchString(m.Name):
		errs = errors.Join(errs, errors.New("name must be alphanumeric with hyphens"))
	}

	// version
	switch {
	case strings.TrimSpace(m.Version) == "":
		errs = errors.Join(errs, errors.New("version is required"))
	case !semverPattern.MatchString(m.Version):
		errs = errors.Join(errs, fmt.Errorf("version %q is not a valid semantic version", m.Version))
	}

	// component paths must be bundle-local relative paths
	for field, p := range map[string]string{
		"skills":     m.Skills,
		"mcpServers": m.MCPServers,
		"agents":     m.Agents,
		"channels":   m.Channels,
		"providers":  m.Providers,
	} {
		if p == "" {
			continue
		}
		if err := validateBundlePath(field, p); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	// kind: at least one component must be declared, otherwise the bundle has
	// no recognizable kind (skill-pack / agent-pack / plugin-pack).
	if m.Kind() == BundleKindUnknown {
		errs = errors.Join(errs, errors.New("manifest declares no components: a bundle must carry at least one of skills, mcpServers, agents, channels, or providers"))
	}

	return errs
}

// BundleKind is the derived classification of a bundle, computed from which
// component fields are present. The concept (`.preview-doc/plugins.html`) does
// NOT carry an explicit "kind" field on the manifest — a bundle's kind is
// implied by its components (a skill-only bundle is a skill pack; an
// agent-bearing bundle is an agent pack; anything mixing executable extensions
// is a plugin pack). Kind() makes that classification explicit without
// inventing a schema field.
type BundleKind string

const (
	// BundleKindUnknown is an empty/component-less manifest (rejected by Validate).
	BundleKindUnknown BundleKind = ""
	// BundleKindSkill is a bundle that carries only skills.
	BundleKindSkill BundleKind = "skill-pack"
	// BundleKindAgent is a bundle that carries agents (an agent pack).
	BundleKindAgent BundleKind = "agent-pack"
	// BundleKindPlugin is a bundle that carries executable extensions
	// (MCP servers / channels / providers) — the full plugin pack.
	BundleKindPlugin BundleKind = "plugin-pack"
)

// Kind returns the derived classification of the bundle.
//
// Precedence: a bundle that declares any executable extension (mcpServers,
// channels, providers) is a plugin-pack; otherwise a bundle that declares
// agents is an agent-pack; otherwise a skills-only bundle is a skill-pack; a
// bundle with no components is unknown.
func (m *BundleManifest) Kind() BundleKind {
	if m.MCPServers != "" || m.Channels != "" || m.Providers != "" {
		return BundleKindPlugin
	}
	if m.Agents != "" {
		return BundleKindAgent
	}
	if m.Skills != "" {
		return BundleKindSkill
	}
	return BundleKindUnknown
}

// validateBundlePath rejects a component path that escapes the bundle directory.
// Component paths are resolved RELATIVE to the bundle root; absolute paths,
// parent-directory traversal ("..") and rooted paths are forbidden.
func validateBundlePath(field, p string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("%s path %q must be relative to the bundle directory", field, p)
	}
	// filepath.IsLocal rejects "..", absolute paths, and (on Windows) reserved
	// names — exactly the bundle-escape cases we must forbid.
	if !filepath.IsLocal(filepath.Clean(p)) {
		return fmt.Errorf("%s path %q must stay within the bundle directory (no \"..\" or absolute paths)", field, p)
	}
	return nil
}

// ParseBundleManifest parses raw manifest bytes and validates the result.
// It rejects malformed JSON, unknown manifest fields (forward-incompatible
// bundles are caught early rather than silently ignored), and any manifest
// that fails Validate.
func ParseBundleManifest(data []byte) (*BundleManifest, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()

	var m BundleManifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse bundle manifest: %w", err)
	}
	// Reject trailing content after the JSON object.
	if dec.More() {
		return nil, errors.New("parse bundle manifest: unexpected trailing data after manifest object")
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("invalid bundle manifest: %w", err)
	}
	return &m, nil
}

// LoadBundleManifest reads and validates the manifest from a bundle directory.
// bundleDir is the root of an installable bundle; the manifest is expected at
// <bundleDir>/omnipus-plugin.json. A missing or malformed manifest is rejected
// with a clear error — this is the gate the (deferred) installer wires in so a
// bundle with a bad/missing manifest fails cleanly before any component is
// fanned out to its subsystem.
func LoadBundleManifest(bundleDir string) (*BundleManifest, error) {
	manifestPath := filepath.Join(bundleDir, BundleManifestFilename)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("bundle manifest %s not found in %q", BundleManifestFilename, bundleDir)
		}
		return nil, fmt.Errorf("read bundle manifest: %w", err)
	}
	m, err := ParseBundleManifest(data)
	if err != nil {
		return nil, err
	}
	return m, nil
}
