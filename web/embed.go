package web

import (
	"embed"
	"encoding/json"
)

// Assets is replaced by the Vite production build before building the release.
//
//go:embed all:dist
var Assets embed.FS

//go:embed fallback.html
var Fallback []byte

//go:embed package.json
var packageJSON []byte

// Version is the release version from package.json, shared with the console.
var Version = func() string {
	var p struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(packageJSON, &p) != nil || p.Version == "" {
		panic("web/package.json has no version")
	}
	return p.Version
}()
