//go:build bedrock

package bedrock

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestConvertMessages_ToolImageNativeBlock(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	messages, _ := convertMessages([]Message{{Role: "tool", ToolCallID: "call-bed", Content: "marker", Media: []string{dataURL}}})
	if len(messages) != 1 || len(messages[0].Content) != 1 {
		t.Fatalf("messages=%#v", messages)
	}
	tool, ok := messages[0].Content[0].(*types.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("block=%T", messages[0].Content[0])
	}
	if tool.Value.ToolUseId == nil || *tool.Value.ToolUseId != "call-bed" || len(tool.Value.Content) != 2 {
		t.Fatalf("tool=%#v", tool.Value)
	}
	imageBlock, ok := tool.Value.Content[1].(*types.ToolResultContentBlockMemberImage)
	if !ok {
		t.Fatalf("image block=%T", tool.Value.Content[1])
	}
	bytesSource, ok := imageBlock.Value.Source.(*types.ImageSourceMemberBytes)
	if !ok {
		t.Fatalf("source=%T", imageBlock.Value.Source)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(bytesSource.Value))
	if err != nil || cfg.Width != 1 || cfg.Height != 1 {
		t.Fatalf("config=%#v err=%v", cfg, err)
	}
}
