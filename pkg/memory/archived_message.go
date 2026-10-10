package memory

import "github.com/elicify-ai/omnipus/pkg/providers"

// ArchivedMessage is one model message as the archive returns it.
//
// It embeds providers.Message so that JSON serialization is flat:
//
//	{"role":"user","content":"hello","ts":1751234567}
//
// The ts field carries the Unix write timestamp. TS==0 means unknown/earlier
// and callers must NOT error on it. This type is internal state — it is NOT a
// gateway/SPA wire type and must not be added to contracts/openapi.yaml.
type ArchivedMessage struct {
	providers.Message
	TS int64 `json:"ts,omitempty"`
}

const (
	// maxLineSize is the largest encoded archive line the platform admits.
	maxLineSize = 10 * 1024 * 1024 // 10 MB

	// EncodedLineBound is the largest JSON-encoded ArchivedMessage line the
	// ADR-066 D4 choke point lets reach the archive: 0.8 x maxLineSize (FR-012,
	// US-3.AC6). The 20 % margin absorbs the per-line framing (timestamp, ids,
	// escapes) the cap cannot see.
	EncodedLineBound = maxLineSize * 4 / 5
)
