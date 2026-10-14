//go:build !dev

package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed public
var publicEmbed embed.FS

var publicFS fs.FS

func init() {
	sub, err := fs.Sub(publicEmbed, "public")
	if err != nil {
		panic(err)
	}
	publicFS = sub
}

func public() http.Handler {
	return http.FileServer(http.FS(publicFS))
}
