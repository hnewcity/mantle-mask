package web

import "embed"

// Assets is replaced by the Vite production build before building the release.
//
//go:embed all:dist
var Assets embed.FS

//go:embed fallback.html
var Fallback []byte
