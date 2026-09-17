package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestAttachInspectionImages_LiveOnlyAndRechecksAccess(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	img.Set(0, 0, color.RGBA{R: 0x44, A: 0xff})
	var fixture bytes.Buffer
	if err := png.Encode(&fixture, img); err != nil {
		t.Fatal(err)
	}
	pngBytes := fixture.Bytes()
	checks := 0
	images := []tools.InspectionImage{{Bytes: pngBytes, MIMEType: "image/png", Source: "page.png", Reauthorize: func(context.Context) error { checks++; return nil }}}
	canonical := providers.Message{Role: "tool", Content: "not retained; re-read to view", ToolCallID: "call-7"}
	live, err := attachInspectionImages(context.Background(), canonical, images, true)
	if err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatalf("access checks = %d, want 1", checks)
	}
	if len(live.Media) != 1 || !strings.HasPrefix(live.Media[0], "data:image/png;base64,") {
		t.Fatalf("live media = %#v", live.Media)
	}
	normalized, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(live.Media[0], "data:image/png;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(normalized))
	if err != nil || cfg.Width != 32 || cfg.Height != 24 {
		t.Fatalf("normalized config=%#v err=%v", cfg, err)
	}
	if len(canonical.Media) != 0 || !bytes.Equal(images[0].Bytes, pngBytes) {
		t.Fatal("canonical record or snapshot mutated")
	}
}

func TestAttachInspectionImages_UsesActualCandidateBudget(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 512, 256))
	var fixture bytes.Buffer
	if err := png.Encode(&fixture, img); err != nil {
		t.Fatal(err)
	}
	input := tools.InspectionImage{Bytes: fixture.Bytes(), MIMEType: "image/png", Reauthorize: func(context.Context) error { return nil }}
	live, err := attachInspectionImagesWithBudget(context.Background(), providers.Message{Role: "tool"}, []tools.InspectionImage{input}, true, catalog.ResizeLimits{LongEdgePx: 384, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(live.Content, "presented to model: 384x192, resized from 512x256") {
		t.Fatalf("content=%q", live.Content)
	}
}

func TestAttachInspectionImages_DeniedOrNonVisionHasNoBytes(t *testing.T) {
	image := tools.InspectionImage{Bytes: []byte("secret"), MIMEType: "image/png", Reauthorize: func(context.Context) error { return errors.New("outside workspace") }}
	denied, err := attachInspectionImages(context.Background(), providers.Message{Role: "tool", ToolCallID: "call-8"}, []tools.InspectionImage{image}, true)
	if err != nil || len(denied.Media) != 0 || !strings.Contains(denied.Content, "outside workspace") {
		t.Fatalf("denial=%#v error=%v", denied, err)
	}
	nonVision, err := attachInspectionImages(context.Background(), providers.Message{Role: "tool", ToolCallID: "call-8"}, []tools.InspectionImage{image}, false)
	if err != nil || len(nonVision.Media) != 0 || !strings.Contains(nonVision.Content, "does not support image input") {
		t.Fatalf("non-vision = %#v err=%v", nonVision, err)
	}
}
