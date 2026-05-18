//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/dapicom-ai/omnipus/pkg/gateway/inboundschemas"
)

// ── Inbound schema validator ──────────────────────────────────────────────────
//
// decodeAndValidate reads r.Body, optionally validates the raw JSON against the
// named OpenAPI component schema (from pkg/gateway/inboundschemas/), and then
// decodes the bytes into dst.
//
// The validation step is gated by gateway.validate_inbound (default false) so
// existing deployments are unaffected until an operator opts in. When disabled,
// decodeAndValidate behaves identically to json.NewDecoder(r.Body).Decode(dst).
//
// When enabled, a schema-invalid body is rejected with HTTP 400 and a
// descriptive error message before the body is ever decoded into dst.
//
// Usage — replace the standard decode pattern:
//
//	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
//	    jsonErr(w, http.StatusBadRequest, "invalid JSON body")
//	    return
//	}
//
// With:
//
//	if !decodeAndValidate(w, r, "AgentCreateRequest", &req, validateInboundEnabled) {
//	    return
//	}
//
// The bool parameter `validateEnabled` is read from cfg.Gateway.ValidateInbound
// at the call site.
//
// Returns true if the body was successfully decoded (and validated when enabled).
// On false the handler must return immediately — the HTTP error was already sent.

// inboundValidatorState holds the once-initialized schema compiler and the
// compiled schema map. Both fields are written once (under mu) and then
// read concurrently by all request goroutines.
var inboundValidatorState struct {
	mu       sync.Mutex
	once     sync.Once
	compiler *jsonschema.Compiler
	schemas  map[string]*jsonschema.Schema // keyed by schema name (e.g. "AgentCreateRequest")
	initErr  error
}

// inboundSchemaLoader is a jsonschema URLLoader that reads embedded YAML schema
// files from the inboundschemas.FS embed.FS. It handles the file:// URL scheme
// used internally by the jsonschema/v6 compiler for $ref resolution.
type inboundSchemaLoader struct{}

func (inboundSchemaLoader) Load(rawURL string) (any, error) {
	// Strip file:// prefix. The compiler builds URLs like:
	//   file:///AgentCreateRequest.yaml  (absolute, no real path)
	// We strip the scheme and leading slashes to get the bare filename.
	path := strings.TrimPrefix(rawURL, "file://")
	path = strings.TrimLeft(path, "/")

	data, err := inboundschemas.FS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("inboundSchemaLoader: read %s: %w", path, err)
	}

	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("inboundSchemaLoader: unmarshal %s: %w", path, err)
	}
	return doc, nil
}

// initInboundValidator initializes the singleton schema compiler from the
// embedded YAML schema files. Called once at first use (lazy init so gateway
// startup is not delayed when validate_inbound is false).
func initInboundValidator() (*jsonschema.Compiler, error) {
	inboundValidatorState.once.Do(func() {
		c := jsonschema.NewCompiler()
		c.UseLoader(jsonschema.SchemeURLLoader{
			"file": inboundSchemaLoader{},
		})
		inboundValidatorState.compiler = c
	})
	return inboundValidatorState.compiler, inboundValidatorState.initErr
}

// compileInboundSchema returns the compiled jsonschema.Schema for the named
// OpenAPI component schema file (without the .yaml extension). Compiled schemas
// are cached in inboundValidatorState.schemas after the first call.
func compileInboundSchema(name string) (*jsonschema.Schema, error) {
	inboundValidatorState.mu.Lock()
	defer inboundValidatorState.mu.Unlock()

	if inboundValidatorState.schemas == nil {
		inboundValidatorState.schemas = make(map[string]*jsonschema.Schema)
	}
	if s, ok := inboundValidatorState.schemas[name]; ok {
		return s, nil
	}

	c, err := initInboundValidator()
	if err != nil {
		return nil, fmt.Errorf("inbound validator: init failed: %w", err)
	}

	// The loader accepts URLs of the form file:///Filename.yaml (no real path —
	// the loader strips leading slashes and reads from the embed.FS directly).
	url := "file:///" + name + ".yaml"
	s, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("inbound validator: compile schema %q: %w", name, err)
	}
	inboundValidatorState.schemas[name] = s
	return s, nil
}

// validateBodyAgainstSchema validates rawJSON against the named component schema.
// Returns a human-readable error string on failure, or "" on success.
func validateBodyAgainstSchema(schemaName string, rawJSON []byte) string {
	s, err := compileInboundSchema(schemaName)
	if err != nil {
		// Schema compilation failure is a server-side misconfiguration — log and
		// skip validation rather than breaking all requests.
		slog.Warn("inbound validation: schema compile error — skipping validation",
			"schema", schemaName, "error", err)
		return ""
	}

	var doc any
	if err := json.Unmarshal(rawJSON, &doc); err != nil {
		// If JSON is invalid we still want to report it; the subsequent
		// json.Decoder call will produce the same error with a better message.
		return fmt.Sprintf("invalid JSON: %v", err)
	}

	if err := s.Validate(doc); err != nil {
		// Extract a concise first-error message. The error from jsonschema/v6
		// is a *jsonschema.ValidationError with a tree of failures.
		return formatSchemaError(err)
	}
	return ""
}

// formatSchemaError converts a jsonschema/v6 validation error to a short
// human-readable string suitable for a 400 response body.
func formatSchemaError(err error) string {
	if err == nil {
		return ""
	}
	// jsonschema/v6 ValidationError has a rich tree; use the top-level message
	// which already summarises the first failure nicely.
	msg := err.Error()
	// Trim verbose path prefixes like "jsonschema: '' does not validate..."
	// to leave just the actionable part.
	if idx := strings.Index(msg, ": "); idx >= 0 {
		msg = msg[idx+2:]
	}
	return msg
}

// decodeAndValidate reads and validates r.Body against schemaName, then decodes
// into dst. Returns true on success, false on any failure (error response
// already written to w).
//
//   - When validateEnabled is false: behaves like json.NewDecoder(r.Body).Decode(dst).
//   - When validateEnabled is true: reads body bytes, validates against schemaName,
//     then decodes into dst. A schema-invalid body is rejected with 400.
//
// The body is limited to 1 MB (matching withAuth's body limit).
func decodeAndValidate(w http.ResponseWriter, r *http.Request, schemaName string, dst any, validateEnabled bool) bool {
	lr := io.LimitReader(r.Body, 1<<20)

	if !validateEnabled {
		// Fast path: no validation, decode directly.
		if err := json.NewDecoder(lr).Decode(dst); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid JSON body")
			return false
		}
		return true
	}

	// Validation path: buffer the body so we can both validate and decode.
	raw, err := io.ReadAll(lr)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return false
	}

	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		jsonErr(w, http.StatusBadRequest, "request body is required")
		return false
	}

	if errMsg := validateBodyAgainstSchema(schemaName, raw); errMsg != "" {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("request body does not match schema %s: %s", schemaName, errMsg))
		return false
	}

	if err := json.Unmarshal(raw, dst); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}
