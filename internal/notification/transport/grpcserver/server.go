// Package grpcserver — слой представления Notification Service: реализация
// gRPC-контракта notification.v1.NotificationService.
package grpcserver

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	notificationv1 "github.com/thaperfectcell/food-delivery/gen/notification/v1"
	"github.com/thaperfectcell/food-delivery/internal/notification/domain"
	"github.com/thaperfectcell/food-delivery/internal/notification/service"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
)

// Server реализует notificationv1.NotificationServiceServer.
type Server struct {
	notificationv1.UnimplementedNotificationServiceServer
	svc *service.Service
}

// New создаёт gRPC-обработчики.
func New(svc *service.Service) *Server { return &Server{svc: svc} }

func (s *Server) ListNotifications(ctx context.Context, req *notificationv1.ListNotificationsRequest) (*notificationv1.ListNotificationsResponse, error) {
	list, unread, err := s.svc.ListNotifications(ctx, grpcx.Actor(ctx), int(req.GetLimit()), req.GetUnreadOnly())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	resp := &notificationv1.ListNotificationsResponse{
		UnreadCount:   int32(unread),
		Notifications: make([]*notificationv1.Notification, 0, len(list)),
	}
	for _, n := range list {
		resp.Notifications = append(resp.Notifications, ToProto(n))
	}
	return resp, nil
}

func (s *Server) MarkRead(ctx context.Context, req *notificationv1.MarkReadRequest) (*notificationv1.MarkReadResponse, error) {
	n, err := s.svc.MarkRead(ctx, grpcx.Actor(ctx), req.GetIds(), req.GetAll())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return &notificationv1.MarkReadResponse{Updated: int32(n)}, nil
}

// ToProto преобразует уведомление в сообщение protobuf.
func ToProto(n domain.Notification) *notificationv1.Notification {
	return &notificationv1.Notification{
		Id:        n.ID,
		UserId:    n.UserID,
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		OrderId:   n.OrderID,
		Read:      n.Read(),
		CreatedAt: timestamppb.New(n.CreatedAt),
	}
}
