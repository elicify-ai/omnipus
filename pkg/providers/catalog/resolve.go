package catalog

// Handle is the answer to one Resolve(provider, model) question. It is a
// small value (no heap allocation, SC-011) that reads through to the
// immutable snapshot it was resolved from, so it stays valid across a
// later Apply.
//
// On a miss (Found() == false) every accessor returns the FR-004 default
// for its consumer: the media pipeline sees the optimistic modality set
// (text + image) and the catalog default resize limits; the agent loop sees
// window/output unknown (0) so the ADR-066 ladder continues.
type Handle struct {
	provider string
	model    string
	p        *Provider // nil when the provider id is unknown
	m        *Model    // nil on a miss
	budget   ResizeLimits
}

// Found reports whether the exact (provider, model) pair exists.
func (h Handle) Found() bool { return h.m != nil }

// ProviderID echoes the provider id that was asked for.
func (h Handle) ProviderID() string { return h.provider }

// ModelID echoes the model id that was asked for.
func (h Handle) ModelID() string { return h.model }

// Window is the context window in tokens; 0 means unknown (A-11).
func (h Handle) Window() int {
	if h.m == nil {
		return 0
	}
	return h.m.ContextWindow
}

// MaxOutput is the output-token limit; 0 means unknown (A-11).
func (h Handle) MaxOutput() int {
	if h.m == nil {
		return 0
	}
	return h.m.MaxOutputTokens
}

// Supports reports whether the model accepts the modality. A miss is
// optimistic for text and image only (US-4.AC3).
func (h Handle) Supports(mod Modality) bool {
	if h.m == nil {
		return mod == ModalityText || mod == ModalityImage
	}
	for _, m := range h.m.InputModalities {
		if m == mod {
			return true
		}
	}
	return false
}

// InputModalities returns a copy of the model's input modalities (the
// optimistic default on a miss). Mutating the result never reaches the
// catalog.
func (h Handle) InputModalities() []Modality {
	if h.m == nil {
		return []Modality{ModalityText, ModalityImage}
	}
	return append([]Modality(nil), h.m.InputModalities...)
}

// ToolCall reports whether the model supports tool calling; false on a
// miss.
func (h Handle) ToolCall() bool { return h.m != nil && h.m.ToolCall }

// Status is the model's lifecycle marker; StatusActive on a miss so a miss
// never reads as retired.
func (h Handle) Status() Status {
	if h.m == nil {
		return StatusActive
	}
	return h.m.Status
}

// Disputed reports the A-22 marker; false on a miss.
func (h Handle) Disputed() bool { return h.m != nil && h.m.Disputed }

// Budget is the resize budget the media pipeline must honour: the
// provider's own limits on a hit, the catalog default on a miss, the
// package default when no document is loaded.
func (h Handle) Budget() ResizeLimits { return h.budget }

// Locality is the provider's FR-039 classification; LocalityCloud when the
// provider id is unknown.
func (h Handle) Locality() Locality {
	if h.p == nil {
		return LocalityCloud
	}
	return h.p.Locality
}
