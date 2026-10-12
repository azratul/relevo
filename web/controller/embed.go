// Package webcontroller embeds the built web controller (web/controller/dist).
// Build it with `make web` before building the relay; otherwise the relay
// serves only the API and a hint page.
package webcontroller

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built files rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
