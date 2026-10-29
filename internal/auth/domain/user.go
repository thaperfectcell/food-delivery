// Package domain содержит доменную модель Auth Service: пользователя,
// правила проверки регистрационных данных и доменные ошибки.
// Пакет не зависит от инфраструктуры (БД, gRPC, алгоритмов хеширования).
package domain

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
)

// Ограничения регистрационных данных.
const (
	MinPasswordLength = 8   // минимальная длина пароля (символов)
	MaxPasswordBytes  = 72  // предел алгоритма bcrypt (байт)
	MaxNameLength     = 100 // символов
	MaxEmailLength    = 254 // RFC 5321
	MaxPhoneLength    = 32
)

// User — учётная запись пользователя.
type User struct {
	ID           string
	Email        string
	PasswordHash string
	Name         string
	Phone        string
	Role         auth.Role
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ListFilter — параметры выборки пользователей.
type ListFilter struct {
	Role   auth.Role // пусто — все роли
	Limit  int
	Offset int
}

// Ошибки доменного уровня.
var (
	ErrNotFound           = apperr.NotFound("пользователь не найден")
	ErrEmailTaken         = apperr.Conflict("пользователь с таким email уже существует")
	ErrInvalidCredentials = apperr.Unauthenticated("неверный email или пароль")
	ErrInvalidEmail       = apperr.Invalid("некорректный email")
	ErrWeakPassword       = apperr.Invalid("пароль должен содержать не менее 8 символов")
	ErrPasswordTooLong    = apperr.Invalid("пароль слишком длинный (не более 72 байт)")
	ErrNameRequired       = apperr.Invalid("не указано имя")
	ErrNameTooLong        = apperr.Invalid("имя слишком длинное (не более 100 символов)")
	ErrPhoneTooLong       = apperr.Invalid("номер телефона слишком длинный")
	ErrRoleNotAllowed     = apperr.Invalid("недопустимая роль: при регистрации доступны customer, restaurant и courier")
	ErrForbidden          = apperr.Forbidden("недостаточно прав")
	ErrUnauthenticated    = apperr.Unauthenticated("требуется вход в систему")
)

// NormalizeEmail приводит email к каноническому виду (без пробелов, в нижнем регистре).
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateEmail выполняет упрощённую проверку формата email:
// ровно один символ «@», непустая локальная часть, домен с точкой, без пробелов.
func ValidateEmail(email string) error {
	if email == "" || len(email) > MaxEmailLength {
		return ErrInvalidEmail
	}
	if strings.IndexFunc(email, unicode.IsSpace) >= 0 {
		return ErrInvalidEmail
	}
	at := strings.Index(email, "@")
	if at <= 0 || at != strings.LastIndex(email, "@") {
		return ErrInvalidEmail
	}
	domainPart := email[at+1:]
	dot := strings.LastIndex(domainPart, ".")
	if dot <= 0 || dot == len(domainPart)-1 {
		return ErrInvalidEmail
	}
	return nil
}

// ValidatePassword проверяет требования к паролю.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return ErrWeakPassword
	}
	if len(password) > MaxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}

// NormalizeName обрезает пробелы и проверяет имя.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrNameRequired
	}
	if utf8.RuneCountInString(name) > MaxNameLength {
		return "", ErrNameTooLong
	}
	return name, nil
}

// NormalizePhone обрезает пробелы и проверяет длину номера (необязательное поле).
func NormalizePhone(phone string) (string, error) {
	phone = strings.TrimSpace(phone)
	if utf8.RuneCountInString(phone) > MaxPhoneLength {
		return "", ErrPhoneTooLong
	}
	return phone, nil
}

// RegistrableRole сообщает, можно ли самостоятельно зарегистрироваться с ролью.
// Учётные записи администраторов создаются только служебно (начальные данные).
func RegistrableRole(r auth.Role) bool {
	switch r {
	case auth.RoleCustomer, auth.RoleRestaurant, auth.RoleCourier:
		return true
	}
	return false
}
