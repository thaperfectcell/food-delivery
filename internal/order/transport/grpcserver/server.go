// Package grpcserver — слой представления Order Service: реализация
// gRPC-контракта order.v1.OrderService. Отвечает только за преобразование
// сообщений protobuf ↔ доменная модель и ошибок → коды gRPC.
package grpcserver

import (
	"context"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/thaperfectcell/food-delivery/gen/order/v1"
	"github.com/thaperfectcell/food-delivery/internal/order/domain"
	"github.com/thaperfectcell/food-delivery/internal/order/service"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
)

// Server реализует orderv1.OrderServiceServer.
type Server struct {
	orderv1.UnimplementedOrderServiceServer
	svc *service.Service
}

// New создаёт gRPC-обработчики.
func New(svc *service.Service) *Server { return &Server{svc: svc} }

func (s *Server) CreateOrder(ctx context.Context, req *orderv1.CreateOrderRequest) (*orderv1.Order, error) {
	lines := make([]domain.Line, 0, len(req.GetItems()))
	for _, it := range req.GetItems() {
		lines = append(lines, domain.Line{MenuItemID: strings.TrimSpace(it.GetMenuItemId()), Quantity: it.GetQuantity()})
	}
	o, err := s.svc.CreateOrder(ctx, grpcx.Actor(ctx), service.CreateOrderInput{
		RestaurantID:    strings.TrimSpace(req.GetRestaurantId()),
		Lines:           lines,
		DeliveryAddress: req.GetDeliveryAddress(),
		Comment:         req.GetComment(),
		IdempotencyKey:  req.GetIdempotencyKey(),
	})
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return ToProto(o), nil
}

func (s *Server) GetOrder(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.Order, error) {
	o, err := s.svc.GetOrder(ctx, grpcx.Actor(ctx), req.GetId())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return ToProto(o), nil
}

func (s *Server) ListOrders(ctx context.Context, req *orderv1.ListOrdersRequest) (*orderv1.ListOrdersResponse, error) {
	f := domain.ListFilter{
		RestaurantID: strings.TrimSpace(req.GetRestaurantId()),
		Limit:        int(req.GetLimit()),
		Offset:       int(req.GetOffset()),
	}
	for _, st := range req.GetStatuses() {
		if ds := StatusFromProto(st); ds != "" {
			f.Statuses = append(f.Statuses, ds)
		}
	}
	list, total, err := s.svc.ListOrders(ctx, grpcx.Actor(ctx), f)
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	resp := &orderv1.ListOrdersResponse{Total: int32(total), Orders: make([]*orderv1.Order, 0, len(list))}
	for _, o := range list {
		resp.Orders = append(resp.Orders, ToProto(o))
	}
	return resp, nil
}

func (s *Server) CancelOrder(ctx context.Context, req *orderv1.CancelOrderRequest) (*orderv1.Order, error) {
	o, err := s.svc.CancelOrder(ctx, grpcx.Actor(ctx), req.GetId(), req.GetReason())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return ToProto(o), nil
}

func (s *Server) AcceptOrder(ctx context.Context, req *orderv1.AcceptOrderRequest) (*orderv1.Order, error) {
	o, err := s.svc.AcceptOrder(ctx, grpcx.Actor(ctx), req.GetId())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return ToProto(o), nil
}

func (s *Server) RejectOrder(ctx context.Context, req *orderv1.RejectOrderRequest) (*orderv1.Order, error) {
	o, err := s.svc.RejectOrder(ctx, grpcx.Actor(ctx), req.GetId(), req.GetReason())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return ToProto(o), nil
}

func (s *Server) MarkOrderReady(ctx context.Context, req *orderv1.MarkOrderReadyRequest) (*orderv1.Order, error) {
	o, err := s.svc.MarkReady(ctx, grpcx.Actor(ctx), req.GetId())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return ToProto(o), nil
}

func (s *Server) GetStats(ctx context.Context, _ *orderv1.GetStatsRequest) (*orderv1.OrderStats, error) {
	st, err := s.svc.Stats(ctx, grpcx.Actor(ctx))
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	by := make(map[string]int64, len(domain.AllStatuses))
	for _, ds := range domain.AllStatuses {
		by[string(ds)] = st.ByStatus[ds]
	}
	return &orderv1.OrderStats{
		TotalOrders:  st.TotalOrders,
		ByStatus:     by,
		Revenue:      st.Revenue,
		AverageCheck: st.AverageCheck,
		OrdersToday:  st.OrdersToday,
	}, nil
}

// ---------------------------------------------------------------- маппинг

var statusToProto = map[domain.Status]orderv1.OrderStatus{
	domain.StatusCreated:    orderv1.OrderStatus_ORDER_STATUS_CREATED,
	domain.StatusPaid:       orderv1.OrderStatus_ORDER_STATUS_PAID,
	domain.StatusAccepted:   orderv1.OrderStatus_ORDER_STATUS_ACCEPTED,
	domain.StatusReady:      orderv1.OrderStatus_ORDER_STATUS_READY,
	domain.StatusInDelivery: orderv1.OrderStatus_ORDER_STATUS_IN_DELIVERY,
	domain.StatusDelivered:  orderv1.OrderStatus_ORDER_STATUS_DELIVERED,
	domain.StatusCancelled:  orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
}

// StatusToProto преобразует доменный статус в enum protobuf.
func StatusToProto(s domain.Status) orderv1.OrderStatus { return statusToProto[s] }

// StatusFromProto преобразует enum protobuf в доменный статус ("" для неизвестного).
func StatusFromProto(s orderv1.OrderStatus) domain.Status {
	for k, v := range statusToProto {
		if v == s {
			return k
		}
	}
	return ""
}

// ToProto преобразует заказ в сообщение protobuf.
func ToProto(o *domain.Order) *orderv1.Order {
	p := &orderv1.Order{
		Id:                o.ID,
		CustomerId:        o.CustomerID,
		RestaurantId:      o.RestaurantID,
		RestaurantName:    o.RestaurantName,
		RestaurantAddress: o.RestaurantAddress,
		ItemsTotal:        o.ItemsTotal,
		DeliveryFee:       o.DeliveryFee,
		Total:             o.Total,
		Status:            StatusToProto(o.Status),
		DeliveryAddress:   o.DeliveryAddress,
		Comment:           o.Comment,
		CourierId:         o.CourierID,
		CourierName:       o.CourierName,
		CancelReason:      o.CancelReason,
		CreatedAt:         timestamppb.New(o.CreatedAt),
		UpdatedAt:         timestamppb.New(o.UpdatedAt),
	}
	for _, it := range o.Items {
		p.Items = append(p.Items, &orderv1.OrderItem{
			MenuItemId: it.MenuItemID,
			Name:       it.Name,
			Price:      it.Price,
			Quantity:   it.Quantity,
			Total:      it.Total(),
		})
	}
	for _, h := range o.History {
		p.History = append(p.History, &orderv1.StatusChange{
			Status: StatusToProto(h.Status),
			Reason: h.Reason,
			At:     timestamppb.New(h.At),
		})
	}
	return p
}
