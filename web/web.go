// Package web встраивает статический веб-клиент (SPA) в бинарник API Gateway.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var embedded embed.FS

// Static возвращает файловую систему с корнем в каталоге static
// (index.html и ресурсы веб-клиента).
func Static() fs.FS {
	sub, err := fs.Sub(embedded, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
