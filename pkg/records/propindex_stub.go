// Omnipus — ADR-068 D16.2a / spec FR-020h: platform posture for the derived
// properties index.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS — ADR-068 D16.2a, spec FR-020h
//
// The vault's typed record layer narrows candidates through a derived SQLite
// properties index (ADR-068 D16.2). modernc.org/sqlite does not compile
// everywhere, so on some targets that index cannot exist at all:
//
//	linux/mipsle (softfloat)  pkg/gateway/channel_matrix.go:20-22
//	netbsd/*                  pkg/gateway/channel_matrix.go:23-25
//	freebsd/arm               pkg/gateway/channel_matrix.go:26-28
//
// Of those, exactly ONE is a shipped target — linux/mipsle, the only Makefile
// target built with GO_BUILD_TAGS_NO_GOOLM (Makefile:210, :234), which ships as
// two binaries (omnipus-linux-mipsle and omnipus-lite-linux-mipsle). netbsd and
// freebsd/arm are not produced by `make build-all` at all; the exposure there is
// a person compiling from source.
//
// -tags lite KEEPS RECORDS. A reasonable reader assumes the opposite, because
// `lite` sounds like it drops the heavy dependency. It does not: `lite` drops
// whatsmeow only, Matrix is not lite-gated, and `make build-lite` builds with
// $(GO_BUILD_TAGS),lite = goolm,stdjson,lite (Makefile:205-213), so SQLite is
// still linked. That is why `lite` is deliberately ABSENT from the build
// constraints in propindex_stub_available.go / propindex_stub_unavailable.go,
// even though the precedent this file follows —
// pkg/channels/whatsapp_native/whatsapp_native_stub.go — does include it.
//
// THE POSTURE: on a target without SQLite the record layer MUST refuse BY NAME.
// It must not fail to build, and — this is the whole point — it must never
// return an EMPTY RESULT. An empty success is indistinguishable from "there is
// nothing there", which is the exact failure mode ADR-068 exists to remove; it
// would be worse arriving from the build system, because nothing in the vault or
// the query would explain it. Hard Constraint #4 (graceful degradation) is
// satisfied by degradation that is VISIBLE, not by degradation that is quiet.
//
// What keeps working on such a build: vault_read, and the plain-word half of
// vault_find, because both resolve through bleve. What refuses: typed filters,
// relation joins, grouping, aggregation, typed integrity checks and record-type
// declaration — every capability enumerated below.
//
// HOW TO USE IT: every entry point that needs the properties index calls
// RequirePropertyIndex with the capability it is about to perform, and returns
// the error unchanged if one comes back. On a SQLite-capable build the check is
// a compile-time-constant branch the compiler removes, so it costs nothing.
// ---------------------------------------------------------------------------

// PropertyIndexCapability names one thing the derived properties index makes
// possible. It exists so a refusal can say which capability the caller lost
// rather than a generic "unavailable" — an operator on linux/mipsle needs to
// know that grouping is gone and plain-word search is not.
type PropertyIndexCapability string

const (
	// CapabilityTypedFilter is a filter over a declared, typed property —
	// the candidate-narrowing select the properties index answers.
	CapabilityTypedFilter PropertyIndexCapability = "typed_filters"

	// CapabilityRelationJoin is reachability through the relation child
	// table (near / hops, inverse relations).
	CapabilityRelationJoin PropertyIndexCapability = "relation_joins"

	// CapabilityGrouping is grouping a result set by a property value.
	CapabilityGrouping PropertyIndexCapability = "grouping"

	// CapabilityAggregation is count/sum/min/max over a grouped result.
	CapabilityAggregation PropertyIndexCapability = "aggregation"

	// CapabilityIntegrityCheck is vault_describe's check_integrity sweep over
	// duplicate identifiers, relation targets and orphan index rows.
	CapabilityIntegrityCheck PropertyIndexCapability = "typed_integrity_checks"

	// CapabilityRecordTypeDeclaration is declaring a record type. It refuses
	// rather than writing a schema file that could never be enforced.
	CapabilityRecordTypeDeclaration PropertyIndexCapability = "record_type_declaration"

	// CapabilityOpenIndex is opening (or rebuilding) the store itself.
	CapabilityOpenIndex PropertyIndexCapability = "properties_index_open"
)

// PropertyIndexCapabilities is every capability that depends on the properties
// index, in a stable order. Anything added here without a refusal message in
// propertyIndexRefusals fails TestPropertyIndexRefusal_EveryCapabilityHasItsOwnMessage.
var PropertyIndexCapabilities = []PropertyIndexCapability{
	CapabilityTypedFilter,
	CapabilityRelationJoin,
	CapabilityGrouping,
	CapabilityAggregation,
	CapabilityIntegrityCheck,
	CapabilityRecordTypeDeclaration,
	CapabilityOpenIndex,
}

// propertyIndexRefusals holds one refusal message per capability. Each carries a
// single %s for the platform, and each states what STILL works — a refusal that
// only says "no" sends the reader looking for a fault that is not there.
//
// The wording of the first, fifth and sixth entries is verbatim from the
// implementing spec (docs/internal/specs/vault-records-spec-2026-08-25.md
// lines 1377, 1293 and 1622 respectively); do not paraphrase them without
// changing the spec.
var propertyIndexRefusals = map[PropertyIndexCapability]string{
	CapabilityTypedFilter: "typed filters are unavailable on %s: this build has no properties index. " +
		"Plain-word search and vault_read still work",
	CapabilityRelationJoin: "relation joins are unavailable on %s: this build has no properties index. " +
		"Plain-word search and vault_read still work",
	CapabilityGrouping: "grouping is unavailable on %s: this build has no properties index. " +
		"Plain-word search and vault_read still work",
	CapabilityAggregation: "aggregation is unavailable on %s: this build has no properties index. " +
		"Plain-word search and vault_read still work",
	CapabilityIntegrityCheck: "typed integrity checks are unavailable on %s: this build has no properties index. " +
		"Duplicate identifiers, relation targets and orphan rows cannot be checked here; " +
		"wikilink and orphan checks still run",
	CapabilityRecordTypeDeclaration: "record types cannot be declared on %s: this build has no properties index. " +
		"The schema file would be written and never enforced",
	CapabilityOpenIndex: "the vault properties index cannot be opened on %s: modernc.org/sqlite has no working " +
		"build for this target. Records are a feature of the SQLite-capable builds; " +
		"plain-word search and vault_read still work",
}

// ErrPropertyIndexUnavailable is the sentinel every platform refusal unwraps to,
// so a caller can branch on the class (errors.Is) without matching on message
// text and without importing the capability vocabulary.
var ErrPropertyIndexUnavailable = errors.New("the vault properties index is not compiled into this build")

// PropertyIndexUnavailableError is the refusal returned in place of a result on
// a build where the properties index cannot exist. It is an error and never an
// empty success — see the posture note at the top of this file.
type PropertyIndexUnavailableError struct {
	// Capability is what the caller was about to do.
	Capability PropertyIndexCapability
	// Platform is the GOOS/GOARCH pair the refusal names, e.g. "linux/mipsle".
	Platform string
}

// Error renders the capability-specific refusal, naming the platform.
func (e *PropertyIndexUnavailableError) Error() string {
	platform := e.Platform
	if platform == "" {
		platform = PropertyIndexPlatform()
	}
	if tmpl, ok := propertyIndexRefusals[e.Capability]; ok {
		return fmt.Sprintf(tmpl, platform)
	}
	// An unregistered capability still refuses, still names the platform and
	// still names itself. A test asserts the map covers every declared
	// capability, so this path is reachable only from a caller inventing a
	// capability string — and even then it must not degrade into silence.
	name := string(e.Capability)
	if name == "" {
		name = "this record-layer operation"
	}
	return fmt.Sprintf("%s is unavailable on %s: this build has no properties index. "+
		"Plain-word search and vault_read still work", name, platform)
}

// Unwrap ties every refusal to the sentinel.
func (e *PropertyIndexUnavailableError) Unwrap() error { return ErrPropertyIndexUnavailable }

// PropertyIndexPlatform is the platform name a refusal uses: GOOS/GOARCH, e.g.
// "linux/mipsle". It is the identifier the operator's own build command and
// release artefact use, which is what makes the refusal actionable.
func PropertyIndexPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// RequirePropertyIndex reports whether the caller may proceed with a capability
// that needs the derived properties index.
//
// It returns nil on every SQLite-capable build — the condition is a build-tagged
// constant, so the compiler removes the check entirely there. On a build where
// modernc.org/sqlite cannot compile it logs a WARN naming the platform (spec
// FR-049b, which requires exactly that on a SQLite-less refusal) and returns a
// *PropertyIndexUnavailableError.
//
// Callers MUST return the error rather than substituting a zero value: the
// contract this satisfies (FR-020h) is that the record layer refuses by name and
// never returns an empty result on such a build.
func RequirePropertyIndex(capability PropertyIndexCapability) error {
	if PropertyIndexAvailable {
		return nil
	}
	err := &PropertyIndexUnavailableError{
		Capability: capability,
		Platform:   PropertyIndexPlatform(),
	}
	slog.Warn("records: the properties index is not compiled into this build — refusing by name",
		"capability", string(capability),
		"platform", err.Platform,
		"reason", "modernc.org/sqlite has no working build for this target",
		"still_available", "plain-word search (bleve) and vault_read",
		"adr", "ADR-068 D16.2a",
	)
	return err
}
