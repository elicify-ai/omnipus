package catalog

import "time"

// SchemaVersion is the only document schema this package loads (FR-001).
const SchemaVersion = "2.0.0"

// Protocol is the wire protocol a provider endpoint speaks. The factory
// dispatches on it (ADR-067 D11); vendor ids never appear in Go code.
type Protocol string

// The closed protocol set (FR-002).
const (
	ProtocolOpenAICompatible Protocol = "openai-compatible"
	ProtocolAnthropic        Protocol = "anthropic"
	ProtocolGoogle           Protocol = "google"
	ProtocolOllama           Protocol = "ollama"
	ProtocolCLI              Protocol = "cli"
)

// Tier is the picker tier a provider row carries (ADR-067 D12; data, never
// a Go list).
type Tier string

// The closed tier set (FR-002).
const (
	TierPopular     Tier = "popular"
	TierStandard    Tier = "standard"
	TierUnsupported Tier = "unsupported"
)

// AuthMethod is one way a provider can be authenticated (FR-030).
type AuthMethod string

// The closed auth-method set (FR-002).
const (
	AuthAPIKey AuthMethod = "api_key"
	AuthSignIn AuthMethod = "sign_in"
)

// Status is a model's lifecycle marker (A-3).
type Status string

// The closed status set (FR-002).
const (
	StatusActive  Status = "active"
	StatusRetired Status = "retired"
)

// Locality is the ONE classification of "local endpoint" every consumer
// (ADR-066, ADR-068) reads (FR-039, X-16/X-17). It is derived on load by
// DeriveLocality and never published by the assembly job.
type Locality string

// The two localities.
const (
	LocalityLocal Locality = "local"
	LocalityCloud Locality = "cloud"
)

// Modality is one input modality a model accepts. The document MAY carry
// values outside the known constants (forward-compat for new formats); they
// are recorded as-is and observable through Handle.Supports.
type Modality string

// Known modality constants — the runtime's recognition boundary, not a
// closed enum.
const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityPDF   Modality = "pdf"
	ModalityAudio Modality = "audio"
	ModalityVideo Modality = "video"
)

// ResizeLimits is the per-provider (or catalog-default) media resize budget
// consumed by the media pipeline (FR-004, A-10).
type ResizeLimits struct {
	// LongEdgePx is the max long-edge dimension after resize.
	LongEdgePx int `json:"long_edge_px"`
	// MaxBytes is the max file size after the quality ladder. int64 keeps
	// byte counts exact on 32-bit targets.
	MaxBytes int64 `json:"max_bytes"`
}

// DefaultResizeLimits is the budget served when no document is loaded at
// all (E7). A loaded document's own default_resize_limits wins over it.
var DefaultResizeLimits = ResizeLimits{LongEdgePx: 7680, MaxBytes: 10 << 20}

// Document is the validated 2.0.0 catalog (§5 "Document shape"). Every
// invariant of FR-002 and FR-033 holds on a Document returned by
// ParseDocument; Locality is populated on every Provider.
//
// Documents are immutable once built: Catalog hands out pointers into them,
// so callers MUST treat a Document and everything it owns as read-only.
type Document struct {
	SchemaVersion       string
	Version             Version
	UpdatedAt           time.Time
	Source              string
	DefaultResizeLimits ResizeLimits
	Providers           []Provider
}

// Endpoint is one (protocol, base URL) pair a provider offers (A-8).
type Endpoint struct {
	Protocol Protocol
	API      string
}

// Provider is one catalog provider row with its nested models.
type Provider struct {
	ID      string
	Name    string // display name (A-14)
	Company string // grouping key (X-10); defaults to Name
	// API is the primary base URL. Empty only on tier-unsupported rows.
	API       string
	Protocol  Protocol   // primary; empty only when Tier == TierUnsupported
	Protocols []Endpoint // optional; when present contains the primary
	Env       []string   // opaque picker hint (F-20); never consumed by the factory
	Region    string
	Plan      string
	Tier      Tier
	// UnsupportedReason is cloud-iam | deployment-url | withdrawn on
	// unsupported rows.
	UnsupportedReason string
	AuthMethods       []AuthMethod
	// Aliases are search-only strings for the picker's filter (A-9). They
	// never participate in resolution, validation, or construction.
	Aliases []string
	// CLIKind is codex | copilot on protocol cli rows (X-14).
	CLIKind string
	// TokenSource is e.g. codex-auth-json (X-41).
	TokenSource string
	// Custom marks an operator-typed row (FR-035). The published snapshot
	// never carries one; the flag exists so a custom row can be classified
	// by DeriveLocality and URL-checked for parseability only (F-03).
	Custom bool
	// Locality is derived on load (FR-039) — the field in the JSON, if any,
	// is ignored.
	Locality     Locality
	ResizeLimits ResizeLimits
	Models       []Model
}

// Model is one model row under a provider.
type Model struct {
	ID          string
	Name        string
	ReleaseDate string // YYYY-MM-DD, optional
	// ContextWindow and MaxOutputTokens are 0 when unknown (A-11); the
	// ADR-066 ladder decides what to do.
	ContextWindow   int
	MaxOutputTokens int
	InputModalities []Modality
	ToolCall        bool
	Status          Status
	// Disputed marks a row whose upstream registries disagreed beyond the
	// tolerance and the last-known-good value was kept (A-22).
	Disputed bool
}
