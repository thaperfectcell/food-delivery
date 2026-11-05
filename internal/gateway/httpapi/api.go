// Package httpapi — слой представления API Gateway: REST-обработчики,
// преобразование JSON ↔ protobuf, композиция ответов нескольких сервисов,
// доставка уведомлений по SSE, служебные маршруты и раздача веб-клиента.
package httpapi

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/thaperfectcell/food-delivery/internal/gateway/clients"
	"github.com/thaperfectcell/food-delivery/internal/gateway/middleware"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
)

// NotificationSource — источник push-уведомлений пользователя (реализуется
// подпиской на core NATS; в тестах — подделкой).
type NotificationSource interface {
	// Subscribe подписывается на уведомления пользователя. Возвращает канал
	// сообщений (JSON) и функцию отписки.
	Subscribe(userID string) (<-chan []byte, func(), error)
}

// ReadinessFunc проверяет готовность зависимостей шлюза.
type ReadinessFunc func(ctx context.Context) (ready bool, checks map[string]string)

// Deps — зависимости HTTP-слоя.
type Deps struct {
	Clients      clients.Set
	Tokens       *auth.TokenManager
	Notifier     NotificationSource // nil — уведомления в реальном времени недоступны
	Ready        ReadinessFunc      // nil — шлюз считается готовым
	Log          *slog.Logger
	Static       fs.FS  // файлы веб-клиента (index.html и ресурсы)
	OpenAPI      []byte // спецификация REST API
	CallTimeout  time.Duration
	RateLimiter  *middleware.RateLimiter // nil — без ограничения частоты
	CORSOrigins  []string
	BodyLimit    int64
	Shutdown     <-chan struct{} // закрывается при остановке сервера (завершает SSE)
	PingInterval time.Duration   // период служебных сообщений SSE
}

// API — HTTP-обработчики шлюза.
type API struct {
	c        clients.Set
	tokens   *auth.TokenManager
	notifier NotificationSource
	ready    ReadinessFunc
	log      *slog.Logger
	static   fs.FS
	openapi  []byte
	timeout  time.Duration
	shutdown <-chan struct{}
	ping     time.Duration
}

// NewHandler собирает маршрутизатор и цепочку middleware.
func NewHandler(d Deps) http.Handler {
	a := &API{
		c:        d.Clients,
		tokens:   d.Tokens,
		notifier: d.Notifier,
		ready:    d.Ready,
		log:      d.Log,
		static:   d.Static,
		openapi:  d.OpenAPI,
		timeout:  d.CallTimeout,
		shutdown: d.Shutdown,
		ping:     d.PingInterval,
	}
	if a.log == nil {
		a.log = slog.Default()
	}
	if a.timeout <= 0 {
		a.timeout = 5 * time.Second
	}
	if a.ping <= 0 {
		a.ping = 15 * time.Second
	}
	if a.shutdown == nil {
		a.shutdown = make(chan struct{})
	}
	limit := d.BodyLimit
	if limit <= 0 {
		limit = 1 << 20
	}
	origins := d.CORSOrigins
	if len(origins) == 0 {
		origins = []string{"*"}
	}

	mux := http.NewServeMux()
	a.routes(mux)

	mws := []middleware.Middleware{
		middleware.RequestID,
		middleware.AccessLog(a.log),
		middleware.Recover(a.log),
		middleware.CORS(origins),
	}
	if d.RateLimiter != nil {
		mws = append(mws, middleware.RateLimit(d.RateLimiter, isAPIRequest))
	}
	mws = append(mws, middleware.BodyLimit(limit), middleware.Authenticate(d.Tokens))
	return middleware.Chain(mux, mws...)
}

// isAPIRequest — ограничение частоты применяется только к REST API.
func isAPIRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, apiPrefix+"/")
}

const apiPrefix = "/api/v1"

func (a *API) routes(mux *http.ServeMux) {
	var (
		role       = middleware.RequireRole
		anyUser    = role()
		customer   = role(auth.RoleCustomer)
		restaurant = role(auth.RoleRestaurant, auth.RoleAdmin)
		courier    = role(auth.RoleCourier)
		admin      = role(auth.RoleAdmin)
		p          = apiPrefix
	)

	// Аутентификация (Auth Service).
	mux.HandleFunc("POST "+p+"/auth/register", a.register)
	mux.HandleFunc("POST "+p+"/auth/login", a.login)
	mux.HandleFunc("GET "+p+"/auth/me", anyUser(a.me))

	// Каталог (Restaurant Service).
	mux.HandleFunc("GET "+p+"/restaurants", a.listRestaurants)
	mux.HandleFunc("POST "+p+"/restaurants", restaurant(a.createRestaurant))
	mux.HandleFunc("GET "+p+"/restaurants/{id}", a.getRestaurant)
	mux.HandleFunc("PATCH "+p+"/restaurants/{id}", restaurant(a.updateRestaurant))
	mux.HandleFunc("GET "+p+"/restaurants/{id}/menu", a.listMenu)
	mux.HandleFunc("POST "+p+"/restaurants/{id}/menu", restaurant(a.createMenuItem))
	mux.HandleFunc("PATCH "+p+"/restaurants/{id}/menu/{itemId}", restaurant(a.updateMenuItem))
	mux.HandleFunc("DELETE "+p+"/restaurants/{id}/menu/{itemId}", restaurant(a.deleteMenuItem))
	mux.HandleFunc("GET "+p+"/my/restaurants", restaurant(a.myRestaurants))

	// Заказы (Order Service + композиция с Payment и Delivery).
	mux.HandleFunc("POST "+p+"/orders", customer(a.createOrder))
	mux.HandleFunc("GET "+p+"/orders", role(auth.RoleCustomer, auth.RoleRestaurant, auth.RoleAdmin)(a.listOrders))
	mux.HandleFunc("GET "+p+"/orders/{id}", anyUser(a.getOrder))
	mux.HandleFunc("POST "+p+"/orders/{id}/cancel", role(auth.RoleCustomer, auth.RoleAdmin)(a.cancelOrder))
	mux.HandleFunc("POST "+p+"/orders/{id}/accept", restaurant(a.acceptOrder))
	mux.HandleFunc("POST "+p+"/orders/{id}/reject", restaurant(a.rejectOrder))
	mux.HandleFunc("POST "+p+"/orders/{id}/ready", restaurant(a.markOrderReady))

	// Курьер (Delivery Service).
	mux.HandleFunc("GET "+p+"/courier/me", courier(a.courierMe))
	mux.HandleFunc("POST "+p+"/courier/status", courier(a.courierStatus))
	mux.HandleFunc("GET "+p+"/courier/deliveries", courier(a.courierDeliveries))
	mux.HandleFunc("POST "+p+"/courier/deliveries/{id}/pickup", courier(a.pickUpDelivery))
	mux.HandleFunc("POST "+p+"/courier/deliveries/{id}/deliver", courier(a.completeDelivery))

	// Уведомления (Notification Service + SSE через NATS).
	mux.HandleFunc("GET "+p+"/notifications", anyUser(a.listNotifications))
	mux.HandleFunc("POST "+p+"/notifications/read", anyUser(a.markNotificationsRead))
	mux.HandleFunc("GET "+p+"/notifications/stream", a.notificationStream)

	// Администрирование (композиция нескольких сервисов).
	mux.HandleFunc("GET "+p+"/admin/stats", admin(a.adminStats))
	mux.HandleFunc("GET "+p+"/admin/users", admin(a.adminUsers))

	// Служебные маршруты.
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)
	mux.HandleFunc("GET /api/openapi.yaml", a.openAPISpec)
	mux.HandleFunc("GET /api/docs", a.apiDocs)

	// Всё остальное: веб-клиент (SPA) или JSON-ошибка 404 для API.
	mux.Handle("/", a.fallback())
}

// call создаёт контекст исходящего gRPC-вызова с таймаутом. Личность
// пользователя и request-id уже находятся в контексте запроса и будут
// переданы в метаданных перехватчиком grpcx.PropagateContext.
func (a *API) call(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), a.timeout)
}
