// Package nativebinding checks the provenance of test input/action bindings.
// It never implements a product action or computes an expected product result.
package nativebinding

import (
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Validate accepts only a direct, named function/method in the specified
// production module. A test closure that deletes files, returns a verdict or
// changes a fake clock cannot masquerade as the real input/action path.
func Validate(module string, fn any) (string, error) {
	value := reflect.ValueOf(fn)
	if !value.IsValid() || value.Kind() != reflect.Func || value.IsNil() {
		return "", fmt.Errorf("no native function is bound")
	}
	native := runtime.FuncForPC(value.Pointer())
	if native == nil {
		return "", fmt.Errorf("binding has no named runtime function")
	}
	name := native.Name()
	file, _ := native.FileLine(value.Pointer())
	file = filepath.ToSlash(file)
	if strings.HasSuffix(file, "_test.go") {
		return "", fmt.Errorf("binding source is test code: %s", name)
	}
	if !strings.HasPrefix(name, "github.com/elicify-ai/omnipus/"+module+".") ||
		!strings.Contains(file, "/"+module+"/") || !strings.HasSuffix(file, ".go") {
		return "", fmt.Errorf("binding is not from production module %s: %s", module, name)
	}
	if strings.Contains(name, ".func") || strings.HasSuffix(name, "-fm") {
		return "", fmt.Errorf("binding must be a named native function or method expression, not a closure: %s", name)
	}
	return file + "::" + name, nil
}

// Require logs the actual native path. Until the native input exists the test
// fails loudly; it cannot be unblocked by assigning an arbitrary test callback.
func Require(t testing.TB, module string, fn any, obligation string) string {
	t.Helper()
	path, err := Validate(module, fn)
	if err != nil {
		t.Fatalf("BLOCKED: native binding not implemented (%v) — required by %s", err, obligation)
	}
	t.Logf("NATIVE_BINDING: %s", path)
	return path
}
