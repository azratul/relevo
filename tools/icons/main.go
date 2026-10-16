// Command icons writes the generated icon files to assets/icons:
//
//	go run ./tools/icons
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/azratul/relevo/internal/icon"
)

func main() {
	dir := filepath.Join("assets", "icons")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	files := map[string][]byte{
		"relevo.svg":  []byte(icon.SVG()),
		"relevo.png":  icon.PNG(512, false),
		"relevo.ico":  icon.ICO(false, 16, 24, 32, 48, 64, 256),
		"relevo.icns": icon.ICNS(),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
