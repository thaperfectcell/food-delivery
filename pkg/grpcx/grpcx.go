// Package grpcx — общая инфраструктура gRPC: сервер с перехватчиками
// (восстановление после паники, сквозной request-id, личность вызывающего,
// журналирование), клиентские соединения с распространением контекста,
// повторами и таймаутами, преобразование ошибок и проверка здоровья.
package grpcx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/requestid"
)

// Ключи метаданных gRPC для передачи контекста вызова.
const (
	MDRequestID = requestid.Header // x-request-id
	MDActorID   = "x-actor-id"
	MDActorRole = "x-actor-role"
	MDActorName = "x-actor-name-bin" // суффикс -bin: значение может содержать не-ASCII (кириллицу)
)

// ---------------------------------------------------------------- сервер

// Server — gRPC-сервер со стандартным набором перехватчиков и health-сервисом.
type Server struct {
	*grpc.Server
	Health *health.Server
	log    *slog.Logger
}

// NewServer создаёт сервер. Health-статус изначально NOT_SERVING —
// сервис переводит его в SERVING вызовом SetServing после инициализации.
func NewServer(log *slog.Logger) *Server {
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recoveryInterceptor(log),
			contextInterceptor(),
			loggingInterceptor(log),
		),
		grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionIdle: 5 * time.Minute}),
	)
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(s, hs)
	return &Server{Server: s, Health: hs, log: log}
}

// SetServing отмечает сервис готовым к обслуживанию запросов.
func (s *Server) SetServing() {
	s.Health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
}

// Run слушает addr до отмены ctx, затем выполняет корректную остановку
// (дожидается завершения активных вызовов не дольше 10 секунд).
func (s *Server) Run(ctx context.Context, addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.Serve(lis) }()
	s.log.Info("grpc server started", slog.String("addr", addr))

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	s.Health.Shutdown()
	stopped := make(chan struct{})
	go func() { s.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		s.Stop()
	}
	s.log.Info("grpc server stopped")
	return nil
}

func recoveryInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if p := recover(); p != nil {
				log.ErrorContext(ctx, "panic in grpc handler",
					slog.String("method", info.FullMethod), slog.Any("panic", p),
					slog.String("stack", string(debug.Stack())))
				err = status.Error(codes.Internal, "внутренняя ошибка сервиса")
			}
		}()
		return handler(ctx, req)
	}
}

// contextInterceptor переносит request-id и личность вызывающего из
// метаданных gRPC в контекст.
func contextInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		rid := first(md, MDRequestID)
		if rid == "" {
			rid = requestid.New()
		}
		ctx = requestid.With(ctx, rid)
		if id := first(md, MDActorID); id != "" {
			ctx = auth.WithIdentity(ctx, auth.Identity{
				UserID: id,
				Role:   auth.Role(first(md, MDActorRole)),
				Name:   first(md, MDActorName),
			})
		}
		return handler(ctx, req)
	}
}

func loggingInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if strings.HasPrefix(info.FullMethod, "/grpc.health.v1.Health/") {
			return handler(ctx, req)
		}
		start := time.Now()
		resp, err := handler(ctx, req)
		code := status.Code(err)
		attrs := []any{
			slog.String("method", info.FullMethod),
			slog.String("code", code.String()),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
		}
		if id, ok := auth.IdentityFrom(ctx); ok {
			attrs = append(attrs, slog.String("actor", id.UserID), slog.String("role", string(id.Role)))
		}
		switch {
		case code == codes.Internal || code == codes.Unknown:
			log.ErrorContext(ctx, "grpc call failed", append(attrs, slog.String("error", err.Error()))...)
		case err != nil:
			log.InfoContext(ctx, "grpc call rejected", append(attrs, slog.String("error", status.Convert(err).Message()))...)
		default:
			log.InfoContext(ctx, "grpc call", attrs...)
		}
		return resp, err
	}
}

func first(md metadata.MD, key string) string {
	if v := md.Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

// Actor возвращает личность вызывающего из контекста входящего вызова.
func Actor(ctx context.Context) auth.Identity {
	id, _ := auth.IdentityFrom(ctx)
	return id
}

// ---------------------------------------------------------------- ошибки

// ToStatus преобразует ошибку прикладного уровня в статус gRPC.
func ToStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok && !isAppErr(err) {
		return err // уже статус gRPC
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "превышено время ожидания")
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, "запрос отменён")
	}
	return status.Error(codeOf(apperr.KindOf(err)), apperr.MessageOf(err))
}

func isAppErr(err error) bool {
	var e *apperr.Error
	return errors.As(err, &e)
}

func codeOf(k apperr.Kind) codes.Code {
	switch k {
	case apperr.KindInvalid:
		return codes.InvalidArgument
	case apperr.KindNotFound:
		return codes.NotFound
	case apperr.KindConflict:
		return codes.AlreadyExists
	case apperr.KindPrecondition:
		return codes.FailedPrecondition
	case apperr.KindForbidden:
		return codes.PermissionDenied
	case apperr.KindUnauthenticated:
		return codes.Unauthenticated
	case apperr.KindUnavailable:
		return codes.Unavailable
	default:
		return codes.Internal
	}
}

// FromStatus преобразует ошибку gRPC-вызова обратно в ошибку apperr
// (используется клиентскими адаптерами сервисов).
func FromStatus(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return apperr.Wrap(apperr.KindUnavailable, "сервис недоступен", err)
	}
	var kind apperr.Kind
	switch st.Code() {
	case codes.InvalidArgument, codes.OutOfRange:
		kind = apperr.KindInvalid
	case codes.NotFound:
		kind = apperr.KindNotFound
	case codes.AlreadyExists, codes.Aborted:
		kind = apperr.KindConflict
	case codes.FailedPrecondition:
		kind = apperr.KindPrecondition
	case codes.PermissionDenied:
		kind = apperr.KindForbidden
	case codes.Unauthenticated:
		kind = apperr.KindUnauthenticated
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Canceled:
		kind = apperr.KindUnavailable
	default:
		kind = apperr.KindInternal
	}
	return &apperr.Error{Kind: kind, Message: st.Message(), Err: err}
}

// ---------------------------------------------------------------- клиент

// Политика повторов: только при UNAVAILABLE (запрос не дошёл до сервера),
// поэтому безопасна и для неидемпотентных операций.
const defaultServiceConfig = `{
  "methodConfig": [{
    "name": [{}],
    "timeout": "10s",
    "retryPolicy": {
      "maxAttempts": 3,
      "initialBackoff": "0.1s",
      "maxBackoff": "1s",
      "backoffMultiplier": 2.0,
      "retryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}`

// Dial создаёт клиентское соединение (ленивое: подключение устанавливается
// при первом вызове и автоматически восстанавливается).
func Dial(target string, extra ...grpc.UnaryClientInterceptor) (*grpc.ClientConn, error) {
	interceptors := append([]grpc.UnaryClientInterceptor{PropagateContext()}, extra...)
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(defaultServiceConfig),
		grpc.WithChainUnaryInterceptor(interceptors...),
	)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", target, err)
	}
	return conn, nil
}

// PropagateContext передаёт request-id и личность вызывающего в метаданных
// исходящего вызова.
func PropagateContext() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(OutgoingContext(ctx), method, req, reply, cc, opts...)
	}
}

// OutgoingContext добавляет в исходящие метаданные данные из контекста.
func OutgoingContext(ctx context.Context) context.Context {
	kv := make([]string, 0, 8)
	if rid := requestid.From(ctx); rid != "" {
		kv = append(kv, MDRequestID, rid)
	}
	if id, ok := auth.IdentityFrom(ctx); ok {
		kv = append(kv, MDActorID, id.UserID, MDActorRole, string(id.Role))
		if id.Name != "" {
			kv = append(kv, MDActorName, id.Name)
		}
	}
	if len(kv) == 0 {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, kv...)
}

// ---------------------------------------------------------------- здоровье

// CheckHealth вызывает стандартный grpc.health.v1 сервиса по адресу addr.
// Используется в режиме -healthcheck бинарника (HEALTHCHECK в Docker).
func CheckHealth(addr string) error {
	target := addr
	if strings.HasPrefix(target, ":") {
		target = "127.0.0.1" + target
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return err
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("status %s", resp.GetStatus())
	}
	return nil
}
