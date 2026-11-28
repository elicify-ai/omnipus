// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

// A Stack represents a stack of values.
type Stack struct {
	stack []Value
}

func (stk *Stack) Len() int {
	return len(stk.stack)
}

func (stk *Stack) Push(v Value) {
	if len(stk.stack) >= maxOperandStack {
		structuralPanicf("operand stack deeper than %d", maxOperandStack)
	}
	stk.stack = append(stk.stack, v)
}

func (stk *Stack) Pop() Value {
	n := len(stk.stack)
	if n == 0 {
		return Value{}
	}
	v := stk.stack[n-1]
	stk.stack[n-1] = Value{}
	stk.stack = stk.stack[:n-1]
	return v
}

// popRequired pops like Pop but ends the parse with an ErrStructureLimit
// error when the stack is empty, for loops whose count came from the
// document and must not run on past the operands actually supplied.
func (stk *Stack) popRequired() Value {
	if len(stk.stack) == 0 {
		structuralPanicf("operand stack empty")
	}
	return stk.Pop()
}

func newDict() Value {
	return Value{nil, objptr{}, make(dict)}
}

// Interpret interprets the content in a stream as a basic PostScript program,
// pushing values onto a stack and then calling the do function to execute
// operators. The do function may push or pop values from the stack as needed
// to implement op.
//
// Interpret handles the operators "dict", "currentdict", "begin", "end", "def", and "pop" itself.
//
// Interpret is not a full-blown PostScript interpreter. Its job is to handle the
// very limited PostScript found in certain supporting file formats embedded
// in PDF files, such as cmap files that describe the mapping from font code
// points to Unicode code points.
//
// A stream can also be represented by an array of streams that has to be handled as a single stream
// In the case of a simple stream read only once, otherwise get the length of the stream to handle it properly
//
// There is no support for executable blocks, among other limitations.
func Interpret(strm Value, do func(stk *Stack, op string)) {
	var stk Stack
	var dicts []dict
	s := strm
	strmlen := 1
	if strm.Kind() == Array {
		strmlen = strm.Len()
	}

	for i := 0; i < strmlen; i++ {
		if strm.Kind() == Array {
			s = strm.Index(i)
		}

		rd := s.Reader()

		b := newBuffer(rd, 0, strm.r)
		b.allowEOF = true
		b.allowObjptr = false
		b.allowStream = false

	Reading:
		for {
			tok := b.readToken()
			if isEOFToken(tok) {
				break
			}
			if kw, ok := tok.(keyword); ok {
				switch kw {
				case "null", "[", "]", "<<", ">>":
					break
				case "dict":
					stk.Pop()
					stk.Push(Value{nil, objptr{}, make(dict)})
					continue
				case "currentdict":
					if len(dicts) == 0 {
						panic("no current dictionary")
					}
					strm.r.chargeReuse(dicts[len(dicts)-1])
					stk.Push(Value{nil, objptr{}, dicts[len(dicts)-1]})
					continue
				case "begin":
					d := stk.Pop()
					dd, isDict := d.data.(dict)
					if !isDict {
						panic("cannot begin non-dict")
					}
					if len(dicts) >= maxDictStack {
						structuralPanicf("dictionary stack deeper than %d", maxDictStack)
					}
					dicts = append(dicts, dd)
					continue
				case "end":
					if len(dicts) <= 0 {
						panic("mismatched begin/end")
					}
					dicts = dicts[:len(dicts)-1]
					continue
				case "def":
					if len(dicts) <= 0 {
						panic("def without open dict")
					}
					val := stk.Pop()
					key, ok := stk.Pop().data.(name)
					if !ok {
						// panic(fmt.Sprintf("def of non-name: %+v", stk.Pop().data))
						// Skip the value if it has key without value
						continue
					}
					dicts[len(dicts)-1][key] = val.data
					continue
				case "pop":
					stk.Pop()
					continue
				default:
					// Each open dictionary hashes the keyword again: the
					// lexer paid for its bytes once, the probes pay per
					// dictionary. A name bound with def is handed out
					// without being lexed again, so its use pays its size.
					strm.r.charge(int64(len(kw)) * int64(len(dicts)))
					for i := len(dicts) - 1; i >= 0; i-- {
						if v, ok := dicts[i][name(kw)]; ok {
							strm.r.chargeReuse(v)
							stk.Push(Value{nil, objptr{}, v})
							continue Reading
						}
					}
					do(&stk, string(kw))
					continue
				}
			}
			b.unreadToken(tok)
			obj := b.readObject()
			stk.Push(Value{nil, objptr{}, obj})
		}
	}
}
