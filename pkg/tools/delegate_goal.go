package tools

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

func parseDelegateGoal(raw any) (*steer.GoalSpec, error) {
	if raw == nil {
		return nil, nil
	}
	goalMap, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("must be an object")
	}
	criteria, err := parseDelegateCriteria(goalMap["criteria"], "criteria")
	if err != nil {
		return nil, err
	}
	dod, err := parseDelegateCriteria(goalMap["dod"], "dod")
	if err != nil {
		return nil, err
	}
	if len(criteria) == 0 && len(dod) == 0 {
		return nil, fmt.Errorf("must contain at least one criterion or definition-of-done item")
	}
	return &steer.GoalSpec{Criteria: criteria, DoD: dod}, nil
}

func parseDelegateCriteria(raw any, field string) ([]steer.Criterion, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", field)
	}
	out := make([]steer.Criterion, 0, len(items))
	for i, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be an object", field, i)
		}
		criterion, err := parseDelegateCriterion(item)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", field, i, err)
		}
		out = append(out, criterion)
	}
	return out, nil
}

func parseDelegateCriterion(item map[string]any) (steer.Criterion, error) {
	text, _ := item["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return steer.Criterion{}, fmt.Errorf("text is required")
	}
	kind, _ := item["kind"].(string)
	if kind == "" {
		if item["check"] != nil {
			kind = string(steer.CriterionKindCheck)
		} else {
			kind = string(steer.CriterionKindProse)
		}
	}
	criterion := steer.Criterion{
		Kind: steer.CriterionKind(kind),
		Text: text,
	}
	judgment, _ := item["judgment"].(string)
	if judgment == "" {
		judgment = string(steer.JudgmentBoolean)
	}
	criterion.Judgment = steer.JudgmentKind(judgment)

	switch criterion.Kind {
	case steer.CriterionKindProse:
		if item["check"] != nil {
			return steer.Criterion{}, fmt.Errorf("check must be omitted for prose criteria")
		}
	case steer.CriterionKindCheck:
		rawCheck, ok := item["check"].(map[string]any)
		if !ok {
			return steer.Criterion{}, fmt.Errorf("check is required for check criteria")
		}
		command, _ := rawCheck["command"].(string)
		command = strings.TrimSpace(command)
		if command == "" {
			return steer.Criterion{}, fmt.Errorf("check.command is required")
		}
		expected, err := integerArgument(rawCheck["expected_exit_code"])
		if err != nil || expected < 0 || expected > 255 {
			return steer.Criterion{}, fmt.Errorf("check.expected_exit_code must be an integer from 0 to 255")
		}
		criterion.Check = &steer.CriterionCheck{Command: command, ExpectedExitCode: expected}
	default:
		return steer.Criterion{}, fmt.Errorf("kind must be %q or %q", steer.CriterionKindProse, steer.CriterionKindCheck)
	}

	switch criterion.Judgment {
	case steer.JudgmentBoolean, steer.JudgmentQuantitative, steer.JudgmentArtifact:
	default:
		return steer.Criterion{}, fmt.Errorf("judgment must be boolean, quantitative, or artifact")
	}
	return criterion, nil
}

func integerArgument(raw any) (int, error) {
	switch value := raw.(type) {
	case int:
		return value, nil
	case float64:
		integer := int(value)
		if float64(integer) != value {
			return 0, fmt.Errorf("not an integer")
		}
		return integer, nil
	default:
		return 0, fmt.Errorf("not an integer")
	}
}
