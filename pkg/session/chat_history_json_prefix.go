package session

// Minimum completion of an incomplete derived-index frame body (#1523). The
// writer encodes each body as one compact JSON object, so a cut-off write
// leaves a prefix of that object. historyJSONMinCompletion returns the fewest
// bytes ANY JSON text needs to finish such a prefix: the shortest key is "",
// the shortest value is 0, and each open container needs its own closer.
// Every real writer completion is at least that long, so the result is a
// lower bound and never refuses a genuine cut. It assumes the caller already
// checked that the prefix is syntactically valid JSON.

// Obligations of one open container, after its current token (if any) ends.
const (
	historyJSONOpen  = iota // just after '{' or '[': it may close at once
	historyJSONKey          // object after ',': `"":0` then the closer
	historyJSONColon        // object key closed: `:0` then the closer
	historyJSONValue        // after ':' or an array ',': `0` then the closer
	historyJSONNext         // value complete: only the closer
)

type historyJSONLevel struct {
	object bool
	state  int
}

func (l historyJSONLevel) need() int64 {
	switch l.state {
	case historyJSONKey:
		return 5 // `"":0` + closer
	case historyJSONColon:
		return 3 // `:0` + closer
	case historyJSONValue:
		return 2 // `0` + closer
	default:
		return 1 // closer
	}
}

// Token in progress at the end of the prefix.
const (
	historyJSONNoToken = iota
	historyJSONString
	historyJSONNumber
	historyJSONLiteral
)

type historyJSONPrefix struct {
	stack []historyJSONLevel
	token int
	// escape: 0 none, 1 just after '\', 2..5 after `\u` plus 0..3 hex digits.
	escape  int
	last    byte  // last byte of a number token
	pending int64 // letters still missing from a true/false/null literal
}

func historyJSONMinCompletion(prefix []byte) int64 {
	var p historyJSONPrefix
	for _, c := range prefix {
		p.step(c)
	}
	return p.need()
}

func (p *historyJSONPrefix) step(c byte) {
	switch p.token {
	case historyJSONString:
		p.stringByte(c)
		return
	case historyJSONNumber:
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-' {
			p.last = c
			return
		}
		p.token = historyJSONNoToken
	case historyJSONLiteral:
		if c >= 'a' && c <= 'z' {
			p.pending--
			return
		}
		p.token = historyJSONNoToken
	}
	p.structural(c)
}

func (p *historyJSONPrefix) stringByte(c byte) {
	switch {
	case p.escape == 1 && c == 'u':
		p.escape = 2
	case p.escape == 1:
		p.escape = 0
	case p.escape >= 2:
		if p.escape++; p.escape == 6 {
			p.escape = 0
		}
	case c == '\\':
		p.escape = 1
	case c == '"':
		p.token = historyJSONNoToken
	}
}

func (p *historyJSONPrefix) structural(c byte) {
	top := len(p.stack) - 1
	switch {
	case c == '{' || c == '[':
		p.valueStarts(top)
		p.stack = append(p.stack, historyJSONLevel{object: c == '{', state: historyJSONOpen})
	case c == '}' || c == ']':
		if top >= 0 {
			p.stack = p.stack[:top]
		}
	case top < 0:
	case c == ',' && p.stack[top].object:
		p.stack[top].state = historyJSONKey
	case c == ',' || c == ':':
		p.stack[top].state = historyJSONValue
	case c == '"' && p.stack[top].object && (p.stack[top].state == historyJSONOpen || p.stack[top].state == historyJSONKey):
		p.token = historyJSONString
		p.stack[top].state = historyJSONColon
	case c == '"':
		p.token = historyJSONString
		p.valueStarts(top)
	case (c >= '0' && c <= '9') || c == '-':
		p.token, p.last = historyJSONNumber, c
		p.valueStarts(top)
	case c == 't' || c == 'n':
		p.token, p.pending = historyJSONLiteral, 3 // "rue", "ull"
		p.valueStarts(top)
	case c == 'f':
		p.token, p.pending = historyJSONLiteral, 4 // "alse"
		p.valueStarts(top)
	}
}

// valueStarts records that the container at top now holds a value in progress:
// once that value ends, only the container's closer is still owed.
func (p *historyJSONPrefix) valueStarts(top int) {
	if top >= 0 {
		p.stack[top].state = historyJSONNext
	}
}

func (p *historyJSONPrefix) need() int64 {
	var n int64
	for _, l := range p.stack {
		n += l.need()
	}
	switch p.token {
	case historyJSONString:
		n++ // closing quote
		if p.escape == 1 {
			n++ // the escaped character
		} else if p.escape >= 2 {
			n += int64(6 - p.escape) // missing \u hex digits
		}
	case historyJSONNumber:
		if p.last == '-' || p.last == '.' || p.last == 'e' || p.last == 'E' || p.last == '+' {
			n++ // a digit is still required
		}
	case historyJSONLiteral:
		n += p.pending
	}
	return n
}
