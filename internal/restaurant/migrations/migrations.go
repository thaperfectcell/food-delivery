// Package migrations встраивает SQL-миграции схемы Restaurant Service в бинарник.
package migrations

import "embed"

// FS — файлы миграций.
//
//go:embed *.sql
var FS embed.FS
