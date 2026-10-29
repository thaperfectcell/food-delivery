// Package grpcserver — слой представления Auth Service: реализация
// gRPC-контракта auth.v1.AuthService (маппинг protobuf ↔ домен, ошибки → коды gRPC).
package grpcserver

import (
	"context"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/thaperfectcell/food-delivery/gen/auth/v1"
	"github.com/thaperfectcell/food-delivery/internal/auth/domain"
	"github.com/thaperfectcell/food-delivery/internal/auth/service"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/grpcx"
)

// Server реализует authv1.AuthServiceServer.
type Server struct {
	authv1.UnimplementedAuthServiceServer
	svc *service.Service
}

// New создаёт gRPC-обработчики.
func New(svc *service.Service) *Server { return &Server{svc: svc} }

// Register — регистрация. Не указанная роль трактуется как «клиент».
func (s *Server) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.AuthResponse, error) {
	role := auth.RoleCustomer
	if req.GetRole() != authv1.Role_ROLE_UNSPECIFIED {
		role = RoleFromProto(req.GetRole())
	}
	res, err := s.svc.Register(ctx, service.RegisterInput{
		Email:    req.GetEmail(),
		Password: req.GetPassword(),
		Name:     req.GetName(),
		Phone:    req.GetPhone(),
		Role:     role,
	})
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return authResponse(res), nil
}

func (s *Server) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.AuthResponse, error) {
	res, err := s.svc.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return authResponse(res), nil
}

func (s *Server) GetUser(ctx context.Context, req *authv1.GetUserRequest) (*authv1.User, error) {
	u, err := s.svc.GetUser(ctx, grpcx.Actor(ctx), strings.TrimSpace(req.GetId()))
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	return UserToProto(u), nil
}

func (s *Server) ListUsers(ctx context.Context, req *authv1.ListUsersRequest) (*authv1.ListUsersResponse, error) {
	f := domain.ListFilter{Limit: int(req.GetLimit()), Offset: int(req.GetOffset())}
	if req.GetRole() != authv1.Role_ROLE_UNSPECIFIED {
		f.Role = RoleFromProto(req.GetRole())
	}
	users, total, err := s.svc.ListUsers(ctx, grpcx.Actor(ctx), f)
	if err != nil {
		return nil, grpcx.ToStatus(err)
	}
	resp := &authv1.ListUsersResponse{Total: int32(total), Users: make([]*authv1.User, 0, len(users))}
	for _, u := range users {
		resp.Users = append(resp.Users, UserToProto(u))
	}
	return resp, nil
}

// ---------------------------------------------------------------- маппинг

var roleToProto = map[auth.Role]authv1.Role{
	auth.RoleCustomer:   authv1.Role_ROLE_CUSTOMER,
	auth.RoleRestaurant: authv1.Role_ROLE_RESTAURANT,
	auth.RoleCourier:    authv1.Role_ROLE_COURIER,
	auth.RoleAdmin:      authv1.Role_ROLE_ADMIN,
}

// RoleToProto преобразует роль в enum protobuf.
func RoleToProto(r auth.Role) authv1.Role { return roleToProto[r] }

// RoleFromProto преобразует enum protobuf в роль ("" для неизвестного значения).
func RoleFromProto(r authv1.Role) auth.Role {
	for k, v := range roleToProto {
		if v == r {
			return k
		}
	}
	return ""
}

// UserToProto преобразует пользователя в сообщение protobuf (без хеша пароля).
func UserToProto(u *domain.User) *authv1.User {
	return &authv1.User{
		Id:        u.ID,
		Email:     u.Email,
		Name:      u.Name,
		Phone:     u.Phone,
		Role:      RoleToProto(u.Role),
		CreatedAt: timestamppb.New(u.CreatedAt),
	}
}

func authResponse(res *service.AuthResult) *authv1.AuthResponse {
	return &authv1.AuthResponse{
		User:        UserToProto(res.User),
		AccessToken: res.Token,
		ExpiresAt:   timestamppb.New(res.ExpiresAt),
	}
}
