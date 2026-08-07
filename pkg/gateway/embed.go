package gateway

import (
	"compress/gzip"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
)

// spaFS is the embedded SPA filesystem.
// Requires the spa/ directory to exist at build time (run 'pnpm build' first).
//
//go:embed all:spa
var spaFS embed.FS

// newSPAHandler returns an http.Handler that serves the embedded SPA,
// or nil if no SPA was embedded at build time.
func newSPAHandler() http.Handler {
	sub, err := fs.Sub(spaFS, "spa")
	if err != nil {
		// No embedded SPA - return nil to signal gateway to skip registration
		return nil
	}
	fileServer := http.FileServer(http.FS(sub))

	spaHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Check if the file exists in the embedded FS
		cleanPath := strings.TrimPrefix(path, "/")
		if _, err := fs.Stat(sub, cleanPath); err == nil {
			switch {
			case cleanPath == "index.html" || cleanPath == "":
				// index.html must never be cached — it references hashed JS/CSS bundles.
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			case cleanPath == "sw.js" || cleanPath == "manifest.json":
				// M14: service worker and manifest must always be fresh.
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			case strings.HasPrefix(cleanPath, "assets/"):
				// M4: Vite hashes asset filenames (e.g. index-Abc123.js).
				// These can be cached indefinitely by the browser.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		// File not found — serve index.html for SPA routing (no-cache)
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	return gzipHandler(spaHandler)
}

// HasSPA returns true if a SPA was embedded at build time.
func HasSPA() bool {
	if _, err := fs.Sub(spaFS, "spa"); err != nil {
		return false
	}
	return true
}

// gzipPool reuses gzip writers to reduce allocation pressure.
var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

// gzipResponseWriter wraps http.ResponseWriter to transparently gzip the response.
type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

// gzipHandler wraps an http.Handler to add gzip compression for compressible content types.
func gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only compress JS, CSS, HTML, JSON, SVG
		path := r.URL.Path
		compressible := strings.HasSuffix(path, ".js") ||
			strings.HasSuffix(path, ".css") ||
			strings.HasSuffix(path, ".html") ||
			strings.HasSuffix(path, ".json") ||
			strings.HasSuffix(path, ".svg") ||
			path == "/" || path == ""

		if !compressible || !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(w)
		defer func() {
			gz.Close()
			gzipPool.Put(gz)
		}()

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length") // length changes with compression
		next.ServeHTTP(&gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	})
}
