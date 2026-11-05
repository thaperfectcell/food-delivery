// Package api встраивает спецификацию внешнего REST API (OpenAPI 3) в бинарник
// API Gateway: она отдаётся по адресу /api/openapi.yaml и отображается в /api/docs.
package api

import _ "embed"

// OpenAPI — спецификация REST API шлюза.
//
//go:embed openapi.yaml
var OpenAPI []byte
