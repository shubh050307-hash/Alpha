package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	port := ":3003"
	dir := os.Getenv("ALPHA_WEB_DIR")
	if dir == "" {
		dir = "/var/www/alphanetx"
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		log.Fatalf("Invalid directory %q: %v", dir, err)
	}

	fs := http.FileServer(http.Dir(absDir))

	log.Printf("Alpha Network static server on %s serving %s", port, absDir)
	if err := http.ListenAndServe(port, fs); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
