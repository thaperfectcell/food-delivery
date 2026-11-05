package httpapi

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
)

// healthz — проверка живости процесса (liveness): шлюз запущен и отвечает.
func (a *API) healthz(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz — проверка готовности (readiness) с состоянием зависимостей:
// NATS и внутренних gRPC-сервисов. Шлюз обслуживает REST API и при частичной
// недоступности зависимостей, поэтому статус "degraded" возвращается с кодом
// 200; код 503 — только если зависимость, без которой работа невозможна,
// объявлена неготовой функцией ReadinessFunc.
func (a *API) readyz(w http.ResponseWriter, r *http.Request) {
	if a.ready == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "checks": map[string]string{}})
		return
	}
	ready, checks := a.ready(r.Context())
	statusText, code := "ok", http.StatusOK
	for _, v := range checks {
		if v != "ok" && v != "serving" {
			statusText = "degraded"
		}
	}
	if !ready {
		statusText, code = "not_ready", http.StatusServiceUnavailable
	}
	httpx.WriteJSON(w, code, map[string]any{"status": statusText, "checks": checks})
}

// openAPISpec отдаёт спецификацию REST API.
func (a *API) openAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(a.openapi)
}

// swaggerVersion — версия Swagger UI, загружаемой с CDN unpkg.
const swaggerVersion = "5.17.14"

const docsPage = `<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>FoodFlow API — документация</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@` + swaggerVersion + `/swagger-ui.css">
  <style>body{margin:0;background:#fafafa}</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@` + swaggerVersion + `/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: '/api/openapi.yaml',
      dom_id: '#swagger-ui',
      deepLinking: true,
      persistAuthorization: true
    });
  </script>
</body>
</html>
`

// apiDocs отдаёт страницу Swagger UI для спецификации /api/openapi.yaml.
func (a *API) apiDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(docsPage))
}

// fallback обрабатывает все прочие пути: для /api/… — JSON-ошибка 404,
// для остальных — файлы веб-клиента; неизвестные пути без расширения
// отдают index.html (маршрутизация на стороне SPA).
func (a *API) fallback() http.Handler {
	var files http.Handler
	if a.static != nil {
		files = http.FileServerFS(a.static)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			httpx.WriteError(w, r, http.StatusNotFound, "not_found", "маршрут API не найден")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "метод не поддерживается")
			return
		}
		if files == nil {
			httpx.WriteError(w, r, http.StatusNotFound, "not_found", "веб-клиент не подключён")
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "."
		}
		if info, err := fs.Stat(a.static, name); err == nil {
			if info.IsDir() && name != "." {
				a.serveIndex(w, r) // каталоги не листингуем — это маршрут SPA
				return
			}
			if name == "." || strings.HasSuffix(name, ".html") {
				w.Header().Set("Cache-Control", "no-cache")
			}
			files.ServeHTTP(w, r)
			return
		} else if !errors.Is(err, fs.ErrNotExist) {
			httpx.WriteError(w, r, http.StatusInternalServerError, "internal", "ошибка чтения файла")
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r) // отсутствующий ресурс (скрипт, картинка)
			return
		}
		a.serveIndex(w, r)
	})
}

// serveIndex отдаёт index.html веб-клиента.
func (a *API) serveIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(a.static, "index.html")
	if err != nil {
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "веб-клиент не найден")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}
