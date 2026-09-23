// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import (
	"reflect"
	"testing"
)

func TestTokenizeWords(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"simple words", "git commit -m msg", []string{"git", "commit", "-m", "msg"}},
		{"double-quoted word with a space", `git commit -m "fix: a bug"`, []string{"git", "commit", "-m", "fix: a bug"}},
		{"single-quoted word", `sh -c 'rm -rf /'`, []string{"sh", "-c", "rm -rf /"}},
		{"escaped space outside quotes", `a\ b c`, []string{"a b", "c"}},
		{"empty string", "", nil},
		{"only whitespace", "   \t  ", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tokenizeWords(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("tokenizeWords(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

func TestIsAssignment(t *testing.T) {
	cases := []struct {
		tok  string
		want bool
	}{
		{"VAR=value", true},
		{"X=1", true},
		{"_underscore=ok", true},
		{"echo", false},
		{"=noname", false},
		{"3ABC=bad", false}, // must not start with a digit
		{"", false},
	}
	for _, c := range cases {
		if got := isAssignment(c.tok); got != c.want {
			t.Errorf("isAssignment(%q) = %v, want %v", c.tok, got, c.want)
		}
	}
}

func TestSplitHeadArgs(t *testing.T) {
	cases := []struct {
		name     string
		seg      string
		wantHead string
		wantArgs []string
		wantOK   bool
	}{
		{"simple command", "git commit -m msg", "git", []string{"commit", "-m", "msg"}, true},
		{"leading single assignment", "X=1 echo hi", "echo", []string{"hi"}, true},
		{"leading multiple assignments", "A=1 B=2 echo hi", "echo", []string{"hi"}, true},
		{"pure assignments, no head", "A=1 B=2", "", nil, false},
		{"empty segment", "", "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			head, args, ok := splitHeadArgs(c.seg)
			if head != c.wantHead || ok != c.wantOK || !reflect.DeepEqual(args, c.wantArgs) {
				t.Errorf("splitHeadArgs(%q) = (%q, %v, %v), want (%q, %v, %v)",
					c.seg, head, args, ok, c.wantHead, c.wantArgs, c.wantOK)
			}
		})
	}
}
