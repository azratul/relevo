// Command icons writes the generated icon files (SVG, PNG, ICO, ICNS) to a
// directory. The release build calls it; the files are not committed because
// PNG compression output differs between Go versions.
//
//	go run ./tools/icons OUTPUT_DIR
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/azratul/relevo/internal/icon"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: icons OUTPUT_DIR")
	}
	dir := os.Args[1]
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
