package catalog

// served.go — the pre-serialised GET body and its ETag (T067-04, FR-017).
//
// The GET /api/v1/providers/catalog response body is serialised ONCE, at
// apply time, into the ProvidersCatalog.yaml envelope: the full 2.0.0
// document (locality included, FR-039) plus the gateway serving markers
// served_from and stale. The bytes and their quoted strong SHA-256 ETag
// live on the same immutable snapshot the resolver reads, so the pair is
// swapped atomically as one unit — a reader can never observe the bytes of
// one apply with the ETag of another (T34c). The REST handler (T067-10)
// writes these bytes verbatim; there is no per-request marshal (SC-011).
//
// The envelope structs below mirror contracts/components/schemas/
// ProvidersCatalog.yaml / CatalogProvider.yaml / CatalogModel.yaml
// field-for-field. They are private marshal shapes, not a parallel wire
// type: this package must not import pkg/api/generated (spec §3), and the
// generated ProvidersCatalog type stays the only cross-boundary type the
// gateway and SPA consume; the contract test T30 validates the shape.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ServedFrom is the gateway envelope's origin marker (FR-017): which
// transport produced the served document.
type ServedFrom string

// The two origins.
const (
	// ServedEmbedded — the committed build-time snapshot.
	ServedEmbedded ServedFrom = "embedded"
	// ServedPulled — a release fetched at startup or by the 24 h refresh,
	// including a persisted last-known-good read back at boot (E6).
	ServedPulled ServedFrom = "pulled"
)

// StaleAfter is the FR-017/FR-037 staleness horizon: a served document
// whose updated_at is older than this is flagged stale on the envelope and
// degrades /health.
const StaleAfter = 14 * 24 * time.Hour

// ServedCatalog is the atomically-published serving pair plus its envelope
// markers. Body and ETag always belong to the same apply. Callers MUST
// treat Body as read-only — it is shared with every other reader.
type ServedCatalog struct {
	// Body is the pre-serialised ProvidersCatalog envelope.
	Body []byte
	// ETag is the quoted strong SHA-256 of Body, e.g. `"9f86d0…"` —
	// exactly what the ETag response header carries (FR-017).
	ETag string
	// From is the served_from marker baked into Body.
	From ServedFrom
	// Stale is the stale flag baked into Body (updated_at older than
	// StaleAfter at apply time).
	Stale bool
}

// servedPair is the snapshot-internal form of ServedCatalog.
type servedPair struct {
	body  []byte
	etag  string
	from  ServedFrom
	stale bool
}

// Served returns the pre-serialised body + ETag pair for the currently
// applied document. ok is false when no document is loaded (E7) — the
// handler answers 503, never an empty 200.
func (c *Catalog) Served() (ServedCatalog, bool) {
	s := c.cur.Load()
	if s == nil {
		return ServedCatalog{}, false
	}
	return ServedCatalog{
		Body:  s.served.body,
		ETag:  s.served.etag,
		From:  s.served.from,
		Stale: s.served.stale,
	}, true
}

// Version returns the served document's version, or the zero Version when
// no document is loaded.
func (c *Catalog) Version() Version {
	s := c.cur.Load()
	if s == nil {
		return Version{}
	}
	return s.doc.Version
}

// Degraded reports the catalog state /health surfaces (FR-037): degraded
// with the explaining error when no document is loaded (E7), when the last
// refresh attempt failed, when the served document was pulled over the
// degraded raw-fallback transport (US-3.AC8), or when the served document
// is stale (updated_at older than StaleAfter).
func (c *Catalog) Degraded() (bool, error) {
	s := c.cur.Load()
	if s == nil {
		return true, errors.New("catalog: no document loaded")
	}
	c.stateMu.Lock()
	refreshErr := c.lastRefreshErr
	transportDegraded := c.degradedTransport
	releaseErr := c.degradedReleaseErr
	c.stateMu.Unlock()
	if refreshErr != nil {
		return true, refreshErr
	}
	if transportDegraded {
		if releaseErr == nil {
			releaseErr = errors.New("catalog: served document was pulled over the raw fallback transport")
		}
		return true, releaseErr
	}
	if age := c.now().Sub(s.doc.UpdatedAt); age > StaleAfter {
		return true, fmt.Errorf("catalog: served document is stale: updated_at %s is %s old",
			s.doc.UpdatedAt.Format(time.RFC3339), age.Round(time.Hour))
	}
	return false, nil
}

// ── envelope marshal shapes (ProvidersCatalog.yaml, private) ────────────────

type envelopeJSON struct {
	SchemaVersion       string           `json:"schema_version"`
	Version             string           `json:"version"`
	UpdatedAt           time.Time        `json:"updated_at"`
	Source              string           `json:"source"`
	DefaultResizeLimits resizeLimitsJSON `json:"default_resize_limits"`
	Providers           []providerJSON   `json:"providers"`
	ServedFrom          ServedFrom       `json:"served_from"`
	Stale               bool             `json:"stale"`
}

type resizeLimitsJSON struct {
	LongEdgePx int   `json:"long_edge_px"`
	MaxBytes   int64 `json:"max_bytes"`
}

type endpointJSON struct {
	Protocol Protocol `json:"protocol"`
	API      string   `json:"api"`
}

type providerJSON struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Company           string           `json:"company"`
	API               string           `json:"api"`
	Protocol          Protocol         `json:"protocol,omitempty"`
	Protocols         []endpointJSON   `json:"protocols,omitempty"`
	Env               []string         `json:"env,omitempty"`
	Region            string           `json:"region,omitempty"`
	Plan              string           `json:"plan,omitempty"`
	Tier              Tier             `json:"tier"`
	UnsupportedReason string           `json:"unsupported_reason,omitempty"`
	AuthMethods       []AuthMethod     `json:"auth_methods"`
	Aliases           []string         `json:"aliases"`
	Locality          Locality         `json:"locality"`
	CLIKind           string           `json:"cli_kind,omitempty"`
	TokenSource       string           `json:"token_source,omitempty"`
	ResizeLimits      resizeLimitsJSON `json:"resize_limits"`
	Models            []modelJSON      `json:"models"`
}

type modelJSON struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	ReleaseDate     string     `json:"release_date,omitempty"`
	ContextWindow   int        `json:"context_window"`
	MaxOutputTokens int        `json:"max_output_tokens"`
	InputModalities []Modality `json:"input_modalities"`
	ToolCall        bool       `json:"tool_call"`
	Status          Status     `json:"status"`
	Disputed        bool       `json:"disputed,omitempty"`
}

// buildServed serialises doc into the envelope with the given origin
// marker, computing stale against now, and returns the body + quoted
// strong ETag as one pair.
func buildServed(doc *Document, from ServedFrom, now time.Time) (servedPair, error) {
	env := envelopeJSON{
		SchemaVersion:       doc.SchemaVersion,
		Version:             doc.Version.String(),
		UpdatedAt:           doc.UpdatedAt,
		Source:              doc.Source,
		DefaultResizeLimits: resizeLimitsJSON(doc.DefaultResizeLimits),
		Providers:           make([]providerJSON, 0, len(doc.Providers)),
		ServedFrom:          from,
		Stale:               now.Sub(doc.UpdatedAt) > StaleAfter,
	}
	for i := range doc.Providers {
		env.Providers = append(env.Providers, providerToJSON(&doc.Providers[i]))
	}
	body, err := json.Marshal(env)
	if err != nil {
		return servedPair{}, fmt.Errorf("catalog: serialise served envelope: %w", err)
	}
	sum := sha256.Sum256(body)
	return servedPair{
		body:  body,
		etag:  `"` + hex.EncodeToString(sum[:]) + `"`,
		from:  from,
		stale: env.Stale,
	}, nil
}

func providerToJSON(p *Provider) providerJSON {
	out := providerJSON{
		ID:                p.ID,
		Name:              p.Name,
		Company:           p.Company,
		API:               p.API,
		Protocol:          p.Protocol,
		Env:               p.Env,
		Region:            p.Region,
		Plan:              p.Plan,
		Tier:              p.Tier,
		UnsupportedReason: p.UnsupportedReason,
		AuthMethods:       p.AuthMethods,
		Aliases:           p.Aliases,
		Locality:          p.Locality,
		CLIKind:           p.CLIKind,
		TokenSource:       p.TokenSource,
		ResizeLimits:      resizeLimitsJSON(p.ResizeLimits),
		Models:            make([]modelJSON, 0, len(p.Models)),
	}
	// Required arrays serialise as [], never null.
	if out.AuthMethods == nil {
		out.AuthMethods = []AuthMethod{}
	}
	if out.Aliases == nil {
		out.Aliases = []string{}
	}
	for i := range p.Models {
		m := &p.Models[i]
		out.Models = append(out.Models, modelJSON{
			ID:              m.ID,
			Name:            m.Name,
			ReleaseDate:     m.ReleaseDate,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
			InputModalities: m.InputModalities,
			ToolCall:        m.ToolCall,
			Status:          m.Status,
			Disputed:        m.Disputed,
		})
	}
	if len(p.Protocols) > 0 {
		out.Protocols = make([]endpointJSON, 0, len(p.Protocols))
		for _, e := range p.Protocols {
			out.Protocols = append(out.Protocols, endpointJSON(e))
		}
	}
	return out
}
