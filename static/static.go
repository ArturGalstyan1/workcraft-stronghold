package static

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed css
var staticFiles embed.FS

func GetFileSystem() http.FileSystem {
	fsys, err := fs.Sub(staticFiles, "css")
	if err != nil {
		panic(err)
	}
	return http.FS(fsys)
}
