package main

import (
	"embed"
	"log"
	"net/http"
)

//go:embed pages/view.html pages/metronome.html
var pagesFS embed.FS

// pageHandler serves a single embedded HTML file as text/html. Kept
// deliberately trivial (no directory listing, no template execution) since
// every page here is static HTML+inline-JS.
func pageHandler(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := pagesFS.ReadFile(path)
		if err != nil {
			log.Printf("pageHandler(%s) read failed: %v", path, err)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, werr := w.Write(data); werr != nil {
			log.Printf("pageHandler(%s) write failed: %v", path, werr)
		}
	}
}
