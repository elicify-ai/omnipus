package session

// The derived index is a persistent ordered treap. Its immutable nodes contain
// only locations and projection references. A checkpoint names one root and a
// durable byte end; appending a new path never changes a reader's old root.
import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

const historyFrameHeader = 40
const historyFrameBound = 16 * 1024 * 1024

type historyNode struct { // not-wire-format: private derived metadata codec.
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	Left     int64           `json:"left,omitempty"`
	Right    int64           `json:"right,omitempty"`
	Priority uint64          `json:"priority"`
}
type historyFrame struct { // not-wire-format: private checksummed journal frame.
	Node   *historyNode       `json:"node,omitempty"`
	Commit *historyCheckpoint `json:"commit,omitempty"`
}
type chatHistoryIndexTree struct {
	ctx        context.Context
	file       *os.File
	end        int64
	root       int64
	pending    map[int64]historyNode
	readCache  map[int64]historyNode
	readBuffer []byte
	order      []int64
	buffer     []byte
	sid        string
	// planning exists only in the explicitly offline, no-write constructor.
	planning       map[string]json.RawMessage
	knownRanks     map[HistoryPosition]uint64
	expiredAddress func(ArchiveAddress) bool
	// overlay validates a bounded saved delta against an immutable old root.
	// Unlike pending tree nodes, these values can never flush to the journal.
	overlay      map[string]json.RawMessage
	dependencies map[historyDependencyKey]struct{}
	// Only successfully projected occurrences at this cut, not a session map.
	positions map[HistoryPosition]uint64
}

func historyEncodeFrame(v historyFrame) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) > historyFrameBound {
		return nil, errors.New("history index frame too large")
	}
	out := make([]byte, historyFrameHeader+len(b))
	copy(out, "CHI1")
	binary.BigEndian.PutUint32(out[4:8], uint32(len(b)))
	sum := sha256.Sum256(b)
	copy(out[8:40], sum[:])
	copy(out[40:], b)
	return out, nil
}

func (t *chatHistoryIndexTree) readFrame(off, end int64) (historyFrame, int64, error) {
	var v historyFrame
	if err := t.ctx.Err(); err != nil {
		return v, off, err
	}
	if off < 0 || end-off < historyFrameHeader {
		return v, off, io.ErrUnexpectedEOF
	}
	var header [historyFrameHeader]byte
	n, err := t.file.ReadAt(header[:], off)
	historyIndexBytes(t.ctx, int64(n))
	if err != nil {
		return v, off, err
	}
	if string(header[:4]) != "CHI1" {
		return v, off, errors.New("invalid history index framing")
	}
	size := int64(binary.BigEndian.Uint32(header[4:8]))
	if size <= 0 || size > historyFrameBound {
		return v, off, errors.New("invalid history index frame length")
	}
	if size > end-off-historyFrameHeader {
		return v, off, io.ErrUnexpectedEOF
	}
	// Reuse one bounded frame scratch buffer per immutable read cut. A deeper
	// metadata tree must not allocate another encoded frame for every visited
	// node. JSON decoding owns its strings and RawMessage values, so cached
	// nodes do not alias this buffer when the next frame overwrites it.
	if int64(cap(t.readBuffer)) < size {
		t.readBuffer = make([]byte, size)
	}
	b := t.readBuffer[:size]
	n, err = t.file.ReadAt(b, off+historyFrameHeader)
	historyIndexBytes(t.ctx, int64(n))
	if err != nil {
		return v, off, err
	}
	sum := sha256.Sum256(b)
	if string(sum[:]) != string(header[8:40]) {
		return v, off, errors.New("history index checksum mismatch")
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return v, off, err
	}
	if (v.Node == nil) == (v.Commit == nil) {
		return v, off, errors.New("invalid history index frame variant")
	}
	return v, off + historyFrameHeader + size, nil
}

func (t *chatHistoryIndexTree) node(ref int64) (historyNode, error) {
	if ref <= 0 {
		return historyNode{}, errors.New("invalid history tree reference")
	}
	if n, ok := t.pending[ref]; ok {
		return n, nil
	}
	if n, ok := t.readCache[ref]; ok {
		return n, nil
	}
	f, _, err := t.readFrame(ref-1, t.end)
	if err != nil {
		return historyNode{}, err
	}
	if f.Node == nil || f.Node.Key == "" {
		return historyNode{}, errors.New("invalid history tree node")
	}
	if f.Node.Left >= ref || f.Node.Right >= ref {
		return historyNode{}, errors.New("cyclic history tree")
	}
	// Bounded request-local metadata reuse; never cache a lifetime tree.
	if t.readCache == nil {
		t.readCache = make(map[int64]historyNode)
	}
	if len(t.readCache) < 2048 {
		t.readCache[ref] = *f.Node
	}
	return *f.Node, nil
}
func (t *chatHistoryIndexTree) stage(n historyNode) (int64, error) {
	b, err := historyEncodeFrame(historyFrame{Node: &n})
	if err != nil {
		return 0, err
	}
	ref := t.end + int64(len(t.buffer)) + 1
	t.buffer = append(t.buffer, b...)
	if t.pending == nil {
		t.pending = map[int64]historyNode{}
	}
	t.pending[ref] = n
	t.order = append(t.order, ref)
	return ref, nil
}
func (t *chatHistoryIndexTree) get(key string, out any) (bool, error) {
	if b, ok := t.overlay[key]; ok {
		if out == nil {
			return true, nil
		}
		return true, json.Unmarshal(b, out)
	}
	if t.planning != nil {
		b, ok := t.planning[key]
		if !ok || out == nil {
			return ok, nil
		}
		return true, json.Unmarshal(b, out)
	}
	p := t.root
	for depth := 0; p != 0; depth++ {
		if depth > 256 {
			return false, errors.New("history tree depth exceeded")
		}
		n, err := t.node(p)
		if err != nil {
			return false, err
		}
		if key == n.Key {
			if out == nil {
				return true, nil
			}
			return true, json.Unmarshal(n.Value, out)
		}
		if key < n.Key {
			p = n.Left
		} else {
			p = n.Right
		}
	}
	return false, nil
}
func (t *chatHistoryIndexTree) set(key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if t.overlay != nil {
		t.overlay[key] = b
		return nil
	}
	if t.planning != nil {
		t.planning[key] = b
		return nil
	}
	h := sha256.Sum256([]byte(key))
	n := historyNode{Key: key, Value: b, Priority: binary.BigEndian.Uint64(h[:8])}
	root, err := t.put(t.root, n, 0)
	if err == nil {
		t.root = root
	}
	if err == nil && len(t.buffer) > 1024*1024 {
		err = t.flushNodes()
	}
	return err
}
func (t *chatHistoryIndexTree) put(p int64, value historyNode, depth int) (int64, error) {
	if depth > 256 {
		return 0, errors.New("history tree depth exceeded")
	}
	if p == 0 {
		return t.stage(value)
	}
	n, err := t.node(p)
	if err != nil {
		return 0, err
	}
	if value.Key == n.Key {
		n.Value = value.Value
		return t.stage(n)
	}
	if value.Key < n.Key {
		n.Left, err = t.put(n.Left, value, depth+1)
		if err != nil {
			return 0, err
		}
		child, e := t.node(n.Left)
		if e != nil {
			return 0, e
		}
		if child.Priority < n.Priority {
			n.Left = child.Right
			child.Right, err = t.stage(n)
			if err != nil {
				return 0, err
			}
			return t.stage(child)
		}
	} else {
		n.Right, err = t.put(n.Right, value, depth+1)
		if err != nil {
			return 0, err
		}
		child, e := t.node(n.Right)
		if e != nil {
			return 0, e
		}
		if child.Priority < n.Priority {
			n.Right = child.Left
			child.Left, err = t.stage(n)
			if err != nil {
				return 0, err
			}
			return t.stage(child)
		}
	}
	return t.stage(n)
}

// seek finds the nearest key at or beyond a boundary in either direction.
func (t *chatHistoryIndexTree) seek(key string, forward, inclusive bool) (historyNode, bool, error) {
	p := t.root
	var best historyNode
	found := false
	for depth := 0; p != 0; depth++ {
		if depth > 256 {
			return best, false, errors.New("history tree depth exceeded")
		}
		n, err := t.node(p)
		if err != nil {
			return best, false, err
		}
		eligible := (forward && (n.Key > key || (inclusive && n.Key == key))) || (!forward && (n.Key < key || (inclusive && n.Key == key)))
		if eligible {
			best = n
			found = true
			if forward {
				p = n.Left
			} else {
				p = n.Right
			}
		} else if forward {
			p = n.Right
		} else {
			p = n.Left
		}
	}
	return best, found, nil
}
func (t *chatHistoryIndexTree) walk(fn func(historyNode) error) error {
	if t.planning != nil {
		keys := make([]string, 0, len(t.planning))
		for k := range t.planning {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := fn(historyNode{Key: k, Value: t.planning[k]}); err != nil {
				return err
			}
		}
		return nil
	}
	var visit func(int64, int) error
	visit = func(ref int64, depth int) error {
		if ref == 0 {
			return nil
		}
		if depth > 256 {
			return fmt.Errorf("history tree depth exceeded")
		}
		n, err := t.node(ref)
		if err != nil {
			return err
		}
		if err = visit(n.Left, depth+1); err != nil {
			return err
		}
		if err = fn(n); err != nil {
			return err
		}
		return visit(n.Right, depth+1)
	}
	return visit(t.root, 0)
}
func (t *chatHistoryIndexTree) flushNodes() error {
	if len(t.buffer) == 0 {
		return nil
	}
	n, err := t.file.WriteAt(t.buffer, t.end)
	if err != nil {
		return err
	}
	if n != len(t.buffer) {
		return io.ErrShortWrite
	}
	t.end += int64(n)
	t.buffer = nil
	t.pending = nil
	t.order = nil
	return nil
}
