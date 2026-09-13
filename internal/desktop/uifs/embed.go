package uifs

import "embed"

// Dist holds the built desktop SPA (or the in-tree stub when the UI was not built).
//
//go:embed all:dist
var Dist embed.FS
