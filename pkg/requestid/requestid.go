// Package requestid хранит сквозной идентификатор запроса (correlation ID)
// в контексте. Идентификатор создаётся в API Gateway, передаётся в сервисы
// через метаданные gRPC и в доменные события (поле trace_id), что позволяет
// связать записи журналов всех сервисов, участвовавших в обработке запроса.
package requestid

import (
	"context"

	"github.com/google/uuid"
)

// Header — имя HTTP-заголовка и ключа метаданных gRPC.
const Header = "x-request-id"

type ctxKey struct{}

// With возвращает контекст с идентификатором запроса.
func With(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, id)
}

// From извлекает идентификатор запроса из контекста (пустая строка, если его нет).
func From(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// New генерирует новый идентификатор.
func New() string {
	return uuid.NewString()
}
