//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

// Omnipus — ADR-068 D16.2a / spec FR-020h: the SQLite-capable half of the
// platform gate.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

// PropertyIndexAvailable reports whether the derived SQLite properties index
// (ADR-068 D16.2) can exist in this binary. True here.
//
// The build constraint above is the exact complement of the one in
// propindex_stub_unavailable.go, so exactly one of the two files compiles.
//
// Read the constraint term by term:
//
//	!mipsle, !netbsd, !(freebsd && arm)
//	    the three targets where modernc.org/sqlite cannot build. Same triple as
//	    pkg/channels/whatsapp_native/whatsapp_native.go:1 and
//	    pkg/gateway/channel_matrix.go:1, which is not a coincidence — all three
//	    gates exist for the same dependency.
//
//	!records_no_sqlite
//	    a FORCING tag, not a product tag. No Makefile target sets it and no
//	    release artefact is built with it. It exists so the refusal path can be
//	    COMPILED AND RUN on an ordinary developer machine and in CI on any
//	    platform — a mipsle binary cannot be executed on darwin/arm64 or on a CI
//	    runner, so without it the degradation would be verifiable only by
//	    cross-compiling, i.e. never actually executed. Run the refusal side with:
//	        go test -tags goolm,stdjson,records_no_sqlite ./pkg/records/
//
// NOTE the tag that is deliberately ABSENT: `lite`. -tags lite KEEPS records
// (ADR-068 D16.2a consequence 1) — it drops whatsmeow only, while Matrix, which
// is not lite-gated, keeps SQLite linked. Adding `lite` here would remove a
// working capability from the lite builds for no reason. See the header of
// propindex_stub.go.
const PropertyIndexAvailable = true
