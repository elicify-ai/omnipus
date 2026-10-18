package modelavailability

import "strings"

// Assertion reports whether supported availability prose asserts model absence.
// Structured error codes and other absence-evidence families belong to the caller.
func Assertion(message, model string) bool {
	return modelAvailabilityAssertion(message, model)
}

// modelAvailabilityAssertion parses the first subject-bearing availability
// clause, not an occurrence of "model" anywhere in a message. Supported model
// grammar is [the|this|a] [requested] model [identifier] is not available|offered
// [on|by this provider], with optional final period/exclamation. Keywords are
// case-insensitive, ASCII whitespace separates tokens, and outer space is trimmed.
// Quoted identifiers are opaque ASCII model IDs (the established canonical-ID
// policy); unquoted IDs must match the configured model. IDs may contain
// -_./:@~+, so their punctuation is not treated as a clause boundary.
//
// Neutral explanatory clauses may precede the subject, separated by :;,.!?
// with or without spaces. A model-only colon heading may introduce the actual
// subject ("Model m:image input ..."). Input-type subjects never assert model
// absence. Clauses carrying model/input/availability/negation evidence, or a
// fronted request-shape qualifier (condition markers such as with/for/when and
// nouns such as attachments/files/tools — availabilityRequestQualifier), are not
// skipped as explanations, and trailing qualifiers or additional diagnostic
// clauses decline a positive assertion. This is a finite grammar, not an English
// parser: unsupported subjects, identifiers, verbs, contractions and compound
// diagnoses return false, leaving the caller's format/unknown handling intact.
// The caller checks structured model_not_found first and retains its separate
// existing not-found/invalid-model-ID evidence families.
func modelAvailabilityAssertion(message, model string) bool {
	p := modelAvailabilityParser{text: strings.TrimSpace(message)}
	for {
		clause := p
		switch p.subject(model) {
		case availabilityModelSubject:
			if p.takeSeparator(':') {
				continue // a heading, not an availability verdict
			}
			return p.availabilityPredicate() && p.messageEnd()
		case availabilityInputSubject:
			return false
		default:
			p = clause
			if !p.neutralExplanation() {
				return false
			}
		}
	}
}

type availabilitySubject uint8

const (
	availabilityOtherSubject availabilitySubject = iota
	availabilityModelSubject
	availabilityInputSubject
)

type availabilityTokenKind uint8

const (
	availabilityWord availabilityTokenKind = iota
	availabilityQuotedID
	availabilitySeparator
	availabilityInvalid
	availabilityEnd
)

// These are parser state/tokens, not wire-format or persisted types.
type availabilityToken struct {
	kind availabilityTokenKind
	text string
}

type modelAvailabilityParser struct {
	text string
	pos  int
}

func (p *modelAvailabilityParser) subject(model string) availabilitySubject {
	q := *p
	q.takeWord("the", "this", "a")
	q.takeWord("requested")
	if q.takeWord("model") {
		predicate := q
		generic := predicate.takeWord("is") && predicate.takeWord("not")
		if generic || q.takeModelID(model) || q.peek().kind == availabilityEnd || q.peek().isSeparator(':') {
			*p = q
			return availabilityModelSubject
		}
		return availabilityOtherSubject
	}
	if q.takeWord("input", "inputs") ||
		(q.takeWord("image", "images", "pdf", "pdfs", "audio", "video", "document", "documents") && q.takeWord("input", "inputs")) {
		*p = q
		return availabilityInputSubject
	}
	return availabilityOtherSubject
}

func (p *modelAvailabilityParser) availabilityPredicate() bool {
	if !p.takeWord("is") || !p.takeWord("not") || !p.takeWord("available", "offered") {
		return false
	}
	if p.takeWord("on", "by") {
		return p.takeWord("this") && p.takeWord("provider")
	}
	return true
}

func (p *modelAvailabilityParser) messageEnd() bool {
	if !p.takeSeparator('.') {
		p.takeSeparator('!')
	}
	return p.peek().kind == availabilityEnd
}

func (p *modelAvailabilityParser) neutralExplanation() bool {
	words := false
	for {
		t := p.next()
		switch t.kind {
		case availabilityWord:
			if t.isWord("model", "input", "inputs", "image", "images", "pdf", "pdfs", "audio", "video", "document", "documents", "not", "available", "offered") ||
				availabilityRequestQualifier(t) {
				return false
			}
			words = true
		case availabilitySeparator:
			return words
		default:
			return false
		}
	}
}

// availabilityRequestQualifier reports whether an explanatory-clause word makes
// the following availability sentence conditional on the request's shape: a
// condition/scope marker ("With attachments,", "For file uploads,", "When tools
// are enabled,") or a request-shape noun (attachments, files, tools, media).
// Such a fronted clause is not neutral, so it is never skipped to reach a model
// subject: "With attachments, model m is not available." is a capability or
// format verdict, not unconditional model absence. Neutral phrases ("Sorry,",
// "Error:", "Request rejected:") contain none of these words.
func availabilityRequestQualifier(t availabilityToken) bool {
	return t.isWord(
		// condition / scope markers
		"with", "without", "for", "when", "whenever", "if", "unless", "while", "using", "via", "given",
		// request-shape nouns
		"attachment", "attachments", "attached", "file", "files", "upload", "uploads", "uploaded",
		"tool", "tools", "function", "functions", "media", "vision", "multimodal",
	)
}

func (p *modelAvailabilityParser) takeModelID(model string) bool {
	if t := p.peek(); t.kind == availabilityQuotedID {
		if !availabilityIdentifier(t.text) {
			return false
		}
		p.next()
		return true
	}
	model = strings.TrimSpace(model)
	if !availabilityIdentifier(model) {
		return false
	}
	p.skipSpace()
	end := p.pos + len(model)
	if end > len(p.text) || !strings.EqualFold(p.text[p.pos:end], model) {
		return false
	}
	if end < len(p.text) && !availabilitySpace(p.text[end]) && !availabilityClauseSeparator(p.text[end]) {
		return false
	}
	p.pos = end // consume the whole configured ID, including its punctuation
	return true
}

func availabilityIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte("-_./:@~+", c) >= 0 {
			continue
		}
		return false
	}
	return true
}

func (p *modelAvailabilityParser) next() availabilityToken {
	p.skipSpace()
	if p.pos == len(p.text) {
		return availabilityToken{kind: availabilityEnd}
	}
	start := p.pos
	c := p.text[p.pos]
	p.pos++
	if availabilityClauseSeparator(c) {
		return availabilityToken{kind: availabilitySeparator, text: p.text[start:p.pos]}
	}
	if availabilityQuote(c) {
		for p.pos < len(p.text) && p.text[p.pos] != c {
			p.pos++
		}
		if p.pos == len(p.text) {
			return availabilityToken{kind: availabilityInvalid}
		}
		t := availabilityToken{kind: availabilityQuotedID, text: p.text[start+1 : p.pos]}
		p.pos++
		return t
	}
	for p.pos < len(p.text) && !availabilitySpace(p.text[p.pos]) && !availabilityClauseSeparator(p.text[p.pos]) && !availabilityQuote(p.text[p.pos]) {
		p.pos++
	}
	return availabilityToken{kind: availabilityWord, text: p.text[start:p.pos]}
}

func (p *modelAvailabilityParser) peek() availabilityToken {
	q := *p
	return q.next()
}

func (p *modelAvailabilityParser) takeWord(words ...string) bool {
	if !p.peek().isWord(words...) {
		return false
	}
	p.next()
	return true
}

func (p *modelAvailabilityParser) takeSeparator(c byte) bool {
	if !p.peek().isSeparator(c) {
		return false
	}
	p.next()
	return true
}

func (t availabilityToken) isWord(words ...string) bool {
	if t.kind != availabilityWord {
		return false
	}
	for _, word := range words {
		if strings.EqualFold(t.text, word) {
			return true
		}
	}
	return false
}

func (t availabilityToken) isSeparator(c byte) bool {
	return t.kind == availabilitySeparator && len(t.text) == 1 && t.text[0] == c
}

func (p *modelAvailabilityParser) skipSpace() {
	for p.pos < len(p.text) && availabilitySpace(p.text[p.pos]) {
		p.pos++
	}
}

func availabilitySpace(c byte) bool {
	return c == ' ' || (c >= '\t' && c <= '\r')
}

func availabilityClauseSeparator(c byte) bool {
	return strings.IndexByte(":;,.!?", c) >= 0
}

func availabilityQuote(c byte) bool {
	return c == '\'' || c == '"' || c == '`'
}
