package httpapi

import (
	"log/slog"
	"net/http"
	"sync"

	"google.golang.org/grpc/status"

	authv1 "github.com/thaperfectcell/food-delivery/gen/auth/v1"
	deliveryv1 "github.com/thaperfectcell/food-delivery/gen/delivery/v1"
	orderv1 "github.com/thaperfectcell/food-delivery/gen/order/v1"
	restaurantv1 "github.com/thaperfectcell/food-delivery/gen/restaurant/v1"
	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
)

// adminStats — GET /admin/stats: сводка для администратора, собранная из
// четырёх сервисов параллельными запросами (API Composition). Если часть
// сервисов недоступна, возвращаются доступные данные и список предупреждений.
func (a *API) adminStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.call(r)
	defer cancel()

	var (
		out      statsDTO
		mu       sync.Mutex
		wg       sync.WaitGroup
		failures int
	)
	fail := func(part string, err error) {
		mu.Lock()
		defer mu.Unlock()
		failures++
		out.Warnings = append(out.Warnings, part+": "+status.Convert(err).Message())
		a.log.WarnContext(ctx, "stats part unavailable", slog.String("part", part), slog.String("error", err.Error()))
	}

	wg.Add(4)
	go func() {
		defer wg.Done()
		s, err := a.c.Order.GetStats(ctx, &orderv1.GetStatsRequest{})
		if err != nil {
			fail("orders", err)
			return
		}
		mu.Lock()
		out.Orders = toOrderStats(s)
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		s, err := a.c.Delivery.GetStats(ctx, &deliveryv1.GetStatsRequest{})
		if err != nil {
			fail("delivery", err)
			return
		}
		mu.Lock()
		out.Delivery = toDeliveryStats(s)
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		resp, err := a.c.Restaurant.ListRestaurants(ctx, &restaurantv1.ListRestaurantsRequest{IncludeInactive: true, Limit: 1})
		if err != nil {
			fail("restaurants", err)
			return
		}
		mu.Lock()
		out.Restaurants = int(resp.GetTotal())
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		resp, err := a.c.Auth.ListUsers(ctx, &authv1.ListUsersRequest{Limit: 1})
		if err != nil {
			fail("users", err)
			return
		}
		mu.Lock()
		out.Users = int(resp.GetTotal())
		mu.Unlock()
	}()
	wg.Wait()

	if failures == 4 {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "unavailable", "сервисы статистики временно недоступны")
		return
	}
	if out.Orders.ByStatus == nil {
		out.Orders.ByStatus = map[string]int64{}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
