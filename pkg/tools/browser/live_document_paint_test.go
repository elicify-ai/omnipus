package browser

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
)

func TestDocumentPaintChecksOriginalDocumentAroundIsolatedPaint(t *testing.T) {
	for _, scenario := range []string{"current", "changed before", "changed during", "paint exception", "paint transport failure"} {
		t.Run(scenario, func(t *testing.T) {
			var methods []string
			reads := 0
			transportFailure := errors.New("paint transport failed")
			executor := liveInputExecutor(func(_ context.Context, method string, params, result any) error {
				methods = append(methods, method)
				switch method {
				case "Page.getFrameTree":
					reads++
					loader := cdp.LoaderID("document-a")
					if scenario == "changed before" || scenario == "changed during" && reads == 2 {
						loader = "document-b"
					}
					result.(*page.GetFrameTreeReturns).FrameTree = &page.FrameTree{Frame: &cdp.Frame{ID: "main", LoaderID: loader}}
				case "Page.createIsolatedWorld":
					p := params.(*page.CreateIsolatedWorldParams)
					if p.FrameID != "main" || p.WorldName == "" || p.GrantUniveralAccess {
						t.Fatalf("paint world is not restricted to original main frame: %+v", p)
					}
					result.(*page.CreateIsolatedWorldReturns).ExecutionContextID = 71
				case "Runtime.evaluate":
					p := params.(*runtime.EvaluateParams)
					if p.ContextID != 71 || !p.AwaitPromise {
						t.Fatalf("paint did not await isolated execution context: %+v", p)
					}
					if scenario == "paint transport failure" {
						return transportFailure
					}
					if scenario == "paint exception" {
						result.(*runtime.EvaluateReturns).ExceptionDetails = &runtime.ExceptionDetails{Text: "document gone"}
					}
				default:
					t.Fatalf("unexpected protocol operation: %s", method)
				}
				return nil
			})
			err := (documentPaintAction{frameID: "main", loaderID: "document-a"}).Do(cdp.WithExecutor(context.Background(), executor))
			if scenario == "current" {
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"Page.getFrameTree", "Page.createIsolatedWorld", "Runtime.evaluate", "Page.getFrameTree"}
				if !reflect.DeepEqual(methods, want) {
					t.Fatalf("paint proof order %v, want %v", methods, want)
				}
			} else if err == nil {
				t.Fatal("obsolete or failed paint was accepted")
			}
			if scenario == "changed before" && len(methods) != 1 {
				t.Fatalf("paint entered a replacement document: %v", methods)
			}
			if scenario == "paint transport failure" && !errors.Is(err, transportFailure) {
				t.Fatalf("paint failure identity lost: %v", err)
			}
		})
	}
}
