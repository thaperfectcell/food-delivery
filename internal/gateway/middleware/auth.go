package middleware

import (
	"net/http"
	"strings"

	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
)

// Authenticate проверяет заголовок "Authorization: Bearer <JWT>". Корректный
// токен превращается в auth.Identity в контексте запроса; запрос без токена
// проходит анонимно (публичные маршруты), а с недействительным токеном
// отклоняется с кодом 401.
func Authenticate(tokens *auth.TokenManager) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, present := BearerToken(r)
			if !present {
				next.ServeHTTP(w, r)
				return
			}
			id, err := tokens.Parse(raw)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "недействительный или просроченный токен")
				return
			}
			if info := infoFrom(r.Context()); info != nil {
				info.userID, info.role = id.UserID, string(id.Role)
			}
			next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), id)))
		})
	}
}

// BearerToken извлекает токен из заголовка Authorization.
func BearerToken(r *http.Request) (string, bool) {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h == "" {
		return "", false
	}
	const prefix = "bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", true // заголовок есть, но формат неверный
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

// Identity возвращает личность пользователя из контекста запроса.
func Identity(r *http.Request) (auth.Identity, bool) {
	return auth.IdentityFrom(r.Context())
}

// RequireRole пропускает только аутентифицированных пользователей с одной
// из ролей roles (пустой список — любая роль). Анонимный запрос → 401,
// неподходящая роль → 403. Это «грубая» авторизация на уровне маршрута;
// права на конкретный ресурс (владение заказом, рестораном) проверяют сервисы.
func RequireRole(roles ...auth.Role) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, ok := Identity(r)
			if !ok {
				w.Header().Set("WWW-Authenticate", "Bearer")
				httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "требуется вход в систему")
				return
			}
			if len(roles) > 0 && !id.Is(roles...) {
				httpx.WriteError(w, r, http.StatusForbidden, "forbidden", "недостаточно прав для этой операции")
				return
			}
			next(w, r)
		}
	}
}
