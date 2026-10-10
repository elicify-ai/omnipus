package memory

import "errors"

// WindowState is internal context-view persistence, never a gateway wire type.
// AnchorLine addresses the original user message when it lies before Skip.
type WindowState struct {
	Skip       int
	Count      int
	AnchorLine *int
	Projection ProjectionMeta
}

func (w WindowState) Clone() WindowState {
	out := w
	if w.AnchorLine != nil {
		v := *w.AnchorLine
		out.AnchorLine = &v
	}
	out.Projection.Entries = w.Projection.Entries.Clone()
	out.Projection.SourceRunes = make(map[ProjectionKey]int, len(w.Projection.SourceRunes))
	for k, v := range w.Projection.SourceRunes {
		out.Projection.SourceRunes[k] = v
	}
	out.Projection.TranscriptAddr = make(map[ProjectionKey]RecordAddress, len(w.Projection.TranscriptAddr))
	for k, v := range w.Projection.TranscriptAddr {
		out.Projection.TranscriptAddr[k] = v
	}
	return out
}

// ArchiveSpan is a half-open [Start, End) range of physical archive-line
// indices that is RETAINED on disk but EXCLUDED from the model view by an
// append-only rollback effect (session-core FR-006 / DEL-12). The bytes stay in
// the archive — ReadArchive/recall still return them — while every provider
// request built from WindowHistory omits them. Because the archive is strictly
// append-only and never renumbered, a span address stays exact for the
// session's whole lifetime, which is why spans are never re-derived or shifted.
type ArchiveSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// ErrWindowChanged is returned when a compare-and-set checkpoint finds the
// window moved while the checkpoint was being staged.
var ErrWindowChanged = errors.New("memory: context window changed while staging checkpoint")
