package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// archiveRangeRequest is internal tool state, not a gateway wire format. A range
// is inclusive; paging counts literal JSONL runes, including record delimiters.
type archiveRangeRequest struct {
	from, to       int
	offset, length int
}

func parseArchiveRangeRequest(args map[string]any) (archiveRangeRequest, error) {
	var req archiveRangeRequest
	for _, key := range []string{"query", "turn_range", "time", "tool_call_id"} {
		if _, present := args[key]; present {
			return req, fmt.Errorf("provide exactly one recall mode: archive_range cannot be combined with %s", key)
		}
	}
	bounds, ok := args["archive_range"].(map[string]any)
	if !ok {
		return req, fmt.Errorf("archive_range must be an object with integer from and to")
	}
	var err error
	if req.from, err = archiveRangeInteger(bounds, "from", 0); err != nil {
		return req, err
	}
	if req.to, err = archiveRangeInteger(bounds, "to", 0); err != nil {
		return req, err
	}
	if req.from > req.to {
		return req, fmt.Errorf("archive_range.from must be <= archive_range.to")
	}
	if _, present := args["offset"]; present {
		if req.offset, err = archiveRangeInteger(args, "offset", 0); err != nil {
			return req, err
		}
	}
	if _, present := args["length"]; present {
		if req.length, err = archiveRangeInteger(args, "length", 1); err != nil {
			return req, err
		}
	}
	return req, nil
}

// Unlike the legacy addressed-id argument helper, range integers do not coerce
// strings, nulls or booleans. Integral JSON float64 numbers remain accepted.
func archiveRangeInteger(args map[string]any, key string, minimum int) (int, error) {
	invalid := func() (int, error) {
		return 0, fmt.Errorf("archive_range.%s must be an integer >= %d within the supported integer range", key, minimum)
	}
	var val int
	switch n := args[key].(type) {
	case int:
		val = n
	case int64:
		if n > int64(maxIntValue()) || n < int64(minimum) {
			return invalid()
		}
		val = int(n)
	case float64:
		// The exclusive bound is exactly representable even when float64 cannot
		// represent MaxInt itself. Never convert an overflowing/non-finite value.
		if n != math.Trunc(n) || n < float64(minimum) || n >= math.Ldexp(1, strconv.IntSize-1) {
			return invalid()
		}
		val = int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil || i > int64(maxIntValue()) || i < int64(minimum) {
			return invalid()
		}
		val = int(i)
	default:
		return invalid()
	}
	if val < minimum {
		return invalid()
	}
	return val, nil
}

func maxIntValue() int { return int(^uint(0) >> 1) }

// executeArchiveRange returns historical JSONL only inside the CURRENT result.
// It never installs, replaces or drops a RecallSpan or reconstructs live calls.
func (t *RecallConversationTool) executeArchiveRange(ctx context.Context, key string, args map[string]any) *tools.ToolResult {
	req, err := parseArchiveRangeRequest(args)
	if err != nil {
		return archiveRangeError(err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return archiveRangeError(ctxErr)
	}
	reader, ok := t.archive.(memory.ArchiveRangeScanner)
	if !ok {
		return archiveRangeError(fmt.Errorf("archive_range requires a literal JSONL archive reader"))
	}
	capRunes := t.recallCapPolicy().effectiveCap(surfaceBuiltinSuccess, 1)
	// Reserve worst-width integer fields before scanning. The default and an
	// oversized explicit length therefore produce the same deterministic page.
	maxInt := maxIntValue()
	reserve := utf8.RuneCountInString(archiveRangePageFraming(maxInt, maxInt, maxInt, maxInt, maxInt, strconv.Itoa(maxInt))) + 1
	pageLimit := capRunes - reserve
	if pageLimit < 1 {
		return archiveRangeError(fmt.Errorf("archive_range result cap %d leaves no room for a framed page", capRunes))
	}
	if req.length > 0 {
		pageLimit = min(pageLimit, req.length)
	}
	page := archiveRangePage{offset: req.offset, limit: pageLimit}
	err = reader.ScanArchiveRange(ctx, key, req.from, req.to, func(_ int, raw []byte, _ memory.ArchivedMessage) error {
		return page.collect(ctx, raw)
	})
	if err != nil {
		return archiveRangeError(err)
	}
	if err := ctx.Err(); err != nil {
		return archiveRangeError(err)
	}
	next := "end"
	if req.offset < page.total && page.returned < page.total-req.offset {
		next = strconv.Itoa(req.offset + page.returned)
	}
	header := archiveRangePageFraming(req.from, req.to, page.total, req.offset, page.returned, next)
	incRecallCounter("hit")
	return &tools.ToolResult{ForLLM: header + "\n" + page.payload.String()}
}

func archiveRangeError(err error) *tools.ToolResult {
	incRecallCounter("error")
	res := tools.ErrorResult("recall_conversation: archive_range: " + err.Error())
	res.Err = err
	return res
}

func archiveRangePageFraming(from, to, total, offset, returned int, next string) string {
	return fmt.Sprintf("[recall page: archive_range={from:%d,to:%d}, data=JSONL, total_runes=%d, offset=%d, returned=%d, next_offset=%s]",
		from, to, total, offset, returned, next)
}

// archiveRangePage retains only the requested capped page. Counting continues
// through every selected record even when returned == limit, so late faults and
// caller cancellation never become a partial-success page with an invented total.
type archiveRangePage struct {
	offset, limit   int
	total, returned int
	payload         strings.Builder
}

func (p *archiveRangePage) collect(ctx context.Context, raw []byte) error {
	checkpoint := 0
	for pos := 0; pos < len(raw); {
		if pos >= checkpoint {
			if err := ctx.Err(); err != nil {
				return err
			}
			checkpoint = pos + 64*1024
		}
		_, width := utf8.DecodeRune(raw[pos:])
		if width == 1 && raw[pos] >= utf8.RuneSelf {
			return fmt.Errorf("archive record is not valid UTF-8 at byte %d", pos)
		}
		if p.total == maxIntValue() {
			return fmt.Errorf("archive_range total runes exceed supported integer range")
		}
		if p.total >= p.offset && p.returned < p.limit {
			p.payload.Write(raw[pos : pos+width])
			p.returned++
		}
		p.total++
		pos += width
	}
	return ctx.Err()
}
