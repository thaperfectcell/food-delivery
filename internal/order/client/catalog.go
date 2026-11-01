// Package client — исходящие адаптеры Order Service к другим сервисам.
package client

import (
	"context"
	"time"

	restaurantv1 "github.com/thaperfectcell/food-delivery/gen/restaurant/v1"
	"github.com/thaperfectcell/food-delivery/internal/order/domain"
	"github.com/thaperfectcell/food-delivery/internal/order/service"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
)

// Catalog — адаптер к Restaurant Service (реализует service.Catalog).
type Catalog struct {
	client  restaurantv1.RestaurantServiceClient
	timeout time.Duration
}

var _ service.Catalog = (*Catalog)(nil)

// NewCatalog создаёт адаптер.
func NewCatalog(client restaurantv1.RestaurantServiceClient) *Catalog {
	return &Catalog{client: client, timeout: 3 * time.Second}
}

// GetMenuItems запрашивает ресторан и позиции меню с актуальными ценами.
func (c *Catalog) GetMenuItems(ctx context.Context, restaurantID string, ids []string) (domain.Restaurant, []domain.MenuItem, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	resp, err := c.client.GetMenuItems(ctx, &restaurantv1.GetMenuItemsRequest{RestaurantId: restaurantID, Ids: ids})
	if err != nil {
		return domain.Restaurant{}, nil, grpcx.FromStatus(err)
	}
	r := resp.GetRestaurant()
	restaurant := domain.Restaurant{
		ID:      r.GetId(),
		OwnerID: r.GetOwnerId(),
		Name:    r.GetName(),
		Address: r.GetAddress(),
		Active:  r.GetIsActive(),
	}
	items := make([]domain.MenuItem, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, domain.MenuItem{
			ID:        it.GetId(),
			Name:      it.GetName(),
			Price:     it.GetPrice(),
			Available: it.GetIsAvailable(),
		})
	}
	return restaurant, items, nil
}
