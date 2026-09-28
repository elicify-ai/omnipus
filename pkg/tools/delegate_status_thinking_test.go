// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C guard tests for the delegate-status progress line (spec section 16
// item 24: reasoning travels as COUNTS, never text - the delegate_status
// surface renders "%d bytes of reasoning so far this round"). The pinned
// production symbol is pkg/tools/delegate_status.go::formatToolCallProgressLine.
//
// Guards: expected GREEN in RED. ToolCallProgressSnapshot structurally
// cannot carry reasoning text (counts only), and the formatter renders the
// count, never content.

func TestFormatToolCallProgressLine_ReasoningRendersCountNotText(t *testing.T) {
	got := formatToolCallProgressLine(ToolCallProgressSnapshot{
		Name:           "bash",
		ArgsBytes:      0,
		TotalArgsBytes: 0,
		ReasoningBytes: 1234,
		Age:            2 * time.Second,
	})
	require.NotEmpty(t, got)
	assert.Contains(t, got, "1234 bytes of reasoning",
		"the progress line renders the reasoning BYTE COUNT")
	assert.NotContains(t, got, "sk-", "no credential-shaped text can appear - the line carries a count only")
}

func TestToolCallProgressSnapshot_CountsOnlyShape(t *testing.T) {
	// The snapshot type must not gain a field that could carry reasoning
	// TEXT: every field is numeric or the tool name. This is the invariant
	// item 24 pins at the type level.
	tp := reflect.TypeOf(ToolCallProgressSnapshot{})
	require.NotNil(t, tp)
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.Name == "Name" {
			continue // the tool name is the one string field
		}
		switch f.Type.Kind() {
		case reflect.Int, reflect.Int64, reflect.Float64, reflect.Bool:
			// counts / durations / flags - the legal shapes
		default:
			t.Errorf("ToolCallProgressSnapshot.%s is %s (%v) - only counts and the tool Name may "+
				"ride the snapshot; a text field here could carry reasoning content (item 24)", f.Name, f.Type.Kind(), f.Type)
		}
	}
}
