package httpapi

import (
	"net/http"
	"strings"

	restaurantv1 "github.com/thaperfectcell/food-delivery/gen/restaurant/v1"
	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
	"github.com/thaperfectcell/food-delivery/internal/gateway/middleware"
)

// listRestaurants — GET /restaurants (публичный каталог активных ресторанов).
func (a *API) listRestaurants(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	ctx, cancel := a.call(r)
	defer cancel()
	resp, err := a.c.Restaurant.ListRestaurants(ctx, &restaurantv1.ListRestaurantsRequest{
		Query:   strings.TrimSpace(q.Get("q")),
		Cuisine: strings.TrimSpace(q.Get("cuisine")),
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.writeRestaurants(w, resp)
}

// myRestaurants — GET /my/restaurants (рестораны текущего пользователя,
// включая неактивные).
func (a *API) myRestaurants(w http.ResponseWriter, r *http.Request) {
	id, _ := middleware.Identity(r)
	ctx, cancel := a.call(r)
	defer cancel()
	resp, err := a.c.Restaurant.ListRestaurants(ctx, &restaurantv1.ListRestaurantsRequest{
		OwnerId:         id.UserID,
		IncludeInactive: true,
		Limit:           100,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.writeRestaurants(w, resp)
}

func (a *API) writeRestaurants(w http.ResponseWriter, resp *restaurantv1.ListRestaurantsResponse) {
	items := make([]restaurantDTO, 0, len(resp.GetRestaurants()))
	for _, x := range resp.GetRestaurants() {
		items = append(items, toRestaurant(x))
	}
	httpx.WriteJSON(w, http.StatusOK, newList(items, int(resp.GetTotal())))
}

type restaurantInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Address     string `json:"address"`
	Cuisine     string `json:"cuisine"`
}

// createRestaurant — POST /restaurants.
func (a *API) createRestaurant(w http.ResponseWriter, r *http.Request) {
	var in restaurantInput
	if !decodeJSON(w, r, &in, false) {
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	res, err := a.c.Restaurant.CreateRestaurant(ctx, &restaurantv1.CreateRestaurantRequest{
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		Address:     strings.TrimSpace(in.Address),
		Cuisine:     strings.TrimSpace(in.Cuisine),
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toRestaurant(res))
}

// getRestaurant — GET /restaurants/{id}.
func (a *API) getRestaurant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	res, err := a.c.Restaurant.GetRestaurant(ctx, &restaurantv1.GetRestaurantRequest{Id: id})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toRestaurant(res))
}

type restaurantPatch struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Address     *string `json:"address"`
	Cuisine     *string `json:"cuisine"`
	IsActive    *bool   `json:"is_active"`
}

// updateRestaurant — PATCH /restaurants/{id} (частичное обновление: меняются
// только переданные поля).
func (a *API) updateRestaurant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in restaurantPatch
	if !decodeJSON(w, r, &in, false) {
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	res, err := a.c.Restaurant.UpdateRestaurant(ctx, &restaurantv1.UpdateRestaurantRequest{
		Id:          id,
		Name:        trimPtr(in.Name),
		Description: trimPtr(in.Description),
		Address:     trimPtr(in.Address),
		Cuisine:     trimPtr(in.Cuisine),
		IsActive:    in.IsActive,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toRestaurant(res))
}

// listMenu — GET /restaurants/{id}/menu.
func (a *API) listMenu(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	resp, err := a.c.Restaurant.ListMenu(ctx, &restaurantv1.ListMenuRequest{
		RestaurantId:  id,
		OnlyAvailable: queryBool(r, "available"),
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	items := make([]menuItemDTO, 0, len(resp.GetItems()))
	for _, m := range resp.GetItems() {
		items = append(items, toMenuItem(m))
	}
	httpx.WriteJSON(w, http.StatusOK, newList(items, len(items)))
}

type menuItemInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Price       int64  `json:"price"`
}

// createMenuItem — POST /restaurants/{id}/menu.
func (a *API) createMenuItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in menuItemInput
	if !decodeJSON(w, r, &in, false) {
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	item, err := a.c.Restaurant.CreateMenuItem(ctx, &restaurantv1.CreateMenuItemRequest{
		RestaurantId: id,
		Name:         strings.TrimSpace(in.Name),
		Description:  strings.TrimSpace(in.Description),
		Category:     strings.TrimSpace(in.Category),
		Price:        in.Price,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toMenuItem(item))
}

type menuItemPatch struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Category    *string `json:"category"`
	Price       *int64  `json:"price"`
	IsAvailable *bool   `json:"is_available"`
}

// updateMenuItem — PATCH /restaurants/{id}/menu/{itemId}.
func (a *API) updateMenuItem(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	itemID, ok := pathID(w, r, "itemId")
	if !ok {
		return
	}
	var in menuItemPatch
	if !decodeJSON(w, r, &in, false) {
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	item, err := a.c.Restaurant.UpdateMenuItem(ctx, &restaurantv1.UpdateMenuItemRequest{
		Id:           itemID,
		RestaurantId: restaurantID,
		Name:         trimPtr(in.Name),
		Description:  trimPtr(in.Description),
		Category:     trimPtr(in.Category),
		Price:        in.Price,
		IsAvailable:  in.IsAvailable,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toMenuItem(item))
}

// deleteMenuItem — DELETE /restaurants/{id}/menu/{itemId}.
func (a *API) deleteMenuItem(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	itemID, ok := pathID(w, r, "itemId")
	if !ok {
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	if _, err := a.c.Restaurant.DeleteMenuItem(ctx, &restaurantv1.DeleteMenuItemRequest{
		Id:           itemID,
		RestaurantId: restaurantID,
	}); err != nil {
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	return &v
}
