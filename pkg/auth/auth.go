// Package auth — общие элементы безопасности: роли, личность вызывающего
// (Identity) и работа с JWT-токенами доступа.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Role — роль пользователя.
type Role string

const (
	RoleCustomer   Role = "customer"
	RoleRestaurant Role = "restaurant"
	RoleCourier    Role = "courier"
	RoleAdmin      Role = "admin"
	// RoleSystem — внутренние операции (обработчики событий, композиция в шлюзе).
	RoleSystem Role = "system"
)

// Valid сообщает, является ли роль пользовательской ролью системы.
func (r Role) Valid() bool {
	switch r {
	case RoleCustomer, RoleRestaurant, RoleCourier, RoleAdmin:
		return true
	}
	return false
}

// Identity — личность вызывающего.
type Identity struct {
	UserID string
	Role   Role
	Name   string
}

// System — личность для внутренних операций.
var System = Identity{UserID: "system", Role: RoleSystem, Name: "system"}

// IsZero сообщает, что личность не задана (анонимный вызов).
func (i Identity) IsZero() bool { return i.UserID == "" }

// Is сообщает, обладает ли вызывающий одной из ролей.
func (i Identity) Is(roles ...Role) bool {
	for _, r := range roles {
		if i.Role == r {
			return true
		}
	}
	return false
}

// Privileged — администратор или система.
func (i Identity) Privileged() bool { return i.Is(RoleAdmin, RoleSystem) }

type ctxKey struct{}

// WithIdentity помещает личность в контекст.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// IdentityFrom извлекает личность из контекста.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok && !id.IsZero()
}

// Claims — полезная нагрузка JWT.
type Claims struct {
	jwt.RegisteredClaims
	Role string `json:"role"`
	Name string `json:"name"`
}

// ErrInvalidToken — токен отсутствует, повреждён или просрочен.
var ErrInvalidToken = errors.New("invalid or expired token")

const issuer = "foodflow-auth"

// TokenManager выпускает и проверяет JWT (HS256).
type TokenManager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewTokenManager создаёт менеджер токенов.
func NewTokenManager(secret string, ttl time.Duration) *TokenManager {
	return &TokenManager{secret: []byte(secret), ttl: ttl, now: time.Now}
}

// Issue выпускает токен доступа для пользователя.
func (m *TokenManager) Issue(userID string, role Role, name string) (string, time.Time, error) {
	now := m.now()
	exp := now.Add(m.ttl)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
		Role: string(role),
		Name: name,
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return token, exp, nil
}

// Parse проверяет подпись и срок действия токена и возвращает личность.
func (m *TokenManager) Parse(token string) (Identity, error) {
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, claims,
		func(t *jwt.Token) (any, error) { return m.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(m.now),
	)
	if err != nil || !parsed.Valid {
		return Identity{}, ErrInvalidToken
	}
	role := Role(claims.Role)
	if claims.Subject == "" || !role.Valid() {
		return Identity{}, ErrInvalidToken
	}
	return Identity{UserID: claims.Subject, Role: role, Name: claims.Name}, nil
}
