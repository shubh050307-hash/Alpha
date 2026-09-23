// Alpha Network website static server
// Usage: go run website/serve.go
// Serves the website directory on :3000
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

func main() {
	port := flag.String("port", "3000", "port to listen on")
	dir := flag.String("dir", "", "directory to serve (defaults to directory of this file)")
	flag.Parse()

	// Resolve serve directory
	serveDir := *dir
	if serveDir == "" {
		// Use the directory containing this source file
		_, filename, _, ok := runtime.Caller(0)
		if ok {
			serveDir = filepath.Dir(filename)
		} else {
			// Fallback: look for a 'website' folder next to the binary
			exe, err := os.Executable()
			if err != nil {
				serveDir = "."
			} else {
				serveDir = filepath.Join(filepath.Dir(exe), "website")
			}
		}
	}

	// Verify directory exists
	if info, err := os.Stat(serveDir); err != nil || !info.IsDir() {
		log.Fatalf("Serve directory does not exist or is not a directory: %s", serveDir)
	}

	addr := fmt.Sprintf(":%s", *port)
	log.Printf("Alpha Network website")
	log.Printf("Serving %s on http://localhost%s", serveDir, addr)
	log.Printf("Press Ctrl+C to stop")

	fs := http.FileServer(http.Dir(serveDir))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Add CORS and cache headers for local dev
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache")
		fs.ServeHTTP(w, r)
	})

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
