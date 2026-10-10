// Package api embeds the canonical public contract (JSON Schemas and
// examples) so that the runtime validates exactly what is published.
package api

import "embed"

//go:embed schemas examples
var FS embed.FS
