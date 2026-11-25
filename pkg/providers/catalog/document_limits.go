package catalog

// document_limits.go — the catalogue's inline-route PDF limits (issue #1968,
// spec §4.2). Every value is catalogue data: this file holds no limit
// number. A field is resolved per field through the cascade model row →
// provider row → catalogue default row; the first level that states the
// field wins, an explicit 0 states "no limit" and stops the cascade, and a
// field no level states means no client-side limit.

// limitField is one optional integer limit: set reports whether the level
// states it at all.
type limitField struct {
	value int64
	set   bool
}

// boolField is one optional boolean member.
type boolField struct {
	value bool
	set   bool
}

// DocumentLimits is one level's (or the resolved) set of PDF limits. Each
// accessor answers (value, stated):
//
//   - stated == false: not stated at any level consulted (no client-side
//     limit for the field);
//   - stated && value == 0: explicitly no limit;
//   - stated && value > 0: inclusive limit (a value equal to it is allowed).
//
// The zero value states nothing, so it answers (0, false) everywhere.
type DocumentLimits struct {
	inlinePDF              boolField
	maxFileBytes           limitField
	maxTotalFileBytes      limitField
	maxRequestBytes        limitField
	maxPagesPerFile        limitField
	maxPagesPerRequest     limitField
	maxDocumentsPerMessage limitField
	maxDocumentsPerRequest limitField
}

// InlinePDF reports whether the provider has an inline PDF route. Not
// stated means true at the consumer (spec §4.2); this accessor only reports
// what the data says.
func (d DocumentLimits) InlinePDF() (bool, bool) { return d.inlinePDF.value, d.inlinePDF.set }

// MaxFileBytes is the decoded size limit of one PDF.
func (d DocumentLimits) MaxFileBytes() (int64, bool) { return d.maxFileBytes.get() }

// MaxTotalFileBytes is the decoded size limit of all PDFs of one request.
func (d DocumentLimits) MaxTotalFileBytes() (int64, bool) { return d.maxTotalFileBytes.get() }

// MaxRequestBytes is the limit on the whole serialized request body.
func (d DocumentLimits) MaxRequestBytes() (int64, bool) { return d.maxRequestBytes.get() }

// MaxPagesPerFile is the page limit of one PDF.
func (d DocumentLimits) MaxPagesPerFile() (int64, bool) { return d.maxPagesPerFile.get() }

// MaxPagesPerRequest is the page limit of all PDFs of one request.
func (d DocumentLimits) MaxPagesPerRequest() (int64, bool) { return d.maxPagesPerRequest.get() }

// MaxDocumentsPerMessage is the PDF count limit of one wire message.
func (d DocumentLimits) MaxDocumentsPerMessage() (int64, bool) {
	return d.maxDocumentsPerMessage.get()
}

// MaxDocumentsPerRequest is the PDF count limit of one request.
func (d DocumentLimits) MaxDocumentsPerRequest() (int64, bool) {
	return d.maxDocumentsPerRequest.get()
}

// Empty reports whether no field is stated.
func (d DocumentLimits) Empty() bool { return d == DocumentLimits{} }

func (f limitField) get() (int64, bool) { return f.value, f.set }

// over returns d with every field d does not state taken from next — the
// per-field cascade step of spec §4.2.
func (d DocumentLimits) over(next DocumentLimits) DocumentLimits {
	if !d.inlinePDF.set {
		d.inlinePDF = next.inlinePDF
	}
	pick := func(own *limitField, fallback limitField) {
		if !own.set {
			*own = fallback
		}
	}
	pick(&d.maxFileBytes, next.maxFileBytes)
	pick(&d.maxTotalFileBytes, next.maxTotalFileBytes)
	pick(&d.maxRequestBytes, next.maxRequestBytes)
	pick(&d.maxPagesPerFile, next.maxPagesPerFile)
	pick(&d.maxPagesPerRequest, next.maxPagesPerRequest)
	pick(&d.maxDocumentsPerMessage, next.maxDocumentsPerMessage)
	pick(&d.maxDocumentsPerRequest, next.maxDocumentsPerRequest)
	return d
}

// DocumentLimits resolves the pair's PDF limits per field: the model row,
// then the provider row, then the catalogue default row (spec §4.2). With
// no document loaded, or nothing stated anywhere, it is the zero value (no
// client-side limit).
func (h Handle) DocumentLimits() DocumentLimits {
	var out DocumentLimits
	if h.m != nil {
		out = h.m.DocumentLimits
	}
	if h.p != nil {
		out = out.over(h.p.DocumentLimits)
	}
	if h.defLimits != nil {
		out = out.over(*h.defLimits)
	}
	return out
}

// documentLimitsDTO is the published document_limits object; pointer
// members keep "absent" apart from an explicit 0.
type documentLimitsDTO struct {
	InlinePDF              *bool  `json:"inline_pdf"`
	MaxFileBytes           *int64 `json:"max_file_bytes"`
	MaxTotalFileBytes      *int64 `json:"max_total_file_bytes"`
	MaxRequestBytes        *int64 `json:"max_request_bytes"`
	MaxPagesPerFile        *int64 `json:"max_pages_per_file"`
	MaxPagesPerRequest     *int64 `json:"max_pages_per_request"`
	MaxDocumentsPerMessage *int64 `json:"max_documents_per_message"`
	MaxDocumentsPerRequest *int64 `json:"max_documents_per_request"`
}

// parseDocumentLimits validates one document_limits object. An absent
// object is the zero value; a negative integer is a structural defect
// (the contract's minimum is 0).
func parseDocumentLimits(path string, dto *documentLimitsDTO) (DocumentLimits, error) {
	var out DocumentLimits
	if dto == nil {
		return out, nil
	}
	if dto.InlinePDF != nil {
		out.inlinePDF = boolField{value: *dto.InlinePDF, set: true}
	}
	for _, f := range []struct {
		name string
		src  *int64
		dst  *limitField
	}{
		{"max_file_bytes", dto.MaxFileBytes, &out.maxFileBytes},
		{"max_total_file_bytes", dto.MaxTotalFileBytes, &out.maxTotalFileBytes},
		{"max_request_bytes", dto.MaxRequestBytes, &out.maxRequestBytes},
		{"max_pages_per_file", dto.MaxPagesPerFile, &out.maxPagesPerFile},
		{"max_pages_per_request", dto.MaxPagesPerRequest, &out.maxPagesPerRequest},
		{"max_documents_per_message", dto.MaxDocumentsPerMessage, &out.maxDocumentsPerMessage},
		{"max_documents_per_request", dto.MaxDocumentsPerRequest, &out.maxDocumentsPerRequest},
	} {
		if f.src == nil {
			continue
		}
		if *f.src < 0 {
			return DocumentLimits{}, invalid(path+"."+f.name, "must be >= 0 (0 = no limit), got %d", *f.src)
		}
		*f.dst = limitField{value: *f.src, set: true}
	}
	return out, nil
}

// toDTO is the served projection: exactly the members the level states,
// explicit 0 included, so the served catalogue exposes the raw published
// object (spec §4.5). nil when the level states nothing.
func (d DocumentLimits) toDTO() *documentLimitsJSON {
	if d.Empty() {
		return nil
	}
	out := &documentLimitsJSON{}
	if d.inlinePDF.set {
		v := d.inlinePDF.value
		out.InlinePDF = &v
	}
	for _, f := range []struct {
		src limitField
		dst **int64
	}{
		{d.maxFileBytes, &out.MaxFileBytes},
		{d.maxTotalFileBytes, &out.MaxTotalFileBytes},
		{d.maxRequestBytes, &out.MaxRequestBytes},
		{d.maxPagesPerFile, &out.MaxPagesPerFile},
		{d.maxPagesPerRequest, &out.MaxPagesPerRequest},
		{d.maxDocumentsPerMessage, &out.MaxDocumentsPerMessage},
		{d.maxDocumentsPerRequest, &out.MaxDocumentsPerRequest},
	} {
		if f.src.set {
			v := f.src.value
			*f.dst = &v
		}
	}
	return out
}

// documentLimitsJSON is the served marshal shape of CatalogDocumentLimits
// (private, like the other envelope shapes in served.go): omitempty on
// pointers drops only absent members, never an explicit 0.
type documentLimitsJSON struct {
	InlinePDF              *bool  `json:"inline_pdf,omitempty"`
	MaxFileBytes           *int64 `json:"max_file_bytes,omitempty"`
	MaxTotalFileBytes      *int64 `json:"max_total_file_bytes,omitempty"`
	MaxRequestBytes        *int64 `json:"max_request_bytes,omitempty"`
	MaxPagesPerFile        *int64 `json:"max_pages_per_file,omitempty"`
	MaxPagesPerRequest     *int64 `json:"max_pages_per_request,omitempty"`
	MaxDocumentsPerMessage *int64 `json:"max_documents_per_message,omitempty"`
	MaxDocumentsPerRequest *int64 `json:"max_documents_per_request,omitempty"`
}
