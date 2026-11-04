// Package clients создаёт gRPC-клиенты внутренних сервисов для API Gateway.
// Каждое соединение оснащено перехватчиками: распространение контекста
// (request-id, личность пользователя), повторы при UNAVAILABLE (grpcx.Dial)
// и Circuit Breaker (resilience).
package clients

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	authv1 "github.com/thaperfectcell/food-delivery/gen/auth/v1"
	deliveryv1 "github.com/thaperfectcell/food-delivery/gen/delivery/v1"
	notificationv1 "github.com/thaperfectcell/food-delivery/gen/notification/v1"
	orderv1 "github.com/thaperfectcell/food-delivery/gen/order/v1"
	paymentv1 "github.com/thaperfectcell/food-delivery/gen/payment/v1"
	restaurantv1 "github.com/thaperfectcell/food-delivery/gen/restaurant/v1"
	"github.com/thaperfectcell/food-delivery/internal/gateway/config"
	"github.com/thaperfectcell/food-delivery/internal/gateway/resilience"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
)

// Set — клиенты всех внутренних сервисов (интерфейсы сгенерированного кода,
// поэтому в тестах их легко заменить подделками).
type Set struct {
	Auth         authv1.AuthServiceClient
	Restaurant   restaurantv1.RestaurantServiceClient
	Order        orderv1.OrderServiceClient
	Payment      paymentv1.PaymentServiceClient
	Delivery     deliveryv1.DeliveryServiceClient
	Notification notificationv1.NotificationServiceClient
}

// Pool — открытые соединения с сервисами.
type Pool struct {
	Set
	conns    map[string]*grpc.ClientConn
	breakers map[string]*resilience.Breaker
	order    []string
}

// Dial открывает соединения со всеми сервисами. Соединения ленивые:
// недоступность сервиса при старте шлюза не является ошибкой.
func Dial(addrs config.Services, log *slog.Logger) (*Pool, error) {
	p := &Pool{conns: map[string]*grpc.ClientConn{}, breakers: map[string]*resilience.Breaker{}}
	targets := []struct{ name, addr string }{
		{"auth", addrs.Auth},
		{"restaurant", addrs.Restaurant},
		{"order", addrs.Order},
		{"payment", addrs.Payment},
		{"delivery", addrs.Delivery},
		{"notification", addrs.Notification},
	}
	for _, t := range targets {
		b := resilience.New(t.name, resilience.WithStateChange(func(name string, from, to resilience.State) {
			log.Warn("circuit breaker state changed",
				slog.String("service", name), slog.String("from", from.String()), slog.String("to", to.String()))
		}))
		conn, err := grpcx.Dial(t.addr, b.UnaryClientInterceptor())
		if err != nil {
			p.Close()
			return nil, fmt.Errorf("dial %s: %w", t.name, err)
		}
		p.conns[t.name] = conn
		p.breakers[t.name] = b
		p.order = append(p.order, t.name)
	}
	p.Auth = authv1.NewAuthServiceClient(p.conns["auth"])
	p.Restaurant = restaurantv1.NewRestaurantServiceClient(p.conns["restaurant"])
	p.Order = orderv1.NewOrderServiceClient(p.conns["order"])
	p.Payment = paymentv1.NewPaymentServiceClient(p.conns["payment"])
	p.Delivery = deliveryv1.NewDeliveryServiceClient(p.conns["delivery"])
	p.Notification = notificationv1.NewNotificationServiceClient(p.conns["notification"])
	return p, nil
}

// Health опрашивает стандартный grpc.health.v1 всех сервисов параллельно.
// Результат: имя сервиса → "serving" | "not_serving" | "unavailable" | "circuit_open".
func (p *Pool) Health(ctx context.Context) map[string]string {
	res := make(map[string]string, len(p.conns))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, name := range p.order {
		name, conn := name, p.conns[name]
		if p.breakers[name].State() == resilience.StateOpen {
			res[name] = "circuit_open"
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			state := "unavailable"
			resp, err := healthpb.NewHealthClient(conn).Check(cctx, &healthpb.HealthCheckRequest{})
			if err == nil {
				if resp.GetStatus() == healthpb.HealthCheckResponse_SERVING {
					state = "serving"
				} else {
					state = "not_serving"
				}
			}
			mu.Lock()
			res[name] = state
			mu.Unlock()
		}()
	}
	wg.Wait()
	return res
}

// Close закрывает все соединения.
func (p *Pool) Close() {
	for _, c := range p.conns {
		_ = c.Close()
	}
}
