package tools

// ingest_bound.go — ADR-066 D10 (FR-038): every network source read by a
// search provider is bounded at `ingest_bound_bytes`. Exceeding the bound is
// a tool FAILURE naming the bound — never a silent truncation. The bound is
// the setting's value (default config.DefaultIngestBoundBytes, 8,000,000);
// the 8,388,608 ceiling is validated at the settings write (T066-17).

import (
	"fmt"
	"io"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// IngestBoundError is returned when a single upstream response exceeds the
// ingest bound. Callers match it with errors.As; the message names the bound
// and the setting so the failure is actionable from the tool result alone.
type IngestBoundError struct {
	Source string
	Bound  int64
}

func (e *IngestBoundError) Error() string {
	return fmt.Sprintf("%s response exceeded the ingest bound of %d bytes (ingest_bound_bytes); "+
		"the response was rejected, not truncated", e.Source, e.Bound)
}

// effectiveIngestBound maps an unset (≤ 0) per-provider bound to the
// config default so a provider constructed without options is still bounded.
func effectiveIngestBound(bound int64) int64 {
	if bound <= 0 {
		return int64(config.DefaultIngestBoundBytes)
	}
	return bound
}

// readIngestBounded reads r fully if it holds at most bound bytes; one byte
// more and it returns *IngestBoundError without buffering the remainder.
func readIngestBounded(r io.Reader, bound int64, source string) ([]byte, error) {
	bound = effectiveIngestBound(bound)
	body, err := io.ReadAll(io.LimitReader(r, bound+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > bound {
		return nil, &IngestBoundError{Source: source, Bound: bound}
	}
	return body, nil
}
