// Package grpcserver — слой представления Restaurant Service: реализация
// gRPC-контракта restaurant.v1.RestaurantService.
package grpcserver

import (
	"context"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	restaurantv1 "github.com/thaperfectcell/food-delivery/gen/restaurant/v1"
	"github.com/thaperfectcell/food-delivery/internal/restaurant/domain"
	"github.com/thaperfectcell/food-delivery/internal/restaurant/service"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
)

// Server реализует restaurantv1.RestaurantServiceServer.
type Server struct {
	restaurantv1.UnimplementedRestaurantServiceServer
	svc *service.Service
}

// New создаёт gRPC-обработчики.
func New(svc *service.Service) *Server { return &Server{svc: svc} }

// ---------------------------------------------------------------- рестораны

func (s *Server) CreateRestaurant(ctx context.Context, req *restaurantv1.CreateRestaurantRequest) (*restaurantv1.Restaurant, error) {
	r, err := s.svc.CreateRestaurant(ctx, grpcx.Actor(ctx), domain.RestaurantInput{
		Name:        req.GetName(),
		Description: req.GetDescription(),
		Address:     req.GetAddress(),
		Cuisine:     req.GetCuisine(),
	})
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return RestaurantToProto(r), nil
}

func (s *Server) UpdateRestaurant(ctx context.Context, req *restaurantv1.UpdateRestaurantRequest) (*restaurantv1.Restaurant, error) {
	r, err := s.svc.UpdateRestaurant(ctx, grpcx.Actor(ctx), strings.TrimSpace(req.GetId()), domain.RestaurantPatch{
		Name:        req.Name,
		Description: req.Description,
		Address:     req.Address,
		Cuisine:     req.Cuisine,
		IsActive:    req.IsActive,
	})
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return RestaurantToProto(r), nil
}

func (s *Server) GetRestaurant(ctx context.Context, req *restaurantv1.GetRestaurantRequest) (*restaurantv1.Restaurant, error) {
	r, err := s.svc.GetRestaurant(ctx, strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return RestaurantToProto(r), nil
}

func (s *Server) ListRestaurants(ctx context.Context, req *restaurantv1.ListRestaurantsRequest) (*restaurantv1.ListRestaurantsResponse, error) {
	list, total, err := s.svc.ListRestaurants(ctx, grpcx.Actor(ctx), domain.ListFilter{
		OwnerID:         req.GetOwnerId(),
		IncludeInactive: req.GetIncludeInactive(),
		Cuisine:         req.GetCuisine(),
		Query:           req.GetQuery(),
		Limit:           int(req.GetLimit()),
		Offset:          int(req.GetOffset()),
	})
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	resp := &restaurantv1.ListRestaurantsResponse{
		Total:       int32(total),
		Restaurants: make([]*restaurantv1.Restaurant, 0, len(list)),
	}
	for _, r := range list {
		resp.Restaurants = append(resp.Restaurants, RestaurantToProto(r))
	}
	return resp, nil
}

// ---------------------------------------------------------------- меню

func (s *Server) ListMenu(ctx context.Context, req *restaurantv1.ListMenuRequest) (*restaurantv1.ListMenuResponse, error) {
	items, err := s.svc.ListMenu(ctx, strings.TrimSpace(req.GetRestaurantId()), req.GetOnlyAvailable())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return &restaurantv1.ListMenuResponse{Items: itemsToProto(items)}, nil
}

func (s *Server) CreateMenuItem(ctx context.Context, req *restaurantv1.CreateMenuItemRequest) (*restaurantv1.MenuItem, error) {
	m, err := s.svc.CreateMenuItem(ctx, grpcx.Actor(ctx), strings.TrimSpace(req.GetRestaurantId()), domain.MenuItemInput{
		Name:        req.GetName(),
		Description: req.GetDescription(),
		Category:    req.GetCategory(),
		Price:       req.GetPrice(),
	})
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return MenuItemToProto(m), nil
}

func (s *Server) UpdateMenuItem(ctx context.Context, req *restaurantv1.UpdateMenuItemRequest) (*restaurantv1.MenuItem, error) {
	m, err := s.svc.UpdateMenuItem(ctx, grpcx.Actor(ctx),
		strings.TrimSpace(req.GetRestaurantId()), strings.TrimSpace(req.GetId()),
		domain.MenuItemPatch{
			Name:        req.Name,
			Description: req.Description,
			Category:    req.Category,
			Price:       req.Price,
			IsAvailable: req.IsAvailable,
		})
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return MenuItemToProto(m), nil
}

func (s *Server) DeleteMenuItem(ctx context.Context, req *restaurantv1.DeleteMenuItemRequest) (*restaurantv1.DeleteMenuItemResponse, error) {
	err := s.svc.DeleteMenuItem(ctx, grpcx.Actor(ctx), strings.TrimSpace(req.GetRestaurantId()), strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return &restaurantv1.DeleteMenuItemResponse{}, nil
}

func (s *Server) GetMenuItems(ctx context.Context, req *restaurantv1.GetMenuItemsRequest) (*restaurantv1.GetMenuItemsResponse, error) {
	r, items, err := s.svc.GetMenuItems(ctx, strings.TrimSpace(req.GetRestaurantId()), req.GetIds())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return &restaurantv1.GetMenuItemsResponse{
		Restaurant: RestaurantToProto(r),
		Items:      itemsToProto(items),
	}, nil
}

// ---------------------------------------------------------------- маппинг

// RestaurantToProto преобразует ресторан в сообщение protobuf.
func RestaurantToProto(r *domain.Restaurant) *restaurantv1.Restaurant {
	return &restaurantv1.Restaurant{
		Id:          r.ID,
		OwnerId:     r.OwnerID,
		Name:        r.Name,
		Description: r.Description,
		Address:     r.Address,
		Cuisine:     r.Cuisine,
		IsActive:    r.IsActive,
		CreatedAt:   timestamppb.New(r.CreatedAt),
	}
}

// MenuItemToProto преобразует позицию меню в сообщение protobuf.
func MenuItemToProto(m *domain.MenuItem) *restaurantv1.MenuItem {
	return &restaurantv1.MenuItem{
		Id:           m.ID,
		RestaurantId: m.RestaurantID,
		Name:         m.Name,
		Description:  m.Description,
		Category:     m.Category,
		Price:        m.Price,
		IsAvailable:  m.IsAvailable,
		CreatedAt:    timestamppb.New(m.CreatedAt),
	}
}

func itemsToProto(items []*domain.MenuItem) []*restaurantv1.MenuItem {
	out := make([]*restaurantv1.MenuItem, 0, len(items))
	for _, m := range items {
		out = append(out, MenuItemToProto(m))
	}
	return out
}
