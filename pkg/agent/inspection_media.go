package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/media/resize"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// attachInspectionImages builds the active provider-request form from the
// canonical text-only message. Callers persist canonical and discard live at
// turn end, so image bytes and snapshot references never enter history.
func attachInspectionImages(ctx context.Context, canonical providers.Message, images []tools.InspectionImage, supportsImages bool) (providers.Message, error) {
	return attachInspectionImagesWithBudget(ctx, canonical, images, supportsImages, catalog.DefaultResizeLimits)
}

func attachInspectionImagesWithBudget(ctx context.Context, canonical providers.Message, images []tools.InspectionImage, supportsImages bool, budget catalog.ResizeLimits) (providers.Message, error) {
	live := canonical
	live.Media = nil
	if len(images) == 0 {
		return live, nil
	}
	if !supportsImages {
		live.Content += "\n[visual inspection unavailable: selected model does not support image input; choose a vision-capable model]"
		return live, nil
	}
	// A denied recheck downgrades the entire attachment to a text notice
	// rather than failing the turn; the downgrade is applied after the loop.
	var denied error
	for _, inspection := range images {
		if inspection.Reauthorize == nil {
			return live, fmt.Errorf("inspection image access cannot be rechecked")
		}
		if reauthErr := inspection.Reauthorize(ctx); reauthErr != nil {
			denied = reauthErr
			break
		}
		if len(inspection.Bytes) == 0 || !strings.HasPrefix(inspection.MIMEType, "image/") {
			return live, fmt.Errorf("invalid inspection image")
		}
		decoded, _, err := image.Decode(bytes.NewReader(inspection.Bytes))
		if err != nil {
			return live, fmt.Errorf("invalid inspection image: %w", err)
		}
		normalized, err := resize.ResizeToFit(decoded, budget)
		if err != nil {
			return live, fmt.Errorf("image cannot fit model limits: %w", err)
		}
		bounds := decoded.Bounds()
		presented, _, decodeErr := image.DecodeConfig(bytes.NewReader(normalized.Data))
		if decodeErr != nil {
			return live, fmt.Errorf("invalid normalized inspection image: %w", decodeErr)
		}
		if bounds.Dx() != presented.Width || bounds.Dy() != presented.Height {
			live.Content += fmt.Sprintf("\n[presented to model: %dx%d, resized from %dx%d]", presented.Width, presented.Height, bounds.Dx(), bounds.Dy())
		} else {
			live.Content += fmt.Sprintf("\n[presented to model: %dx%d]", presented.Width, presented.Height)
		}
		live.Media = append(live.Media, "data:"+normalized.Mime+";base64,"+base64.StdEncoding.EncodeToString(normalized.Data))
	}
	if denied != nil {
		live.Media = nil
		live.Content += "\n[visual inspection unavailable: inspection image access denied: " + denied.Error() + "]"
	}
	return live, nil
}

func attachTurnInspectionImagesWithBudget(ctx context.Context, messages []providers.Message, byToolCall map[string][]tools.InspectionImage, supportsImages bool, budget catalog.ResizeLimits) ([]providers.Message, error) {
	if len(byToolCall) == 0 {
		return messages, nil
	}
	live := append([]providers.Message(nil), messages...)
	for i := range live {
		images := byToolCall[live[i].ToolCallID]
		if len(images) == 0 {
			continue
		}
		var err error
		live[i], err = attachInspectionImagesWithBudget(ctx, live[i], images, supportsImages, budget)
		if err != nil {
			return nil, err
		}
	}
	return live, nil
}
