package server

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/**
var embeddedWeb embed.FS

// GetEmbeddedFS returns the embedded frontend file system
func GetEmbeddedFS() http.FileSystem {
	sub, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		return http.Dir("web")
	}
	return http.FS(sub)
}
