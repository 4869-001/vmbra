// Package webui 内嵌控制台静态资源。
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:web
var assets embed.FS

// Dir 返回控制台资源根目录。
func Dir() fs.FS {
	sub, err := fs.Sub(assets, "web")
	if err != nil {
		panic(err)
	}
	return sub
}
