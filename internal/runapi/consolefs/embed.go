// Package consolefs embeds the Anvil Agents Console static assets.
//
// Production images copy a Vite build into dist/ before compiling the API.
// A minimal stub ships in dist/ (restored from stub/ via `make console-embed-restore`)
// so `go build` and tests work without Node.
package consolefs

import "embed"

// Dist holds the built SPA (or the in-tree stub when the console was not built).
//
//go:embed all:dist
var Dist embed.FS
