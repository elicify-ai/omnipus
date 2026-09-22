package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

var ErrImageSourceNotRegular = errors.New("image source must be a regular file")

const (
	MaxInspectionImageBytes  = config.DefaultMaxMediaSize
	MaxInspectionImagePixels = 16 * 1024 * 1024
)

func imageFormat(sniff []byte, name string) (mime string, candidate, unsupported bool) {
	if len(sniff) >= 8 && string(sniff[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png", true, false
	}
	if len(sniff) >= 3 && sniff[0] == 0xff && sniff[1] == 0xd8 && sniff[2] == 0xff {
		return "image/jpeg", true, false
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".png" {
		return "image/png", true, false
	}
	if ext == ".jpg" || ext == ".jpeg" {
		return "image/jpeg", true, false
	}
	if ext == ".heic" || ext == ".heif" || (len(sniff) >= 12 && string(sniff[4:12]) == "ftypheic") {
		return "", true, true
	}
	return "", false, false
}

func inspectionImageResult(ctx context.Context, file fs.File, source string, sniff []byte, pagination bool, maxBytes int64, reauthorize func(context.Context) error) (*ToolResult, bool) {
	mime, candidate, unsupported := imageFormat(sniff, source)
	if !candidate {
		return nil, false
	}
	if unsupported {
		return ErrorResult("unsupported image format"), true
	}
	if pagination {
		return ErrorResult("image pagination is not supported; omit offset and length"), true
	}
	info, err := file.Stat()
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid image: %v", err)), true
	}
	if !info.Mode().IsRegular() {
		return ErrorResult("image source must be a regular file"), true
	}
	if info.Size() > maxBytes {
		return ErrorResult("image exceeds byte limit"), true
	}
	remaining := maxBytes + 1 - int64(len(sniff))
	if remaining < 0 {
		return ErrorResult("image exceeds byte limit"), true
	}
	tail, err := io.ReadAll(&contextReader{ctx: ctx, r: io.LimitReader(file, remaining)})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ErrorResult(err.Error()), true
		}
		return ErrorResult(fmt.Sprintf("invalid image: %v", err)), true
	}
	// A canceled context can race a legitimate io.EOF from io.LimitReader:
	// the bounded read loop may consume its final, in-limit chunk and see a
	// clean end-of-stream before the next contextReader.Read call would have
	// observed ctx.Done(). Re-check here so cancellation deterministically
	// wins over the byte-limit outcome below, instead of depending on which
	// one the scheduler happens to observe first.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ErrorResult(ctxErr.Error()), true
	}
	data := append(append([]byte(nil), sniff...), tail...)
	if int64(len(data)) > maxBytes {
		return ErrorResult("image exceeds byte limit"), true
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid image: %v", err)), true
	}
	if (mime == "image/png" && format != "png") || (mime == "image/jpeg" && format != "jpeg") {
		return ErrorResult("invalid image: format mismatch"), true
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return ErrorResult("invalid image: non-positive dimensions"), true
	}
	if cfg.Width > MaxInspectionImagePixels/cfg.Height {
		return ErrorResult("image exceeds pixel limit"), true
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return ErrorResult(fmt.Sprintf("invalid image: %v", err)), true
	}
	sum := sha256.Sum256(data)
	marker := fmt.Sprintf("[image: %s | original: %dx%d | sha256: %s | not retained; re-read to view]", source, cfg.Width, cfg.Height, hex.EncodeToString(sum[:]))
	return &ToolResult{ForLLM: marker, InspectionImages: []InspectionImage{{Bytes: data, MIMEType: mime, Reauthorize: reauthorize}}}, true
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}
