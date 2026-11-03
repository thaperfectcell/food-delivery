// Package grpcserver — слой представления Delivery Service: реализация
// gRPC-контракта delivery.v1.DeliveryService.
package grpcserver

import (
	"context"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	deliveryv1 "github.com/thaperfectcell/food-delivery/gen/delivery/v1"
	"github.com/thaperfectcell/food-delivery/internal/delivery/domain"
	"github.com/thaperfectcell/food-delivery/internal/delivery/service"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
)

// Server реализует deliveryv1.DeliveryServiceServer.
type Server struct {
	deliveryv1.UnimplementedDeliveryServiceServer
	svc *service.Service
}

// New создаёт gRPC-обработчики.
func New(svc *service.Service) *Server { return &Server{svc: svc} }

func (s *Server) SetCourierStatus(ctx context.Context, req *deliveryv1.SetCourierStatusRequest) (*deliveryv1.Courier, error) {
	c, err := s.svc.SetCourierStatus(ctx, grpcx.Actor(ctx), req.GetOnline(), req.GetName())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return CourierToProto(c), nil
}

func (s *Server) GetCourier(ctx context.Context, req *deliveryv1.GetCourierRequest) (*deliveryv1.Courier, error) {
	c, err := s.svc.GetCourier(ctx, grpcx.Actor(ctx), req.GetId())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return CourierToProto(c), nil
}

func (s *Server) ListCourierDeliveries(ctx context.Context, req *deliveryv1.ListCourierDeliveriesRequest) (*deliveryv1.ListDeliveriesResponse, error) {
	list, err := s.svc.ListCourierDeliveries(ctx, grpcx.Actor(ctx), req.GetActiveOnly(), int(req.GetLimit()))
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	resp := &deliveryv1.ListDeliveriesResponse{Deliveries: make([]*deliveryv1.Delivery, 0, len(list))}
	for _, d := range list {
		resp.Deliveries = append(resp.Deliveries, DeliveryToProto(d))
	}
	return resp, nil
}

func (s *Server) GetDeliveryByOrder(ctx context.Context, req *deliveryv1.GetDeliveryByOrderRequest) (*deliveryv1.Delivery, error) {
	d, err := s.svc.GetDeliveryByOrder(ctx, grpcx.Actor(ctx), strings.TrimSpace(req.GetOrderId()))
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return DeliveryToProto(d), nil
}

func (s *Server) PickUpDelivery(ctx context.Context, req *deliveryv1.DeliveryActionRequest) (*deliveryv1.Delivery, error) {
	d, err := s.svc.PickUp(ctx, grpcx.Actor(ctx), strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return DeliveryToProto(d), nil
}

func (s *Server) CompleteDelivery(ctx context.Context, req *deliveryv1.DeliveryActionRequest) (*deliveryv1.Delivery, error) {
	d, err := s.svc.Complete(ctx, grpcx.Actor(ctx), strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return DeliveryToProto(d), nil
}

func (s *Server) GetStats(ctx context.Context, _ *deliveryv1.GetStatsRequest) (*deliveryv1.DeliveryStats, error) {
	st, err := s.svc.Stats(ctx, grpcx.Actor(ctx))
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return &deliveryv1.DeliveryStats{
		CouriersOnline:      int32(st.CouriersOnline),
		CouriersBusy:        int32(st.CouriersBusy),
		DeliveriesPending:   int32(st.DeliveriesPending),
		DeliveriesActive:    int32(st.DeliveriesActive),
		DeliveriesCompleted: int32(st.DeliveriesCompleted),
	}, nil
}

// ---------------------------------------------------------------- маппинг

var courierStatusToProto = map[domain.CourierStatus]deliveryv1.CourierStatus{
	domain.CourierOffline:   deliveryv1.CourierStatus_COURIER_STATUS_OFFLINE,
	domain.CourierAvailable: deliveryv1.CourierStatus_COURIER_STATUS_AVAILABLE,
	domain.CourierBusy:      deliveryv1.CourierStatus_COURIER_STATUS_BUSY,
}

var statusToProto = map[domain.Status]deliveryv1.DeliveryStatus{
	domain.StatusPending:   deliveryv1.DeliveryStatus_DELIVERY_STATUS_PENDING,
	domain.StatusAssigned:  deliveryv1.DeliveryStatus_DELIVERY_STATUS_ASSIGNED,
	domain.StatusPickedUp:  deliveryv1.DeliveryStatus_DELIVERY_STATUS_PICKED_UP,
	domain.StatusDelivered: deliveryv1.DeliveryStatus_DELIVERY_STATUS_DELIVERED,
	domain.StatusCancelled: deliveryv1.DeliveryStatus_DELIVERY_STATUS_CANCELLED,
}

// CourierToProto преобразует курьера в сообщение protobuf.
func CourierToProto(c *domain.Courier) *deliveryv1.Courier {
	return &deliveryv1.Courier{
		Id:                  c.ID,
		Name:                c.Name,
		Status:              courierStatusToProto[c.Status],
		CompletedDeliveries: int32(c.CompletedDeliveries),
		StatusChangedAt:     ts(c.StatusChangedAt),
	}
}

// DeliveryToProto преобразует доставку в сообщение protobuf.
func DeliveryToProto(d *domain.Delivery) *deliveryv1.Delivery {
	return &deliveryv1.Delivery{
		Id:                d.ID,
		OrderId:           d.OrderID,
		CustomerId:        d.CustomerID,
		CourierId:         d.CourierID,
		CourierName:       d.CourierName,
		Status:            statusToProto[d.Status],
		RestaurantName:    d.RestaurantName,
		RestaurantAddress: d.RestaurantAddress,
		DeliveryAddress:   d.DeliveryAddress,
		OrderTotal:        d.OrderTotal,
		OrderReady:        d.OrderReady,
		CreatedAt:         ts(d.CreatedAt),
		AssignedAt:        ts(d.AssignedAt),
		PickedUpAt:        ts(d.PickedUpAt),
		DeliveredAt:       ts(d.DeliveredAt),
	}
}

// ts возвращает nil для нулевого времени (поле не заполнено).
func ts(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
