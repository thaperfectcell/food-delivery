package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
)

// HTTPStatus сопоставляет код gRPC с HTTP-статусом и машиночитаемым кодом
// ошибки внешнего API.
func HTTPStatus(code codes.Code) (int, string) {
	switch code {
	case codes.OK:
		return http.StatusOK, "ok"
	case codes.InvalidArgument, codes.OutOfRange:
		return http.StatusBadRequest, "invalid_argument"
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "unauthenticated"
	case codes.PermissionDenied:
		return http.StatusForbidden, "forbidden"
	case codes.NotFound:
		return http.StatusNotFound, "not_found"
	case codes.AlreadyExists, codes.Aborted:
		return http.StatusConflict, "conflict"
	case codes.FailedPrecondition:
		return http.StatusUnprocessableEntity, "failed_precondition"
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests, "rate_limited"
	case codes.Unavailable:
		return http.StatusServiceUnavailable, "unavailable"
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout, "timeout"
	case codes.Canceled:
		return 499, "canceled" // клиент закрыл соединение (нестандартный код, как в nginx)
	default:
		return http.StatusInternalServerError, "internal"
	}
}

// writeError отправляет клиенту ошибку вызова внутреннего сервиса.
// Сообщения бизнес-ошибок (4xx) передаются как есть — сервисы формулируют их
// для пользователя; подробности технических сбоев (5xx) скрываются и только
// журналируются.
func (a *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		err = status.Error(codes.DeadlineExceeded, err.Error())
	} else if errors.Is(err, context.Canceled) {
		err = status.Error(codes.Canceled, err.Error())
	}
	st := status.Convert(err)
	httpStatus, code := HTTPStatus(st.Code())
	msg := st.Message()

	switch st.Code() {
	case codes.Unavailable:
		msg = "сервис временно недоступен, повторите попытку позже"
	case codes.DeadlineExceeded:
		msg = "превышено время ожидания ответа сервиса"
	case codes.Canceled:
		msg = "запрос отменён"
	case codes.Internal, codes.Unknown, codes.DataLoss, codes.Unimplemented:
		msg = "внутренняя ошибка сервиса"
	}
	if httpStatus >= 500 {
		a.log.WarnContext(r.Context(), "downstream call failed",
			slog.String("path", r.URL.Path),
			slog.String("grpc_code", st.Code().String()),
			slog.String("error", st.Message()))
	}
	if msg == "" {
		msg = http.StatusText(httpStatus)
	}
	httpx.WriteError(w, r, httpStatus, code, msg)
}

// badRequest — ошибка валидации запроса на стороне шлюза.
func badRequest(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, "invalid_argument", msg)
}
