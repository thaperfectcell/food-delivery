// Package apperr задаёт классификацию ошибок прикладного уровня.
// Слой бизнес-логики возвращает ошибки apperr, транспортный слой
// преобразует их в коды gRPC, а API Gateway — в HTTP-статусы.
package apperr

import (
	"errors"
	"fmt"
)

// Kind — категория ошибки.
type Kind int

const (
	KindInternal        Kind = iota // внутренняя ошибка
	KindInvalid                     // некорректные входные данные
	KindNotFound                    // объект не найден
	KindConflict                    // конфликт (дубликат и т.п.)
	KindPrecondition                // операция недопустима в текущем состоянии
	KindForbidden                   // недостаточно прав
	KindUnauthenticated             // не выполнен вход / неверные учётные данные
	KindUnavailable                 // зависимость временно недоступна
)

func (k Kind) String() string {
	switch k {
	case KindInvalid:
		return "invalid_argument"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindPrecondition:
		return "failed_precondition"
	case KindForbidden:
		return "forbidden"
	case KindUnauthenticated:
		return "unauthenticated"
	case KindUnavailable:
		return "unavailable"
	default:
		return "internal"
	}
}

// Error — ошибка прикладного уровня с категорией и сообщением для клиента.
type Error struct {
	Kind    Kind
	Message string
	Err     error // исходная причина (не показывается клиенту)
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// New создаёт ошибку заданной категории.
func New(kind Kind, msg string) *Error { return &Error{Kind: kind, Message: msg} }

// Newf создаёт ошибку с форматированным сообщением.
func Newf(kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// Wrap оборачивает причину err в ошибку заданной категории.
func Wrap(kind Kind, msg string, err error) *Error {
	return &Error{Kind: kind, Message: msg, Err: err}
}

func Invalid(msg string) *Error         { return New(KindInvalid, msg) }
func NotFound(msg string) *Error        { return New(KindNotFound, msg) }
func Conflict(msg string) *Error        { return New(KindConflict, msg) }
func Precondition(msg string) *Error    { return New(KindPrecondition, msg) }
func Forbidden(msg string) *Error       { return New(KindForbidden, msg) }
func Unauthenticated(msg string) *Error { return New(KindUnauthenticated, msg) }
func Unavailable(msg string) *Error     { return New(KindUnavailable, msg) }

// KindOf определяет категорию ошибки (KindInternal для «чужих» ошибок).
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

// MessageOf возвращает безопасное для клиента сообщение.
func MessageOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Message
	}
	return "внутренняя ошибка сервиса"
}

// Is сообщает, относится ли ошибка к категории kind.
func Is(err error, kind Kind) bool {
	return err != nil && KindOf(err) == kind
}
