package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/session"
)

const historyInvalidQuery = "invalid history query"
const historyInvalidLimit = "limit must be an integer from 20 to 100"
const historyInvalidFloor = "min_rows must be an integer from 1 to limit"
const historyInvalidAttachLimit = "history_limit must be an integer from 20 to 100"
const historyInvalidAttachFloor = "history_min_rows must be an integer from 1 to history_limit"

type historyQueryError struct{ message string }

func (e *historyQueryError) Error() string     { return e.message }
func historyQueryRefusal(message string) error { return &historyQueryError{message: message} }
func historyDigits(raw string, low, high int) (int, bool) {
	if raw == "" {
		return 0, false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	return int(n), err == nil && n >= uint64(low) && n <= uint64(high)
}
func parseChatHistoryQuery(raw string) (session.ChatHistoryPageOptions, *int, error) {
	opts := session.ChatHistoryPageOptions{Limit: 40, MinRows: 20}
	recognized := map[string]bool{"limit": true, "min_rows": true, "before": true, "after": true, "window_start": true, "window_end": true}
	fields := map[string][]string{}
	badValue := map[string]bool{}
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		decoded, err := url.QueryUnescape(key)
		if err != nil {
			return opts, nil, historyQueryRefusal(historyInvalidQuery)
		}
		value, err = url.QueryUnescape(value)
		if err != nil && !recognized[decoded] {
			return opts, nil, historyQueryRefusal(historyInvalidQuery)
		}
		if recognized[decoded] {
			fields[decoded] = append(fields[decoded], value)
			badValue[decoded] = badValue[decoded] || err != nil
		}
	}
	var requested *int
	if values, ok := fields["limit"]; ok {
		if len(values) != 1 || badValue["limit"] {
			return opts, nil, historyQueryRefusal(historyInvalidLimit)
		}
		n, valid := historyDigits(values[0], 20, 100)
		if !valid {
			return opts, nil, historyQueryRefusal(historyInvalidLimit)
		}
		opts.Limit = n
		v := n
		requested = &v
	}
	if values, ok := fields["min_rows"]; ok {
		if len(values) != 1 || badValue["min_rows"] {
			return opts, requested, historyQueryRefusal(historyInvalidFloor)
		}
		n, valid := historyDigits(values[0], 1, opts.Limit)
		if !valid {
			return opts, requested, historyQueryRefusal(historyInvalidFloor)
		}
		opts.MinRows = n
	}
	positions := []struct {
		key    string
		target *session.HistoryPosition
	}{{"before", &opts.Before}, {"after", &opts.After}, {"window_start", &opts.WindowStart}, {"window_end", &opts.WindowEnd}}
	for _, field := range positions {
		if values, ok := fields[field.key]; ok {
			if len(values) != 1 || badValue[field.key] {
				return opts, requested, session.ErrInvalidHistoryPosition
			}
			p, err := session.ParseHistoryPosition(values[0])
			if err != nil {
				return opts, requested, err
			}
			*field.target = p
		}
	}
	if opts.Before != "" && opts.After != "" || (opts.WindowStart == "") != (opts.WindowEnd == "") || opts.WindowStart != "" && opts.After != "" {
		return opts, requested, session.ErrInvalidHistoryPageRange
	}
	return opts, requested, nil
}

func historyAttachFields(raw []byte) (map[string][]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("invalid attach object")
	}
	fields := map[string][]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("invalid attach field")
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = append(fields[key], value)
	}
	if _, err = d.Token(); err != nil {
		return nil, err
	}
	if _, err = d.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid attach trailing data")
	}
	return fields, nil
}
func parseChatHistoryAttach(raw []byte) (session.ChatHistoryPageOptions, *int, error) {
	opts := session.ChatHistoryPageOptions{Limit: 40, MinRows: 20}
	var requested *int
	fields, err := historyAttachFields(raw)
	if err != nil {
		return opts, nil, historyQueryRefusal(historyInvalidAttachLimit)
	}
	if values, ok := fields["history_limit"]; ok {
		if len(values) != 1 {
			return opts, nil, historyQueryRefusal(historyInvalidAttachLimit)
		}
		n, valid := historyDigits(string(values[0]), 20, 100)
		if !valid {
			return opts, nil, historyQueryRefusal(historyInvalidAttachLimit)
		}
		opts.Limit = n
		v := n
		requested = &v
	}
	if values, ok := fields["history_min_rows"]; ok {
		if len(values) != 1 {
			return opts, requested, historyQueryRefusal(historyInvalidAttachFloor)
		}
		n, valid := historyDigits(string(values[0]), 1, opts.Limit)
		if !valid {
			return opts, requested, historyQueryRefusal(historyInvalidAttachFloor)
		}
		opts.MinRows = n
	}
	return opts, requested, nil
}
