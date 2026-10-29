// Package migrations встраивает SQL-миграции схемы Auth Service в бинарник.
package migrations

import "embed"

// FS — файлы миграций.
//
//go:embed *.sql
var FS embed.FS
