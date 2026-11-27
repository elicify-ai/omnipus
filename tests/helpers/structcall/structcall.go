// Package structcall calls a production function that takes structs and returns
// one struct, naming the fields by their exported names instead of by type.
//
// Why it exists. The two production authorities the Skills & Tools tests pin
// (tools.ResolveSkillDecision for skills, tools.ToolSurface.Plan for tool
// levels) take and return production structs. A test file that names those
// types does not compile on a tree that does not have them yet, so a tree
// without the backend could not even show the RED run. The seam is therefore
// held as an `any` (checked first with tests/helpers/nativebinding, which
// accepts only a direct named production function), and this package fills the
// argument structs and reads the result struct by field name. A field the bound
// function does not have is a loud BLOCKED, never a silent zero.
//
// It decides nothing and computes no expected value: callers keep their spec
// tables and compare what comes back. Named string and bool types (for example
// config.ToolPolicy) are filled from plain strings and bools, and come back as
// plain strings, so a table row can stay written in the spec's own words.
package structcall

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

// Fields is the exported fields of a struct, by field name.
type Fields map[string]any

// Invoke calls fn with args and returns the fields of its one struct result.
// A Fields argument fills a fresh value of the parameter's struct type; any
// other argument is converted to the parameter's type when both have the same
// kind (a plain string for a named string type).
func Invoke(fn any, args ...any) (Fields, error) {
	value := reflect.ValueOf(fn)
	if !value.IsValid() || value.Kind() != reflect.Func || value.IsNil() {
		return nil, fmt.Errorf("no function is bound")
	}
	fnType := value.Type()
	if fnType.IsVariadic() || fnType.NumIn() != len(args) || fnType.NumOut() != 1 || fnType.Out(0).Kind() != reflect.Struct {
		return nil, fmt.Errorf("the bound function has the shape %s, but this call passes %d argument(s) and reads one struct result", fnType, len(args))
	}
	in := make([]reflect.Value, len(args))
	for i, arg := range args {
		built, err := argument(fnType.In(i), arg)
		if err != nil {
			return nil, fmt.Errorf("argument %d of %s: %w", i, fnType, err)
		}
		in[i] = built
	}
	return fieldsOf(value.Call(in)[0]), nil
}

// Call is Invoke that ends the calling test with BLOCKED when the bound
// function does not have the shape the caller needs.
func Call(t testing.TB, fn any, args ...any) Fields {
	t.Helper()
	out, err := Invoke(fn, args...)
	if err != nil {
		t.Fatalf("BLOCKED: the bound production function does not have the shape this test calls it with: %v", err)
	}
	return out
}

// Str returns the string field name; the test ends when it is absent or not a string.
func (f Fields) Str(t testing.TB, name string) string {
	t.Helper()
	raw, present := f[name]
	if !present {
		t.Fatalf("BLOCKED: the production result has no field %q (it has %v)", name, f.names())
	}
	text, isString := raw.(string)
	if !isString {
		t.Fatalf("BLOCKED: the production result field %q is a %T, want a string", name, raw)
	}
	return text
}

// Bool returns the bool field name; the test ends when it is absent or not a bool.
func (f Fields) Bool(t testing.TB, name string) bool {
	t.Helper()
	raw, present := f[name]
	if !present {
		t.Fatalf("BLOCKED: the production result has no field %q (it has %v)", name, f.names())
	}
	flag, isBool := raw.(bool)
	if !isBool {
		t.Fatalf("BLOCKED: the production result field %q is a %T, want a bool", name, raw)
	}
	return flag
}

func (f Fields) names() []string {
	names := make([]string, 0, len(f))
	for name := range f {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func argument(want reflect.Type, arg any) (reflect.Value, error) {
	built := reflect.New(want).Elem()
	fields, isFields := arg.(Fields)
	if !isFields {
		if err := assign(built, arg); err != nil {
			return reflect.Value{}, err
		}
		return built, nil
	}
	if want.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("the parameter is a %s, not a struct, so it cannot be filled by field name", want)
	}
	for name, raw := range fields {
		field := built.FieldByName(name)
		if !field.IsValid() || !field.CanSet() {
			return reflect.Value{}, fmt.Errorf("%s has no settable field %q", want, name)
		}
		if err := assign(field, raw); err != nil {
			return reflect.Value{}, fmt.Errorf("field %q: %w", name, err)
		}
	}
	return built, nil
}

// assign stores raw in dst when it is assignable, or when both have the same
// kind (a plain string into a named string type, a plain bool into a named
// bool). It never converts across kinds (an int would become a rune string).
func assign(dst reflect.Value, raw any) error {
	source := reflect.ValueOf(raw)
	if !source.IsValid() {
		return fmt.Errorf("a nil value cannot be stored in a %s", dst.Type())
	}
	switch {
	case source.Type().AssignableTo(dst.Type()):
		dst.Set(source)
	case source.Kind() == dst.Kind() && source.Type().ConvertibleTo(dst.Type()):
		dst.Set(source.Convert(dst.Type()))
	default:
		return fmt.Errorf("a %s cannot be stored in a %s", source.Type(), dst.Type())
	}
	return nil
}

func fieldsOf(result reflect.Value) Fields {
	out := Fields{}
	for i := range result.NumField() {
		field := result.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		value := result.Field(i)
		if value.Kind() == reflect.String {
			out[field.Name] = value.String() // a named string type reads back as a plain string
			continue
		}
		out[field.Name] = value.Interface()
	}
	return out
}
