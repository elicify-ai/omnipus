package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

// ErrInvalid is wrapped by every ParseDocument rejection (reason=invalid in
// the refresh log). The message names the offending JSON path.
var ErrInvalid = errors.New("catalog: invalid document")

// ErrSchemaVersion is additionally wrapped when the rejection is the
// schema_version gate (FR-001), so the refresh path can log
// reason=schema_version distinctly.
var ErrSchemaVersion = errors.New("catalog: unsupported schema_version")

// versionRe is FR-002's version rule: vYYYY.M.D[.N] with the leading v so
// Version.Compare orders numerically (F-01).
var versionRe = regexp.MustCompile(`^v\d{4}\.\d{1,2}\.\d{1,2}(\.\d+)?$`)

// The unsupported_reason vocabulary (F-16, Unasked Q3).
const (
	UnsupportedCloudIAM      = "cloud-iam"
	UnsupportedDeploymentURL = "deployment-url"
	UnsupportedWithdrawn     = "withdrawn"
)

// The cli_kind vocabulary (X-14).
const (
	CLIKindCodex   = "codex"
	CLIKindCopilot = "copilot"
)

// --- wire DTOs (the 2.0.0 document as published; never exported) ---

type documentDTO struct {
	SchemaVersion       string           `json:"schema_version"`
	Version             string           `json:"version"`
	UpdatedAt           string           `json:"updated_at"`
	Source              string           `json:"source"`
	DefaultResizeLimits *resizeLimitsDTO `json:"default_resize_limits"`
	Providers           []providerDTO    `json:"providers"`
}

type resizeLimitsDTO struct {
	LongEdgePx int   `json:"long_edge_px"`
	MaxBytes   int64 `json:"max_bytes"`
}

type endpointDTO struct {
	Protocol string `json:"protocol"`
	API      string `json:"api"`
}

type providerDTO struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Company           string           `json:"company"`
	API               string           `json:"api"`
	Protocol          string           `json:"protocol"`
	Protocols         []endpointDTO    `json:"protocols"`
	Env               []string         `json:"env"`
	Region            string           `json:"region"`
	Plan              string           `json:"plan"`
	Tier              string           `json:"tier"`
	UnsupportedReason string           `json:"unsupported_reason"`
	AuthMethods       []string         `json:"auth_methods"`
	Aliases           []string         `json:"aliases"`
	CLIKind           string           `json:"cli_kind"`
	TokenSource       string           `json:"token_source"`
	ResizeLimits      *resizeLimitsDTO `json:"resize_limits"`
	Models            []modelDTO       `json:"models"`
	// locality, if published, is ignored: it is derived on load (FR-039).
}

type modelDTO struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	ReleaseDate     string   `json:"release_date"`
	ContextWindow   int      `json:"context_window"`
	MaxOutputTokens int      `json:"max_output_tokens"`
	InputModalities []string `json:"input_modalities"`
	ToolCall        bool     `json:"tool_call"`
	Status          string   `json:"status"`
	Disputed        bool     `json:"disputed"`
}

// invalid builds a rejection naming path.
func invalid(path, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, path, fmt.Sprintf(format, args...))
}

// ParseDocument decodes and validates a 2.0.0 document (FR-001, FR-002,
// FR-033) and derives locality on every provider (FR-039). It never
// touches any catalog state: callers (Catalog.Apply, the refresh
// transaction, the boot path) decide whether to swap the result in.
func ParseDocument(data []byte) (*Document, error) {
	var dto documentDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrInvalid, err)
	}
	if dto.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %q (want %q): %w",
			ErrInvalid, dto.SchemaVersion, SchemaVersion, ErrSchemaVersion)
	}
	if !versionRe.MatchString(dto.Version) {
		return nil, invalid("version", "%q must match vYYYY.M.D[.N]", dto.Version)
	}
	if dto.UpdatedAt == "" {
		return nil, invalid("updated_at", "must be non-empty")
	}
	updatedAt, err := time.Parse(time.RFC3339, dto.UpdatedAt)
	if err != nil {
		return nil, invalid("updated_at", "%q is not RFC 3339", dto.UpdatedAt)
	}
	if dto.Source == "" {
		return nil, invalid("source", "must be non-empty")
	}
	defaults, err := parseResizeLimits("default_resize_limits", dto.DefaultResizeLimits, nil)
	if err != nil {
		return nil, err
	}
	if len(dto.Providers) == 0 {
		return nil, invalid("providers", "must contain at least one provider")
	}

	doc := &Document{
		SchemaVersion:       dto.SchemaVersion,
		Version:             dto.Version,
		UpdatedAt:           updatedAt,
		Source:              dto.Source,
		DefaultResizeLimits: defaults,
		Providers:           make([]Provider, 0, len(dto.Providers)),
	}
	seenProviders := make(map[string]struct{}, len(dto.Providers))
	for i := range dto.Providers {
		path := "providers[" + strconv.Itoa(i) + "]"
		p, err := parseProvider(path, &dto.Providers[i], defaults)
		if err != nil {
			return nil, err
		}
		if _, dup := seenProviders[p.ID]; dup {
			return nil, invalid(path+".id", "duplicate provider id %q", p.ID)
		}
		seenProviders[p.ID] = struct{}{}
		doc.Providers = append(doc.Providers, p)
	}
	return doc, nil
}

func parseResizeLimits(path string, dto *resizeLimitsDTO, inherit *ResizeLimits) (ResizeLimits, error) {
	if dto == nil {
		if inherit != nil {
			return *inherit, nil
		}
		return ResizeLimits{}, invalid(path, "must be present")
	}
	if dto.LongEdgePx <= 0 {
		return ResizeLimits{}, invalid(path+".long_edge_px", "must be positive, got %d", dto.LongEdgePx)
	}
	if dto.MaxBytes <= 0 {
		return ResizeLimits{}, invalid(path+".max_bytes", "must be positive, got %d", dto.MaxBytes)
	}
	return ResizeLimits{LongEdgePx: dto.LongEdgePx, MaxBytes: dto.MaxBytes}, nil
}

func parseProtocol(s string) (Protocol, bool) {
	switch p := Protocol(s); p {
	case ProtocolOpenAICompatible, ProtocolAnthropic, ProtocolGoogle, ProtocolOllama, ProtocolCLI:
		return p, true
	}
	return "", false
}

func parseProvider(path string, dto *providerDTO, defaults ResizeLimits) (Provider, error) {
	if dto.ID == "" {
		return Provider{}, invalid(path+".id", "must be non-empty")
	}
	if dto.Name == "" {
		return Provider{}, invalid(path+".name", "must be non-empty")
	}

	tier := Tier(dto.Tier)
	switch tier {
	case TierPopular, TierStandard, TierUnsupported:
	default:
		return Provider{}, invalid(path+".tier", "%q is not one of popular|standard|unsupported", dto.Tier)
	}
	if tier == TierUnsupported {
		switch dto.UnsupportedReason {
		case UnsupportedCloudIAM, UnsupportedDeploymentURL, UnsupportedWithdrawn:
		default:
			return Provider{}, invalid(path+".unsupported_reason", "%q is required on an unsupported row (cloud-iam|deployment-url|withdrawn)", dto.UnsupportedReason)
		}
	}

	// protocol: empty only when tier unsupported (F-19).
	var protocol Protocol
	if dto.Protocol == "" {
		if tier != TierUnsupported {
			return Provider{}, invalid(path+".protocol", "must be non-empty unless tier is unsupported")
		}
	} else {
		p, ok := parseProtocol(dto.Protocol)
		if !ok {
			return Provider{}, invalid(path+".protocol", "%q is not one of openai-compatible|anthropic|google|ollama|cli", dto.Protocol)
		}
		protocol = p
	}

	// cli_kind: required iff protocol cli (X-14).
	if protocol == ProtocolCLI {
		switch dto.CLIKind {
		case CLIKindCodex, CLIKindCopilot:
		default:
			return Provider{}, invalid(path+".cli_kind", "%q is required on a cli row (codex|copilot)", dto.CLIKind)
		}
	} else if dto.CLIKind != "" {
		return Provider{}, invalid(path+".cli_kind", "only allowed when protocol is cli")
	}

	// Locality is derived before the URL rule because the rule's exceptions
	// are "locality = local" (FR-033, FR-039). Published rows are never custom.
	locality := DeriveLocality(dto.ID, protocol, false, dto.API)

	// api: empty only on unsupported or cli rows (nothing to dial).
	if dto.API == "" {
		if tier != TierUnsupported && protocol != ProtocolCLI {
			return Provider{}, invalid(path+".api", "must be non-empty unless tier is unsupported or protocol is cli")
		}
	} else if err := validateAPIURL(path+".api", dto.API, locality); err != nil {
		return Provider{}, err
	}

	// protocols[]: optional; when present contains the primary with the
	// same api; entries unique per protocol (F-19).
	var endpoints []Endpoint
	if len(dto.Protocols) > 0 {
		endpoints = make([]Endpoint, 0, len(dto.Protocols))
		seen := make(map[Protocol]struct{}, len(dto.Protocols))
		hasPrimary := false
		for i, e := range dto.Protocols {
			epath := path + ".protocols[" + strconv.Itoa(i) + "]"
			ep, ok := parseProtocol(e.Protocol)
			if !ok {
				return Provider{}, invalid(epath+".protocol", "%q is not a known protocol", e.Protocol)
			}
			if _, dup := seen[ep]; dup {
				return Provider{}, invalid(epath, "duplicate protocol %q", e.Protocol)
			}
			seen[ep] = struct{}{}
			if e.API == "" {
				return Provider{}, invalid(epath+".api", "must be non-empty")
			}
			if err := validateAPIURL(epath+".api", e.API, locality); err != nil {
				return Provider{}, err
			}
			if ep == protocol && e.API == dto.API {
				hasPrimary = true
			}
			endpoints = append(endpoints, Endpoint{Protocol: ep, API: e.API})
		}
		if !hasPrimary {
			return Provider{}, invalid(path+".protocols", "must contain the primary protocol %q with api %q", dto.Protocol, dto.API)
		}
	}

	if len(dto.AuthMethods) == 0 {
		return Provider{}, invalid(path+".auth_methods", "must contain at least one of api_key|sign_in")
	}
	auth := make([]AuthMethod, 0, len(dto.AuthMethods))
	for i, a := range dto.AuthMethods {
		switch m := AuthMethod(a); m {
		case AuthAPIKey, AuthSignIn:
			auth = append(auth, m)
		default:
			return Provider{}, invalid(path+".auth_methods["+strconv.Itoa(i)+"]", "%q is not one of api_key|sign_in", a)
		}
	}

	limits, err := parseResizeLimits(path+".resize_limits", dto.ResizeLimits, &defaults)
	if err != nil {
		return Provider{}, err
	}

	company := dto.Company
	if company == "" {
		company = dto.Name // X-10 default
	}

	out := Provider{
		ID:                dto.ID,
		Name:              dto.Name,
		Company:           company,
		API:               dto.API,
		Protocol:          protocol,
		Protocols:         endpoints,
		Env:               append([]string(nil), dto.Env...),
		Region:            dto.Region,
		Plan:              dto.Plan,
		Tier:              tier,
		UnsupportedReason: dto.UnsupportedReason,
		AuthMethods:       auth,
		Aliases:           append([]string(nil), dto.Aliases...),
		CLIKind:           dto.CLIKind,
		TokenSource:       dto.TokenSource,
		Locality:          locality,
		ResizeLimits:      limits,
		Models:            make([]Model, 0, len(dto.Models)),
	}

	seenModels := make(map[string]struct{}, len(dto.Models))
	for i := range dto.Models {
		mpath := path + ".models[" + strconv.Itoa(i) + "]"
		m, err := parseModel(mpath, &dto.Models[i])
		if err != nil {
			return Provider{}, err
		}
		if _, dup := seenModels[m.ID]; dup {
			return Provider{}, invalid(mpath+".id", "duplicate model id %q under provider %q", m.ID, dto.ID)
		}
		seenModels[m.ID] = struct{}{}
		out.Models = append(out.Models, m)
	}
	return out, nil
}

func parseModel(path string, dto *modelDTO) (Model, error) {
	if dto.ID == "" {
		return Model{}, invalid(path+".id", "must be non-empty")
	}
	if dto.Name == "" {
		return Model{}, invalid(path+".name", "must be non-empty")
	}
	if dto.ReleaseDate != "" {
		if _, err := time.Parse("2006-01-02", dto.ReleaseDate); err != nil {
			return Model{}, invalid(path+".release_date", "%q is not YYYY-MM-DD", dto.ReleaseDate)
		}
	}
	if dto.ContextWindow < 0 {
		return Model{}, invalid(path+".context_window", "must be >= 0 (0 = unknown), got %d", dto.ContextWindow)
	}
	if dto.MaxOutputTokens < 0 {
		return Model{}, invalid(path+".max_output_tokens", "must be >= 0 (0 = unknown), got %d", dto.MaxOutputTokens)
	}
	hasText := false
	mods := make([]Modality, 0, len(dto.InputModalities))
	for i, s := range dto.InputModalities {
		if s == "" {
			return Model{}, invalid(path+".input_modalities["+strconv.Itoa(i)+"]", "must be non-empty")
		}
		if Modality(s) == ModalityText {
			hasText = true
		}
		mods = append(mods, Modality(s))
	}
	if !hasText {
		return Model{}, invalid(path+".input_modalities", "must include text")
	}
	status := Status(dto.Status)
	switch status {
	case StatusActive, StatusRetired:
	default:
		return Model{}, invalid(path+".status", "%q is not one of active|retired", dto.Status)
	}
	return Model{
		ID:              dto.ID,
		Name:            dto.Name,
		ReleaseDate:     dto.ReleaseDate,
		ContextWindow:   dto.ContextWindow,
		MaxOutputTokens: dto.MaxOutputTokens,
		InputModalities: mods,
		ToolCall:        dto.ToolCall,
		Status:          status,
		Disputed:        dto.Disputed,
	}, nil
}

// validateAPIURL is FR-033: an absolute URL with a non-empty host, no
// userinfo, no query, no fragment. Hosted rows (locality cloud) must be
// https on a host that is not loopback/link-local/private/ULA/metadata;
// local rows MAY use http and local hosts.
func validateAPIURL(path, raw string, locality Locality) error {
	u, err := url.Parse(raw)
	if err != nil {
		return invalid(path, "%q is not a URL: %v", raw, err)
	}
	if u.Hostname() == "" {
		return invalid(path, "%q must be an absolute URL with a host", raw)
	}
	if u.User != nil {
		return invalid(path, "%q must not carry userinfo", raw)
	}
	if u.RawQuery != "" || u.ForceQuery {
		return invalid(path, "%q must not carry a query", raw)
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return invalid(path, "%q must not carry a fragment", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if locality != LocalityLocal {
			return invalid(path, "%q must use https on a hosted provider", raw)
		}
	default:
		return invalid(path, "%q must use https (or http on a local endpoint), got scheme %q", raw, u.Scheme)
	}
	if locality != LocalityLocal && isLocalHost(u.Hostname()) {
		return invalid(path, "%q must not point at a loopback, link-local, private, ULA or metadata host on a hosted provider", raw)
	}
	return nil
}
