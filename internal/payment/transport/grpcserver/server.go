// Package grpcserver — слой представления Payment Service: реализация
// gRPC-контракта payment.v1.PaymentService (только чтение: платежи создаются
// асинхронно по событиям).
package grpcserver

import (
	"context"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	paymentv1 "github.com/thaperfectcell/food-delivery/gen/payment/v1"
	"github.com/thaperfectcell/food-delivery/internal/payment/domain"
	"github.com/thaperfectcell/food-delivery/internal/payment/service"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
)

// Server реализует paymentv1.PaymentServiceServer.
type Server struct {
	paymentv1.UnimplementedPaymentServiceServer
	svc *service.Service
}

// New создаёт gRPC-обработчики.
func New(svc *service.Service) *Server { return &Server{svc: svc} }

func (s *Server) GetPaymentByOrder(ctx context.Context, req *paymentv1.GetPaymentByOrderRequest) (*paymentv1.Payment, error) {
	p, err := s.svc.GetPaymentByOrder(ctx, grpcx.Actor(ctx), strings.TrimSpace(req.GetOrderId()))
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return ToProto(p), nil
}

func (s *Server) ListPayments(ctx context.Context, req *paymentv1.ListPaymentsRequest) (*paymentv1.ListPaymentsResponse, error) {
	list, total, err := s.svc.ListPayments(ctx, grpcx.Actor(ctx), int(req.GetLimit()), int(req.GetOffset()))
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	resp := &paymentv1.ListPaymentsResponse{Total: int32(total), Payments: make([]*paymentv1.Payment, 0, len(list))}
	for _, p := range list {
		resp.Payments = append(resp.Payments, ToProto(p))
	}
	return resp, nil
}

// ---------------------------------------------------------------- маппинг

var statusToProto = map[domain.Status]paymentv1.PaymentStatus{
	domain.StatusPending:   paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
	domain.StatusSucceeded: paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED,
	domain.StatusFailed:    paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED,
	domain.StatusRefunded:  paymentv1.PaymentStatus_PAYMENT_STATUS_REFUNDED,
}

// StatusToProto преобразует доменный статус в enum protobuf.
func StatusToProto(s domain.Status) paymentv1.PaymentStatus { return statusToProto[s] }

// ToProto преобразует платёж в сообщение protobuf.
func ToProto(p *domain.Payment) *paymentv1.Payment {
	return &paymentv1.Payment{
		Id:            p.ID,
		OrderId:       p.OrderID,
		CustomerId:    p.CustomerID,
		Amount:        p.Amount,
		Status:        StatusToProto(p.Status),
		FailureReason: p.FailureReason,
		ProviderRef:   p.ProviderRef,
		CreatedAt:     timestamppb.New(p.CreatedAt),
		UpdatedAt:     timestamppb.New(p.UpdatedAt),
	}
}
