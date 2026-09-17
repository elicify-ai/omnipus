package browser

import (
	"context"
	"fmt"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
)

// A paint opportunity is necessary before requesting a new capture. Input
// still waits for the capture's media boundary; this action never marks it ready.
type documentPaintAction struct {
	frameID  cdp.FrameID
	loaderID cdp.LoaderID
}

func (a documentPaintAction) Do(ctx context.Context) error {
	if err := a.checkDocument(ctx); err != nil {
		return err
	}
	world, err := page.CreateIsolatedWorld(a.frameID).WithWorldName("omnipus-document-frame").Do(ctx)
	if err != nil {
		return err
	}
	_, exception, err := runtime.Evaluate("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve(true))))").WithContextID(world).WithAwaitPromise(true).Do(ctx)
	if err != nil {
		return err
	}
	if exception != nil {
		return fmt.Errorf("browser live: document paint evaluation failed: %s", exception.Text)
	}
	return a.checkDocument(ctx)
}

func (a documentPaintAction) checkDocument(ctx context.Context) error {
	tree, err := page.GetFrameTree().Do(ctx)
	if err != nil {
		return err
	}
	if tree == nil || tree.Frame == nil || tree.Frame.ID != a.frameID || tree.Frame.LoaderID != a.loaderID {
		return ErrStaleCaptureFrame
	}
	return nil
}
