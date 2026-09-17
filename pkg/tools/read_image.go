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
)

var ErrImageSourceNotRegular = errors.New("image source must be a regular file")

const (
	MaxInspectionImageBytes  = 20 * 1024 * 1024
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

func inspectionImageResult(ctx context.Context, file fs.File, source string, sniff []byte, pagination bool, reauthorize func(context.Context) error) (*ToolResult, bool) {
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
	if info.Size() > MaxInspectionImageBytes {
		return ErrorResult("image exceeds byte limit"), true
	}
	seeker, ok := file.(io.Seeker)
	if !ok {
		return ErrorResult("image source must be a regular file"), true
	}
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		return ErrorResult(fmt.Sprintf("invalid image: %v", err)), true
	}
	limited := io.LimitReader(file, MaxInspectionImageBytes+1)
	data, err := io.ReadAll(&contextReader{ctx: ctx, r: limited})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ErrorResult(err.Error()), true
		}
		return ErrorResult(fmt.Sprintf("invalid image: %v", err)), true
	}
	if len(data) > MaxInspectionImageBytes {
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
	sum := sha256.Sum256(data)
	marker := fmt.Sprintf("[image: %s | original: %dx%d | presented: %dx%d | sha256: %s | not retained; re-read to view]", filepath.Base(source), cfg.Width, cfg.Height, cfg.Width, cfg.Height, hex.EncodeToString(sum[:]))
	return &ToolResult{ForLLM: marker, InspectionImages: []InspectionImage{{Bytes: data, MIMEType: mime, Source: source, SHA256: hex.EncodeToString(sum[:]), OriginalWidth: cfg.Width, OriginalHeight: cfg.Height, Reauthorize: reauthorize}}}, true
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
