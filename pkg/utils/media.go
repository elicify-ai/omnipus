package utils

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/pathsafe"
)

// IsPrivateLiteralHost returns true when the host is a literal IP that maps
// to an internal range (loopback, private, link-local, unspecified). It does
// NOT resolve hostnames — channels that download media from arbitrary
// caller-supplied URLs must perform their own resolution-and-recheck if they
// need full SSRF coverage. This helper exists to give DownloadFile a cheap
// "obvious mistake" guard so that a URL like http://127.0.0.1/secret is
// rejected before NewRequest gets a chance to dial it.
func IsPrivateLiteralHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

var audioExtensions = []string{".mp3", ".wav", ".ogg", ".m4a", ".flac", ".aac", ".wma"}

func AudioFormat(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	for _, supportedExt := range audioExtensions {
		if ext == supportedExt {
			return strings.TrimPrefix(ext, "."), nil
		}
	}

	return "", fmt.Errorf("unsupported audio format for %q", path)
}

// IsAudioFile checks if a file is an audio file based on its filename extension and content type.
func IsAudioFile(filename, contentType string) bool {
	audioTypes := []string{"audio/", "application/ogg", "application/x-ogg"}

	for _, ext := range audioExtensions {
		if strings.HasSuffix(strings.ToLower(filename), ext) {
			return true
		}
	}

	for _, audioType := range audioTypes {
		if strings.HasPrefix(strings.ToLower(contentType), audioType) {
			return true
		}
	}

	return false
}

// SanitizeFilename removes potentially dangerous characters from a filename
// and returns a safe version for local filesystem storage.
//
// This is one of the call sites that genuinely CANNOT reject: it sanitizes
// a filename an inbound channel attachment (Discord, Feishu, …) already
// carries from a remote server, and the bytes must be stored somehow
// regardless of what the sender named them. It therefore routes through
// pkg/pathsafe.SanitizeComponent — the shared, cross-platform-safe,
// single-pass rewriter every filename-accepting surface in Omnipus now
// uses — rather than this function's own former ad hoc, iterative
// substring removal (`strings.ReplaceAll(base, "..", "")` followed by
// separate slash/backslash replacement), which could reconstitute a
// dangerous sequence: four dots ("....") reduces, after removing two
// non-overlapping ".." matches, to nothing, but a name like "....//" was
// left as "//" by that first pass alone, relying entirely on the SEPARATE
// slash-replacement pass below it to finish the job — fragile, and exactly
// the "replace-by-substring" bug class pathsafe's package doc calls out.
// pathsafe.SanitizeComponent treats both '/' and '\' as separators
// unconditionally (not gated on runtime.GOOS, unlike path/filepath's own
// Base) — a filename arriving from a remote channel is not a local OS
// path, so it must be neutralized identically regardless of which OS the
// Omnipus binary happens to be running on.
//
// A caller can never be told "please rename your attachment and resend"
// (Sanitize can only rewrite, not reject), so any actual change is logged
// at WARN — never a silent swap — so an operator can trace an unexpected
// stored filename back to the original the sender used.
func SanitizeFilename(filename string) string {
	sanitized, changed := pathsafe.SanitizeComponent(filename)
	if changed {
		logger.WarnCF("utils", "sanitized unsafe filename for local storage", map[string]any{
			"original":  filename,
			"sanitized": sanitized,
		})
	}
	return sanitized
}

// DownloadOptions holds optional parameters for downloading files
type DownloadOptions struct {
	Timeout      time.Duration
	ExtraHeaders map[string]string
	LoggerPrefix string
	ProxyURL     string
}

// DownloadFile downloads a file from URL to a local temp directory.
// Returns the local file path or empty string on error.
func DownloadFile(urlStr, filename string, opts DownloadOptions) string {
	// Set defaults
	if opts.Timeout == 0 {
		opts.Timeout = 60 * time.Second
	}
	if opts.LoggerPrefix == "" {
		opts.LoggerPrefix = "utils"
	}

	mediaDir := media.TempDir()
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		logger.ErrorCF(opts.LoggerPrefix, "Failed to create media directory", map[string]any{
			"error": err.Error(),
		})
		return ""
	}

	// Generate unique filename with UUID prefix to prevent conflicts
	safeName := SanitizeFilename(filename)
	localPath := filepath.Join(mediaDir, uuid.New().String()[:8]+"_"+safeName)

	// Validate URL scheme + host before issuing the request. This is the
	// generic helper used by every channel; SSRF-sensitive callers should
	// supply an explicit allow-list via a pre-validated urlStr, but reject
	// the obvious internal-target cases here regardless. Only http/https
	// schemes are permitted, and literal IPs that are loopback / private /
	// link-local are refused. Hostnames that DNS-resolve to internal IPs
	// are NOT caught here — channels that handle untrusted source URLs
	// must do their own resolved-IP check.
	if parsedReqURL, parseURLErr := url.Parse(urlStr); parseURLErr != nil ||
		(parsedReqURL.Scheme != "http" && parsedReqURL.Scheme != "https") ||
		IsPrivateLiteralHost(parsedReqURL.Hostname()) {
		logger.ErrorCF(opts.LoggerPrefix, "Refused download URL — invalid scheme or internal host", map[string]any{
			"url": urlStr,
		})
		return ""
	}

	// Create HTTP request
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		logger.ErrorCF(opts.LoggerPrefix, "Failed to create download request", map[string]any{
			"error": err.Error(),
		})
		return ""
	}

	// Add extra headers (e.g., Authorization for Slack)
	for key, value := range opts.ExtraHeaders {
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: opts.Timeout}
	if opts.ProxyURL != "" {
		proxyURL, parseErr := url.Parse(opts.ProxyURL)
		if parseErr != nil {
			logger.ErrorCF(opts.LoggerPrefix, "Invalid proxy URL for download", map[string]any{
				"error": parseErr.Error(),
				"proxy": opts.ProxyURL,
			})
			return ""
		}
		client.Transport = &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		logger.ErrorCF(opts.LoggerPrefix, "Failed to download file", map[string]any{
			"error": err.Error(),
			"url":   urlStr,
		})
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.ErrorCF(opts.LoggerPrefix, "File download returned non-200 status", map[string]any{
			"status": resp.StatusCode,
			"url":    urlStr,
		})
		return ""
	}

	out, err := os.Create(localPath)
	if err != nil {
		logger.ErrorCF(opts.LoggerPrefix, "Failed to create local file", map[string]any{
			"error": err.Error(),
		})
		return ""
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(localPath)
		logger.ErrorCF(opts.LoggerPrefix, "Failed to write file", map[string]any{
			"error": err.Error(),
		})
		return ""
	}

	logger.DebugCF(opts.LoggerPrefix, "File downloaded successfully", map[string]any{
		"path": localPath,
	})

	return localPath
}
