//go:build dev

package main

import (
	"io/fs"
	"net/http"
	"os"
)

var publicFS fs.FS = os.DirFS("public")

func public() http.Handler {
	return http.FileServer(http.FS(publicFS))
}
