// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import "strings"

// Platform selects which decision shape EvaluateCommand applies. ADR-092
// D3, founder decision: splitShellSegments is a POSIX operator set that
// does not model PowerShell grammar, and there is no argv[0] slot on
// Windows either, so Windows rules/grants are exact-command only.
type Platform int

const (
	// POSIX is the default: chained-command splitting, and ArgPrefix
	// matched token-boundary as a prefix.
	POSIX Platform = iota
	// WindowsPlatform disables segment splitting (the whole command is one
	// unit) and ArgPrefix, if set, must equal the full remaining argument
	// text exactly — FR-041.
	WindowsPlatform
)

// PlatformForGOOS maps a runtime.GOOS value to a Platform, so a caller can
// write PlatformForGOOS(runtime.GOOS) rather than duplicating the
// `== "windows"` check.
func PlatformForGOOS(goos string) Platform {
	if goos == "windows" {
		return WindowsPlatform
	}
	return POSIX
}

// Options configures EvaluateCommand. Segmenter and HeadResolver are the
// injected pkg/tools functions (see doc.go); Resolve defaults to
// ResolveBinary when nil. ChildPath is the PATH the spawned process will
// actually search (the untrusted value — a session/agent may have altered
// it); TrustedPath is the PATH an operator rule's Binary field resolves
// against (ADR-092 D3's look-alike defence: the two are deliberately kept
// separate, see doc.go and Verify).
type Options struct {
	Platform     Platform
	Segmenter    Segmenter
	HeadResolver HeadResolver
	Resolve      BinaryResolver
	ChildPath    string
	TrustedPath  string
}

// SegmentVerdict is the D3 decision for one chained-command segment (the
// whole command, on Windows — see Platform).
type SegmentVerdict struct {
	Segment      string
	Head         string
	ResolvedPath string
	MatchedRule  *Rule
	Action       Action
	Blind        bool
	BlindReason  string
}

// CommandVerdict is EvaluateCommand's result: Action is the strictest
// action across every segment (deny beats ask beats allow beats none —
// ActionNone means no D3 rule applied to that segment and D3 defers to the
// caller's ceiling policy for it, not that the segment is denied); Segments
// carries each segment's own verdict for audit/UI purposes (ADR-092
// FR-032/FR-027 — per-segment display) and for FullyAllowed.
type CommandVerdict struct {
	Action   Action
	Segments []SegmentVerdict
}

// FullyAllowed reports whether every segment independently cleared via an
// explicit ALLOW rule match — ADR-092 D3's replacement for the retired
// per-binary exec allowlist, which must pre-approve a WHOLE chained
// command, not merely part of it ("each segment must independently clear,
// or the whole call asks", FR-020). A single segment with no matching rule
// (ActionNone) or a blind spot is enough to make this false, even when
// Action alone would still report ActionAllow for the same CommandVerdict
// (a "none" segment ranks below "allow" in Action's own combination) — the
// two fields answer different questions: Action is "does anything here
// need a deny/ask override applied on top of the ceiling", FullyAllowed is
// "can this whole chain be silently approved without falling back to the
// ceiling for any part of it". A command with zero segments (should not
// happen — even an empty string segments to one empty segment) is
// conservatively not fully allowed.
func (v CommandVerdict) FullyAllowed() bool {
	if len(v.Segments) == 0 {
		return false
	}
	for _, s := range v.Segments {
		if s.Blind || s.Action != ActionAllow {
			return false
		}
	}
	return true
}

// EvaluateCommand is the D3 entry point: split command into segments
// (skipped on Windows — the whole string is one segment), resolve and
// match each segment against rules, and combine per-segment verdicts into
// one overall Action. "Each segment must independently clear, or the whole
// call asks" (ADR-092 D3) is realised by Decide's deny > ask > allow > none
// precedence applied across segments, where a blind spot (FR-020) is
// treated as an ask verdict for its segment, never as "no rule applied".
func EvaluateCommand(command string, rules []Rule, opts Options) CommandVerdict {
	resolve := opts.Resolve
	if resolve == nil {
		resolve = ResolveBinary
	}

	var segments []string
	if opts.Platform == WindowsPlatform {
		segments = []string{command}
	} else {
		segments = opts.Segmenter(command)
	}

	verdicts := make([]SegmentVerdict, 0, len(segments))
	overall := ActionNone
	for _, seg := range segments {
		v := evaluateSegment(seg, rules, opts, resolve)
		verdicts = append(verdicts, v)
		overall = strictestAction(overall, v.Action)
	}
	return CommandVerdict{Action: overall, Segments: verdicts}
}

// strictestAction returns whichever of a, b outranks the other under
// deny > ask > allow > none.
func strictestAction(a, b Action) Action {
	if actionRank(b) > actionRank(a) {
		return b
	}
	return a
}

func actionRank(a Action) int {
	switch a {
	case ActionDeny:
		return 3
	case ActionAsk:
		return 2
	case ActionAllow:
		return 1
	default:
		return 0
	}
}

// evaluateSegment resolves seg's head, resolves-and-verifies it against
// opts.ChildPath, and matches it against rules.
func evaluateSegment(seg string, rules []Rule, opts Options, resolve BinaryResolver) SegmentVerdict {
	v := SegmentVerdict{Segment: seg}

	head, reason := classifySegment(seg, opts.HeadResolver, false)
	if reason != blindNone {
		v.Blind = true
		v.BlindReason = string(reason)
		v.Action = ActionAsk
		return v
	}
	v.Head = head

	resolvedHead, err := resolve(head, opts.ChildPath)
	if err != nil {
		// The command that will actually run does not resolve to anything
		// on the child's own PATH — fail safe to ask, never to a rule
		// match, exactly like FR-020's other blind spots.
		v.Blind = true
		v.BlindReason = "binary does not resolve on the child PATH: " + err.Error()
		v.Action = ActionAsk
		return v
	}
	v.ResolvedPath = resolvedHead

	_, args, _ := splitHeadArgs(seg)
	matched := matchRules(resolvedHead, args, seg, rules, opts, resolve)
	v.Action = Decide(matched)
	if v.Action != ActionNone {
		if r := representativeRule(matched, v.Action); r != nil {
			v.MatchedRule = r
		}
	}
	return v
}

// representativeRule returns a pointer to the first rule in matched whose
// Action equals action — the rule that actually drove the returned
// verdict, useful for the FR-032 audit event's "resolved binary" detail.
func representativeRule(matched []Rule, action Action) *Rule {
	for i := range matched {
		if matched[i].Action == action {
			return &matched[i]
		}
	}
	return nil
}

// matchRules returns every rule in rules that matches resolvedHead/args:
// the rule's own Binary field resolves (against opts.TrustedPath — never
// opts.ChildPath, the look-alike defence) to the same absolute path as
// resolvedHead, and its ArgPrefix (if any) matches args under the
// platform's matching mode (prefix on POSIX, exact on Windows — FR-041).
func matchRules(resolvedHead string, args []string, seg string, rules []Rule, opts Options, resolve BinaryResolver) []Rule {
	var out []Rule
	for _, r := range rules {
		if !r.Action.Valid() {
			continue
		}
		ruleResolved, err := resolve(r.Binary, opts.TrustedPath)
		if err != nil || ruleResolved != resolvedHead {
			continue
		}
		// FR-040: an ALLOW rule must never be satisfied from a normalised
		// head (case-folded or directory-prefix-stripped) — that is
		// precisely the shape a look-alike binary exploits. DENY/ASK are
		// unaffected: matching them off a normalised head only makes the
		// engine MORE cautious, never less.
		if r.Action == ActionAllow {
			if _, reason := classifySegment(seg, opts.HeadResolver, true); reason == blindNormalisedHead {
				continue
			}
		}
		if opts.Platform == WindowsPlatform {
			if r.ArgPrefix != "" && !tokenBoundaryExact(args, r.ArgPrefix) {
				continue
			}
		} else if !tokenBoundaryHasPrefix(args, strings.Fields(r.ArgPrefix)) {
			continue
		}
		out = append(out, r)
	}
	return out
}
