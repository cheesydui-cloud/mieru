package web

import "embed"

// Dist is the built Vue app (npm run build → dist/).
//
//go:embed all:dist
var Dist embed.FS
