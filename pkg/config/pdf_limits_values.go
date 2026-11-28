package config

// PDFLimits is an immutable resolved view. Accessors return (value, stated):
// absent means no client-side limit; explicit zero means unlimited; positive
// values are inclusive limits. InlinePDF absent means true at the consumer.
// No provider limit or default number lives in Go code.
type PDFLimits struct {
	inlinePDF pdfBoolField
	integers  [7]pdfLimitField
}

type pdfBoolField struct {
	value bool
	set   bool
}

type pdfLimitField struct {
	value int64
	set   bool
}

// InlinePDF reports whether the inline PDF route is enabled, when stated.
func (d PDFLimits) InlinePDF() (bool, bool) { return d.inlinePDF.value, d.inlinePDF.set }

// MaxFileBytes reports the decoded byte limit for one PDF.
func (d PDFLimits) MaxFileBytes() (int64, bool) { return d.integers[0].get() }

// MaxTotalFileBytes reports the decoded byte limit across a request's PDFs.
func (d PDFLimits) MaxTotalFileBytes() (int64, bool) { return d.integers[1].get() }

// MaxRequestBytes reports the whole serialized request body limit.
func (d PDFLimits) MaxRequestBytes() (int64, bool) { return d.integers[2].get() }

// MaxPagesPerFile reports the page limit for one PDF.
func (d PDFLimits) MaxPagesPerFile() (int64, bool) { return d.integers[3].get() }

// MaxPagesPerRequest reports the page limit across a request's PDFs.
func (d PDFLimits) MaxPagesPerRequest() (int64, bool) { return d.integers[4].get() }

// MaxDocumentsPerMessage reports the PDF count limit for one wire message.
func (d PDFLimits) MaxDocumentsPerMessage() (int64, bool) { return d.integers[5].get() }

// MaxDocumentsPerRequest reports the PDF count limit for one request.
func (d PDFLimits) MaxDocumentsPerRequest() (int64, bool) { return d.integers[6].get() }

// Empty reports whether no field is stated.
func (d PDFLimits) Empty() bool { return d == PDFLimits{} }

func (f pdfLimitField) get() (int64, bool) { return f.value, f.set }
