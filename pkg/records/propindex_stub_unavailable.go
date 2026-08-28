//go:build records_no_sqlite || mipsle || netbsd || (freebsd && arm)

// Omnipus — ADR-068 D16.2a / spec FR-020h: the SQLite-less half of the platform
// gate. This is the stub.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

// PropertyIndexAvailable reports whether the derived SQLite properties index
// (ADR-068 D16.2) can exist in this binary. False here — modernc.org/sqlite has
// no working build for this target, so there is no properties index to query.
//
// Setting it false is what turns RequirePropertyIndex (propindex_stub.go) into a
// refusal that NAMES THE PLATFORM. Nothing on this build returns an empty
// result in place of a typed answer: that is FR-020h's normative requirement and
// the reason this file exists rather than the record layer simply not compiling.
//
// The build constraint is the exact complement of propindex_stub_available.go's:
//
//	mipsle, netbsd, freebsd && arm
//	    modernc.org/sqlite cannot compile there
//	    (pkg/gateway/channel_matrix.go:20-28 documents each). Only linux/mipsle
//	    is a SHIPPED target (Makefile:210, :234 — GO_BUILD_TAGS_NO_GOOLM),
//	    producing omnipus-linux-mipsle and omnipus-lite-linux-mipsle. netbsd and
//	    freebsd/arm are source-buildable only.
//
//	records_no_sqlite
//	    the forcing tag that makes this path runnable in tests on any host. See
//	    propindex_stub_available.go for why it exists.
//
// `lite` is deliberately not in the constraint: -tags lite KEEPS records.
const PropertyIndexAvailable = false
